package workflow

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"
)

// subagentPollInterval controls how often Context.Agent polls a subagent's
// status while waiting for it to complete. It is a var (not a const) so
// tests can shrink it to keep polling tests fast and deterministic.
var subagentPollInterval = 250 * time.Millisecond

// Context is the execution context passed to workflow scripts.
// It provides all orchestration primitives: agent(), parallel(), pipeline(),
// phase(), log(), workflow(), plus access to Args and Budget.
type Context struct {
	// Args is the input arguments passed to the workflow. Set by the engine
	// before the script runs. Can be any type; scripts should type-assert.
	Args any

	// Budget tracks token usage. Shared across all agent calls and nested
	// workflows within this run. Total of 0 means unlimited.
	Budget *Budget

	ctx    context.Context
	engine *Engine
	phase  string
	runID  string

	mu      sync.Mutex
	wg      sync.WaitGroup
	sem     chan struct{} // concurrency semaphore
	results []AgentResult // accumulated agent call results
}

// newContext creates a Context for a workflow run.
func newContext(ctx context.Context, eng *Engine, runID string, args any, budget *Budget) *Context {
	concurrency := eng.maxConcurrency
	return &Context{
		Args:   args,
		Budget: budget,
		ctx:    ctx,
		engine: eng,
		runID:  runID,
		sem:    make(chan struct{}, concurrency),
	}
}

// Agent spawns a sub-agent with the given prompt and options.
//
// It mirrors Claude Code's agent() function:
//   - Without schema: returns the agent's text output in AgentResult.Output
//   - With schema: validates the output against the JSON Schema and populates AgentResult.Schema
//
// The agent call consumes one concurrency slot. If the sub-agent fails,
// the error is returned and the caller decides how to handle it.
//
// Agent calls respect the budget: if Remaining() is 0, the call returns an error.
func (c *Context) Agent(prompt string, opts *AgentOpts) (*AgentResult, error) {
	if c.Budget.Remaining() == 0 && c.Budget.Total > 0 {
		return nil, fmt.Errorf("budget exhausted: %d/%d tokens spent", c.Budget.Spent(), c.Budget.Total)
	}

	// Acquire semaphore.
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-c.ctx.Done():
		return nil, c.ctx.Err()
	}

	if opts == nil {
		opts = &AgentOpts{}
	}

	label := opts.Label
	if label == "" {
		label = truncate(prompt, 60)
	}

	phase := opts.Phase
	if phase == "" {
		phase = c.phase
	}

	c.emit(EventWorkflowAgentStarted, map[string]any{
		"label":     label,
		"phase":     phase,
		"prompt":    truncate(prompt, 200),
		"isolation": opts.Isolation,
	})

	req := SubagentRequest{
		Prompt:        prompt,
		Model:         opts.Model,
		Provider:      opts.Provider,
		Profile:       opts.Profile,
		AllowedTools:  append([]string(nil), opts.AllowedTools...),
		Isolation:     opts.Isolation,
		CleanupPolicy: opts.CleanupPolicy,
		AgentType:     opts.AgentType,
		MaxSteps:      opts.MaxSteps,
		MaxCostUSD:    opts.MaxCostUSD,
	}

	result, err := c.engine.subagents.Create(c.ctx, req)
	if err != nil {
		c.emit(EventWorkflowAgentFailed, map[string]any{
			"label": label,
			"phase": phase,
			"error": err.Error(),
		})
		return nil, err
	}

	// Wait for the subagent to complete by polling its status. Each
	// iteration blocks on either context cancellation or the poll
	// interval elapsing — it must never spin, since this loop runs for
	// the entire lifetime of every in-flight subagent.
	completed := false
	var finalResult SubagentResult
	for !completed {
		finalResult, err = c.engine.subagents.Get(c.ctx, result.ID)
		if err != nil {
			c.emit(EventWorkflowAgentFailed, map[string]any{
				"label": label,
				"phase": phase,
				"error": err.Error(),
			})
			return nil, err
		}
		switch finalResult.Status {
		case "completed", "failed", "cancelled":
			completed = true
		}
		if !completed {
			// Backoff polling — in production this would use streaming events.
			select {
			case <-c.ctx.Done():
				return nil, c.ctx.Err()
			case <-time.After(subagentPollInterval):
			}
		}
	}

	agentResult := &AgentResult{
		Output: finalResult.Output,
	}

	// If the subagent itself failed or errored, record it and return the error.
	// Per Claude Code semantics, agent() errors propagate: return nil, not a
	// partial result.
	if finalResult.Status == "failed" || finalResult.Error != "" {
		agentResult.Error = finalResult.Error
		if agentResult.Error == "" {
			agentResult.Error = "subagent failed"
		}
		c.emit(EventWorkflowAgentFailed, map[string]any{
			"label":  label,
			"phase":  phase,
			"error":  agentResult.Error,
			"output": truncate(agentResult.Output, 500),
		})
		c.trackResult(agentResult)
		return nil, fmt.Errorf("agent %q failed: %s", label, agentResult.Error)
	}

	// If schema is provided, validate and parse structured output.
	if opts.Schema != nil {
		parsed, schemaErr := ParseStructuredOutput(agentResult.Output, opts.Schema)
		if schemaErr != nil {
			agentResult.Error = schemaErr.Error()
			c.emit(EventWorkflowAgentFailed, map[string]any{
				"label":       label,
				"phase":       phase,
				"error":       schemaErr.Error(),
				"schemaError": true,
			})
			c.trackResult(agentResult)
			return nil, fmt.Errorf("schema validation for agent %q: %w", label, schemaErr)
		}
		agentResult.Schema = parsed
	}

	c.emit(EventWorkflowAgentCompleted, map[string]any{
		"label":     label,
		"phase":     phase,
		"hasSchema": opts.Schema != nil,
	})

	c.trackResult(agentResult)
	return agentResult, nil
}

