package harness

import (
	"context"
	"encoding/json"
	"testing"
)

// --------------------------------------------------------------------------
// Runner integration tests for cost anomaly detection (#213)
// --------------------------------------------------------------------------

// makeFloat64Ptr is a helper that returns a pointer to a float64 value.
func makeFloat64Ptr(v float64) *float64 { return &v }

// TestCostAnomalyEventEmittedWhenEnabled verifies that a cost.anomaly event
// is emitted when CostAnomalyDetectionEnabled=true and a step cost exceeds
// the threshold.
func TestCostAnomalyEventEmittedWhenEnabled(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "noop_ca",
		Description: "does nothing",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})

	// Step 1: baseline cost $0.01.
	// Step 2: spike cost $0.50 (50× baseline → well above 2×).
	// Step 3: LLM returns no tool calls → run completes.
	prov := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls:  []ToolCall{{ID: "c1", Name: "noop_ca", Arguments: `{}`}},
			Cost:       &CompletionCost{TotalUSD: 0.01},
			CostStatus: CostStatusAvailable,
		},
		{
			ToolCalls:  []ToolCall{{ID: "c2", Name: "noop_ca", Arguments: `{}`}},
			Cost:       &CompletionCost{TotalUSD: 0.50},
			CostStatus: CostStatusAvailable,
		},
		{Content: "done"},
	}}

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:                "test-model",
		MaxSteps:                    10,
		CostAnomalyDetectionEnabled: true,
		CostAnomalyStepMultiplier:   2.0,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test cost anomaly"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	var anomalyEvents []Event
	for _, ev := range events {
		if ev.Type == EventCostAnomaly {
			anomalyEvents = append(anomalyEvents, ev)
		}
	}

	if len(anomalyEvents) == 0 {
		t.Fatal("expected at least one cost.anomaly event, got none")
	}

	ev := anomalyEvents[0]

	// Verify step field.
	step := payloadInt(ev.Payload, "step")
	if step <= 0 {
		t.Errorf("step: got %d, want > 0", step)
	}

	// Verify anomaly_type field.
	anomalyType, ok := ev.Payload["anomaly_type"].(string)
	if !ok || anomalyType != "step_multiplier" {
		t.Errorf("anomaly_type: got %v, want step_multiplier", ev.Payload["anomaly_type"])
	}

	// Verify step_cost_usd is present and positive.
	stepCost, ok := ev.Payload["step_cost_usd"].(float64)
	if !ok || stepCost <= 0 {
		t.Errorf("step_cost_usd: got %v (%T), want > 0 float64", ev.Payload["step_cost_usd"], ev.Payload["step_cost_usd"])
	}

	// Verify avg_cost_usd is present and positive.
	avgCost, ok := ev.Payload["avg_cost_usd"].(float64)
	if !ok || avgCost <= 0 {
		t.Errorf("avg_cost_usd: got %v (%T), want > 0 float64", ev.Payload["avg_cost_usd"], ev.Payload["avg_cost_usd"])
	}

	// Verify threshold_multiplier is present and equals configured value.
	mult, ok := ev.Payload["threshold_multiplier"].(float64)
	if !ok || mult != 2.0 {
		t.Errorf("threshold_multiplier: got %v, want 2.0", ev.Payload["threshold_multiplier"])
	}
}

// TestCostAnomalyEventNotEmittedWhenDisabled verifies that no cost.anomaly
// event is emitted when CostAnomalyDetectionEnabled=false (the default).
func TestCostAnomalyEventNotEmittedWhenDisabled(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "noop_ca_disabled",
		Description: "does nothing",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})

	// Spike cost that would trigger anomaly if detection were enabled.
	prov := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls:  []ToolCall{{ID: "c1", Name: "noop_ca_disabled", Arguments: `{}`}},
			Cost:       &CompletionCost{TotalUSD: 0.01},
			CostStatus: CostStatusAvailable,
		},
		{
			ToolCalls:  []ToolCall{{ID: "c2", Name: "noop_ca_disabled", Arguments: `{}`}},
			Cost:       &CompletionCost{TotalUSD: 0.50},
			CostStatus: CostStatusAvailable,
		},
		{Content: "done"},
	}}

	// CostAnomalyDetectionEnabled NOT set (defaults to false).
	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     10,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	for _, ev := range events {
		if ev.Type == EventCostAnomaly {
			t.Error("unexpected cost.anomaly event when CostAnomalyDetectionEnabled=false")
		}
	}
}

