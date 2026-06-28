package harness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// panicProvider is a Provider that panics on every call.
type panicProvider struct {
	message string
}

func (p *panicProvider) Complete(_ context.Context, _ CompletionRequest) (CompletionResult, error) {
	panic(p.message)
}

// TestPanicInProviderEmitsRunFailed verifies that a panic inside the provider
// call within execute() is recovered and results in a run.failed event with
// an "internal panic: ..." error message, rather than crashing the process.
func TestPanicInProviderEmitsRunFailed(t *testing.T) {
	t.Parallel()

	runner := NewRunner(&panicProvider{message: "boom from provider"}, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     4,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "trigger panic"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// Must have a run.failed event
	var failedEvent *Event
	for i := range events {
		if events[i].Type == EventRunFailed {
			failedEvent = &events[i]
			break
		}
	}
	if failedEvent == nil {
		t.Fatalf("expected run.failed event, got events: %v", eventTypes(events))
	}

	// The error message must contain "internal panic:" prefix
	errMsg, ok := failedEvent.Payload["error"].(string)
	if !ok {
		t.Fatalf("run.failed event missing 'error' string field: %+v", failedEvent.Payload)
	}
	if !strings.HasPrefix(errMsg, "internal panic:") {
		t.Fatalf("expected error to start with 'internal panic:', got: %q", errMsg)
	}
	if !strings.Contains(errMsg, "boom from provider") {
		t.Fatalf("expected error to contain panic value 'boom from provider', got: %q", errMsg)
	}

	// Run must be in failed status
	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state to exist")
	}
	if state.Status != RunStatusFailed {
		t.Fatalf("expected RunStatusFailed, got %q", state.Status)
	}
}

// TestPanicInToolHandlerEmitsRunFailed verifies that a panic inside a tool
// handler during execute() is recovered and results in a run.failed event.
func TestPanicInToolHandlerEmitsRunFailed(t *testing.T) {
	t.Parallel()

	// Provider returns a single tool call, then we'll see the panic
	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-panic",
				Name:      "panic_tool",
				Arguments: `{}`,
			}},
		},
	}}

	registry := NewRegistry()
	err := registry.Register(ToolDefinition{
		Name:        "panic_tool",
		Description: "a tool that panics",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		panic("tool handler exploded")
	})
	if err != nil {
		t.Fatalf("register tool: %v", err)
	}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     4,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "trigger tool panic"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	var failedEvent *Event
	for i := range events {
		if events[i].Type == EventRunFailed {
			failedEvent = &events[i]
			break
		}
	}
	if failedEvent == nil {
		t.Fatalf("expected run.failed event, got events: %v", eventTypes(events))
	}

	errMsg, ok := failedEvent.Payload["error"].(string)
	if !ok {
		t.Fatalf("run.failed event missing 'error' string field: %+v", failedEvent.Payload)
	}
	if !strings.HasPrefix(errMsg, "internal panic:") {
		t.Fatalf("expected error to start with 'internal panic:', got: %q", errMsg)
	}
	if !strings.Contains(errMsg, "tool handler exploded") {
		t.Fatalf("expected error to contain panic value 'tool handler exploded', got: %q", errMsg)
	}
}

