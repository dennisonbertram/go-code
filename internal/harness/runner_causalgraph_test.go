package harness

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// --------------------------------------------------------------------------
// Runner integration tests for the causal event graph (T-A1).
//
// CausalGraph had ZERO test coverage prior to this file. These tests lock in
// the current, observed behavior of the EventCausalGraphSnapshot emission:
//
//   - With CausalGraphEnabled=true, a snapshot is emitted at run END carrying
//     a meaningful graph payload (turns/edges/context ids).
//   - With the flag false (the default), no snapshot is emitted at all.
//   - The terminal asymmetry: the graph IS emitted on normal completion paths
//     and on the failRunMaxSteps path, but is ABSENT on the generic failRun
//     provider-error path (asserted below so the asymmetry is documented by a
//     test rather than assumed).
// --------------------------------------------------------------------------

// causalSnapshotEvents returns all EventCausalGraphSnapshot events for a run,
// in emission order.
func causalSnapshotEvents(events []Event) []Event {
	var out []Event
	for _, ev := range events {
		if ev.Type == EventCausalGraphSnapshot {
			out = append(out, ev)
		}
	}
	return out
}

// TestCausalGraphSnapshotEmittedAtRunEndWhenEnabled verifies that a run with
// CausalGraphEnabled=true emits a causal.graph.snapshot as its terminal causal
// event, carrying a meaningful graph payload: multiple LLM turns, a tool-call
// node, and at least one context edge linking the recorded tool result to the
// turn that consumed it.
func TestCausalGraphSnapshotEmittedAtRunEndWhenEnabled(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "noop_cg",
		Description: "does nothing",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "tool-output", nil
	})

	// Step 1: a tool call (records a tool_call node + tool result).
	// Step 2: no tool calls + content → run completes (records a second turn
	// whose context includes the step-1 tool result, producing a context edge).
	prov := &stubProvider{turns: []CompletionResult{
		{ToolCalls: []ToolCall{{ID: "c1", Name: "noop_cg", Arguments: `{}`}}},
		{Content: "done"},
	}}

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:       "test-model",
		MaxSteps:           10,
		CausalGraphEnabled: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test causal graph"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	snapshots := causalSnapshotEvents(events)
	if len(snapshots) == 0 {
		t.Fatal("expected at least one causal.graph.snapshot when CausalGraphEnabled=true, got none")
	}

	// The terminal causal event must be a snapshot: the LAST snapshot is the
	// run-end emission. Assert no run-terminal event (run.completed) precedes it
	// without a trailing snapshot — i.e. a snapshot is the final causal artifact.
	last := snapshots[len(snapshots)-1]

	// The run-end snapshot must be emitted at or before the run.completed event.
	// Confirm the run completed (not failed) and a snapshot exists in the stream.
	var completedIdx, lastSnapshotIdx = -1, -1
	for i, ev := range events {
		switch ev.Type {
		case EventRunCompleted:
			completedIdx = i
		case EventCausalGraphSnapshot:
			lastSnapshotIdx = i
		}
	}
	if completedIdx < 0 {
		t.Fatalf("expected a run.completed event, got none; statuses=%v", eventTypes(events))
	}
	if lastSnapshotIdx < 0 {
		t.Fatal("expected a causal.graph.snapshot event in the stream")
	}
	// The terminal snapshot is emitted immediately before completeRun, so it
	// must precede run.completed in the stream.
	if lastSnapshotIdx > completedIdx {
		t.Errorf("expected terminal causal.graph.snapshot (idx %d) to precede run.completed (idx %d)",
			lastSnapshotIdx, completedIdx)
	}

	// --- Payload shape: meaningful turns/edges/context ids. ---

	// step field present and positive.
	if step := payloadInt(last.Payload, "step"); step <= 0 {
		t.Errorf("snapshot step: got %d, want > 0", step)
	}

	graph, ok := last.Payload["graph"].(map[string]any)
	if !ok {
		t.Fatalf("snapshot graph: got %T, want map[string]any", last.Payload["graph"])
	}

	nodes, ok := graph["nodes"].([]any)
	if !ok {
		t.Fatalf("graph nodes: got %T, want []any", graph["nodes"])
	}
	// Expect at least: turn-1, tool-call node c1, turn-2.
	if len(nodes) < 3 {
		t.Errorf("graph nodes: got %d, want >= 3 (turn-1, tool-call, turn-2)", len(nodes))
	}

	// Verify the graph contains both an llm_turn node and a tool_call node.
	var sawTurn, sawToolCall bool
	for _, n := range nodes {
		nm, ok := n.(map[string]any)
		if !ok {
			continue
		}
		switch nm["type"] {
		case "llm_turn":
			sawTurn = true
		case "tool_call":
			sawToolCall = true
			if nm["tool_name"] != "noop_cg" {
				t.Errorf("tool_call node tool_name: got %v, want noop_cg", nm["tool_name"])
			}
		}
	}
	if !sawTurn {
		t.Error("graph nodes missing an llm_turn node")
	}
	if !sawToolCall {
		t.Error("graph nodes missing a tool_call node")
	}

	edges, ok := graph["edges"].([]any)
	if !ok {
		t.Fatalf("graph edges: got %T, want []any", graph["edges"])
	}
	if len(edges) == 0 {
		t.Fatal("graph edges: got 0, want >= 1 (context edge from tool result to consuming turn)")
	}
	// At least one edge should be a context edge whose target is an llm turn and
	// whose source is the step-1 tool call id (the context id fed into turn-2).
	var sawContextEdgeToTurn bool
	for _, e := range edges {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if em["type"] == "context" && em["to"] == "turn-2" && em["from"] == "c1" {
			sawContextEdgeToTurn = true
		}
	}
	if !sawContextEdgeToTurn {
		t.Errorf("expected a context edge {from:c1, to:turn-2}; edges=%v", edges)
	}
}

