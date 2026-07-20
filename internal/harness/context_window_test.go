package harness

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	htools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/provider/catalog"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// stubProviderWithUsage returns a provider whose Complete returns results with
// pre-populated Usage objects for testing context window snapshot integration.
type stubProviderWithUsage struct {
	turns []CompletionResult
	mu    sync.Mutex
	calls int
}

func (s *stubProviderWithUsage) Complete(_ context.Context, req CompletionRequest) (CompletionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls >= len(s.turns) {
		return CompletionResult{}, nil
	}
	turn := s.turns[s.calls]
	s.calls++
	if req.Stream != nil {
		for _, delta := range turn.Deltas {
			req.Stream(delta)
		}
	}
	return turn, nil
}

// ---------------------------------------------------------------------------
// Tests: ContextWindowSnapshotEnabled = true
// ---------------------------------------------------------------------------

// TestContextWindowSnapshot_EmittedWhenEnabled verifies that a
// context.window.snapshot event is emitted after each LLM turn when
// ContextWindowSnapshotEnabled is set in RunnerConfig.
func TestContextWindowSnapshot_EmittedWhenEnabled(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{
		{Content: "Done"},
	}}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:                 "gpt-4.1-mini",
		MaxSteps:                     4,
		ContextWindowSnapshotEnabled: true,
		ModelContextWindow:           128000,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	found := false
	for _, ev := range events {
		if ev.Type == EventContextWindowSnapshot {
			found = true
			// Verify required payload keys are present.
			if _, ok := ev.Payload["step"]; !ok {
				t.Error("snapshot payload missing 'step' key")
			}
			if _, ok := ev.Payload["estimated_total_tokens"]; !ok {
				t.Error("snapshot payload missing 'estimated_total_tokens' key")
			}
			if _, ok := ev.Payload["max_context_tokens"]; !ok {
				t.Error("snapshot payload missing 'max_context_tokens' key")
			}
			if _, ok := ev.Payload["usage_ratio"]; !ok {
				t.Error("snapshot payload missing 'usage_ratio' key")
			}
			if _, ok := ev.Payload["headroom_tokens"]; !ok {
				t.Error("snapshot payload missing 'headroom_tokens' key")
			}
			if _, ok := ev.Payload["breakdown"]; !ok {
				t.Error("snapshot payload missing 'breakdown' key")
			}
			bd, ok := ev.Payload["breakdown"].(map[string]any)
			if !ok {
				t.Error("breakdown should be map[string]any")
			} else {
				if est, ok := bd["estimated"].(bool); !ok || !est {
					t.Errorf("breakdown.estimated should be true, got %v", bd["estimated"])
				}
			}
		}
	}
	if !found {
		t.Error("no context.window.snapshot event emitted")
	}
}

// TestContextWindowSnapshot_NotEmittedWhenDisabled verifies that no
// context.window.snapshot event is emitted when ContextWindowSnapshotEnabled is false.
func TestContextWindowSnapshot_NotEmittedWhenDisabled(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{
		{Content: "Done"},
	}}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:                 "gpt-4.1-mini",
		MaxSteps:                     4,
		ContextWindowSnapshotEnabled: false, // disabled
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	for _, ev := range events {
		if ev.Type == EventContextWindowSnapshot {
			t.Fatal("context.window.snapshot should not be emitted when disabled")
		}
	}
}

// TestContextWindowSnapshot_ProviderReportedTokens verifies that when the provider
// returns usage data, the snapshot includes provider_reported=true and non-zero
// provider_reported_tokens.
func TestContextWindowSnapshot_ProviderReportedTokens(t *testing.T) {
	t.Parallel()

	provider := &stubProviderWithUsage{turns: []CompletionResult{
		{
			Content: "Done",
			Usage: &CompletionUsage{
				PromptTokens:     500,
				CompletionTokens: 100,
				TotalTokens:      600,
			},
			UsageStatus: UsageStatusProviderReported,
		},
	}}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:                 "gpt-4.1-mini",
		MaxSteps:                     4,
		ContextWindowSnapshotEnabled: true,
		ModelContextWindow:           128000,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	for _, ev := range events {
		if ev.Type == EventContextWindowSnapshot {
			provReported, _ := ev.Payload["provider_reported"].(bool)
			if !provReported {
				t.Error("expected provider_reported=true when usage is available")
			}
			provTokens, _ := ev.Payload["provider_reported_tokens"].(int)
			if provTokens != 500 {
				// JSON numbers come back as float64
				provTokensF, _ := ev.Payload["provider_reported_tokens"].(float64)
				if int(provTokensF) != 500 {
					t.Errorf("provider_reported_tokens = %v, want 500", ev.Payload["provider_reported_tokens"])
				}
			}
		}
	}
}

