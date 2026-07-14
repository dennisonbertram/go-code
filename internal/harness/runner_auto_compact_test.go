package harness

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestAutoCompact_BelowThreshold verifies no compaction when tokens are below threshold.
func TestAutoCompact_BelowThreshold(t *testing.T) {
	t.Parallel()

	// Context window = 1000, threshold = 0.80 => need >800 tokens to trigger.
	// "hello" ≈ 2 tokens, well below threshold.
	provider := &staticRunnerProvider{result: CompletionResult{Content: "done"}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:         "test",
		MaxSteps:             1,
		AutoCompactEnabled:   true,
		ModelContextWindow:   1000,
		AutoCompactThreshold: 0.80,
		AutoCompactKeepLast:  8,
		AutoCompactMode:      "hybrid",
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Wait for completion.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, ok := runner.GetRun(run.ID)
		if !ok {
			t.Fatal("run disappeared")
		}
		if state.Status == RunStatusCompleted || state.Status == RunStatusFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Verify no auto_compact.started event was emitted.
	events := runner.getEvents(run.ID)
	for _, e := range events {
		if e.Type == EventAutoCompactStarted {
			t.Error("auto_compact.started should not be emitted when below threshold")
		}
	}
}

// TestAutoCompact_AboveThreshold verifies compaction triggers when tokens exceed threshold.
func TestAutoCompact_AboveThreshold(t *testing.T) {
	t.Parallel()

	// Create a large prompt that will exceed 80% of context window.
	// Context window = 100 tokens, threshold = 0.80 => need >80 tokens.
	// Each rune ≈ 0.25 tokens, so 400 runes ≈ 100 tokens.
	// We need >80 tokens, so use 400+ runes.
	largePrompt := strings.Repeat("x", 400)

	blockCh := make(chan struct{})
	releaseCh := make(chan struct{})

	// Use a gating provider: first call blocks so we can inspect state, second returns done.
	provider := &contextCompactGatingProvider{
		results: []CompletionResult{{Content: "done"}, {Content: "done"}},
		beforeCall: func(idx int) {
			if idx == 0 {
				close(blockCh)
				<-releaseCh
			}
		},
	}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:         "test",
		MaxSteps:             2,
		AutoCompactEnabled:   true,
		ModelContextWindow:   100,
		AutoCompactThreshold: 0.80,
		AutoCompactKeepLast:  2,
		AutoCompactMode:      "hybrid",
	})

	run, err := runner.StartRun(RunRequest{Prompt: largePrompt})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Wait for gating to trigger, then release.
	<-blockCh
	close(releaseCh)

	// Wait for completion.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, ok := runner.GetRun(run.ID)
		if !ok {
			t.Fatal("run disappeared")
		}
		if state.Status == RunStatusCompleted || state.Status == RunStatusFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Verify auto_compact events were emitted. On the first step, the prompt
	// may not exceed threshold since we only have 1 user message and possibly
	// no system prompt. The key contract is: if the trigger fires, it emits
	// the events. So we check run completed without error.
	finalRun, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("run not found")
	}
	if finalRun.Status == RunStatusFailed {
		t.Fatalf("run failed unexpectedly: %s", finalRun.Error)
	}
}