// TestRunnerAcceptsNewRunsAfterPanic verifies that after a run panics and is
// recovered, the runner continues to accept and complete new runs successfully.
func TestRunnerAcceptsNewRunsAfterPanic(t *testing.T) {
	t.Parallel()

	// First run will use a panic provider
	panicProv := &panicProvider{message: "panic in run 1"}
	// Second run will use a normal provider
	normalProv := &stubProvider{turns: []CompletionResult{
		{Content: "success after panic"},
	}}

	// Use a runner with the panic provider first
	runner1 := NewRunner(panicProv, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	run1, err := runner1.StartRun(RunRequest{Prompt: "first run - will panic"})
	if err != nil {
		t.Fatalf("start run 1: %v", err)
	}

	// Wait for run1 to finish (it should fail due to panic)
	events1, err := collectRunEvents(t, runner1, run1.ID)
	if err != nil {
		t.Fatalf("collect run 1 events: %v", err)
	}

	// Verify run1 failed
	if !hasTerminalEvent(events1) {
		t.Fatalf("expected run 1 to terminate")
	}
	var found bool
	for _, ev := range events1 {
		if ev.Type == EventRunFailed {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected run 1 to emit run.failed, got: %v", eventTypes(events1))
	}

	// Now use a runner with the normal provider — it must work fine
	runner2 := NewRunner(normalProv, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	run2, err := runner2.StartRun(RunRequest{Prompt: "second run - should succeed"})
	if err != nil {
		t.Fatalf("start run 2: %v", err)
	}

	events2, err := collectRunEvents(t, runner2, run2.ID)
	if err != nil {
		t.Fatalf("collect run 2 events: %v", err)
	}

	state2, ok := runner2.GetRun(run2.ID)
	if !ok {
		t.Fatalf("expected run 2 state to exist")
	}
	if state2.Status != RunStatusCompleted {
		t.Fatalf("expected run 2 to complete, got %q; events: %v", state2.Status, eventTypes(events2))
	}
}

func TestPoolDispatcherRecoverKeepsDispatchAlive(t *testing.T) {
	holdRelease := make(chan struct{})
	provider := &promptGateProvider{
		gates: map[string]<-chan struct{}{
			"hold": holdRelease,
		},
	}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:   "gpt-4.1-mini",
		WorkerPoolSize: 1,
	})
	runner.poolDispatchHook = func(item queuedRun) {
		if item.req.Prompt == "panic" {
			panic("dispatcher hook panic")
		}
	}

	holdRun, err := runner.StartRun(RunRequest{Prompt: "hold"})
	if err != nil {
		t.Fatalf("StartRun hold: %v", err)
	}
	waitForStatus(t, runner, holdRun.ID, RunStatusRunning)

	panicRun, err := runner.StartRun(RunRequest{Prompt: "panic"})
	if err != nil {
		t.Fatalf("StartRun panic: %v", err)
	}
	afterOne, err := runner.StartRun(RunRequest{Prompt: "after-one"})
	if err != nil {
		t.Fatalf("StartRun after-one: %v", err)
	}
	afterTwo, err := runner.StartRun(RunRequest{Prompt: "after-two"})
	if err != nil {
		t.Fatalf("StartRun after-two: %v", err)
	}

	close(holdRelease)
	waitForStatus(t, runner, holdRun.ID, RunStatusCompleted)
	waitForStatus(t, runner, panicRun.ID, RunStatusFailed)
	waitForStatus(t, runner, afterOne.ID, RunStatusCompleted)
	waitForStatus(t, runner, afterTwo.ID, RunStatusCompleted)

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- runner.Shutdown(context.Background())
	}()
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown hung; dispatcher panic leaked an inflight count")
	}
}

// TestPanicWithNonStringValue verifies that a non-string panic value (e.g. an
// integer or error) is still captured in the run.failed event.
func TestPanicWithNonStringValue(t *testing.T) {
	t.Parallel()

	// Provider that panics with an integer value
	provider := &structuredPanicProvider{value: 42}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "trigger int panic"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	var failedEvent *Event
	for i := range events {
		if events[i].Type == EventRunFailed {
			failedEvent = &events[i]
			break
		}
	}
	if failedEvent == nil {
		t.Fatalf("expected run.failed event, got events: %v", eventTypes(events))
	}

	errMsg, ok := failedEvent.Payload["error"].(string)
	if !ok {
		t.Fatalf("run.failed event missing 'error' string field: %+v", failedEvent.Payload)
	}
	if !strings.HasPrefix(errMsg, "internal panic:") {
		t.Fatalf("expected error to start with 'internal panic:', got: %q", errMsg)
	}
}

// structuredPanicProvider panics with a non-string value.
type structuredPanicProvider struct {
	value any
}

func (p *structuredPanicProvider) Complete(_ context.Context, _ CompletionRequest) (CompletionResult, error) {
	panic(p.value)
}
