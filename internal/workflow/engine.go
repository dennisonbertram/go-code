package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	subs      map[string]map[chan Event]*subEntry
	eventSeqs map[string]int64
	runs      map[string]*Run
}

// subEntry tracks one Subscribe call's live channel and, while it is
// still copying history (the window between registration and Subscribe
// returning), a buffer of events that arrived during that window.
//
// emit()'s fan-out send is non-blocking into a 64-slot channel, and the
// subscribing goroutine cannot drain that channel until Subscribe()
// itself returns (it hasn't been given the channel yet). So without this
// buffer, any burst of more than 64 events emitted during the
// (deliberately unlocked, O(history)) store.GetEvents copy would
// overflow the channel send AND be excluded from Subscribe's history
// trim (their Seq is > recordedSeq) -- reaching neither set. That is a
// real gap, just a different route to it than the one BUG 5 originally
// closed.
//
// pending non-nil means "this subscriber is still initializing": emit()
// appends to it instead of attempting the channel send. Subscribe sets
// it back to nil (under e.mu) once it has drained pending into the
// returned history, after which emit() delivers live via the channel as
// normal.
type subEntry struct {
	pending []Event
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
		subs:           make(map[string]map[chan Event]*subEntry),
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

// scriptFor looks up a registered script by name under e.mu. It is the
// single shared, defer-safe way to read e.scripts from a standalone
// caller (Start, Context.Workflow). Callers that already hold e.mu for a
// larger compound operation (e.g. Resume's check-and-transition) must NOT
// call this — sync.Mutex is not reentrant, and doing so would deadlock.
// Those callers read e.scripts directly under their own lock instead.
func (e *Engine) scriptFor(name string) (registeredScript, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	reg, ok := e.scripts[name]
	return reg, ok
}

// registerRun records a newly-created run under e.mu.
func (e *Engine) registerRun(run *Run) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.runs[run.ID] = run
}

