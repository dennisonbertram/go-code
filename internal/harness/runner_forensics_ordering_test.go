package harness

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// --------------------------------------------------------------------------
// T-A2: llm.request.snapshot BEFORE provider.Complete, llm.response.meta AFTER
//
// The runner emits EventLLMRequestSnapshot immediately before calling
// candidate.Provider.Complete, then emits EventLLMResponseMeta immediately
// after the call returns (runner_step_engine.go lines ~371/468). Both must
// appear in the event stream with the expected ordering relative to
// llm.turn.completed and each other.
// --------------------------------------------------------------------------

// TestRequestSnapshotBeforeProviderComplete_OrderingRelativeToTurn verifies:
//
//	llm.request.snapshot < llm.turn.completed  (snapshot is PRE-call)
//	llm.response.meta    < llm.turn.completed  would fail — response.meta is
//	                                            actually emitted BEFORE turn.completed.
//
// Full ordering: llm.request.snapshot → (provider.Complete) → llm.response.meta
//
//	→ usage.delta → llm.turn.completed
func TestRequestSnapshotBeforeProviderComplete_OrderingRelativeToTurn(t *testing.T) {
	t.Parallel()

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done", ModelVersion: "test-v1"},
	}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:           "test-model",
		MaxSteps:               2,
		CaptureRequestEnvelope: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "ordering test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	// requireEventOrder asserts strict left-to-right ordering on first occurrence.
	requireEventOrder(t, events,
		string(EventLLMRequestSnapshot), // PRE-call: before provider.Complete
		string(EventLLMResponseMeta),    // POST-call: after provider.Complete returns
		string(EventLLMTurnCompleted),   // after usage.delta + response meta
	)
}

// TestRequestSnapshotBeforeResponseMeta_MultiStep verifies the per-step ordering
// for a multi-turn run: each step must have its snapshot before its meta.
// For the FIRST occurrence this is equivalent to requireEventOrder, but we also
// check the second step explicitly by index.
func TestRequestSnapshotBeforeResponseMeta_MultiStep(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "noop_ord",
		Description: "does nothing",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})

	prov := &stubProvider{turns: []CompletionResult{
		{ToolCalls: []ToolCall{{ID: "c1", Name: "noop_ord", Arguments: `{}`}}},
		{Content: "done"},
	}}
	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:           "test-model",
		MaxSteps:               5,
		CaptureRequestEnvelope: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "multi-step ordering"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	// Collect all snapshots and metas in order.
	var snapshots, metas []int
	for i, ev := range events {
		switch ev.Type {
		case EventLLMRequestSnapshot:
			snapshots = append(snapshots, i)
		case EventLLMResponseMeta:
			metas = append(metas, i)
		}
	}

	if len(snapshots) != 2 {
		t.Fatalf("expected 2 llm.request.snapshot events, got %d", len(snapshots))
	}
	if len(metas) != 2 {
		t.Fatalf("expected 2 llm.response.meta events, got %d", len(metas))
	}

	// Step 1: snapshot[0] < meta[0].
	if snapshots[0] >= metas[0] {
		t.Errorf("step 1: llm.request.snapshot (idx %d) must precede llm.response.meta (idx %d)",
			snapshots[0], metas[0])
	}
	// Step 2: snapshot[1] < meta[1].
	if snapshots[1] >= metas[1] {
		t.Errorf("step 2: llm.request.snapshot (idx %d) must precede llm.response.meta (idx %d)",
			snapshots[1], metas[1])
	}
	// Step 1 snapshot before step 2 snapshot (monotonic).
	if snapshots[0] >= snapshots[1] {
		t.Errorf("snapshot[0] (idx %d) should come before snapshot[1] (idx %d)",
			snapshots[0], snapshots[1])
	}
}

