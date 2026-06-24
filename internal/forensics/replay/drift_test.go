package replay

import (
	"testing"

	"go-agent-harness/internal/forensics/rollout"
)

// fixtureRollout is a small but representative recorded rollout: a tool-calling
// turn (step 1) with two concurrent tool calls, a terminal assistant turn
// (step 2), and a completed outcome. It exercises step grouping, tool-call
// sets, and outcome detection.
func fixtureRollout() []rollout.RolloutEvent {
	return []rollout.RolloutEvent{
		{Type: "run.started", Step: 0, Payload: map[string]any{"run_id": "r1", "prompt": "do the thing"}},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"run_id": "r1", "provider": "openai", "latency_ms": float64(120),
			"tool_calls": float64(2),
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"run_id": "r1", "call_id": "call_a", "tool": "read_file", "arguments": `{"path":"/a"}`,
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"run_id": "r1", "call_id": "call_a", "tool": "read_file", "output": "contents a",
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"run_id": "r1", "call_id": "call_b", "tool": "read_file", "arguments": `{"path":"/b"}`,
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"run_id": "r1", "call_id": "call_b", "tool": "read_file", "output": "contents b",
		}},
		{Type: "usage.delta", Step: 1, Payload: map[string]any{
			"run_id": "r1", "cumulative_cost_usd": float64(0.0012),
		}},
		{Type: "llm.turn.completed", Step: 2, Payload: map[string]any{
			"run_id": "r1", "provider": "openai", "ttft_ms": float64(40),
		}},
		{Type: "assistant.message", Step: 2, Payload: map[string]any{
			"run_id": "r1", "content": "all done",
		}},
		{Type: "run.completed", Step: 3, Payload: map[string]any{
			"run_id": "r1", "cost_totals": map[string]any{"total_cost_usd": float64(0.0012)},
		}},
	}
}

// T-F1b: DetectDrift on an IDENTICAL rollout must report Matched=true with zero
// structural drift. This is the determinism guarantee: a faithful replay that
// reproduces the recorded stream exactly must not be flagged as drift.
func TestDetectDrift_IdenticalRollout(t *testing.T) {
	events := fixtureRollout()

	res := DetectDrift(events, events, rollout.DriftOptions)

	if !res.Matched {
		t.Fatalf("identical rollout: expected Matched=true, got false (outcome=%q, added=%v, removed=%v, changed=%v)",
			res.OutcomeDiff, res.AddedSteps, res.RemovedSteps, res.ChangedSteps)
	}
	if len(res.AddedSteps) != 0 || len(res.RemovedSteps) != 0 || len(res.ChangedSteps) != 0 {
		t.Errorf("identical rollout: expected zero step drift, got added=%v removed=%v changed=%v",
			res.AddedSteps, res.RemovedSteps, res.ChangedSteps)
	}
	if len(res.DivergentToolCalls) != 0 {
		t.Errorf("identical rollout: expected zero divergent tool calls, got %v", res.DivergentToolCalls)
	}
	if res.OutcomeDiff != "identical" {
		t.Errorf("identical rollout: expected OutcomeDiff=identical, got %q", res.OutcomeDiff)
	}
	if res.CostDeltaUSD != 0 {
		t.Errorf("identical rollout: expected zero cost delta, got %v", res.CostDeltaUSD)
	}
}

// Within-step ordering of concurrent tool events must be treated as
// NON-divergent: the same set of step-1 tool events emitted in a different
// order is still Matched.
func TestDetectDrift_WithinStepReorderNotDrift(t *testing.T) {
	original := fixtureRollout()

	// Reorder the two concurrent tool calls within step 1: emit call_b's
	// started/completed before call_a's. Everything else is identical.
	replayed := []rollout.RolloutEvent{
		original[0], // run.started
		original[1], // llm.turn.completed step1
		original[4], // tool.call.started call_b
		original[5], // tool.call.completed call_b
		original[2], // tool.call.started call_a
		original[3], // tool.call.completed call_a
		original[6], // usage.delta
		original[7], // llm.turn.completed step2
		original[8], // assistant.message
		original[9], // run.completed
	}

	res := DetectDrift(original, replayed, rollout.DriftOptions)

	if !res.Matched {
		t.Fatalf("within-step reorder should NOT be drift: Matched=false (changed=%v, divergent=%v)",
			res.ChangedSteps, res.DivergentToolCalls)
	}
	if len(res.DivergentToolCalls) != 0 {
		t.Errorf("within-step reorder: expected zero divergent tool calls, got %v", res.DivergentToolCalls)
	}
}