// TestCausalGraphSnapshotNotEmittedWhenDisabled verifies that no
// causal.graph.snapshot event is emitted when CausalGraphEnabled=false (the
// default), even for a run that exercises tool calls and multiple turns.
func TestCausalGraphSnapshotNotEmittedWhenDisabled(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "noop_cg_disabled",
		Description: "does nothing",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "tool-output", nil
	})

	prov := &stubProvider{turns: []CompletionResult{
		{ToolCalls: []ToolCall{{ID: "c1", Name: "noop_cg_disabled", Arguments: `{}`}}},
		{Content: "done"},
	}}

	// CausalGraphEnabled NOT set → defaults to false.
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
	if snaps := causalSnapshotEvents(events); len(snaps) != 0 {
		t.Errorf("expected no causal.graph.snapshot when CausalGraphEnabled=false, got %d", len(snaps))
	}
}

// TestCausalGraphSnapshotAbsentOnProviderErrorFailRun locks in the current,
// observed terminal asymmetry: when a run fails via the generic failRun
// provider-error path (provider Complete returns an error), NO
// causal.graph.snapshot is emitted, even with CausalGraphEnabled=true. The
// emitCausalGraph call only appears on normal-completion paths, the empty-
// response-retry continue site, the cost-ceiling completion, and the
// failRunMaxSteps tail — it is absent from failRun. This test documents that
// asymmetry so a future refactor that changes it is caught.
func TestCausalGraphSnapshotAbsentOnProviderErrorFailRun(t *testing.T) {
	t.Parallel()

	prov := &errorProvider{err: errors.New("simulated provider outage")}

	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:       "test-model",
		MaxSteps:           10,
		CausalGraphEnabled: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)

	// Confirm the run actually failed (so we are exercising the failRun path).
	var sawFailed bool
	for _, ev := range events {
		if ev.Type == EventRunFailed {
			sawFailed = true
		}
	}
	if !sawFailed {
		t.Fatalf("expected a run.failed event on the provider-error path; got %v", eventTypes(events))
	}

	// Assert the ACTUAL behavior: the causal graph snapshot is ABSENT on the
	// generic failRun provider-error path.
	if snaps := causalSnapshotEvents(events); len(snaps) != 0 {
		t.Errorf("expected NO causal.graph.snapshot on the failRun provider-error path "+
			"(documenting current asymmetry), got %d", len(snaps))
	}
}