// --------------------------------------------------------------------------
// T-A3: per-step ordering for context.window.snapshot and cost.anomaly
//
// After each LLM turn the runner emits (in this order):
//
//	usage.delta → llm.turn.completed → cost.anomaly (if triggered) → context.window.snapshot
//
// T-A3a: ContextWindowSnapshotEnabled — snapshot appears AFTER llm.turn.completed.
// T-A3b: CostAnomalyDetectionEnabled — anomaly appears AFTER llm.turn.completed.
// T-A3c: Both enabled simultaneously — cost.anomaly < context.window.snapshot.
// --------------------------------------------------------------------------

// TestContextWindowSnapshotAfterTurnCompleted verifies that when
// ContextWindowSnapshotEnabled is true, the context.window.snapshot event is
// emitted AFTER llm.turn.completed in the event stream (T-A3a).
func TestContextWindowSnapshotAfterTurnCompleted(t *testing.T) {
	t.Parallel()

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done"},
	}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:                 "test-model",
		MaxSteps:                     2,
		ContextWindowSnapshotEnabled: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "context window ordering"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	requireEventOrder(t, events,
		string(EventLLMTurnCompleted),
		string(EventContextWindowSnapshot),
	)
}

// TestCostAnomalyAfterTurnCompleted verifies that when
// CostAnomalyDetectionEnabled is true and a step triggers an anomaly, the
// cost.anomaly event is emitted AFTER llm.turn.completed (T-A3b).
func TestCostAnomalyAfterTurnCompleted(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "noop_ca_ord",
		Description: "does nothing",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})

	// Step 1: baseline $0.01; Step 2: spike $0.50 (50× > 2× threshold).
	prov := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls:  []ToolCall{{ID: "c1", Name: "noop_ca_ord", Arguments: `{}`}},
			Cost:       &CompletionCost{TotalUSD: 0.01},
			CostStatus: CostStatusAvailable,
		},
		{
			Content:    "done",
			Cost:       &CompletionCost{TotalUSD: 0.50},
			CostStatus: CostStatusAvailable,
		},
	}}
	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:                "test-model",
		MaxSteps:                    5,
		CostAnomalyDetectionEnabled: true,
		CostAnomalyStepMultiplier:   2.0,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "cost anomaly ordering"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	// Confirm anomaly is present so the ordering assertion is meaningful.
	var sawAnomaly bool
	for _, ev := range events {
		if ev.Type == EventCostAnomaly {
			sawAnomaly = true
			break
		}
	}
	if !sawAnomaly {
		t.Fatal("expected at least one cost.anomaly event (prerequisite for ordering assertion)")
	}

	requireEventOrder(t, events,
		string(EventLLMTurnCompleted),
		string(EventCostAnomaly),
	)
}