// Cost differences must be reported as a delta, never as a hard mismatch:
// a run that costs more but is otherwise structurally identical still Matches.
func TestDetectDrift_CostDeltaNotMismatch(t *testing.T) {
	original := fixtureRollout()
	replayed := fixtureRollout()
	// Bump the replayed cost (pricing/model drift) without changing structure.
	replayed[6].Payload["cumulative_cost_usd"] = float64(0.0050)
	replayed[9].Payload["cost_totals"] = map[string]any{"total_cost_usd": float64(0.0050)}

	res := DetectDrift(original, replayed, rollout.DriftOptions)

	if !res.Matched {
		t.Fatalf("cost-only difference must still Match: Matched=false (changed=%v)", res.ChangedSteps)
	}
	if res.CostDeltaUSD <= 0 {
		t.Errorf("expected positive CostDeltaUSD, got %v", res.CostDeltaUSD)
	}
}

// T-F1c: a mutated turn must produce structured, non-empty drift with the right
// classification. Changing the assistant content of the terminal turn is a
// deterministic difference (a changed step), not a stripped/variable one.
func TestDetectDrift_MutatedTurnIsDrift(t *testing.T) {
	original := fixtureRollout()
	replayed := fixtureRollout()
	replayed[8].Payload["content"] = "something completely different"

	res := DetectDrift(original, replayed, rollout.DriftOptions)

	if res.Matched {
		t.Fatalf("mutated assistant content must be drift: Matched=true")
	}
	if len(res.ChangedSteps) == 0 {
		t.Errorf("expected a changed step for the mutated turn, got none (added=%v removed=%v)",
			res.AddedSteps, res.RemovedSteps)
	}
	foundStep2 := false
	for _, s := range res.ChangedSteps {
		if s == 2 {
			foundStep2 = true
		}
	}
	if !foundStep2 {
		t.Errorf("expected step 2 in ChangedSteps, got %v", res.ChangedSteps)
	}
}

// A diverging tool call (different arguments for the same call_id) must be
// surfaced in DivergentToolCalls, distinct from a plain content change.
func TestDetectDrift_DivergentToolCall(t *testing.T) {
	original := fixtureRollout()
	replayed := fixtureRollout()
	// call_a now reads a different path — a deterministic tool-arg divergence.
	replayed[2].Payload["arguments"] = `{"path":"/HACKED"}`

	res := DetectDrift(original, replayed, rollout.DriftOptions)

	if res.Matched {
		t.Fatalf("divergent tool arguments must be drift: Matched=true")
	}
	if len(res.DivergentToolCalls) == 0 {
		t.Errorf("expected DivergentToolCalls non-empty, got %v", res.DivergentToolCalls)
	}
}

// An added step (the replay took an extra turn) and a removed step (the replay
// took fewer turns) must be classified into AddedSteps / RemovedSteps and break
// the match.
func TestDetectDrift_AddedAndRemovedSteps(t *testing.T) {
	original := fixtureRollout()

	// Replay that ends one step early: drop the terminal completion (step 3) and
	// the terminal assistant turn (step 2 events), simulating a shorter run.
	short := []rollout.RolloutEvent{
		original[0], original[1], original[2], original[3],
		original[4], original[5], original[6],
		{Type: "run.completed", Step: 2, Payload: map[string]any{"run_id": "r1"}},
	}

	res := DetectDrift(original, short, rollout.DriftOptions)
	if res.Matched {
		t.Fatalf("a shorter replay must be drift: Matched=true")
	}
	if len(res.RemovedSteps) == 0 {
		t.Errorf("expected RemovedSteps non-empty for a shorter replay, got %v", res.RemovedSteps)
	}
}

// An outcome flip (completed in the original, failed in the replay) must be
// reported via OutcomeDiff and break the match.
func TestDetectDrift_OutcomeFlip(t *testing.T) {
	original := fixtureRollout()
	replayed := fixtureRollout()
	replayed[9] = rollout.RolloutEvent{Type: "run.failed", Step: 3, Payload: map[string]any{"run_id": "r1", "error": "boom"}}

	res := DetectDrift(original, replayed, rollout.DriftOptions)
	if res.Matched {
		t.Fatalf("outcome flip must be drift: Matched=true")
	}
	if res.OutcomeDiff == "identical" {
		t.Errorf("expected non-identical OutcomeDiff for completed-vs-failed, got %q", res.OutcomeDiff)
	}
}