// Start begins executing a workflow by name with the given args.
// The workflow runs asynchronously in a goroutine. The returned Run has
// status RunStatusRunning and can be monitored via Subscribe.
//
// Start creates a new run ID, persists it via the Store, emits a
// workflow.started event, then executes the script in a goroutine.
func (e *Engine) Start(ctx context.Context, name string, args any) (*Run, error) {
	reg, ok := e.scriptFor(name)
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

	e.registerRun(run)

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

// resumePreTransitionHook is a test-only seam. When non-nil, Resume calls
// it after the status check passes (run.Status == RunStatusFailed, script
// still registered) but before it transitions the run to
// RunStatusRunning — while STILL HOLDING e.mu. It is nil (a no-op) in
// production.
//
// This lets tests deterministically pause Resume mid-critical-section and
// assert that no concurrently-racing Resume call can complete until this
// one does, proving the check-and-transition is genuinely atomic rather
// than relying on winning a timing race under -race. See
// TestConcurrentResumeCriticalSectionIsAtomic.
var resumePreTransitionHook func()

// Resume continues a previously failed workflow run. The args are passed to
// the script, which should use them to pick up where it left off.
//
// This mirrors Claude Code's resume capability where a Workflow invocation
// can include {resumeFromRunId: "..."} to skip cached agent calls.
//
// Only runs with status RunStatusFailed can be resumed. The run's error is
// cleared and its status reset to RunStatusRunning before re-execution.
func (e *Engine) Resume(ctx context.Context, runID string, args any) (*Run, error) {
	// The status check, the registered-script lookup, and the transition
	// to RunStatusRunning are one atomic critical section under e.mu.
	// Otherwise two concurrent Resume calls could both observe
	// RunStatusFailed before either mutates the run, and both spawn
	// `go e.execute` — running the script twice for one Resume. e.scripts
	// is read directly here (not via scriptFor) because this goroutine
	// already holds e.mu and sync.Mutex is not reentrant.
	reg, cp, err := func() (registeredScript, Run, error) {
		e.mu.Lock()
		defer e.mu.Unlock()

		run, ok := e.runs[runID]
		if !ok {
			return registeredScript{}, Run{}, fmt.Errorf("workflow run %q not found", runID)
		}
		if run.Status != RunStatusFailed {
			return registeredScript{}, Run{}, fmt.Errorf("workflow run %q has status %s, can only resume failed runs", runID, run.Status)
		}
		reg, ok := e.scripts[run.WorkflowName]
		if !ok {
			return registeredScript{}, Run{}, fmt.Errorf("workflow %q no longer registered", run.WorkflowName)
		}

		if resumePreTransitionHook != nil {
			resumePreTransitionHook()
		}

		run.Status = RunStatusRunning
		run.Error = ""
		run.UpdatedAt = e.now().UTC()
		// Copy while still holding the lock. This is what actually
		// prevents a race with concurrent readers of the shared *Run:
		// every other engine.go call site that reads or writes a run
		// (GetRun, and executeScriptAsync's terminal transition via
		// finishRun/transitionRunTerminal) also only ever touches a
		// private copy taken under e.mu, or the map entry itself under
		// e.mu — never this pointer after it's released. Passing &cp
		// (not run) to store.UpdateRun below is what closes that loop.
		return reg, *run, nil
	}()
	if err != nil {
		return nil, err
	}

	_ = e.store.UpdateRun(ctx, &cp)

	e.emit(cp.ID, EventWorkflowStarted, map[string]any{
		"workflow": reg.Meta.Name,
		"resumed":  true,
	})

	go e.execute(cp.ID, reg, args)
	return &cp, nil
}

// GetRun returns the current state of a workflow run.
// The returned Run is a copy, safe for concurrent reading.
func (e *Engine) GetRun(runID string) (*Run, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	run, ok := e.runs[runID]
	if !ok {
		return nil, fmt.Errorf("workflow run %q not found", runID)
	}
	cp := *run // copy while holding the lock
	return &cp, nil
}

// Subscribe returns historical events and a channel for live events for a run.
//
// The returned slice contains all events emitted for this run so far.
// The channel receives new events as they are emitted during execution.
//
// The cancel function unsubscribes and closes the channel. Callers MUST call
// cancel when done to avoid goroutine leaks; it is always safe to call even
// if the channel was already closed by a terminal event (see emit()).
//
// On a terminal event (workflow.completed/workflow.failed), emit() closes
// and deregisters every subscriber channel for the run, so the channel
// will also close on its own once the run finishes — subscribers don't
// strictly need to call cancel() to observe termination, only to clean up
// early/non-terminal subscriptions.
//
// Locking: e.mu is held ONLY long enough to register the channel and
// record the current per-run sequence number (recordedSeq) — both O(1).
// The store.GetEvents call, which is O(history) and can be arbitrarily
// large for a long-running run, happens AFTER releasing e.mu.
//
// This is deliberate: an earlier version held e.mu across the
// store.GetEvents call too, on the theory that "read-history +
// register-channel" needed to be one atomic step to avoid losing an
// event emitted in between (see BUG 5). That was correct for closing the
// gap, but wrong for performance — since emit() also needs e.mu for
// every event on every run, an O(history) Subscribe blocked ALL emits
// engine-wide for its duration, and repeated Subscribes against a
// growing-history run made the total cost O(history^2). This is exactly
// the production hazard: "every SSE client connect/reconnect copies the
// full event history under the global engine lock, blocking emits for
// every run engine-wide."
//
// Exactly-once guarantee, precisely: recordedSeq is read atomically with
// channel registration, so:
//   - any event with Seq <= recordedSeq was fully appended (AppendEvent
//     runs under e.mu, before the seq counter that produced it could
//     have been observed) before this Subscribe's critical section ran,
//     and is therefore present in whatever store.GetEvents returns below;
//   - any event with Seq > recordedSeq is emitted by a call to emit()
//     that acquires e.mu strictly after this Subscribe's critical
//     section released it.
//
// That second bullet is NOT the same as "delivered live on ch", and an
// earlier version of this comment incorrectly claimed it was: emit()'s
// fan-out send is non-blocking into ch's 64-slot buffer
// (select/default), and this goroutine cannot drain ch until Subscribe
// returns — it hasn't been given the channel yet. More than 64 events
// emitted during the store.GetEvents call would silently overflow that
// send AND be excluded from the history trim below (Seq > recordedSeq),
// reaching neither set.
//
// The actual fix: every subscriber has a subEntry with a pending buffer
// that starts non-nil ("still initializing"). While pending != nil,
// emit()'s fan-out appends to it instead of attempting the channel send
// — an unbounded, always-succeeding operation, unlike the fixed-size
// channel. Once store.GetEvents returns, Subscribe folds pending (all
// Seq > recordedSeq, accumulated in emit order) onto the trimmed
// history and sets pending back to nil, after which emit() delivers
// live via the channel as normal. The two sets (history, pending) never
// overlap (the trim uses the same recordedSeq the pending buffer starts
// capturing after), so no event is ever lost and none is ever
// duplicated — including bursts far larger than the channel's buffer.
func (e *Engine) Subscribe(runID string) ([]Event, <-chan Event, func(), error) {
	ch := make(chan Event, 64)
	entry := &subEntry{pending: []Event{}} // non-nil: this subscriber is initializing

	e.mu.Lock()
	if _, ok := e.subs[runID]; !ok {
		e.subs[runID] = make(map[chan Event]*subEntry)
	}
	e.subs[runID][ch] = entry
	recordedSeq := e.eventSeqs[runID]
	e.mu.Unlock()

	allEvents, err := e.store.GetEvents(context.Background(), runID, -1)
	if err != nil {
		e.mu.Lock()
		if subsForRun, ok := e.subs[runID]; ok {
			delete(subsForRun, ch)
		}
		e.mu.Unlock()
		return nil, nil, nil, err
	}

	// allEvents is in append (ascending Seq) order; find the split
	// point and trim. Events after it are captured in entry.pending
	// instead (folded in below) — including them here too would
	// duplicate them. The capped 3-index slice bounds capacity to
	// length so the append below always allocates a fresh backing
	// array, never silently reusing/overwriting allEvents' excluded
	// tail.
	cutoff := len(allEvents)
	for i, ev := range allEvents {
		if ev.Seq > recordedSeq {
			cutoff = i
			break
		}
	}
	history := allEvents[:cutoff:cutoff]

	// Finalize: stop buffering into entry.pending and fold whatever
	// accumulated there (all Seq > recordedSeq, in emit order) onto
	// history. Read/reset entry.pending via the entry pointer captured
	// at registration above — NOT a fresh e.subs[runID][ch] lookup —
	// because a terminal event arriving while we were still
	// initializing may already have caused emit() to close ch and
	// delete this run's entire e.subs[runID] map (see emit()'s terminal
	// handling below). entry itself remains valid and still holds
	// whatever was buffered for us regardless of whether it's still
	// reachable from e.subs.
	e.mu.Lock()
	pending := entry.pending
	entry.pending = nil
	e.mu.Unlock()
	if len(pending) > 0 {
		history = append(history, pending...)
	}

	cancel := func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		// Membership in e.subs is the single source of truth for
		// "has this channel already been closed" — emit() removes a
		// channel from e.subs in the SAME critical section where it
		// closes it on a terminal event (see emit() below), so if
		// it's already gone here, emit() got there first; closing
		// again would panic ("close of closed channel"). Both close
		// sites only ever mutate e.subs under e.mu, so this
		// check-then-close is race-free without needing a separate
		// sync.Once per channel — the existing map+lock already is
		// the single point of coordination between the two closers.
		if subsForRun, ok := e.subs[runID]; ok {
			if _, present := subsForRun[ch]; present {
				delete(subsForRun, ch)
				close(ch)
			}
		}
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
	// Defense in depth: this runs on a bare `go e.execute(...)` goroutine
	// with no caller to recover a panic. executeScript already recovers
	// panics from the script body itself; this guards against a panic
	// ANYWHERE ELSE in the execution/emit path (e.g. a panicking
	// MarshalJSON on the script's result, invoked by json.Marshal in
	// executeScriptAsync).
	//
	// This must NEVER be a bare `recover()` that discards the panic: a
	// swallowed panic here leaves the run stuck at RunStatusRunning
	// forever with no persisted error and no terminal event, which hangs
	// every SSE subscriber and every status poll indefinitely — worse
	// than not recovering at all. So this recover makes the run terminal
	// and observable: it logs the panic, marks the run Failed via the
	// same finishRun/transitionRunTerminal helper executeScriptAsync
	// uses for a normal script error, persists it, and emits
	// workflow.failed.
	//
	// json.Marshal is deliberately called OUTSIDE e.mu in
	// executeScriptAsync (see below) specifically so a panic there is
	// caught here with no lock held — finishRun's own e.mu.Lock() below
	// is therefore guaranteed to succeed, never self-deadlock.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("workflow: recovered panic in Engine.execute for run %s: %v", runID, r)
			e.finishRun(runID, reg, fmt.Errorf("workflow engine panic: %v", r), "")
		}
	}()

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

	// json.Marshal is called BEFORE taking e.mu and is NOT wrapped in its
	// own recover here. It can invoke arbitrary user-supplied
	// MarshalJSON/String methods on the script's result, and
	// encoding/json only recovers its OWN internal sentinel error type —
	// any other panic (e.g. a MarshalJSON that indexes a nil map) is
	// re-panicked out of json.Marshal. No user-supplied code may EVER
	// run while e.mu is held (a MarshalJSON that itself calls ctx.Log()
	// would re-enter emit() -> e.mu on this same, non-reentrant mutex and
	// self-deadlock even with e.mu correctly scoped). If it panics, it
	// propagates to execute()'s outer recover, which converts it into a
	// terminal Failed run via this same finishRun helper — so the
	// failure mode is "run fails with a descriptive error", never "run
	// hangs" or "process wedges".
	var resultJSON string
	if err == nil && result != nil {
		if raw, marshalErr := json.Marshal(result); marshalErr == nil {
			resultJSON = string(raw)
		}
		// A normal (non-panic) marshal error is non-fatal, exactly as
		// before: ResultJSON simply stays empty and the run still
		// completes.
	}

	e.finishRun(runID, reg, err, resultJSON)
}