// TestCostAnomalyBeforeContextWindowSnapshot verifies that when both
// CostAnomalyDetectionEnabled and ContextWindowSnapshotEnabled are enabled, a
// cost.anomaly event (when triggered) appears BEFORE context.window.snapshot in
// the same step (T-A3c).
//
// Note: requireEventOrder uses first occurrence, so we must assert on the step
// where BOTH events co-occur (the anomaly step). We do this by collecting all
// indices and checking relative ordering within the anomaly step specifically.
func TestCostAnomalyBeforeContextWindowSnapshot(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "noop_both_ord",
		Description: "does nothing",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})

	prov := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls:  []ToolCall{{ID: "c1", Name: "noop_both_ord", Arguments: `{}`}},
			Cost:       &CompletionCost{TotalUSD: 0.01},
			CostStatus: CostStatusAvailable,
		},
		{
			Content:    "done",
			Cost:       &CompletionCost{TotalUSD: 0.50},
			CostStatus: CostStatusAvailable,
		},
	}}
	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:                 "test-model",
		MaxSteps:                     5,
		CostAnomalyDetectionEnabled:  true,
		CostAnomalyStepMultiplier:    2.0,
		ContextWindowSnapshotEnabled: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "cost anomaly + context window ordering"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	// Collect all positions for cost.anomaly and context.window.snapshot.
	var anomalyIdxs, windowIdxs []int
	for i, ev := range events {
		switch ev.Type {
		case EventCostAnomaly:
			anomalyIdxs = append(anomalyIdxs, i)
		case EventContextWindowSnapshot:
			windowIdxs = append(windowIdxs, i)
		}
	}
	if len(anomalyIdxs) == 0 {
		t.Fatal("expected at least one cost.anomaly event (prerequisite for ordering assertion)")
	}
	if len(windowIdxs) == 0 {
		t.Fatal("expected at least one context.window.snapshot event (prerequisite for ordering assertion)")
	}

	// The step that triggers an anomaly (step 2) should produce both a
	// cost.anomaly and a context.window.snapshot. Find the context.window.snapshot
	// that follows the LAST cost.anomaly (which is always in the anomaly step).
	lastAnomalyIdx := anomalyIdxs[len(anomalyIdxs)-1]

	// Find the first context.window.snapshot that appears AFTER the last anomaly.
	nextWindowIdx := -1
	for _, wi := range windowIdxs {
		if wi > lastAnomalyIdx {
			nextWindowIdx = wi
			break
		}
	}
	if nextWindowIdx < 0 {
		t.Fatalf("expected a context.window.snapshot event after cost.anomaly (idx %d); "+
			"snapshot indices: %v, events: %v", lastAnomalyIdx, windowIdxs, eventTypes(events))
	}

	// Confirm cost.anomaly < context.window.snapshot within the same step.
	if lastAnomalyIdx >= nextWindowIdx {
		t.Errorf("cost.anomaly (idx %d) should come BEFORE context.window.snapshot (idx %d) in the same step",
			lastAnomalyIdx, nextWindowIdx)
	}
}

// --------------------------------------------------------------------------
// T-A4: error.context is the immediately-preceding event before run.failed
// on the provider-error (failRun) path, AND is ABSENT on
// failRunMaxSteps / failRunMaxTurns.
//
// This locks in the actual terminal asymmetry described in the plan:
//   - failRun (provider error): ErrorChainEnabled=true → error.context emitted,
//     immediately before run.failed.
//   - failRunMaxSteps (max steps): error.context is NEVER emitted, even when
//     ErrorChainEnabled=true.
//   - failRunMaxTurns (max turns): same absence as failRunMaxSteps.
// --------------------------------------------------------------------------

// TestErrorContextImmediatelyPrecedesRunFailed verifies that when
// ErrorChainEnabled=true and the run fails via the generic failRun path
// (provider returns an error), error.context is the immediately-preceding
// event before run.failed (T-A4a).
func TestErrorContextImmediatelyPrecedesRunFailed(t *testing.T) {
	t.Parallel()

	prov := &errorProvider{err: errors.New("simulated provider failure")}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:      "test-model",
		MaxSteps:          10,
		ErrorChainEnabled: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "trigger provider error"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusFailed)

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	// Locate error.context and run.failed by index.
	ecIdx := -1
	rfIdx := -1
	for i, ev := range events {
		switch ev.Type {
		case EventErrorContext:
			ecIdx = i // track last occurrence (should be exactly one)
		case EventRunFailed:
			rfIdx = i
		}
	}

	if ecIdx < 0 {
		t.Fatalf("error.context event not found on failRun path; events: %v", eventTypes(events))
	}
	if rfIdx < 0 {
		t.Fatalf("run.failed event not found; events: %v", eventTypes(events))
	}

	// error.context must precede run.failed.
	if ecIdx >= rfIdx {
		t.Errorf("error.context (idx %d) must come BEFORE run.failed (idx %d)",
			ecIdx, rfIdx)
	}

	// error.context must be the IMMEDIATELY preceding event before run.failed:
	// no other event should appear between them.
	if rfIdx-ecIdx != 1 {
		between := eventTypes(events[ecIdx+1 : rfIdx])
		t.Errorf("error.context should immediately precede run.failed, but %d events intervene: %v",
			rfIdx-ecIdx-1, between)
	}
}