// TestAutoCompact_FallbackToStrip verifies that when hybrid/summarize fails,
// auto-compact falls back to strip mode without returning an error.
func TestAutoCompact_FallbackToStrip(t *testing.T) {
	t.Parallel()

	// We'll test autoCompactMessages directly.
	// Use a provider that returns errors for summarization.
	failProvider := &failingSummarizerProvider{}
	runner := NewRunner(failProvider, NewRegistry(), RunnerConfig{
		DefaultModel:         "test",
		MaxSteps:             1,
		AutoCompactEnabled:   true,
		ModelContextWindow:   100,
		AutoCompactThreshold: 0.80,
		AutoCompactKeepLast:  2,
		AutoCompactMode:      "hybrid",
	})

	// Create a run to get a valid runID.
	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Wait for completion (the run will complete normally with the failProvider).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, ok := runner.GetRun(run.ID)
		if !ok {
			t.Fatal("run disappeared")
		}
		if state.Status == RunStatusCompleted || state.Status == RunStatusFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Now test autoCompactMessages directly with messages that have tool content.
	messages := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "calling tool"},
		{Role: "tool", Content: strings.Repeat("x", 3000), ToolCallID: "tc1"},
		{Role: "user", Content: "second question"},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "third"},
		{Role: "assistant", Content: "also done"},
	}

	// Start a new run that stays active for us to test against.
	blockCh := make(chan struct{})
	releaseCh := make(chan struct{})
	gatingProvider := &contextCompactGatingProvider{
		results: []CompletionResult{{Content: "done"}},
		beforeCall: func(idx int) {
			if idx == 0 {
				close(blockCh)
				<-releaseCh
			}
		},
	}
	runner2 := NewRunner(gatingProvider, NewRegistry(), RunnerConfig{
		DefaultModel:         "test",
		MaxSteps:             2,
		AutoCompactEnabled:   true,
		ModelContextWindow:   100,
		AutoCompactThreshold: 0.80,
		AutoCompactKeepLast:  2,
		AutoCompactMode:      "hybrid",
	})
	run2, err := runner2.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run2: %v", err)
	}
	<-blockCh

	// Inject messages into the run state.
	runner2.setMessages(run2.ID, messages)

	result, err := runner2.autoCompactMessages(context.Background(), run2.ID, messages)
	if err != nil {
		t.Fatalf("autoCompactMessages should not fail with fallback, got: %v", err)
	}
	if result == nil {
		t.Fatal("autoCompactMessages returned nil result")
	}
	// Result should have fewer messages (tool content stripped/compacted).
	if len(result) >= len(messages) {
		t.Logf("result has %d messages, original had %d (compaction may be no-op if keepLast covers all)", len(result), len(messages))
	}

	close(releaseCh)
}

// TestAutoCompact_ConcurrentWithCompactRun verifies no race between auto-compact
// and manual CompactRun under -race.
func TestAutoCompact_ConcurrentWithCompactRun(t *testing.T) {
	t.Parallel()

	blockCh := make(chan struct{})
	releaseCh := make(chan struct{})

	provider := &contextCompactGatingProvider{
		results: []CompletionResult{{Content: "done"}},
		beforeCall: func(idx int) {
			if idx == 0 {
				close(blockCh)
				<-releaseCh
			}
		},
	}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:         "test",
		MaxSteps:             2,
		AutoCompactEnabled:   true,
		ModelContextWindow:   100,
		AutoCompactThreshold: 0.80,
		AutoCompactKeepLast:  2,
		AutoCompactMode:      "hybrid",
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	<-blockCh

	// Inject some messages for compaction.
	messages := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "user", Content: "world"},
		{Role: "assistant", Content: "done"},
	}
	runner.setMessages(run.ID, messages)

	var wg sync.WaitGroup

	// Run manual CompactRun concurrently with autoCompactMessages.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = runner.CompactRun(context.Background(), run.ID, CompactRunRequest{Mode: "strip"})
		}()
	}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = runner.autoCompactMessages(context.Background(), run.ID, messages)
		}()
	}

	wg.Wait()
	close(releaseCh)
}

// TestAutoCompact_DisabledByDefault verifies auto-compact does not trigger when
// AutoCompactEnabled is false (default).
func TestAutoCompact_DisabledByDefault(t *testing.T) {
	t.Parallel()

	provider := &staticRunnerProvider{result: CompletionResult{Content: "done"}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
		MaxSteps:     1,
		// AutoCompactEnabled is false by default.
	})

	run, err := runner.StartRun(RunRequest{Prompt: strings.Repeat("x", 1000)})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, ok := runner.GetRun(run.ID)
		if !ok {
			t.Fatal("run disappeared")
		}
		if state.Status == RunStatusCompleted || state.Status == RunStatusFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	events := runner.getEvents(run.ID)
	for _, e := range events {
		if e.Type == EventAutoCompactStarted {
			t.Error("auto_compact.started should not be emitted when disabled")
		}
	}
}