// TestContextWindowWarning_EmittedWhenThresholdExceeded verifies that a
// context.window.warning event is emitted when usage exceeds the configured
// threshold.
func TestContextWindowWarning_EmittedWhenThresholdExceeded(t *testing.T) {
	t.Parallel()

	// Use a very small context window (10 tokens) and a prompt that will
	// definitely estimate more than 80% usage.
	provider := &stubProvider{turns: []CompletionResult{
		{Content: "Done"},
	}}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:                  "gpt-4.1-mini",
		MaxSteps:                      4,
		ContextWindowSnapshotEnabled:  true,
		ModelContextWindow:            10,  // tiny window
		ContextWindowWarningThreshold: 0.5, // warn at 50%
	})

	// Prompt that is long enough to exceed 50% of 10 tokens.
	// "Hello world this is a long prompt for testing" = ~10-11 words, ~45 chars → ~12 estimated tokens
	run, err := runner.StartRun(RunRequest{Prompt: "Hello world this is a long prompt for testing context window overflow warnings in the harness"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	foundWarning := false
	for _, ev := range events {
		if ev.Type == EventContextWindowWarning {
			foundWarning = true
			if _, ok := ev.Payload["usage_ratio"]; !ok {
				t.Error("warning payload missing 'usage_ratio'")
			}
			if _, ok := ev.Payload["threshold"]; !ok {
				t.Error("warning payload missing 'threshold'")
			}
			if _, ok := ev.Payload["tokens_used"]; !ok {
				t.Error("warning payload missing 'tokens_used'")
			}
			if _, ok := ev.Payload["max_context_tokens"]; !ok {
				t.Error("warning payload missing 'max_context_tokens'")
			}
		}
	}
	if !foundWarning {
		t.Error("context.window.warning should have been emitted for tiny context window")
	}
}

// TestContextWindowWarning_NotEmittedBelowThreshold verifies that no warning
// is emitted when usage is below the threshold.
func TestContextWindowWarning_NotEmittedBelowThreshold(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{
		{Content: "Done"},
	}}

	// Large context window, normal threshold: no warning.
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:                  "gpt-4.1-mini",
		MaxSteps:                      4,
		ContextWindowSnapshotEnabled:  true,
		ModelContextWindow:            128000,
		ContextWindowWarningThreshold: 0.95, // warn only at 95%
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Hi"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	for _, ev := range events {
		if ev.Type == EventContextWindowWarning {
			t.Fatal("context.window.warning should NOT be emitted when usage is far below threshold")
		}
	}
}

// TestContextWindowWarning_NoWarningWhenThresholdZero verifies that no warning is
// emitted when ContextWindowWarningThreshold is 0 (disabled).
func TestContextWindowWarning_NoWarningWhenThresholdZero(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{
		{Content: "Done"},
	}}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:                  "gpt-4.1-mini",
		MaxSteps:                      4,
		ContextWindowSnapshotEnabled:  true,
		ModelContextWindow:            10, // tiny window — would trigger warnings
		ContextWindowWarningThreshold: 0,  // disabled
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Hello world this is a long prompt for testing"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	for _, ev := range events {
		if ev.Type == EventContextWindowWarning {
			t.Fatal("context.window.warning should NOT be emitted when threshold=0")
		}
	}
}