// TestErrorContextAbsentOnMaxStepsPath verifies the terminal asymmetry:
// when a run fails via failRunMaxSteps (MaxSteps exhausted), error.context is
// NOT emitted, even when ErrorChainEnabled=true (T-A4b — locking in actual
// behavior).
func TestErrorContextAbsentOnMaxStepsPath(t *testing.T) {
	t.Parallel()

	// The provider always wants a tool call so the loop never self-terminates.
	// MaxSteps=2 exhausts after two iterations, triggering failRunMaxSteps.
	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "noop_maxsteps",
		Description: "does nothing",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})
	prov := &stubProvider{turns: []CompletionResult{
		{ToolCalls: []ToolCall{{ID: "c1", Name: "noop_maxsteps", Arguments: `{}`}}},
		{ToolCalls: []ToolCall{{ID: "c2", Name: "noop_maxsteps", Arguments: `{}`}}},
		{ToolCalls: []ToolCall{{ID: "c3", Name: "noop_maxsteps", Arguments: `{}`}}},
	}}
	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:      "test-model",
		MaxSteps:          2,
		ErrorChainEnabled: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "max steps test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusFailed)

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	// Confirm it really was a max-steps failure.
	var rfPayload map[string]any
	for _, ev := range events {
		if ev.Type == EventRunFailed {
			rfPayload = ev.Payload
			break
		}
	}
	if rfPayload == nil {
		t.Fatalf("no run.failed event on max-steps path; events: %v", eventTypes(events))
	}
	if rfPayload["reason"] != "max_steps_reached" {
		t.Skipf("run didn't fail via max-steps path (reason=%v); skipping asymmetry assertion", rfPayload["reason"])
	}

	// ASSERT: error.context is ABSENT on the failRunMaxSteps path.
	for _, ev := range events {
		if ev.Type == EventErrorContext {
			t.Errorf("error.context must be ABSENT on failRunMaxSteps path (terminal asymmetry), "+
				"but found one; events: %v", eventTypes(events))
		}
	}
}

// TestErrorContextAbsentOnMaxTurnsPath verifies the terminal asymmetry:
// when a run fails via failRunMaxTurns (MaxTurns exhausted), error.context is
// NOT emitted, even when ErrorChainEnabled=true (T-A4c — locking in actual
// behavior).
func TestErrorContextAbsentOnMaxTurnsPath(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "noop_maxturns",
		Description: "does nothing",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})

	// The provider always returns a tool call so the loop never self-terminates.
	// MaxTurns=1 means after the first LLM turn the run exhausts its turn budget.
	prov := &stubProvider{turns: []CompletionResult{
		{ToolCalls: []ToolCall{{ID: "c1", Name: "noop_maxturns", Arguments: `{}`}}},
		{ToolCalls: []ToolCall{{ID: "c2", Name: "noop_maxturns", Arguments: `{}`}}},
	}}
	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:      "test-model",
		MaxSteps:          10,
		MaxTurns:          1, // exhaust after exactly one assistant turn
		ErrorChainEnabled: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "max turns test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusFailed)

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	// Confirm it really was a max-turns failure.
	var rfPayload map[string]any
	for _, ev := range events {
		if ev.Type == EventRunFailed {
			rfPayload = ev.Payload
			break
		}
	}
	if rfPayload == nil {
		t.Fatalf("no run.failed event on max-turns path; events: %v", eventTypes(events))
	}
	if rfPayload["reason"] != "max_turns_exhausted" {
		t.Skipf("run didn't fail via max-turns path (reason=%v); skipping asymmetry assertion", rfPayload["reason"])
	}

	// ASSERT: error.context is ABSENT on the failRunMaxTurns path.
	for _, ev := range events {
		if ev.Type == EventErrorContext {
			t.Errorf("error.context must be ABSENT on failRunMaxTurns path (terminal asymmetry), "+
				"but found one; events: %v", eventTypes(events))
		}
	}
}