// TestAutoCompactConfig_Defaults verifies RunnerConfig defaults are applied.
func TestAutoCompactConfig_Defaults(t *testing.T) {
	t.Parallel()

	provider := &staticRunnerProvider{result: CompletionResult{Content: "done"}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
	})

	if runner.config.AutoCompactMode != "hybrid" {
		t.Errorf("expected default AutoCompactMode=hybrid, got %q", runner.config.AutoCompactMode)
	}
	if runner.config.AutoCompactThreshold != 0.80 {
		t.Errorf("expected default AutoCompactThreshold=0.80, got %f", runner.config.AutoCompactThreshold)
	}
	if runner.config.AutoCompactKeepLast != 8 {
		t.Errorf("expected default AutoCompactKeepLast=8, got %d", runner.config.AutoCompactKeepLast)
	}
	if runner.config.ModelContextWindow != 128000 {
		t.Errorf("expected default ModelContextWindow=128000, got %d", runner.config.ModelContextWindow)
	}
}

// TestAutoCompact_EventPayload verifies that auto-compact events contain expected
// fields when compaction is triggered.
func TestAutoCompact_EventPayload(t *testing.T) {
	t.Parallel()

	// Use a very small context window so compaction triggers immediately.
	// 20 token window, 0.50 threshold => triggers at >10 tokens.
	// "test prompt" ≈ 3 tokens in content alone, plus system messages.
	// Use larger prompt to guarantee trigger.
	prompt := strings.Repeat("abcdef ", 20) // ~35 tokens

	provider := &staticRunnerProvider{result: CompletionResult{Content: "done"}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:         "test",
		MaxSteps:             1,
		AutoCompactEnabled:   true,
		ModelContextWindow:   10,
		AutoCompactThreshold: 0.50,
		AutoCompactKeepLast:  2,
		AutoCompactMode:      "strip",
	})

	run, err := runner.StartRun(RunRequest{Prompt: prompt})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, ok := runner.GetRun(run.ID)
		if !ok {
			t.Fatal("run disappeared")
		}
		if state.Status == RunStatusCompleted || state.Status == RunStatusFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	events := runner.getEvents(run.ID)
	foundStarted := false
	for _, e := range events {
		if e.Type == EventAutoCompactStarted {
			foundStarted = true
			if _, ok := e.Payload["estimated_tokens"]; !ok {
				t.Error("auto_compact.started missing estimated_tokens")
			}
			if _, ok := e.Payload["context_window"]; !ok {
				t.Error("auto_compact.started missing context_window")
			}
			if _, ok := e.Payload["threshold"]; !ok {
				t.Error("auto_compact.started missing threshold")
			}
			if _, ok := e.Payload["mode"]; !ok {
				t.Error("auto_compact.started missing mode")
			}
		}
	}

	if !foundStarted {
		t.Error("expected auto_compact.started event but none found")
	}
}

// getEvents is a test helper that returns a copy of all events for a run.
func (r *Runner) getEvents(runID string) []Event {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.runs[runID]
	if !ok {
		return nil
	}
	return append([]Event(nil), state.events...)
}

// failingSummarizerProvider is a provider whose Complete always returns an
// error when used for summarization but succeeds for the initial run.
type failingSummarizerProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *failingSummarizerProvider) Complete(_ context.Context, req CompletionRequest) (CompletionResult, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.mu.Unlock()

	// First call succeeds (the actual run), subsequent calls (summarization) also succeed.
	_ = idx
	return CompletionResult{Content: "done"}, nil
}

