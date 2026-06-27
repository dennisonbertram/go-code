package replay

import (
	"context"
	"testing"

	htools "go-agent-harness/internal/harness/tools"

	"go-agent-harness/internal/forensics/rollout"
	"go-agent-harness/internal/harness"
)

// multiStepRollout returns a hand-authored two-turn rollout:
//
//	step 1: assistant turn that calls one tool (read_file), tool completes.
//	step 2: terminal assistant turn with content, then run.completed.
//
// It deliberately mirrors the REAL runner emission shape:
//   - llm.turn.completed carries only a tool_calls COUNT (an int), not the
//     actual tool call objects. F2 must NOT read tool calls from here.
//   - tool.call.started carries the authoritative call id/name/arguments.
//   - assistant.message carries the terminal turn's content (present only on
//     the no-tool turn).
//   - tool.call.completed carries the recorded output under the "output" key
//     (the real runner emit key; see F0).
//   - usage.delta carries per-turn usage/cost.
func multiStepRollout() []rollout.RolloutEvent {
	return []rollout.RolloutEvent{
		{Type: "run.started", Step: 0, Payload: map[string]any{"prompt": "read the file"}},
		// --- step 1: tool-calling turn ---
		{Type: "usage.delta", Step: 1, Payload: map[string]any{
			"turn_usage": map[string]any{
				"prompt_tokens":     float64(10),
				"completion_tokens": float64(5),
				"total_tokens":      float64(15),
			},
			"turn_cost_usd": float64(0.0003),
		}},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			// Real runner: only a count here, NOT the tool call objects.
			"tool_calls": float64(1),
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id": "call_1", "tool": "read_file", "arguments": `{"path":"/tmp/x"}`,
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "call_1", "tool": "read_file", "output": "file contents here",
		}},
		// --- step 2: terminal no-tool turn ---
		{Type: "usage.delta", Step: 2, Payload: map[string]any{
			"turn_usage": map[string]any{
				"prompt_tokens":     float64(20),
				"completion_tokens": float64(8),
				"total_tokens":      float64(28),
			},
			"turn_cost_usd": float64(0.0007),
		}},
		{Type: "llm.turn.completed", Step: 2, Payload: map[string]any{
			"tool_calls": float64(0),
		}},
		{Type: "assistant.message", Step: 2, Payload: map[string]any{
			"content": "Here are the files.",
		}},
		{Type: "run.completed", Step: 3, Payload: map[string]any{"output": "done"}},
	}
}

// TestRecordedProvider_ReconstructsTurns is T-F2: the recorded-response provider
// reconstructs each LLM turn from the rollout — tool calls from
// tool.call.started events, content from assistant.message — and returns them in
// order across successive Complete calls.
func TestRecordedProvider_ReconstructsTurns(t *testing.T) {
	events := multiStepRollout()

	p, err := NewRecordedProvider(events)
	if err != nil {
		t.Fatalf("NewRecordedProvider: %v", err)
	}

	if got := p.TurnCount(); got != 2 {
		t.Fatalf("expected 2 scripted turns, got %d", got)
	}

	ctx := context.Background()

	// --- Turn 1: tool-calling turn ---
	r1, err := p.Complete(ctx, harness.CompletionRequest{})
	if err != nil {
		t.Fatalf("turn 1 Complete: %v", err)
	}
	if r1.Content != "" {
		t.Errorf("turn 1 content: expected empty (content lives on assistant.message of the terminal turn), got %q", r1.Content)
	}
	if len(r1.ToolCalls) != 1 {
		t.Fatalf("turn 1: expected 1 tool call from tool.call.started, got %d", len(r1.ToolCalls))
	}
	tc := r1.ToolCalls[0]
	if tc.ID != "call_1" {
		t.Errorf("turn 1 tool call id: want call_1, got %q", tc.ID)
	}
	if tc.Name != "read_file" {
		t.Errorf("turn 1 tool call name: want read_file, got %q", tc.Name)
	}
	if tc.Arguments != `{"path":"/tmp/x"}` {
		t.Errorf("turn 1 tool call arguments: want {\"path\":\"/tmp/x\"}, got %q", tc.Arguments)
	}
	// Recorded usage/cost carried through.
	if r1.Usage == nil {
		t.Fatalf("turn 1: expected recorded usage, got nil")
	}
	if r1.Usage.PromptTokens != 10 || r1.Usage.CompletionTokens != 5 || r1.Usage.TotalTokens != 15 {
		t.Errorf("turn 1 usage mismatch: %+v", *r1.Usage)
	}
	if r1.CostUSD == nil || *r1.CostUSD != 0.0003 {
		t.Errorf("turn 1 cost: want 0.0003, got %v", r1.CostUSD)
	}

	// --- Turn 2: terminal no-tool turn ---
	r2, err := p.Complete(ctx, harness.CompletionRequest{})
	if err != nil {
		t.Fatalf("turn 2 Complete: %v", err)
	}
	if r2.Content != "Here are the files." {
		t.Errorf("turn 2 content: want %q (from assistant.message), got %q", "Here are the files.", r2.Content)
	}
	if len(r2.ToolCalls) != 0 {
		t.Errorf("turn 2: expected no tool calls, got %d", len(r2.ToolCalls))
	}
	if r2.Usage == nil || r2.Usage.TotalTokens != 28 {
		t.Errorf("turn 2 usage mismatch: %+v", r2.Usage)
	}
}