// TestCostAnomalyNoEventOnFirstStep verifies that no cost.anomaly event is
// emitted for the first step of a run (no prior average exists).
func TestCostAnomalyNoEventOnFirstStep(t *testing.T) {
	t.Parallel()

	// Single-step run: the model answers immediately with no tool calls.
	prov := &stubProvider{turns: []CompletionResult{
		{
			Content:    "done in one step",
			Cost:       &CompletionCost{TotalUSD: 999.0}, // absurdly large first step
			CostStatus: CostStatusAvailable,
		},
	}}

	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:                "test-model",
		MaxSteps:                    10,
		CostAnomalyDetectionEnabled: true,
		CostAnomalyStepMultiplier:   2.0,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	for _, ev := range events {
		if ev.Type == EventCostAnomaly {
			t.Errorf("unexpected cost.anomaly on first (only) step: %v", ev.Payload)
		}
	}
}

// TestCostAnomalyDefaultMultiplier verifies that when CostAnomalyStepMultiplier
// is not set (0 value), the default of 2.0 is applied.
func TestCostAnomalyDefaultMultiplier(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "noop_ca_default",
		Description: "does nothing",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})

	// Step 1: $0.10, Step 2: $0.25 (2.5× > default 2.0 threshold → should fire).
	prov := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls:  []ToolCall{{ID: "c1", Name: "noop_ca_default", Arguments: `{}`}},
			Cost:       &CompletionCost{TotalUSD: 0.10},
			CostStatus: CostStatusAvailable,
		},
		{
			Content:    "done",
			Cost:       &CompletionCost{TotalUSD: 0.25},
			CostStatus: CostStatusAvailable,
		},
	}}

	// CostAnomalyStepMultiplier left at 0 — should default to 2.0.
	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:                "test-model",
		MaxSteps:                    5,
		CostAnomalyDetectionEnabled: true,
		// CostAnomalyStepMultiplier: 0 → default 2.0
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	var anomalyEvents []Event
	for _, ev := range events {
		if ev.Type == EventCostAnomaly {
			anomalyEvents = append(anomalyEvents, ev)
		}
	}

	if len(anomalyEvents) == 0 {
		t.Fatal("expected cost.anomaly event with default 2.0 multiplier, got none")
	}

	// The threshold_multiplier in the event should be 2.0.
	mult, ok := anomalyEvents[0].Payload["threshold_multiplier"].(float64)
	if !ok || mult != 2.0 {
		t.Errorf("threshold_multiplier: got %v, want 2.0", anomalyEvents[0].Payload["threshold_multiplier"])
	}
}

// TestCostAnomalyNoBelowThreshold verifies that no cost.anomaly event is emitted
// for a step that is expensive but within the configured multiplier threshold.
func TestCostAnomalyNoBelowThreshold(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "noop_ca_below",
		Description: "does nothing",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})

	// Step 1: $0.10; Step 2: $0.19 (1.9× < 2.0 → no anomaly).
	prov := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls:  []ToolCall{{ID: "c1", Name: "noop_ca_below", Arguments: `{}`}},
			Cost:       &CompletionCost{TotalUSD: 0.10},
			CostStatus: CostStatusAvailable,
		},
		{
			Content:    "done",
			Cost:       &CompletionCost{TotalUSD: 0.19},
			CostStatus: CostStatusAvailable,
		},
	}}

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:                "test-model",
		MaxSteps:                    5,
		CostAnomalyDetectionEnabled: true,
		CostAnomalyStepMultiplier:   2.0,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	for _, ev := range events {
		if ev.Type == EventCostAnomaly {
			t.Errorf("unexpected cost.anomaly for step below threshold: %v", ev.Payload)
		}
	}
}