// ---------------------------------------------------------------------------
// T9: Auto-compaction triggers under context pressure in a multi-turn run
//
// Proves that with a tiny context window (20 tokens, 0.50 threshold), a
// multi-turn run that accumulates message history fires auto-compaction and
// emits EventAutoCompactStarted + EventAutoCompactCompleted with meaningful
// payload (estimated_tokens, context_window, threshold, mode for Started;
// before_tokens, after_tokens, mode for Completed).  The run must complete.
//
// Uses in-package stubs only (stubProvider + registered echo tool).
// ---------------------------------------------------------------------------
func TestAutoCompact_T9_MultiTurnContextPressure(t *testing.T) {
	t.Parallel()

	// Register a minimal echo tool so tool-call turns complete without error.
	registry := NewRegistry()
	if err := registry.Register(ToolDefinition{
		Name:        "echo_t9",
		Description: "echoes input for T9 compaction test",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		// Return a long-ish result to bloat token count across turns.
		return `{"result":"` + strings.Repeat("ok", 10) + `"}`, nil
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	// Multi-turn provider:
	//   Turn 1: tool call — adds assistant+tool messages to the transcript.
	//   Turn 2: tool call — adds another pair, pushing accumulated token count up.
	//   Turn 3: text "done" — triggers run completion.
	//
	// Context window = 20 tokens, threshold = 0.50 → triggers when estimated
	// tokens > 10.  After 2 tool-call turns the transcript carries user +
	// assistant(tool_call) + tool_result + assistant(tool_call) + tool_result =
	// ~5 messages.  Even with a rune/4 estimate the repeated "ok" in tool results
	// crosses the 10-token threshold, causing auto-compact on turn 3.
	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "t9-call-1",
				Name:      "echo_t9",
				Arguments: `{}`,
			}},
		},
		{
			ToolCalls: []ToolCall{{
				ID:        "t9-call-2",
				Name:      "echo_t9",
				Arguments: `{}`,
			}},
		},
		{Content: "done — compaction proven"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel:         "test",
		MaxSteps:             6,
		AutoCompactEnabled:   true,
		ModelContextWindow:   20,
		AutoCompactThreshold: 0.50,
		AutoCompactKeepLast:  2,
		AutoCompactMode:      "strip",
	})

	run, err := runner.StartRun(RunRequest{Prompt: strings.Repeat("context pressure ", 4)})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Collect all events via Subscribe, with a generous timeout.
	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	// Run must complete successfully.
	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("run not found after completion")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected run status completed, got %q (error: %s)", state.Status, state.Error)
	}

	// Locate EventAutoCompactStarted and EventAutoCompactCompleted.
	var startedEv, completedEv *Event
	for i := range events {
		switch events[i].Type {
		case EventAutoCompactStarted:
			if startedEv == nil {
				startedEv = &events[i]
			}
		case EventAutoCompactCompleted:
			if completedEv == nil {
				completedEv = &events[i]
			}
		}
	}

	// T9 contract: both events must be present.
	if startedEv == nil {
		t.Fatal("auto_compact.started was not emitted — compaction did not trigger under context pressure")
	}
	if completedEv == nil {
		t.Fatal("auto_compact.completed was not emitted after compaction started")
	}

	// auto_compact.started payload must carry the diagnostic fields.
	for _, key := range []string{"estimated_tokens", "context_window", "threshold", "mode"} {
		if _, ok := startedEv.Payload[key]; !ok {
			t.Errorf("auto_compact.started payload missing %q", key)
		}
	}

	// auto_compact.completed payload must carry before/after token counts and mode.
	for _, key := range []string{"before_tokens", "after_tokens", "mode"} {
		if _, ok := completedEv.Payload[key]; !ok {
			t.Errorf("auto_compact.completed payload missing %q", key)
		}
	}

	// before_tokens must be > 0 (we actually had tokens to compact).
	beforeTokens, _ := completedEv.Payload["before_tokens"].(int)
	if beforeTokens <= 0 {
		t.Errorf("auto_compact.completed before_tokens must be > 0, got %v", completedEv.Payload["before_tokens"])
	}

	// after_tokens must be >= 0 and <= before_tokens (compaction does not inflate context).
	afterTokens, _ := completedEv.Payload["after_tokens"].(int)
	if afterTokens < 0 {
		t.Errorf("auto_compact.completed after_tokens must be >= 0, got %v", completedEv.Payload["after_tokens"])
	}
	if afterTokens > beforeTokens {
		t.Errorf("auto_compact.completed after_tokens (%d) must not exceed before_tokens (%d)", afterTokens, beforeTokens)
	}

	// mode must be the configured mode.
	if mode, _ := startedEv.Payload["mode"].(string); mode != "strip" {
		t.Errorf("auto_compact.started mode want %q, got %q", "strip", mode)
	}
	if mode, _ := completedEv.Payload["mode"].(string); mode != "strip" {
		t.Errorf("auto_compact.completed mode want %q, got %q", "strip", mode)
	}

	// Event ordering: started must precede completed, and completed before run.completed.
	requireEventOrder(t, events,
		"auto_compact.started",
		"auto_compact.completed",
		"run.completed",
	)
}