// trackResult appends an agent result to the context's accumulated results.
// It is safe for concurrent use.
func (c *Context) trackResult(ar *AgentResult) {
	c.mu.Lock()
	c.results = append(c.results, *ar)
	c.mu.Unlock()
}

// Parallel runs tasks concurrently with a barrier — it waits for ALL tasks
// before returning. This mirrors Claude Code's parallel() function.
//
// A thunk that panics or returns an error resolves to nil in the result slice.
// The function itself never returns an error; callers filter nils from results
// with a simple loop.
//
// Concurrency is limited by the engine's maxConcurrency (default min(16, cpu-2)).
func (c *Context) Parallel(thunks []func() (any, error)) ([]any, error) {
	if len(thunks) == 0 {
		return nil, nil
	}

	results := make([]any, len(thunks))
	var wg sync.WaitGroup

	for i, thunk := range thunks {
		wg.Add(1)
		go func(idx int, t func() (any, error)) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					results[idx] = nil
				}
			}()

			// Semaphore is NOT acquired here: only agent() calls use it.
			// This prevents deadlocks when thunks themselves call agent().

			val, err := t()
			if err != nil {
				results[idx] = nil
				return
			}
			results[idx] = val
		}(i, thunk)
	}

	wg.Wait()

	// Per Claude Code semantics, Parallel never returns an error.
	// Callers filter nils from the results slice.
	return results, nil
}

// Pipeline runs each item through all stages independently, with NO barrier
// between stages. Item A can be in stage 3 while item B is still in stage 1.
// This is the DEFAULT orchestration pattern and mirrors Claude Code's pipeline().
//
// A stage that returns an error drops that item to nil and skips its remaining
// stages. All items must complete all (non-skipped) stages before Pipeline returns.
//
// Results are returned in original item order. nil entries indicate items that
// were dropped due to stage errors.
func (c *Context) Pipeline(items []any, stages ...PipelineStage) ([]any, error) {
	if len(items) == 0 || len(stages) == 0 {
		return nil, nil
	}

	// Each item runs through stages independently.
	results := make([]any, len(items))
	var wg sync.WaitGroup

	for i, item := range items {
		wg.Add(1)
		go func(idx int, it any) {
			defer wg.Done()
			var prev any // nil for the first stage

			for stageIdx, stage := range stages {
				// Check for context cancellation before each stage.
				select {
				case <-c.ctx.Done():
					results[idx] = nil
					return
				default:
				}

				// Run this stage.
				val, err := c.runStage(prev, it, idx, stageIdx, stage)
				if err != nil {
					results[idx] = nil
					return // drop item, skip remaining stages
				}
				prev = val
				// Continue to next stage immediately — no barrier.
			}
			results[idx] = prev
		}(i, item)
	}

	wg.Wait()

	// If the context was cancelled, surface the error. Otherwise Pipeline
	// never returns an error — stage failures are captured as nil entries.
	if err := c.ctx.Err(); err != nil {
		return results, err
	}
	return results, nil
}

