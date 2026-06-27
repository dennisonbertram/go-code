package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// EngineOptions configures a workflow Engine.
type EngineOptions struct {
	// Subagents is the manager used to create and track sub-agent runs.
	// Required. Must implement the SubagentManager interface (matching subagents.Manager).
	Subagents SubagentManager

	// MaxConcurrency caps the number of concurrent agent calls.
	// When 0, defaults to min(16, runtime.NumCPU()-2), minimum 1.
	MaxConcurrency int

	// DefaultBudget is the default token budget for new workflow runs.
	// A value of 0 means unlimited. Can be overridden per-run via arguments
	// (e.g. a RunRequest passed through the API layer).
	DefaultBudget int

	// Store is an optional persistence backend. When nil, an in-memory store
	// is used. The store persists workflow runs and events.
	Store Store

	// QuestionResponder handles workflow questions that need a parent/user
	// answer. When nil, Context.Question returns an error after emitting the
	// question event.
	QuestionResponder QuestionResponder

	// Now overrides the time source for deterministic testing.
	Now func() time.Time
}

// Engine executes workflow scripts with full orchestration semantics.
// It is the top-level entry point for the workflow feature.
//
// The Engine holds a registry of named scripts, manages concurrent execution
// via a semaphore, tracks runs in memory (with optional persistent Store),
// emits events to subscribers, and supports resuming previously failed runs.
type Engine struct {
	subagents      SubagentManager
	scripts        map[string]registeredScript
	maxConcurrency int
	defaultBudget  int
	store          Store
	questions      QuestionResponder
	now            func() time.Time

	mu        sync.Mutex
	subs      map[string]map[chan Event]struct{}
	eventSeqs map[string]int64
	runs      map[string]*Run
}

type registeredScript struct {
	Meta   Meta
	Script Script
}

// NewEngine creates a new workflow Engine.
// The Subagents field in opts is required; a nil Subagents will cause panics
// when agent() is called inside a workflow script.
func NewEngine(opts EngineOptions) *Engine {
	if opts.Store == nil {
		opts.Store = newMemoryStore()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	concurrency := opts.MaxConcurrency
	if concurrency <= 0 {
		concurrency = maxConcurrency()
	}

	return &Engine{
		subagents:      opts.Subagents,
		scripts:        make(map[string]registeredScript),
		maxConcurrency: concurrency,
		defaultBudget:  opts.DefaultBudget,
		store:          opts.Store,
		questions:      opts.QuestionResponder,
		now:            opts.Now,
		subs:           make(map[string]map[chan Event]struct{}),
		eventSeqs:      make(map[string]int64),
		runs:           make(map[string]*Run),
	}
}

// Register adds a workflow script to the engine. Scripts are looked up by
// name when Start or Resume is called, or when a nested workflow() call
// references the name.
//
// The name is used as the workflow's Meta.Name for event emission,
// progress display, and nested workflow identification.
func (e *Engine) Register(name string, script Script) {
	e.RegisterWithMeta(Meta{Name: name}, script)
}

// RegisterWithMeta adds a workflow script with explicit discovery metadata.
func (e *Engine) RegisterWithMeta(meta Meta, script Script) {
	if meta.Name == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.scripts[meta.Name] = registeredScript{
		Meta:   meta,
		Script: script,
	}
}

// List returns all registered workflow metas, sorted by name.
func (e *Engine) List() []Meta {
	e.mu.Lock()
	defer e.mu.Unlock()
	var names []string
	for name := range e.scripts {
		names = append(names, name)
	}
	// Simple bubble sort for small lists
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[i] > names[j] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	out := make([]Meta, 0, len(names))
	for _, name := range names {
		out = append(out, e.scripts[name].Meta)
	}
	return out
}

// Start begins executing a workflow by name with the given args.
// The workflow runs asynchronously in a goroutine. The returned Run has
// status RunStatusRunning and can be monitored via Subscribe.
//
// Start creates a new run ID, persists it via the Store, emits a
// workflow.started event, then executes the script in a goroutine.
func (e *Engine) Start(ctx context.Context, name string, args any) (*Run, error) {
	e.mu.Lock()
	reg, ok := e.scripts[name]
	e.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("workflow %q not found", name)
	}

	run := &Run{
		ID:           "wf_" + uuid.NewString(),
		WorkflowName: reg.Meta.Name,
		Status:       RunStatusRunning,
		CreatedAt:    e.now().UTC(),
		UpdatedAt:    e.now().UTC(),
	}

	e.mu.Lock()
	e.runs[run.ID] = run
	e.mu.Unlock()

	if err := e.store.CreateRun(ctx, run); err != nil {
		return nil, fmt.Errorf("persist run: %w", err)
	}

	e.emit(run.ID, EventWorkflowStarted, map[string]any{
		"workflow": reg.Meta.Name,
	})

	// Return a copy so callers don't race with the execution goroutine.
	cp := *run

	go e.execute(run.ID, reg, args)
	return &cp, nil
}