// TestRecordedProvider_DoesNotReadToolCallsFromTurnCompleted guards the F2
// contract that tool calls come from tool.call.started, NOT from
// llm.turn.completed (which only carries a count in real rollouts). A turn whose
// llm.turn.completed has tool_calls=0 but which has a real tool.call.started must
// still surface the tool call.
func TestRecordedProvider_DoesNotReadToolCallsFromTurnCompleted(t *testing.T) {
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0, Payload: map[string]any{"prompt": "go"}},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			// Misleading count: zero, even though a tool was actually called.
			"tool_calls": float64(0),
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id": "c9", "tool": "bash", "arguments": `{"cmd":"ls"}`,
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "c9", "tool": "bash", "output": "ok",
		}},
		{Type: "run.completed", Step: 2, Payload: map[string]any{"output": "done"}},
	}

	p, err := NewRecordedProvider(events)
	if err != nil {
		t.Fatalf("NewRecordedProvider: %v", err)
	}
	r, err := p.Complete(context.Background(), harness.CompletionRequest{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(r.ToolCalls) != 1 || r.ToolCalls[0].ID != "c9" {
		t.Fatalf("expected tool call c9 reconstructed from tool.call.started, got %+v", r.ToolCalls)
	}
}

// TestRecordedProvider_SetsUsageAndCostStatus verifies that Complete sets
// UsageStatus=ProviderReported and CostStatus=Available when the recorded step
// carries usage and cost. Without these statuses, recordAccounting re-derives
// them and may produce a non-zero CostDeltaUSD on an identical replay (spurious
// drift). When a step has no usage, UsageStatus and CostStatus must remain unset.
func TestRecordedProvider_SetsUsageAndCostStatus(t *testing.T) {
	events := multiStepRollout()

	p, err := NewRecordedProvider(events)
	if err != nil {
		t.Fatalf("NewRecordedProvider: %v", err)
	}

	ctx := context.Background()

	// Turn 1 has usage and cost — statuses must be set.
	r1, err := p.Complete(ctx, harness.CompletionRequest{})
	if err != nil {
		t.Fatalf("turn 1 Complete: %v", err)
	}
	if r1.UsageStatus != harness.UsageStatusProviderReported {
		t.Errorf("turn 1 UsageStatus: got %q, want %q",
			r1.UsageStatus, harness.UsageStatusProviderReported)
	}
	if r1.CostStatus != harness.CostStatusAvailable {
		t.Errorf("turn 1 CostStatus: got %q, want %q",
			r1.CostStatus, harness.CostStatusAvailable)
	}

	// Turn 2 also has usage and cost.
	r2, err := p.Complete(ctx, harness.CompletionRequest{})
	if err != nil {
		t.Fatalf("turn 2 Complete: %v", err)
	}
	if r2.UsageStatus != harness.UsageStatusProviderReported {
		t.Errorf("turn 2 UsageStatus: got %q, want %q",
			r2.UsageStatus, harness.UsageStatusProviderReported)
	}
	if r2.CostStatus != harness.CostStatusAvailable {
		t.Errorf("turn 2 CostStatus: got %q, want %q",
			r2.CostStatus, harness.CostStatusAvailable)
	}
}

// TestRecordedProvider_NoUsageLeavesCostStatusUnset verifies that Complete does
// NOT set UsageStatus/CostStatus when the recorded step has no usage data.
func TestRecordedProvider_NoUsageLeavesCostStatusUnset(t *testing.T) {
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0, Payload: map[string]any{"prompt": "go"}},
		// No usage.delta for step 1 — only the turn completion.
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{"tool_calls": float64(0)}},
		{Type: "assistant.message", Step: 1, Payload: map[string]any{"content": "done"}},
		{Type: "run.completed", Step: 2, Payload: map[string]any{"output": "done"}},
	}

	p, err := NewRecordedProvider(events)
	if err != nil {
		t.Fatalf("NewRecordedProvider: %v", err)
	}

	r, err := p.Complete(context.Background(), harness.CompletionRequest{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if r.UsageStatus != "" {
		t.Errorf("UsageStatus: expected empty (no recorded usage), got %q", r.UsageStatus)
	}
	if r.CostStatus != "" {
		t.Errorf("CostStatus: expected empty (no recorded usage), got %q", r.CostStatus)
	}
}

// TestReplayToolDispatch_ReturnsRecordedOutput verifies the replay tool hook
// short-circuits execution and returns the recorded tool.call.completed output,
// keyed by the call_id carried in the context (F0 output/result fallback).
func TestReplayToolDispatch_ReturnsRecordedOutput(t *testing.T) {
	events := multiStepRollout()

	dispatch, err := NewReplayToolDispatch(events)
	if err != nil {
		t.Fatalf("NewReplayToolDispatch: %v", err)
	}
	if out, ok := dispatch.Output("call_1"); !ok || out != "file contents here" {
		t.Fatalf("Output(call_1) = %q, %v; want recorded output", out, ok)
	}
	if out, ok := dispatch.Output("missing"); ok || out != "" {
		t.Fatalf("Output(missing) = %q, %v; want empty false", out, ok)
	}

	handler, err := NewReplayToolHandler(events)
	if err != nil {
		t.Fatalf("NewReplayToolHandler: %v", err)
	}

	ctx := context.WithValue(context.Background(), htools.ContextKeyToolCallID, "call_1")
	out, err := handler(ctx, nil)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if out != "file contents here" {
		t.Errorf("expected recorded output (from the \"output\" key), got %q", out)
	}

	// Unknown call_id is an error, not a silent empty.
	missingCtx := context.WithValue(context.Background(), htools.ContextKeyToolCallID, "nope")
	if _, err := handler(missingCtx, nil); err == nil {
		t.Errorf("expected error for unrecorded call_id, got nil")
	}
}