// finishRun applies the terminal status transition for a run and persists
// + emits it. It is used both by the normal executeScriptAsync path and
// by execute()'s outer panic recovery, so every terminal transition goes
// through the same, single code path.
//
// scriptErr nil means the script (and marshaling) succeeded; non-nil
// means it failed (including a recovered panic, wrapped by the caller).
func (e *Engine) finishRun(runID string, reg registeredScript, scriptErr error, resultJSON string) {
	cp, ok := e.transitionRunTerminal(runID, scriptErr, resultJSON)
	if !ok {
		// Run no longer tracked (e.g. removed concurrently) — nothing to
		// persist or emit.
		return
	}

	_ = e.store.UpdateRun(context.Background(), &cp)

	if scriptErr != nil {
		e.emit(runID, EventWorkflowFailed, map[string]any{
			"workflow": reg.Meta.Name,
			"error":    scriptErr.Error(),
		})
	} else {
		e.emit(runID, EventWorkflowCompleted, map[string]any{
			"workflow": reg.Meta.Name,
		})
	}
}

// transitionRunTerminal mutates the run's status under e.mu and returns a
// private copy for the caller to persist/emit OUTSIDE the lock. Passing
// this copy (never the shared *Run pointer) to store.UpdateRun is what
// prevents it from racing a concurrent Resume's mutation of the same run
// under e.mu.
func (e *Engine) transitionRunTerminal(runID string, scriptErr error, resultJSON string) (Run, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	run, ok := e.runs[runID]
	if !ok {
		return Run{}, false
	}

	if scriptErr != nil {
		run.Status = RunStatusFailed
		run.Error = scriptErr.Error()
	} else {
		run.Status = RunStatusCompleted
		if resultJSON != "" {
			run.ResultJSON = resultJSON
		}
	}
	run.UpdatedAt = e.now().UTC()
	return *run, true
}