// Resume continues a previously failed workflow run. The args are passed to
// the script, which should use them to pick up where it left off.
//
// This mirrors Claude Code's resume capability where a Workflow invocation
// can include {resumeFromRunId: "..."} to skip cached agent calls.
//
// Only runs with status RunStatusFailed can be resumed. The run's error is
// cleared and its status reset to RunStatusRunning before re-execution.
func (e *Engine) Resume(ctx context.Context, runID string, args any) (*Run, error) {
	e.mu.Lock()
	run, ok := e.runs[runID]
	if !ok {
		e.mu.Unlock()
		return nil, fmt.Errorf("workflow run %q not found", runID)
	}
	if run.Status != RunStatusFailed {
		e.mu.Unlock()
		return nil, fmt.Errorf("workflow run %q has status %s, can only resume failed runs", runID, run.Status)
	}
	reg, ok := e.scripts[run.WorkflowName]
	e.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("workflow %q no longer registered", run.WorkflowName)
	}

	run.Status = RunStatusRunning
	run.Error = ""
	run.UpdatedAt = e.now().UTC()
	_ = e.store.UpdateRun(ctx, run)

	e.emit(run.ID, EventWorkflowStarted, map[string]any{
		"workflow": reg.Meta.Name,
		"resumed":  true,
	})

	// Return a copy so callers don't race with the execution goroutine.
	cp := *run

	go e.execute(run.ID, reg, args)
	return &cp, nil
}

// GetRun returns the current state of a workflow run.
// The returned Run is a copy, safe for concurrent reading.
func (e *Engine) GetRun(runID string) (*Run, error) {
	e.mu.Lock()
	run, ok := e.runs[runID]
	if !ok {
		e.mu.Unlock()
		return nil, fmt.Errorf("workflow run %q not found", runID)
	}
	cp := *run // copy while holding the lock
	e.mu.Unlock()
	return &cp, nil
}

// Subscribe returns historical events and a channel for live events for a run.
//
// The returned slice contains all events emitted for this run so far.
// The channel receives new events as they are emitted during execution.
//
// The cancel function unsubscribes and closes the channel. Callers MUST call
// cancel when done to avoid goroutine leaks.
//
// If the run completes or fails, the channel will stop receiving events;
// subscribers should use the cancel function to clean up.
func (e *Engine) Subscribe(runID string) ([]Event, <-chan Event, func(), error) {
	history, err := e.store.GetEvents(context.Background(), runID, -1)
	if err != nil {
		return nil, nil, nil, err
	}

	ch := make(chan Event, 64)
	e.mu.Lock()
	if _, ok := e.subs[runID]; !ok {
		e.subs[runID] = make(map[chan Event]struct{})
	}
	e.subs[runID][ch] = struct{}{}
	e.mu.Unlock()

	cancel := func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		delete(e.subs[runID], ch)
		close(ch)
	}
	return history, ch, cancel, nil
}

// execute runs a workflow script in a goroutine, handling panics and result
// persistence. It is called by both Start and Resume.
//
// The budget is initialized from the engine's defaultBudget. A value of 0
// means unlimited tokens. The budget can be further constrained by
// RunRequest-level configuration (handled by the API layer before calling Start).
func (e *Engine) execute(runID string, reg registeredScript, args any) {
	budget := newBudget(e.defaultBudget)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wfCtx := newContext(ctx, e, runID, args, budget)
	wfCtx.phase = reg.Meta.Name

	e.executeScriptAsync(runID, reg, wfCtx)
}

// executeScript synchronously runs a script function and returns its result.
// It recovers from panics, converting them to errors.
//
// Used by both top-level execution (via executeScriptAsync) and nested
// workflow() calls (via Context.Workflow).
func (e *Engine) executeScript(ctx *Context, reg registeredScript) (any, error) {
	var result any
	var err error

	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("workflow script panic: %v", r)
			}
		}()
		result, err = reg.Script(ctx)
	}()

	return result, err
}

// executeScriptAsync runs the script in a goroutine and updates the run with
// the result. It is the terminal step for top-level Start/Resume executions.
//
// After the script completes (or panics), the run's status is updated to
// RunStatusCompleted or RunStatusFailed, the result is JSON-marshaled and
// stored, and a terminal event (workflow.completed or workflow.failed) is
// emitted to all subscribers.
func (e *Engine) executeScriptAsync(runID string, reg registeredScript, ctx *Context) {
	result, err := e.executeScript(ctx, reg)

	e.mu.Lock()
	run, ok := e.runs[runID]
	if !ok {
		e.mu.Unlock()
		return
	}

	if err != nil {
		run.Status = RunStatusFailed
		run.Error = err.Error()
	} else {
		run.Status = RunStatusCompleted
		if result != nil {
			raw, marshalErr := json.Marshal(result)
			if marshalErr == nil {
				run.ResultJSON = string(raw)
			}
		}
	}
	run.UpdatedAt = e.now().UTC()
	e.mu.Unlock()

	_ = e.store.UpdateRun(context.Background(), run)

	if err != nil {
		e.emit(runID, EventWorkflowFailed, map[string]any{
			"workflow": reg.Meta.Name,
			"error":    err.Error(),
		})
	} else {
		e.emit(runID, EventWorkflowCompleted, map[string]any{
			"workflow": reg.Meta.Name,
		})
	}
}

// emit sends an event to all subscribers of a run and persists it via the Store.
//
// Events are sequenced per run. Subscribers that are too slow (channel full)
// have the event dropped rather than blocking the emitter. This prevents a
// slow subscriber from stalling workflow execution.
func (e *Engine) emit(runID string, eventType EventType, payload map[string]any) {
	e.mu.Lock()
	e.eventSeqs[runID]++
	seq := e.eventSeqs[runID]
	liveSubs := make([]chan Event, 0, len(e.subs[runID]))
	for ch := range e.subs[runID] {
		liveSubs = append(liveSubs, ch)
	}
	e.mu.Unlock()

	event := Event{
		Seq:       seq,
		RunID:     runID,
		Type:      eventType,
		Payload:   payload,
		Timestamp: e.now().UTC(),
	}

	_ = e.store.AppendEvent(context.Background(), &event)

	for _, ch := range liveSubs {
		select {
		case ch <- event:
		default:
			// Drop event if subscriber channel is full — prevents a slow
			// subscriber from stalling workflow execution.
		}
	}
}
