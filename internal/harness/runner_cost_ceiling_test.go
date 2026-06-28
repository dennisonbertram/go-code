package harness

// runner_cost_ceiling_test.go — deep behavioral tests for the priced
// max_cost_usd ceiling.
//
// Design notes:
//   - Uses file-unique inline provider stubs (costCeilingPricedProvider,
//     costCeilingUnpricedProvider) to avoid importing fakeprovider (which would
//     create an import cycle: fakeprovider→harness).
//   - floatPtr is defined in runner_test.go (same package); reuse it here.
//   - collectRunEvents, requireEventOrder, eventTypes are defined in runner_test.go.
//   - assertNoRunnerGoroutineLeak is defined in runner_shutdown_test.go; do NOT
//     redeclare it here.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// costCeilingPricedProvider is a file-unique inline provider stub scripted with
// multiple turns, each returning a priced CompletionResult
// (Cost.TotalUSD > 0, CostStatus = CostStatusAvailable).
// A tool call is returned on turns 1..N-1 so the loop keeps going; the final
// turn (which should never be reached) returns plain content.
type costCeilingPricedProvider struct {
	mu    sync.Mutex
	turns []CompletionResult
	calls int
}

func (p *costCeilingPricedProvider) Complete(_ context.Context, req CompletionRequest) (CompletionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls >= len(p.turns) {
		return CompletionResult{}, nil
	}
	turn := p.turns[p.calls]
	p.calls++
	if req.Stream != nil {
		for _, delta := range turn.Deltas {
			req.Stream(delta)
		}
	}
	return turn, nil
}