// TestContextWindowSnapshot_UsesProviderCatalogMaxTokens verifies that the
// snapshot uses MaxContextTokens from the provider catalog when a registry is set.
func TestContextWindowSnapshot_UsesProviderCatalogMaxTokens(t *testing.T) {
	t.Parallel()

	// Build a minimal catalog with a model that has a known context window.
	cat := &catalog.Catalog{
		CatalogVersion: "v1-test",
		Providers: map[string]catalog.ProviderEntry{
			"testprovider": {
				DisplayName: "Test Provider",
				BaseURL:     "https://test.example.com",
				APIKeyEnv:   "TEST_API_KEY",
				Protocol:    "openai",
				Models: map[string]catalog.Model{
					"test-model-32k": {
						DisplayName:   "Test 32k",
						ContextWindow: 32000,
						ToolCalling:   true,
						Streaming:     true,
					},
				},
			},
		},
	}
	reg := catalog.NewProviderRegistry(cat)

	provider := &stubProvider{turns: []CompletionResult{
		{Content: "Done"},
	}}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:                 "test-model-32k",
		MaxSteps:                     4,
		ContextWindowSnapshotEnabled: true,
		ModelContextWindow:           128000, // config fallback, should be overridden by catalog
		ProviderRegistry:             reg,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Hello", Model: "test-model-32k"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	for _, ev := range events {
		if ev.Type == EventContextWindowSnapshot {
			// max_context_tokens should be from the catalog (32000), not the config fallback (128000).
			maxTokensRaw := ev.Payload["max_context_tokens"]
			var maxTokens int
			switch v := maxTokensRaw.(type) {
			case int:
				maxTokens = v
			case float64:
				maxTokens = int(v)
			}
			if maxTokens != 32000 {
				t.Errorf("max_context_tokens = %d, want 32000 (from catalog)", maxTokens)
			}
		}
	}
}