// runStage executes a single pipeline stage for a single item with concurrency
// limiting and panic recovery. A panic in the stage is converted to an error,
// which causes the Pipeline to drop that item.
func (c *Context) runStage(prev any, item any, idx int, stageIdx int, stage PipelineStage) (result any, err error) {
	// Semaphore is NOT acquired here: only agent() calls use it.
	// This prevents deadlocks when pipeline stages call agent().

	// Recover panics and convert them to errors so the pipeline drops the item.
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("pipeline stage %d panic: %v", stageIdx, r)
		}
	}()

	return stage(prev, item, idx)
}

// Phase starts a new progress phase. Subsequent agent() calls and log()
// messages are grouped under this phase until the next Phase() call.
func (c *Context) Phase(title string) {
	c.mu.Lock()
	c.phase = title
	c.mu.Unlock()
	c.emit(EventWorkflowPhaseStarted, map[string]any{
		"phase": title,
	})
}

// Log emits a progress message. In Claude Code, this appears as a narrator
// line above the progress tree.
func (c *Context) Log(message string) {
	c.emit(EventWorkflowLog, map[string]any{
		"message": message,
	})
}

// Feedback emits structured progress, findings, warnings, or debug messages
// back to the parent agent and API subscribers.
func (c *Context) Feedback(kind, message string, data map[string]any) {
	if kind == "" {
		kind = "progress"
	}
	eventType := EventWorkflowFeedback
	switch kind {
	case "finding":
		eventType = EventWorkflowFinding
	case "warning":
		eventType = EventWorkflowWarning
	}
	payload := map[string]any{
		"kind":              kind,
		"message":           message,
		"requires_response": false,
	}
	if data != nil {
		payload["data"] = data
	}
	c.emit(eventType, payload)
}

// Question emits a structured question and asks the engine's configured
// responder for an answer. Without a responder the question is visible to
// subscribers and the workflow receives a clear error.
func (c *Context) Question(prompt string, choices []QuestionOption) (any, error) {
	callID := "workflow_question_" + uuid.NewString()
	payload := map[string]any{
		"kind":              "question",
		"message":           prompt,
		"requires_response": true,
		"call_id":           callID,
		"choices":           choices,
	}
	c.emit(EventWorkflowQuestion, payload)
	if c.engine.questions == nil {
		return nil, fmt.Errorf("workflow question responder is not configured")
	}
	return c.engine.questions.AskWorkflowQuestion(c.ctx, QuestionRequest{
		RunID:   c.runID,
		CallID:  callID,
		Prompt:  prompt,
		Choices: choices,
	})
}

// Workflow runs a nested workflow by name. The nested workflow shares this
// context's budget (via Clone) and concurrency pool.
//
// Returns the nested workflow's result. Errors from the nested workflow
// are propagated.
func (c *Context) Workflow(name string, args any) (any, error) {
	// c.engine.scripts is mutated by Engine.Register from arbitrary
	// goroutines, so it must only be read under c.engine.mu — exactly as
	// Engine.Start does. Copy the value out while holding the lock, then
	// unlock before proceeding.
	c.engine.mu.Lock()
	meta, ok := c.engine.scripts[name]
	c.engine.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("nested workflow %q not found", name)
	}

	nestedBudget := c.Budget.Clone()
	nestedCtx := newContext(c.ctx, c.engine, c.runID, args, nestedBudget)
	nestedCtx.phase = meta.Meta.Name

	return c.engine.executeScript(nestedCtx, meta)
}

// emit sends a workflow event to subscribers.
func (c *Context) emit(evtType EventType, payload map[string]any) {
	c.engine.emit(c.runID, evtType, payload)
}

// maxConcurrency returns the default maximum concurrency for agent calls.
// Matches Claude Code: min(16, cpu_cores - 2), minimum 1.
func maxConcurrency() int {
	n := runtime.NumCPU() - 2
	if n < 1 {
		n = 1
	}
	if n > 16 {
		n = 16
	}
	return n
}