func (p *costCeilingPricedProvider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// costCeilingUnpricedProvider returns turns whose CostStatus is NOT
// CostStatusAvailable (i.e. the model is unpriced), so the ceiling must
// never fire even when max_cost_usd is very small.
type costCeilingUnpricedProvider struct {
	mu    sync.Mutex
	turns []CompletionResult
	calls int
}

func (p *costCeilingUnpricedProvider) Complete(_ context.Context, req CompletionRequest) (CompletionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls >= len(p.turns) {
		return CompletionResult{}, nil
	}
	turn := p.turns[p.calls]
	p.calls++
	if req.Stream != nil {
		for _, delta := range turn.Deltas {
			req.Stream(delta)
		}
	}
	return turn, nil
}

func (p *costCeilingUnpricedProvider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// registerEchoTool registers an "echo_json" tool on registry and returns it.
// Shared by the sub-tests below.
func registerEchoTool(t *testing.T, registry *Registry) {
	t.Helper()
	err := registry.Register(ToolDefinition{
		Name:        "echo_json",
		Description: "echoes payload",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{}`, nil
	})
	require.NoError(t, err, "register echo_json tool")
}

// TestCostCeilingDeep_PricedCeilingContract is the primary proof:
// a priced max_cost_usd ceiling triggers run.cost_limit_reached and the run
// terminates with RunStatusCompleted (not failed/cancelled).
//
// Contract assertions (from the plan's "Priced cost-ceiling contract"):
//  1. EventRunCostLimitReached is emitted with payload: step, max_cost_usd,
//     cumulative_cost_usd.
//  2. Event ORDER on the triggering turn:
//     usage.delta → llm.turn.completed → run.cost_limit_reached →
//     run.step.completed → run.completed.
//  3. Final Run.Status == RunStatusCompleted; run.failed and run.cancelled absent.
//  4. Run.CostTotals.CostStatus == CostStatusAvailable and CostUSDTotal >= 0.003.
func TestCostCeilingDeep_PricedCeilingContract(t *testing.T) {
	t.Parallel()

	// Two priced turns at $0.002 each. Ceiling is $0.003.
	// After turn 1: cumulative = $0.002 < $0.003 → continue.
	// After turn 2: cumulative = $0.004 >= $0.003 → ceiling fires.
	// Turn 3 must NEVER be reached.
	prov := &costCeilingPricedProvider{
		turns: []CompletionResult{
			{
				ToolCalls:  []ToolCall{{ID: "call-1", Name: "echo_json", Arguments: `{}`}},
				Cost:       &CompletionCost{TotalUSD: 0.002},
				CostStatus: CostStatusAvailable,
				Usage:      &CompletionUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			},
			{
				ToolCalls:  []ToolCall{{ID: "call-2", Name: "echo_json", Arguments: `{}`}},
				Cost:       &CompletionCost{TotalUSD: 0.002},
				CostStatus: CostStatusAvailable,
				Usage:      &CompletionUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			},
			// Turn 3: should never be reached.
			{Content: "unreachable"},
		},
	}

	registry := NewRegistry()
	registerEchoTool(t, registry)

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     10,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:     "hello",
		MaxCostUSD: 0.003,
	})
	require.NoError(t, err, "start run")

	events, err := collectRunEvents(t, runner, run.ID)
	require.NoError(t, err, "collect events")

	// 1. Locate the cost_limit_reached event.
	var costLimitIdx int = -1
	for i, ev := range events {
		if ev.Type == EventRunCostLimitReached {
			costLimitIdx = i
			break
		}
	}
	require.NotEqual(t, -1, costLimitIdx, "expected run.cost_limit_reached event; got events: %v", eventTypes(events))

	// 1a. Payload fields.
	payload := events[costLimitIdx].Payload
	assert.NotNil(t, payload["step"], "cost_limit_reached payload must carry 'step'")
	assert.NotNil(t, payload["max_cost_usd"], "cost_limit_reached payload must carry 'max_cost_usd'")
	assert.NotNil(t, payload["cumulative_cost_usd"], "cost_limit_reached payload must carry 'cumulative_cost_usd'")

	// max_cost_usd in the payload must equal the requested ceiling.
	maxCostVal, _ := payload["max_cost_usd"].(float64)
	assert.InDelta(t, 0.003, maxCostVal, 1e-12, "payload max_cost_usd must match request ceiling")

	// cumulative_cost_usd must be >= the ceiling (0.003).
	cumCostVal, _ := payload["cumulative_cost_usd"].(float64)
	assert.GreaterOrEqual(t, cumCostVal, 0.003, "payload cumulative_cost_usd must be >= max_cost_usd")

	// 2. Event ORDER on the triggering turn:
	//    usage.delta → llm.turn.completed → run.cost_limit_reached →
	//    run.step.completed → run.completed.
	//
	// We verify this within the exact window of the triggering turn to close the
	// first-occurrence mutation gap: requireEventOrder records only first
	// occurrences, and usage.delta / llm.turn.completed fire on every turn
	// (including pre-trigger turns), so a full-list check could pass even if
	// those events were missing on the triggering turn.
	//
	// Window: find the last run.step.completed that appears BEFORE
	// cost_limit_reached — that marks the end of the previous step. The events
	// from that index onward form the triggering-turn slice. All five events must
	// appear in order within that slice.
	require.NotEqual(t, -1, costLimitIdx, "cost_limit_reached must be present (already asserted above)")

	lastStepCompletedBeforeCeiling := -1
	for i := 0; i < costLimitIdx; i++ {
		if events[i].Type == EventRunStepCompleted {
			lastStepCompletedBeforeCeiling = i
		}
	}
	// If no prior run.step.completed exists (first step triggered), start from 0.
	triggerStart := 0
	if lastStepCompletedBeforeCeiling >= 0 {
		triggerStart = lastStepCompletedBeforeCeiling + 1
	}
	triggerSlice := events[triggerStart:]
	requireEventOrder(t, triggerSlice,
		string(EventUsageDelta),
		string(EventLLMTurnCompleted),
		string(EventRunCostLimitReached),
		string(EventRunStepCompleted),
		string(EventRunCompleted),
	)

	// 3. run.failed and run.cancelled must NOT appear.
	for _, ev := range events {
		assert.NotEqual(t, EventRunFailed, ev.Type, "run.failed must NOT be emitted on cost ceiling")
		assert.NotEqual(t, EventRunCancelled, ev.Type, "run.cancelled must NOT be emitted on cost ceiling")
	}

	// Terminal event must be run.completed.
	var terminalType EventType
	for _, ev := range events {
		if IsTerminalEvent(ev.Type) {
			terminalType = ev.Type
		}
	}
	assert.Equal(t, EventRunCompleted, terminalType, "terminal event must be run.completed")

	// 4. Final run state.
	state, ok := runner.GetRun(run.ID)
	require.True(t, ok, "run must exist in runner store")
	assert.Equal(t, RunStatusCompleted, state.Status, "run.Status must be RunStatusCompleted")

	require.NotNil(t, state.CostTotals, "CostTotals must be populated")
	assert.Equal(t, CostStatusAvailable, state.CostTotals.CostStatus,
		"CostTotals.CostStatus must be CostStatusAvailable")
	assert.GreaterOrEqual(t, state.CostTotals.CostUSDTotal, 0.003,
		"CostTotals.CostUSDTotal must be >= 0.003 (ceiling was crossed)")

	// Provider must have been called exactly twice (ceiling hits after turn 2).
	assert.Equal(t, 2, prov.Calls(), "provider must be called exactly 2 times")
}

// TestCostCeilingDeep_UnpricedDoesNotTrigger is the control sub-test:
// when CostStatus != CostStatusAvailable (unpriced model), the ceiling must
// NEVER fire and the run must proceed to normal completion.
func TestCostCeilingDeep_UnpricedDoesNotTrigger(t *testing.T) {
	t.Parallel()

	// Three turns with CostStatusUnpricedModel (no pricing data).
	// max_cost_usd is set very low (0.001) — but since cost is unpriced,
	// the ceiling must never trip.
	prov := &costCeilingUnpricedProvider{
		turns: []CompletionResult{
			{
				ToolCalls:  []ToolCall{{ID: "u1", Name: "echo_json", Arguments: `{}`}},
				CostStatus: CostStatusUnpricedModel,
				Usage:      &CompletionUsage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
			},
			{
				ToolCalls:  []ToolCall{{ID: "u2", Name: "echo_json", Arguments: `{}`}},
				CostStatus: CostStatusUnpricedModel,
				Usage:      &CompletionUsage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
			},
			{
				Content:    "done — unpriced",
				CostStatus: CostStatusUnpricedModel,
				Usage:      &CompletionUsage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
			},
		},
	}

	registry := NewRegistry()
	registerEchoTool(t, registry)

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     10,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:     "hello",
		MaxCostUSD: 0.001, // tiny ceiling; cost is unpriced so must never fire
	})
	require.NoError(t, err, "start run")

	events, err := collectRunEvents(t, runner, run.ID)
	require.NoError(t, err, "collect events")

	// run.cost_limit_reached must NOT appear.
	for _, ev := range events {
		assert.NotEqual(t, EventRunCostLimitReached, ev.Type,
			"run.cost_limit_reached must NOT fire for unpriced model; got events: %v", eventTypes(events))
	}

	// Run must complete normally (not fail).
	state, ok := runner.GetRun(run.ID)
	require.True(t, ok, "run must exist in runner store")
	assert.Equal(t, RunStatusCompleted, state.Status,
		"unpriced run must complete normally despite low max_cost_usd")

	// Shut down cleanly.
	require.NoError(t, runner.Shutdown(context.Background()))
}

// TestCostCeilingDeep_ProviderUnreportedDoesNotTrigger ensures that
// CostStatusProviderUnreported (another non-available status) also never
// triggers the ceiling.
func TestCostCeilingDeep_ProviderUnreportedDoesNotTrigger(t *testing.T) {
	t.Parallel()

	prov := &costCeilingUnpricedProvider{
		turns: []CompletionResult{
			{
				Content:    "turn 1",
				CostStatus: CostStatusProviderUnreported,
				Usage:      &CompletionUsage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
			},
			{
				Content:    "done",
				CostStatus: CostStatusProviderUnreported,
				Usage:      &CompletionUsage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
			},
		},
	}

	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     10,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:     "hello",
		MaxCostUSD: 0.0001, // very low ceiling; cost is provider-unreported so must never fire
	})
	require.NoError(t, err, "start run")

	events, err := collectRunEvents(t, runner, run.ID)
	require.NoError(t, err, "collect events")

	for _, ev := range events {
		assert.NotEqual(t, EventRunCostLimitReached, ev.Type,
			"run.cost_limit_reached must NOT fire for provider_unreported cost status")
	}

	state, ok := runner.GetRun(run.ID)
	require.True(t, ok, "run must exist in runner store")
	assert.Equal(t, RunStatusCompleted, state.Status,
		"provider_unreported cost run must complete normally")
}

// TestCostCeilingDeep_CostViaCompletionCost verifies that cost reported via
// Cost.TotalUSD (not CostUSD *float64) also drives the ceiling correctly.
func TestCostCeilingDeep_CostViaCompletionCost(t *testing.T) {
	t.Parallel()

	// Each turn reports $0.002 via the Cost struct (not CostUSD).
	// Ceiling is $0.003 → second turn crosses it.
	prov := &costCeilingPricedProvider{
		turns: []CompletionResult{
			{
				ToolCalls:  []ToolCall{{ID: "cc1", Name: "echo_json", Arguments: `{}`}},
				Cost:       &CompletionCost{TotalUSD: 0.002},
				CostStatus: CostStatusAvailable,
			},
			{
				ToolCalls:  []ToolCall{{ID: "cc2", Name: "echo_json", Arguments: `{}`}},
				Cost:       &CompletionCost{TotalUSD: 0.002},
				CostStatus: CostStatusAvailable,
			},
			{Content: "unreachable"},
		},
	}

	registry := NewRegistry()
	registerEchoTool(t, registry)

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     10,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:     "hello",
		MaxCostUSD: 0.003,
	})
	require.NoError(t, err, "start run")

	events, err := collectRunEvents(t, runner, run.ID)
	require.NoError(t, err, "collect events")

	var sawCostLimit bool
	for _, ev := range events {
		if ev.Type == EventRunCostLimitReached {
			sawCostLimit = true
		}
	}
	assert.True(t, sawCostLimit, "run.cost_limit_reached must fire when cost is reported via Cost.TotalUSD")

	state, ok := runner.GetRun(run.ID)
	require.True(t, ok, "run must exist in runner store")
	assert.Equal(t, RunStatusCompleted, state.Status, "run must complete (not fail)")
	require.NotNil(t, state.CostTotals)
	assert.Equal(t, CostStatusAvailable, state.CostTotals.CostStatus)
	assert.GreaterOrEqual(t, state.CostTotals.CostUSDTotal, 0.003)
}

// TestCostCeilingDeep_CostViaFloatPtr verifies that cost reported via CostUSD *float64
// (not Cost struct) also drives the ceiling correctly. normalizeTurnCost uses
// *result.CostUSD when Cost.TotalUSD would otherwise be 0, as the last writer wins.
func TestCostCeilingDeep_CostViaFloatPtr(t *testing.T) {
	t.Parallel()

	prov := &costCeilingPricedProvider{
		turns: []CompletionResult{
			{
				ToolCalls:  []ToolCall{{ID: "fp1", Name: "echo_json", Arguments: `{}`}},
				CostUSD:    floatPtr(0.002),
				CostStatus: CostStatusAvailable,
			},
			{
				ToolCalls:  []ToolCall{{ID: "fp2", Name: "echo_json", Arguments: `{}`}},
				CostUSD:    floatPtr(0.002),
				CostStatus: CostStatusAvailable,
			},
			{Content: "unreachable"},
		},
	}

	registry := NewRegistry()
	registerEchoTool(t, registry)

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     10,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:     "hello",
		MaxCostUSD: 0.003,
	})
	require.NoError(t, err, "start run")

	events, err := collectRunEvents(t, runner, run.ID)
	require.NoError(t, err, "collect events")

	var sawCostLimit bool
	for _, ev := range events {
		if ev.Type == EventRunCostLimitReached {
			sawCostLimit = true
		}
	}
	assert.True(t, sawCostLimit, "run.cost_limit_reached must fire when cost is reported via CostUSD *float64")

	state, ok := runner.GetRun(run.ID)
	require.True(t, ok, "run must exist in runner store")
	assert.Equal(t, RunStatusCompleted, state.Status, "run must complete (not fail)")
}