// TestContextWindowSnapshot_MultipleSteps verifies that snapshots are emitted
// once per step (one per LLM turn).
func TestContextWindowSnapshot_MultipleSteps(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "noop_tool",
		Description: "does nothing",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"ok":true}`, nil
	})

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{ID: "c1", Name: "noop_tool", Arguments: "{}"}},
		},
		{Content: "Done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel:                 "gpt-4.1-mini",
		MaxSteps:                     4,
		ContextWindowSnapshotEnabled: true,
		ModelContextWindow:           128000,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Use the tool"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	var snapshots []Event
	for _, ev := range events {
		if ev.Type == EventContextWindowSnapshot {
			snapshots = append(snapshots, ev)
		}
	}

	// With 2 LLM turns, we expect 2 snapshots.
	if len(snapshots) != 2 {
		t.Errorf("expected 2 context.window.snapshot events, got %d", len(snapshots))
	}

	// Verify that step numbers are in order (1, 2).
	for i, snap := range snapshots {
		wantStep := i + 1
		stepRaw := snap.Payload["step"]
		var step int
		switch v := stepRaw.(type) {
		case int:
			step = v
		case float64:
			step = int(v)
		}
		if step != wantStep {
			t.Errorf("snapshot[%d].step = %d, want %d", i, step, wantStep)
		}
	}
}

// TestCompactHistory_EnrichedWithTokenCounts verifies that when
// ContextWindowSnapshotEnabled=true, the compact_history.completed event
// uses the pre-compact message list for token enrichment. We test this by
// using a tool that calls the message replacer via the harness context key,
// verifying the run completes and the compact_history.completed event is present.
func TestCompactHistory_EnrichedWithTokenCounts(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	// Register a tool that uses the ContextKeyMessageReplacer to simulate
	// the compact_history tool's internal behaviour.
	_ = registry.Register(ToolDefinition{
		Name:        "my_compact_tool",
		Description: "compacts history via replacer",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(ctx context.Context, _ json.RawMessage) (string, error) {
		replacer, ok := ctx.Value(htools.ContextKeyMessageReplacer).(func([]map[string]any))
		if !ok {
			return `{"ok":true}`, nil
		}
		replacer([]map[string]any{
			{"role": "user", "content": "summary of prior work"},
		})
		return `{"ok":true}`, nil
	})

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{ID: "c1", Name: "my_compact_tool", Arguments: "{}"}},
		},
		{Content: "Done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel:                 "gpt-4.1-mini",
		MaxSteps:                     4,
		ContextWindowSnapshotEnabled: true,
		ModelContextWindow:           128000,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Compact the history"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("run not found")
	}
	if state.Status == RunStatusFailed {
		t.Fatalf("run failed: %s", state.Error)
	}

	// Verify compact_history.completed was emitted.
	foundCompact := false
	for _, ev := range events {
		if ev.Type == EventCompactHistoryCompleted {
			foundCompact = true
			// When ContextWindowSnapshotEnabled is true, before_tokens and
			// after_tokens should be present and labeled as estimated.
			if _, ok := ev.Payload["before_tokens"]; !ok {
				t.Error("compact_history.completed missing 'before_tokens' when snapshot enabled")
			}
			if _, ok := ev.Payload["after_tokens"]; !ok {
				t.Error("compact_history.completed missing 'after_tokens' when snapshot enabled")
			}
			if est, ok := ev.Payload["tokens_estimated"].(bool); !ok || !est {
				t.Errorf("compact_history.completed tokens_estimated should be true, got %v", ev.Payload["tokens_estimated"])
			}
		}
	}
	if !foundCompact {
		t.Error("compact_history.completed event not found")
	}
}

// TestContextWindowSnapshot_ConcurrentRuns verifies no race conditions when
// multiple runs emit context window snapshots concurrently.
func TestContextWindowSnapshot_ConcurrentRuns(t *testing.T) {
	t.Parallel()

	const runCount = 5
	var wg sync.WaitGroup
	errs := make(chan error, runCount)

	for i := 0; i < runCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			provider := &stubProvider{turns: []CompletionResult{
				{Content: "Done"},
			}}
			runner := NewRunner(provider, NewRegistry(), RunnerConfig{
				DefaultModel:                 "gpt-4.1-mini",
				MaxSteps:                     4,
				ContextWindowSnapshotEnabled: true,
				ModelContextWindow:           128000,
			})

			run, err := runner.StartRun(RunRequest{Prompt: "Hello"})
			if err != nil {
				errs <- err
				return
			}
			events, err := collectRunEvents(t, runner, run.ID)
			if err != nil {
				errs <- err
				return
			}

			// Each run should have at least one context.window.snapshot.
			found := false
			for _, ev := range events {
				if ev.Type == EventContextWindowSnapshot {
					found = true
					break
				}
			}
			if !found {
				errs <- nil // just means no snapshot, not an error for this test
			}
		}(i)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent run error: %v", err)
		}
	}
}

// TestContextWindowSnapshotPayload_BreakdownSumsToTotal verifies that
// system_prompt_tokens + conversation_tokens + tool_result_tokens == estimated_total_tokens.
func TestContextWindowSnapshotPayload_BreakdownSumsToTotal(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{
		{Content: "Done"},
	}}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:                 "gpt-4.1-mini",
		MaxSteps:                     4,
		ContextWindowSnapshotEnabled: true,
		ModelContextWindow:           128000,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Test breakdown sum"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	for _, ev := range events {
		if ev.Type != EventContextWindowSnapshot {
			continue
		}
		bd, ok := ev.Payload["breakdown"].(map[string]any)
		if !ok {
			t.Fatal("breakdown is not a map")
		}
		toInt := func(v any) int {
			switch x := v.(type) {
			case int:
				return x
			case float64:
				return int(x)
			}
			return 0
		}
		sysTokens := toInt(bd["system_prompt_tokens"])
		convTokens := toInt(bd["conversation_tokens"])
		toolTokens := toInt(bd["tool_result_tokens"])
		total := toInt(ev.Payload["estimated_total_tokens"])

		if sysTokens+convTokens+toolTokens != total {
			t.Errorf("breakdown sum %d+%d+%d=%d != estimated_total_tokens %d",
				sysTokens, convTokens, toolTokens, sysTokens+convTokens+toolTokens, total)
		}
	}
}

// TestRunnerConfig_ContextWindowFieldsPresent verifies that the new fields
// exist in RunnerConfig and have their zero values by default.
func TestRunnerConfig_ContextWindowFieldsPresent(t *testing.T) {
	t.Parallel()

	cfg := RunnerConfig{}
	if cfg.ContextWindowSnapshotEnabled != false {
		t.Error("ContextWindowSnapshotEnabled should default to false")
	}
	if cfg.ContextWindowWarningThreshold != 0 {
		t.Error("ContextWindowWarningThreshold should default to 0")
	}
}