// TestCostAnomalyMultipleSpikes verifies that multiple anomalous steps each
// produce an independent cost.anomaly event.
func TestCostAnomalyMultipleSpikes(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "noop_ca_multi",
		Description: "does nothing",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})

	// Two separate spikes after a cheap baseline.
	prov := &stubProvider{turns: []CompletionResult{
		// Step 1: cheap baseline $0.01.
		{
			ToolCalls:  []ToolCall{{ID: "c1", Name: "noop_ca_multi", Arguments: `{}`}},
			Cost:       &CompletionCost{TotalUSD: 0.01},
			CostStatus: CostStatusAvailable,
		},
		// Step 2: cheap $0.01.
		{
			ToolCalls:  []ToolCall{{ID: "c2", Name: "noop_ca_multi", Arguments: `{}`}},
			Cost:       &CompletionCost{TotalUSD: 0.01},
			CostStatus: CostStatusAvailable,
		},
		// Step 3: spike $0.10 (10× avg of $0.01).
		{
			ToolCalls:  []ToolCall{{ID: "c3", Name: "noop_ca_multi", Arguments: `{}`}},
			Cost:       &CompletionCost{TotalUSD: 0.10},
			CostStatus: CostStatusAvailable,
		},
		// Step 4: spike $0.20 (avg now ≈ $0.03, so 6× → spike).
		{
			ToolCalls:  []ToolCall{{ID: "c4", Name: "noop_ca_multi", Arguments: `{}`}},
			Cost:       &CompletionCost{TotalUSD: 0.20},
			CostStatus: CostStatusAvailable,
		},
		{Content: "done"},
	}}

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:                "test-model",
		MaxSteps:                    10,
		CostAnomalyDetectionEnabled: true,
		CostAnomalyStepMultiplier:   2.0,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	var anomalyEvents []Event
	for _, ev := range events {
		if ev.Type == EventCostAnomaly {
			anomalyEvents = append(anomalyEvents, ev)
		}
	}

	if len(anomalyEvents) < 2 {
		t.Errorf("expected at least 2 cost.anomaly events for two spikes, got %d", len(anomalyEvents))
	}
}

// TestCostAnomalyConfigDefaults verifies that zero-value RunnerConfig has
// CostAnomalyDetectionEnabled=false and CostAnomalyStepMultiplier=0.
func TestCostAnomalyConfigDefaults(t *testing.T) {
	t.Parallel()

	cfg := RunnerConfig{}
	if cfg.CostAnomalyDetectionEnabled {
		t.Error("CostAnomalyDetectionEnabled should default to false")
	}
	if cfg.CostAnomalyStepMultiplier != 0 {
		t.Errorf("CostAnomalyStepMultiplier should default to 0 (use 2.0 at runtime), got %f",
			cfg.CostAnomalyStepMultiplier)
	}
}

// TestAllEventTypesIncludesCostAnomaly verifies that EventCostAnomaly appears
// in AllEventTypes().
func TestAllEventTypesIncludesCostAnomaly(t *testing.T) {
	t.Parallel()

	all := AllEventTypes()
	found := false
	for _, et := range all {
		if et == EventCostAnomaly {
			found = true
			break
		}
	}
	if !found {
		t.Error("AllEventTypes() does not include EventCostAnomaly")
	}
}

// TestCostAnomalyEventConstantValue verifies the string value of EventCostAnomaly.
func TestCostAnomalyEventConstantValue(t *testing.T) {
	t.Parallel()

	if EventCostAnomaly != "cost.anomaly" {
		t.Errorf("EventCostAnomaly = %q, want %q", EventCostAnomaly, "cost.anomaly")
	}
}
