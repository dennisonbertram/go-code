package harness

import (
	"strings"
	"testing"
)

// TestEmptyResponseRetry_RetriesAndContinues verifies that when the LLM
// returns an empty response (no text, no tool calls), the runner injects
// a retry prompt and continues the step loop instead of terminating.
// After maxEmptyRetries consecutive empty responses the run terminates.
func TestEmptyResponseRetry_RetriesAndContinues(t *testing.T) {
	t.Parallel()

	// First two turns are empty (simulating Gemini 2.5 Flash thinking mode).
	// Third turn returns actual content so the run completes normally.
	provider := &stubProvider{turns: []CompletionResult{
		{Content: "", ToolCalls: nil}, // empty turn 1
		{Content: "", ToolCalls: nil}, // empty turn 2
		{Content: "Done after retries"},
	}}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:        "gemini-2.5-flash",
		DefaultSystemPrompt: "You are a test assistant.",
		MaxSteps:            10,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Do something"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// Provider should have been called 3 times (2 empty + 1 real).
	if provider.calls != 3 {
		t.Fatalf("expected provider to be called 3 times, got %d", provider.calls)
	}

	// Run should complete successfully.
	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("expected run state")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected completed status, got %q", state.Status)
	}
	if state.Output != "Done after retries" {
		t.Fatalf("unexpected output: %q", state.Output)
	}

	// There should be exactly 2 EventEmptyResponseRetry events.
	retryCount := 0
	for _, ev := range events {
		if ev.Type == EventEmptyResponseRetry {
			retryCount++
			// Verify payload fields.
			if _, ok := ev.Payload["step"]; !ok {
				t.Error("EventEmptyResponseRetry missing 'step' field")
			}
			if _, ok := ev.Payload["retry"]; !ok {
				t.Error("EventEmptyResponseRetry missing 'retry' field")
			}
			if maxRetries, ok := ev.Payload["max_retries"]; !ok {
				t.Error("EventEmptyResponseRetry missing 'max_retries' field")
			} else if maxRetries != maxEmptyRetries {
				t.Errorf("max_retries = %v, want %d", maxRetries, maxEmptyRetries)
			}
		}
	}
	if retryCount != 2 {
		t.Errorf("expected 2 EventEmptyResponseRetry events, got %d", retryCount)
	}
}

// TestEmptyResponseRetry_MaxRetriesExhausted verifies that after
// maxEmptyRetries consecutive empty responses the run fails explicitly
// instead of silently completing with empty output.
func TestEmptyResponseRetry_MaxRetriesExhausted(t *testing.T) {
	t.Parallel()

	// All turns are empty — exhausts maxEmptyRetries and terminates.
	emptyTurns := make([]CompletionResult, maxEmptyRetries+1)
	provider := &stubProvider{turns: emptyTurns}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gemini-2.5-flash",
		MaxSteps:     20,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Do something"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// Run must reach a terminal state.
	if !hasTerminalEvent(events) {
		t.Fatal("expected terminal event, run appears to be stuck")
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("expected run state")
	}
	if state.Status != RunStatusFailed {
		t.Fatalf("expected failed status after max retries exhausted, got %q", state.Status)
	}
	if !strings.Contains(state.Error, "max_empty_responses") {
		t.Fatalf("expected max_empty_responses error, got %q", state.Error)
	}

	// There should be exactly maxEmptyRetries-1 retry events; the final empty
	// response fails the run instead of scheduling another retry.
	retryCount := 0
	for _, ev := range events {
		if ev.Type == EventEmptyResponseRetry {
			retryCount++
		}
	}
	if retryCount != maxEmptyRetries-1 {
		t.Errorf("expected %d EventEmptyResponseRetry events, got %d", maxEmptyRetries-1, retryCount)
	}
}

// TestEmptyResponseRetry_DoesNotConsumeStepBudget verifies empty-response
// retries do not consume outer step budget. A run with MaxSteps=1 can still
// recover after retryable empty responses.
func TestEmptyResponseRetry_DoesNotConsumeStepBudget(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{
		{Content: "", ToolCalls: nil},
		{Content: "", ToolCalls: nil},
		{Content: "Recovered within the first step"},
	}}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gemini-2.5-flash",
		MaxSteps:     1,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Do something"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	if _, err := collectRunEvents(t, runner, run.ID); err != nil {
		t.Fatalf("collect events: %v", err)
	}
	if provider.calls != 3 {
		t.Fatalf("expected provider to be called 3 times, got %d", provider.calls)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("expected run state")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected completed status, got %q with error %q", state.Status, state.Error)
	}
	if state.Output != "Recovered within the first step" {
		t.Fatalf("unexpected output: %q", state.Output)
	}
}

// TestEmptyResponseRetry_SingleEmptyThenContent verifies that a single empty
// response followed by a real text response fires one retry event, then the
// run completes normally with the real content.
func TestEmptyResponseRetry_SingleEmptyThenContent(t *testing.T) {
	t.Parallel()

	// First turn is empty (retry), second turn is real content (completes run).
	provider := &stubProvider{turns: []CompletionResult{
		{Content: "", ToolCalls: nil}, // empty turn — inject retry prompt
		{Content: "Completed after retry"},
	}}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gemini-2.5-flash",
		MaxSteps:     10,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Do something"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// Provider called twice.
	if provider.calls != 2 {
		t.Fatalf("expected provider to be called 2 times, got %d", provider.calls)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("expected run state")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected completed status, got %q", state.Status)
	}
	if state.Output != "Completed after retry" {
		t.Fatalf("unexpected output: %q", state.Output)
	}

	// Exactly 1 retry event.
	retryCount := 0
	for _, ev := range events {
		if ev.Type == EventEmptyResponseRetry {
			retryCount++
		}
	}
	if retryCount != 1 {
		t.Errorf("expected 1 EventEmptyResponseRetry event, got %d", retryCount)
	}
}