// emit sends an event to all subscribers of a run and persists it via the Store.
//
// Events are sequenced per run. Subscribers that are too slow (channel full)
// have the event dropped rather than blocking the emitter. This prevents a
// slow subscriber from stalling workflow execution.
//
// The sequence bump, the store append, and the subscriber fan-out all
// happen while holding e.mu. This is required for correctness, not just
// convenience:
//   - Subscribe's cancel() also closes the channel under e.mu. Holding the
//     lock across the send loop here makes "send" and "close" mutually
//     exclusive, so emit can never send on a channel that cancel is in the
//     process of (or has already) closed.
//   - Subscribe itself reads history (via the store) and registers its
//     channel under e.mu (see Subscribe below). Holding the lock across
//     AppendEvent + fan-out here makes "append+deliver" atomic against
//     "read-history+register", so an event can never land in the gap
//     between a subscriber's history snapshot and its channel
//     registration.
//
// The default in-memory Store has no I/O and never calls back into the
// Engine, so holding e.mu across AppendEvent is bounded and safe. Sends to
// subscriber channels remain non-blocking (select/default), so the
// critical section stays bounded even if a store implementation is slower.
//
// On a terminal event (workflow.completed/workflow.failed), every
// subscriber channel for the run is closed and deregistered, in the same
// critical section, right after the fan-out. This makes the terminal
// event undroppable: a subscriber whose 64-slot buffer is already full
// silently loses the SEND above (by design, to avoid blocking the
// emitter), but the immediately-following close() still reaches it — a
// closed channel read (`ev, ok := <-ch`) returns immediately with
// ok=false, which every subscriber (e.g. the SSE handler in
// internal/server/http_script_workflows.go) already treats as "stream
// ended". Without this, a slow subscriber that fills its buffer right
// before completion would hang forever waiting for a completion that
// already happened — the same failure mode BUG 5 fixed for the
// history/live gap, via a different route.
func (e *Engine) emit(runID string, eventType EventType, payload map[string]any) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.eventSeqs[runID]++
	seq := e.eventSeqs[runID]

	event := Event{
		Seq:       seq,
		RunID:     runID,
		Type:      eventType,
		Payload:   payload,
		Timestamp: e.now().UTC(),
	}

	_ = e.store.AppendEvent(context.Background(), &event)

	for ch, entry := range e.subs[runID] {
		if entry.pending != nil {
			// This subscriber is still inside Subscribe's O(history)
			// copy (its channel isn't drainable yet — Subscribe hasn't
			// returned it to the caller). Buffer instead of the lossy
			// channel send: pending grows unbounded, so a burst larger
			// than the channel's 64-slot buffer is never dropped. See
			// Subscribe's doc comment for the full exactly-once
			// argument.
			entry.pending = append(entry.pending, event)
			continue
		}
		select {
		case ch <- event:
		default:
			// Drop event if subscriber channel is full — prevents a slow
			// subscriber from stalling workflow execution.
		}
	}

	if eventType == EventWorkflowCompleted || eventType == EventWorkflowFailed {
		for ch := range e.subs[runID] {
			close(ch)
		}
		delete(e.subs, runID)
	}
}
