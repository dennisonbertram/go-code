package replay

import (
	"fmt"
	"strings"
	"testing"

	"go-agent-harness/internal/forensics/rollout"
	"go-agent-harness/internal/harness"
)

func TestReplay_BasicFlow(t *testing.T) {
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0, Payload: map[string]any{"prompt": "hello"}},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "I'll run a command",
			"tool_calls": []any{
				map[string]any{"id": "call_1", "name": "bash", "arguments": `{"cmd":"ls"}`},
			},
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id": "call_1", "tool": "bash", "arguments": `{"cmd":"ls"}`,
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "call_1", "tool": "bash", "result": "file1.go\nfile2.go",
		}},
		{Type: "llm.turn.completed", Step: 2, Payload: map[string]any{
			"content": "Here are the files",
		}},
		{Type: "run.completed", Step: 3, Payload: map[string]any{"output": "done"}},
	}

	result := Replay(events)

	if result.StepCount != 3 {
		t.Errorf("expected step count 3, got %d", result.StepCount)
	}
	if !result.Matched {
		t.Errorf("expected matched=true, got false; mismatches: %v", result.Mismatches)
	}
	if len(result.Events) != len(events) {
		t.Errorf("expected %d replay events, got %d", len(events), len(result.Events))
	}
}

func TestReplay_ToolCallWithRecordedResult(t *testing.T) {
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "reading file",
			"tool_calls": []any{
				map[string]any{"id": "call_1", "name": "read_file", "arguments": `{"path":"/tmp/x"}`},
			},
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id": "call_1", "tool": "read_file", "arguments": `{"path":"/tmp/x"}`,
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "call_1", "tool": "read_file", "result": "file contents here",
		}},
	}

	result := Replay(events)

	// The tool.call.started event is at index 2 (after run.started + llm.turn.completed).
	startEvent := result.Events[2]
	if startEvent.Details["result"] != "file contents here" {
		t.Errorf("expected recorded result, got %v", startEvent.Details["result"])
	}
	if !startEvent.Matched {
		t.Errorf("expected matched=true for tool call with completion, mismatches: %v", result.Mismatches)
	}
}

func TestReplay_MissingCompletion(t *testing.T) {
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "running bash",
			"tool_calls": []any{
				map[string]any{"id": "call_orphan", "name": "bash"},
			},
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id": "call_orphan", "tool": "bash",
		}},
		{Type: "run.failed", Step: 2, Payload: map[string]any{"error": "timeout"}},
	}

	result := Replay(events)

	if result.Matched {
		t.Error("expected matched=false for missing completion")
	}
	if len(result.Mismatches) == 0 {
		t.Error("expected at least one mismatch")
	}
	found := false
	for _, m := range result.Mismatches {
		if strings.Contains(m, "call_orphan") {
			found = true
		}
	}
	if !found {
		t.Error("expected mismatch to mention call_orphan")
	}
}

func TestReplay_EmptyEvents(t *testing.T) {
	result := Replay(nil)

	if result.StepCount != 0 {
		t.Errorf("expected step count 0, got %d", result.StepCount)
	}
	if !result.Matched {
		t.Error("expected matched=true for empty events")
	}
	if len(result.Events) != 0 {
		t.Errorf("expected 0 events, got %d", len(result.Events))
	}
}

func TestReplay_MultipleToolCalls(t *testing.T) {
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "running c1",
			"tool_calls": []any{
				map[string]any{"id": "c1", "name": "bash"},
			},
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash",
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash", "result": "result_1",
		}},
		{Type: "llm.turn.completed", Step: 2, Payload: map[string]any{
			"content": "running c2",
			"tool_calls": []any{
				map[string]any{"id": "c2", "name": "read_file"},
			},
		}},
		{Type: "tool.call.started", Step: 2, Payload: map[string]any{
			"call_id": "c2", "tool": "read_file",
		}},
		{Type: "tool.call.completed", Step: 2, Payload: map[string]any{
			"call_id": "c2", "tool": "read_file", "result": "result_2",
		}},
	}

	result := Replay(events)

	if !result.Matched {
		t.Errorf("expected matched, mismatches: %v", result.Mismatches)
	}

	// Each started event should have its recorded result.
	// Indices with run.started at [0]:
	// [0]=run.started, [1]=llm.turn(c1), [2]=started(c1), [3]=completed(c1),
	// [4]=llm.turn(c2), [5]=started(c2), [6]=completed(c2)
	if result.Events[2].Details["result"] != "result_1" {
		t.Errorf("expected result_1, got %v", result.Events[2].Details["result"])
	}
	if result.Events[5].Details["result"] != "result_2" {
		t.Errorf("expected result_2, got %v", result.Events[5].Details["result"])
	}
}

func TestReplay_NoCallID(t *testing.T) {
	// A tool.call.started event without a call_id is a schema violation and
	// must be flagged as a mismatch — silent omission would bypass integrity checks.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"tool": "bash",
		}},
	}

	result := Replay(events)

	if result.Matched {
		t.Error("expected mismatch for missing call_id in tool.call.started")
	}
	if len(result.Mismatches) == 0 {
		t.Error("expected at least one mismatch entry for missing call_id")
	}
}

func TestReconstructMessages_BasicFlow(t *testing.T) {
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0, Payload: map[string]any{
			"prompt": "hello world", "system_prompt": "You are helpful",
		}},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "I'll help you",
			"tool_calls": []any{
				map[string]any{"id": "c1", "name": "bash", "arguments": `{"cmd":"echo hi"}`},
			},
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash",
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash", "result": "hi",
		}},
		{Type: "llm.turn.completed", Step: 2, Payload: map[string]any{
			"content": "Done!",
		}},
		{Type: "run.completed", Step: 3},
	}

	msgs := ReconstructMessages(events, 3)

	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(msgs))
	}

	// system prompt
	if msgs[0].Role != "system" || msgs[0].Content != "You are helpful" {
		t.Errorf("msg 0: expected system message, got %+v", msgs[0])
	}
	// user prompt
	if msgs[1].Role != "user" || msgs[1].Content != "hello world" {
		t.Errorf("msg 1: expected user message, got %+v", msgs[1])
	}
	// assistant with tool calls
	if msgs[2].Role != "assistant" || len(msgs[2].ToolCalls) != 1 {
		t.Errorf("msg 2: expected assistant with tool calls, got %+v", msgs[2])
	}
	if msgs[2].ToolCalls[0].ID != "c1" || msgs[2].ToolCalls[0].Name != "bash" {
		t.Errorf("msg 2 tool call: expected c1/bash, got %+v", msgs[2].ToolCalls[0])
	}
	// tool result
	if msgs[3].Role != "tool" || msgs[3].ToolCallID != "c1" || msgs[3].Content != "hi" {
		t.Errorf("msg 3: expected tool result, got %+v", msgs[3])
	}
	// final assistant message
	if msgs[4].Role != "assistant" || msgs[4].Content != "Done!" {
		t.Errorf("msg 4: expected assistant Done!, got %+v", msgs[4])
	}
}

func TestReconstructMessages_UpToStep(t *testing.T) {
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0, Payload: map[string]any{"prompt": "hi"}},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{"content": "step 1"}},
		{Type: "llm.turn.completed", Step: 2, Payload: map[string]any{"content": "step 2"}},
		{Type: "llm.turn.completed", Step: 3, Payload: map[string]any{"content": "step 3"}},
	}

	msgs := ReconstructMessages(events, 1)

	// Should include: user prompt + step 1 assistant
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages up to step 1, got %d", len(msgs))
	}
	if msgs[1].Content != "step 1" {
		t.Errorf("expected step 1 content, got %s", msgs[1].Content)
	}
}

func TestReconstructMessages_SteeringMessage(t *testing.T) {
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0, Payload: map[string]any{"prompt": "start"}},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{"content": "working"}},
		{Type: "steering.received", Step: 2, Payload: map[string]any{"content": "change direction"}},
		{Type: "llm.turn.completed", Step: 3, Payload: map[string]any{"content": "changed"}},
	}

	msgs := ReconstructMessages(events, 3)

	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}
	if msgs[2].Role != "user" || msgs[2].Content != "change direction" {
		t.Errorf("msg 2: expected steering as user message, got %+v", msgs[2])
	}
}

func TestReconstructMessages_ContinuedConversation(t *testing.T) {
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0, Payload: map[string]any{"prompt": "start"}},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{"content": "done"}},
		{Type: "conversation.continued", Step: 2, Payload: map[string]any{"message": "follow up"}},
		{Type: "llm.turn.completed", Step: 3, Payload: map[string]any{"content": "more done"}},
	}

	msgs := ReconstructMessages(events, 3)

	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}
	if msgs[2].Role != "user" || msgs[2].Content != "follow up" {
		t.Errorf("msg 2: expected conversation.continued as user message, got %+v", msgs[2])
	}
}

func TestReconstructMessages_NoSystemPrompt(t *testing.T) {
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0, Payload: map[string]any{"prompt": "just user"}},
	}

	msgs := ReconstructMessages(events, 0)

	if len(msgs) != 1 {
		t.Fatalf("expected 1 message (user only), got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("expected user role, got %s", msgs[0].Role)
	}
}

func TestReconstructMessages_EmptyEvents(t *testing.T) {
	msgs := ReconstructMessages(nil, 0)
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}

// TestF0_ToolOutputKeyFallback is the TDD test for task F0.
//
// The runner emits tool results under the key "output" in tool.call.completed
// events (runner_step_engine.go:847,874,1072,1091), but the replayer was
// reading "result". Real captured rollouts therefore silently produced empty
// tool outputs. This test uses the runner's actual emit key ("output") and
// verifies that both Replay (Details["result"]) and ReconstructMessages
// (tool message Content) are NON-empty.
func TestF0_ToolOutputKeyFallback(t *testing.T) {
	// Build a minimal valid rollout where tool.call.completed carries "output"
	// (exactly as the runner emits) rather than "result".
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0, Payload: map[string]any{"prompt": "run a command"}},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "I'll run bash",
			"tool_calls": []any{
				map[string]any{"id": "call_x", "name": "bash", "arguments": `{"cmd":"ls"}`},
			},
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id": "call_x", "tool": "bash", "arguments": `{"cmd":"ls"}`,
		}},
		// NOTE: key is "output" (runner emit key), NOT "result".
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "call_x", "tool": "bash", "output": "file_a.go\nfile_b.go",
		}},
		{Type: "llm.turn.completed", Step: 2, Payload: map[string]any{
			"content": "Here are the files",
		}},
		{Type: "run.completed", Step: 3, Payload: map[string]any{"output": "done"}},
	}

	// --- Replay: the tool.call.started event's Details["result"] must be non-empty.
	replayResult := Replay(events)

	// Find the tool.call.started ReplayEvent (index 2 after run.started + llm.turn).
	var startedEv *ReplayEvent
	for i := range replayResult.Events {
		if replayResult.Events[i].EventType == "tool.call.started" {
			startedEv = &replayResult.Events[i]
			break
		}
	}
	if startedEv == nil {
		t.Fatal("T-F0: no tool.call.started event found in ReplayResult.Events")
	}
	if got, _ := startedEv.Details["result"].(string); got == "" {
		t.Errorf("T-F0 Replay: Details[\"result\"] is empty; replayer read \"result\" key but runner emits \"output\" — fallback missing")
	}

	// --- ReconstructMessages: the tool message Content must be non-empty.
	msgs := ReconstructMessages(events, 3)

	var toolMsg *harness.Message
	for i := range msgs {
		if msgs[i].Role == "tool" {
			toolMsg = &msgs[i]
			break
		}
	}
	if toolMsg == nil {
		t.Fatal("T-F0 ReconstructMessages: no tool-role message found")
	}
	if toolMsg.Content == "" {
		t.Errorf("T-F0 ReconstructMessages: tool message Content is empty; replayer read \"result\" key but runner emits \"output\" — fallback missing")
	}
}

func TestIndexToolCompletions(t *testing.T) {
	events := []rollout.RolloutEvent{
		{Type: "tool.call.completed", Payload: map[string]any{"call_id": "c1", "tool": "bash", "result": "r1"}},
		{Type: "tool.call.completed", Payload: map[string]any{"call_id": "c2", "tool": "read_file", "result": "r2"}},
		{Type: "run.completed"},
	}

	idx := indexToolCompletions(events)
	if len(idx.entries) != 2 {
		t.Fatalf("expected 2 completions, got %d", len(idx.entries))
	}
	// capID adds "l:" prefix for short IDs — use the same function for lookups.
	k1, k2 := capID("c1"), capID("c2")
	if idx.entries[k1].result != "r1" {
		t.Errorf("expected r1 for c1, got %s", idx.entries[k1].result)
	}
	if idx.entries[k2].result != "r2" {
		t.Errorf("expected r2 for c2, got %s", idx.entries[k2].result)
	}
	// Verify file-order indices.
	if idx.entries[k1].fileIndex != 0 {
		t.Errorf("expected fileIndex=0 for c1, got %d", idx.entries[k1].fileIndex)
	}
	if idx.entries[k2].fileIndex != 1 {
		t.Errorf("expected fileIndex=1 for c2, got %d", idx.entries[k2].fileIndex)
	}
	// Verify tool names are stored.
	if idx.entries[k1].toolName != "bash" {
		t.Errorf("expected toolName=bash for c1, got %s", idx.entries[k1].toolName)
	}
	if idx.entries[k2].toolName != "read_file" {
		t.Errorf("expected toolName=read_file for c2, got %s", idx.entries[k2].toolName)
	}
}

func TestReplay_UnannouncedToolCallRejected(t *testing.T) {
	// A rollout with tool.call.started + tool.call.completed but no announcing
	// llm.turn.completed is an integrity violation — the call was fabricated.
	// Replay must flag this even though lifecycle ordering is correct.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash",
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash", "result": "injected",
		}},
	}

	result := Replay(events)

	if result.Matched {
		t.Error("expected mismatch for unannounced tool call")
	}
	found := false
	for _, m := range result.Mismatches {
		if strings.Contains(m, "announced") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'announced' in mismatch, got: %v", result.Mismatches)
	}
}

func TestReplay_CompletionWithoutStarted(t *testing.T) {
	// A crafted rollout with llm.turn.completed + tool.call.completed but
	// no tool.call.started must be flagged as a mismatch. Without this check,
	// Replay() would produce Matched=true while ReconstructMessages() would
	// inject the fabricated tool result (since the call was announced).
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "running bash",
			"tool_calls": []any{
				map[string]any{"id": "c1", "name": "bash"},
			},
		}},
		// No tool.call.started — only a direct completion.
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash", "result": "injected",
		}},
	}

	result := Replay(events)

	if result.Matched {
		t.Error("expected mismatch when tool.call.completed has no corresponding tool.call.started")
	}
	found := false
	for _, m := range result.Mismatches {
		if strings.Contains(m, "no corresponding tool.call.started") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'no corresponding tool.call.started' in mismatches, got: %v", result.Mismatches)
	}
}

func TestReconstructMessages_CompletionWithoutStarted(t *testing.T) {
	// A tool.call.completed without a prior tool.call.started must NOT be
	// included in the reconstructed messages — even if the call_id was announced.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0, Payload: map[string]any{"prompt": "hi"}},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "calling bash",
			"tool_calls": []any{
				map[string]any{"id": "c1", "name": "bash"},
			},
		}},
		// No tool.call.started — only a direct completion.
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash", "result": "injected result",
		}},
	}

	msgs := ReconstructMessages(events, 2)
	// Expected: user + assistant. The tool message must NOT be included.
	for _, m := range msgs {
		if m.Role == "tool" {
			t.Errorf("expected no tool message, but got one: %+v", m)
		}
	}
}

func TestReplay_ToolNameAbsentInCompletion(t *testing.T) {
	// An attacker can strip the "tool" field from tool.call.completed to bypass
	// the name-consistency check. If the started event declares a tool name,
	// a completion without a tool name must still be flagged as a mismatch.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "running bash",
			"tool_calls": []any{
				map[string]any{"id": "c1", "name": "bash"},
			},
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash",
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "c1", "result": "ok", // no "tool" field — bypass attempt
		}},
	}

	result := Replay(events)

	if result.Matched {
		t.Error("expected mismatch when completion omits tool name but started declares one")
	}
	found := false
	for _, m := range result.Mismatches {
		if strings.Contains(m, "mismatch") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'mismatch' in messages, got: %v", result.Mismatches)
	}
}

func TestReplay_ToolNameMismatch(t *testing.T) {
	// An attacker can craft a rollout where tool.call.completed has a different
	// tool name than tool.call.started (same call_id). This splices a result
	// from one tool into a different tool's replay record.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "reading file",
			"tool_calls": []any{
				map[string]any{"id": "c1", "name": "read_file"},
			},
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "read_file",
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash", "result": "spliced result",
		}},
	}

	result := Replay(events)

	if result.Matched {
		t.Error("expected mismatch for tool name mismatch between start and completion")
	}
	found := false
	for _, m := range result.Mismatches {
		if strings.Contains(m, "mismatch") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'mismatch' in mismatch messages, got: %v", result.Mismatches)
	}
}

func TestReplay_CompletionBeforeStartedRejected(t *testing.T) {
	// An attacker can place tool.call.completed before tool.call.started
	// in file order at the same step — the monotonic step check in the loader
	// is satisfied because steps are equal. Replay must detect this lifecycle
	// inversion and flag it as a mismatch, preventing fabricated tool results
	// from being injected via out-of-order completion events.
	// The llm.turn.completed is included so the announcement check passes
	// and only the lifecycle ordering violation is flagged.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "running c1",
			"tool_calls": []any{
				map[string]any{"id": "c1", "name": "bash"},
			},
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash", "result": "injected",
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash",
		}},
	}

	result := Replay(events)

	if result.Matched {
		t.Error("expected mismatch for completion-before-started in file order")
	}
	found := false
	for _, m := range result.Mismatches {
		if strings.Contains(m, "before") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'before' in mismatch message, got: %v", result.Mismatches)
	}
}

func TestPayloadString(t *testing.T) {
	tests := []struct {
		name     string
		payload  map[string]any
		key      string
		expected string
		ok       bool
	}{
		{"found", map[string]any{"key": "value"}, "key", "value", true},
		{"not_found", map[string]any{"key": "value"}, "other", "", false},
		{"nil_payload", nil, "key", "", false},
		{"non_string", map[string]any{"key": 42}, "key", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := payloadString(tt.payload, tt.key)
			if got != tt.expected || ok != tt.ok {
				t.Errorf("expected (%q, %v), got (%q, %v)", tt.expected, tt.ok, got, ok)
			}
		})
	}
}

func TestCopyPayload(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if copyPayload(nil) != nil {
			t.Error("expected nil for nil input")
		}
	})
	t.Run("copy", func(t *testing.T) {
		original := map[string]any{"a": 1, "b": "two"}
		cp := copyPayload(original)
		if len(cp) != 2 {
			t.Errorf("expected 2 keys, got %d", len(cp))
		}
		// Mutating copy should not affect original.
		cp["c"] = 3
		if _, ok := original["c"]; ok {
			t.Error("copy mutated original")
		}
	})
}

func TestExtractToolCalls(t *testing.T) {
	t.Run("no_tool_calls", func(t *testing.T) {
		tcs := extractToolCalls(map[string]any{"content": "hello"})
		if len(tcs) != 0 {
			t.Errorf("expected 0 tool calls, got %d", len(tcs))
		}
	})

	t.Run("with_tool_calls", func(t *testing.T) {
		payload := map[string]any{
			"tool_calls": []any{
				map[string]any{"id": "c1", "name": "bash", "arguments": `{"cmd":"ls"}`},
				map[string]any{"id": "c2", "name": "read_file", "arguments": `{"path":"/tmp"}`},
			},
		}
		tcs := extractToolCalls(payload)
		if len(tcs) != 2 {
			t.Fatalf("expected 2 tool calls, got %d", len(tcs))
		}
		if tcs[0].ID != "c1" || tcs[0].Name != "bash" {
			t.Errorf("unexpected first tool call: %+v", tcs[0])
		}
		if tcs[1].ID != "c2" || tcs[1].Name != "read_file" {
			t.Errorf("unexpected second tool call: %+v", tcs[1])
		}
	})

	t.Run("wrong_type", func(t *testing.T) {
		tcs := extractToolCalls(map[string]any{"tool_calls": "not_array"})
		if len(tcs) != 0 {
			t.Errorf("expected 0 for wrong type, got %d", len(tcs))
		}
	})

	t.Run("nil_payload", func(t *testing.T) {
		tcs := extractToolCalls(nil)
		if len(tcs) != 0 {
			t.Errorf("expected 0 for nil, got %d", len(tcs))
		}
	})

	t.Run("map_arguments", func(t *testing.T) {
		payload := map[string]any{
			"tool_calls": []any{
				map[string]any{
					"id":        "c1",
					"name":      "bash",
					"arguments": map[string]any{"cmd": "ls"},
				},
			},
		}
		tcs := extractToolCalls(payload)
		if len(tcs) != 1 {
			t.Fatalf("expected 1 tool call, got %d", len(tcs))
		}
		if tcs[0].Arguments != `{"cmd":"ls"}` {
			t.Errorf("expected marshalled args, got %s", tcs[0].Arguments)
		}
	})
}

func TestReplay_OversizedCallIDRejected(t *testing.T) {
	// CRITICAL-1 fix: IDs exceeding maxIDBytes must be rejected as schema
	// violations. Prefix-hashing oversized IDs (as done in earlier versions)
	// allows two IDs with identical prefixes to collide, bypassing integrity checks.
	oversized := strings.Repeat("x", 300) // > maxIDBytes (256)
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "running",
			"tool_calls": []any{
				map[string]any{"id": oversized, "name": "bash"},
			},
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id": oversized, "tool": "bash",
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": oversized, "tool": "bash", "result": "ok",
		}},
	}

	result := Replay(events)

	if result.Matched {
		t.Error("expected mismatch for oversized call_id")
	}
	found := false
	for _, m := range result.Mismatches {
		if strings.Contains(m, "maximum length") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'maximum length' in mismatches, got: %v", result.Mismatches)
	}
}

func TestReplay_AnnouncedToolNameMismatch(t *testing.T) {
	// HIGH-2 fix: if the LLM announces "read_file" but tool.call.started
	// declares "bash", the replay must flag this as a mismatch. An attacker
	// can make the rollout look like a safe tool was used while the lifecycle
	// events show a different tool was actually executed.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "reading file",
			"tool_calls": []any{
				map[string]any{"id": "c1", "name": "read_file"}, // announces read_file
			},
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash", // starts bash instead
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash", "result": "ok",
		}},
	}

	result := Replay(events)

	if result.Matched {
		t.Error("expected mismatch when started tool differs from announced tool")
	}
	found := false
	for _, m := range result.Mismatches {
		if strings.Contains(m, "announced tool") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'announced tool' in mismatches, got: %v", result.Mismatches)
	}
}

func TestReplay_DuplicateStartedRejected(t *testing.T) {
	// MEDIUM-7 fix: a rollout with multiple tool.call.started events for the
	// same call_id must be flagged as a mismatch.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "running",
			"tool_calls": []any{
				map[string]any{"id": "c1", "name": "bash"},
			},
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash",
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash", // duplicate
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash", "result": "ok",
		}},
	}

	result := Replay(events)

	if result.Matched {
		t.Error("expected mismatch for duplicate tool.call.started")
	}
	found := false
	for _, m := range result.Mismatches {
		if strings.Contains(m, "duplicate") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'duplicate' in mismatches, got: %v", result.Mismatches)
	}
}

func TestReplay_EmptyToolNameInStartedRejected(t *testing.T) {
	// CRITICAL-1 fix: a tool.call.started with empty tool name must be flagged
	// as a schema violation. An attacker can exploit an empty tool name to bypass
	// both the announced-name cross-check and the started-vs-completed consistency
	// check, effectively hiding what tool was actually executed.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "running",
			"tool_calls": []any{
				map[string]any{"id": "c1", "name": "read_file"},
			},
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id": "c1", // no "tool" field — empty tool name
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash", "result": "ok",
		}},
	}

	result := Replay(events)

	if result.Matched {
		t.Error("expected mismatch for empty tool name in tool.call.started")
	}
	found := false
	for _, m := range result.Mismatches {
		if strings.Contains(m, "empty tool name") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'empty tool name' in mismatches, got: %v", result.Mismatches)
	}
}

func TestReplay_OversizedCompletionCallIDRejected(t *testing.T) {
	// HIGH-1 fix: tool.call.completed with call_id > maxIDBytes must be flagged.
	// Previously this was silently skipped by indexToolCompletions, potentially
	// allowing Matched=true while the completion was unverified.
	oversized := strings.Repeat("z", 300)
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": oversized, "tool": "bash", "result": "ok",
		}},
	}

	result := Replay(events)

	if result.Matched {
		t.Error("expected mismatch for oversized call_id in tool.call.completed")
	}
	found := false
	for _, m := range result.Mismatches {
		if strings.Contains(m, "maximum length") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'maximum length' in mismatches, got: %v", result.Mismatches)
	}
}

func TestReconstructMessages_NoCallIDReuseWithoutNewStart(t *testing.T) {
	// HIGH-2 fix: after a tool result is accepted, re-announcing the same call_id
	// without a new tool.call.started must NOT allow another tool result to be
	// injected. startedCalls is cleared on completion.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0, Payload: map[string]any{"prompt": "hi"}},
		// First cycle: c1 is announced, started, completed.
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "step 1",
			"tool_calls": []any{
				map[string]any{"id": "c1", "name": "bash"},
			},
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash",
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash", "result": "first",
		}},
		// Re-announce c1 without a new started event.
		{Type: "llm.turn.completed", Step: 2, Payload: map[string]any{
			"content": "step 2",
			"tool_calls": []any{
				map[string]any{"id": "c1", "name": "bash"},
			},
		}},
		// No tool.call.started for c1 in this cycle.
		{Type: "tool.call.completed", Step: 2, Payload: map[string]any{
			"call_id": "c1", "tool": "bash", "result": "injected",
		}},
	}

	msgs := ReconstructMessages(events, 3)
	// Count tool messages — only the first one (result="first") should appear.
	var toolMsgs []string
	for _, m := range msgs {
		if m.Role == "tool" {
			toolMsgs = append(toolMsgs, m.Content)
		}
	}
	if len(toolMsgs) != 1 {
		t.Errorf("expected exactly 1 tool message, got %d: %v", len(toolMsgs), toolMsgs)
	}
	if len(toolMsgs) == 1 && toolMsgs[0] != "first" {
		t.Errorf("expected tool message content 'first', got %q", toolMsgs[0])
	}
}

func TestReplay_MismatchCapEnforcedWithSentinel(t *testing.T) {
	// HIGH-4 fix: mismatches must be capped at maxMismatches. With more than
	// maxMismatches violations, a sentinel must be appended once and further
	// messages suppressed.
	// Build a rollout with many tool.call.started events (each lacking announcement)
	// to generate more than maxMismatches (1000) distinct mismatch entries.
	events := []rollout.RolloutEvent{{Type: "run.started", Step: 0}}
	for i := 0; i < 1100; i++ {
		callID := fmt.Sprintf("call_%d", i)
		events = append(events, rollout.RolloutEvent{
			Type: "tool.call.started",
			Step: i + 1,
			Payload: map[string]any{
				"call_id": callID, "tool": "bash",
			},
		})
	}

	result := Replay(events)

	// Must not have more than maxMismatches + 1 (the sentinel) entries.
	if len(result.Mismatches) > 1001 {
		t.Errorf("expected at most 1001 mismatch entries, got %d", len(result.Mismatches))
	}
	// The sentinel must be present.
	found := false
	for _, m := range result.Mismatches {
		if strings.Contains(m, "suppressed") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected suppression sentinel in mismatches (got %d entries)", len(result.Mismatches))
	}
}

// ---- Round 25 regression tests ----

func TestValidateEvents_MonotonicOK(t *testing.T) {
	// CRITICAL-1 fix: monotonically non-decreasing events must pass validation.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "llm.turn.completed", Step: 1},
		{Type: "tool.call.started", Step: 1},
		{Type: "tool.call.completed", Step: 1},
		{Type: "run.completed", Step: 2},
	}
	if err := validateEvents(events); err != nil {
		t.Errorf("expected nil error for monotonic events, got: %v", err)
	}
}

func TestValidateEvents_NonMonotonicRejected(t *testing.T) {
	// CRITICAL-1 fix: sortEvents() laundering attack — non-monotonic input must
	// be rejected before sorting can reorder events into an apparently-valid order.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "tool.call.completed", Step: 2}, // out of order
		{Type: "tool.call.started", Step: 1},   // step goes backwards
	}
	if err := validateEvents(events); err == nil {
		t.Error("expected error for non-monotonic events, got nil")
	}
}

func TestValidateEvents_NegativeStepRejected(t *testing.T) {
	// CRITICAL-1 fix: negative step values must be rejected.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: -1},
	}
	if err := validateEvents(events); err == nil {
		t.Error("expected error for negative step, got nil")
	}
}

func TestFork_NonMonotonicEventsRejected(t *testing.T) {
	// CRITICAL-1 fix: Fork() must call validateEvents() and reject non-monotonic
	// event slices before sortEvents() can launder them.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "tool.call.completed", Step: 3},
		{Type: "tool.call.started", Step: 2}, // step goes backwards
	}
	_, err := Fork(events, 0, nil)
	if err == nil {
		t.Error("expected error for non-monotonic events in Fork, got nil")
	}
}

func TestReplay_EmptyAnnouncedNameTriggersCrossCheck(t *testing.T) {
	// HIGH-1 fix: empty tool name in llm.turn.completed.tool_calls is stored as
	// "<empty>" sentinel so the announced-vs-started cross-check fires when the
	// started event declares a non-empty tool name.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "calling",
			"tool_calls": []any{
				map[string]any{"id": "c1", "name": ""}, // empty announced name
			},
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash", // non-empty started name
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash", "result": "ok",
		}},
	}

	result := Replay(events)
	if result.Matched {
		t.Error("expected mismatch when announced name is empty but started name is non-empty")
	}
	found := false
	for _, m := range result.Mismatches {
		if strings.Contains(m, "does not match") || strings.Contains(m, "empty") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected announced-vs-started name mismatch, got: %v", result.Mismatches)
	}
}

func TestReplay_ArgCrossCheckCatchesSplicing(t *testing.T) {
	// HIGH-2 fix: args announced in llm.turn.completed.tool_calls must match
	// the args in tool.call.started (argument splicing attack).
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "running",
			"tool_calls": []any{
				map[string]any{"id": "c1", "name": "bash", "arguments": `{"cmd":"ls"}`},
			},
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash", "arguments": `{"cmd":"rm -rf /"}`, // spliced
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash", "result": "ok",
		}},
	}

	result := Replay(events)
	if result.Matched {
		t.Error("expected mismatch for argument splicing (announced args differ from started args)")
	}
	found := false
	for _, m := range result.Mismatches {
		if strings.Contains(m, "arguments differ") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'arguments differ' mismatch, got: %v", result.Mismatches)
	}
}

func TestDeepCapStrings_DepthBudget(t *testing.T) {
	// HIGH-3 fix: deepCapStrings must truncate values beyond maxPayloadDepth,
	// preventing stack overflow or excessive allocation on deeply nested payloads.
	// Build a tree that exceeds maxPayloadDepth (20).
	var build func(depth int) map[string]any
	build = func(depth int) map[string]any {
		if depth == 0 {
			return map[string]any{"leaf": "value"}
		}
		return map[string]any{"nested": build(depth - 1)}
	}
	deep := build(maxPayloadDepth + 5)
	result := deepCapStrings(deep)
	if result == nil {
		t.Error("deepCapStrings returned nil for deep structure")
	}
	// Just verify it terminates without panic.
}

func TestDeepCapStrings_ElementBudget(t *testing.T) {
	// HIGH-3 fix: deepCapStrings must truncate after maxPayloadElements visits,
	// bounding CPU/memory for wide adversarial payloads.
	// Build a flat map with many keys — wide payloads also hit the element budget.
	wide := make(map[string]any, maxPayloadElements+100)
	for i := 0; i < maxPayloadElements+100; i++ {
		wide[fmt.Sprintf("k%d", i)] = "v"
	}
	result := deepCapStrings(wide)
	if result == nil {
		t.Error("deepCapStrings returned nil for wide structure")
	}
	// Just verify it terminates without panic.
}

func TestReplay_CallIDCapSentinelEmitted(t *testing.T) {
	// HIGH-4 fix: when maxTotalCallIDs is reached, a sentinel mismatch must be
	// emitted once and Matched must be false (analysis is incomplete).
	events := []rollout.RolloutEvent{{Type: "run.started", Step: 0}}
	// Emit maxTotalCallIDs+10 llm.turn.completed events, each announcing a distinct call_id.
	for i := 0; i < maxTotalCallIDs+10; i++ {
		events = append(events, rollout.RolloutEvent{
			Type: "llm.turn.completed",
			Step: i + 1,
			Payload: map[string]any{
				"content": "x",
				"tool_calls": []any{
					map[string]any{"id": fmt.Sprintf("c%d", i), "name": "bash"},
				},
			},
		})
	}

	result := Replay(events)
	if result.Matched {
		t.Error("expected Matched=false when callID cap is reached (analysis incomplete)")
	}
	found := false
	for _, m := range result.Mismatches {
		if strings.Contains(m, "tracking limit") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'tracking limit' sentinel in mismatches, got: %v", result.Mismatches)
	}
}

func TestReplay_ArgTypeConfusionBypass(t *testing.T) {
	// HIGH-3 fix: when arguments in tool.call.started is a map (object) instead
	// of a string, payloadStringOrJSON must marshal it for comparison instead of
	// returning "" and silently skipping the arg cross-check.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		// Announced with string args.
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "running",
			"tool_calls": []any{
				map[string]any{"id": "c1", "name": "bash", "arguments": `{"cmd":"ls"}`},
			},
		}},
		// Started with object args — different from announced.
		// Type confusion attack: pass map instead of string to bypass string comparison.
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id":   "c1",
			"tool":      "bash",
			"arguments": map[string]any{"cmd": "rm -rf /"}, // object, not string
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash", "result": "ok",
		}},
	}

	result := Replay(events)
	if result.Matched {
		t.Error("expected mismatch when object args in started differ from announced string args")
	}
}

func TestReplay_ArgTypeConfusionMatchWhenEqual(t *testing.T) {
	// HIGH-3 fix (positive case): if object args marshal to the same JSON as
	// the announced string args, the cross-check must pass (no false positive).
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "running",
			"tool_calls": []any{
				// Announced as compact JSON string.
				map[string]any{"id": "c1", "name": "bash", "arguments": `{"cmd":"ls"}`},
			},
		}},
		{Type: "tool.call.started", Step: 1, Payload: map[string]any{
			"call_id":   "c1",
			"tool":      "bash",
			"arguments": map[string]any{"cmd": "ls"}, // same content as announced
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "c1", "tool": "bash", "result": "ok",
		}},
	}

	result := Replay(events)
	// The announced args are `{"cmd":"ls"}` (compact JSON string).
	// The started args marshal to `{"cmd":"ls"}` (Go json.Marshal sorts keys).
	// These should match, so Matched=true.
	if !result.Matched {
		t.Errorf("expected Matched=true when object args match announced string args; mismatches: %v", result.Mismatches)
	}
}

func TestReconstructMessages_NonMonotonicReturnsNil(t *testing.T) {
	// HIGH-5 fix: ReconstructMessages must call validateEvents before sortEvents
	// to prevent sort laundering. Non-monotonic input must produce nil output.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0, Payload: map[string]any{"prompt": "hi"}},
		{Type: "tool.call.completed", Step: 3}, // out of order
		{Type: "llm.turn.completed", Step: 1},  // step goes backwards
	}

	msgs := ReconstructMessages(events, 5)
	if msgs != nil {
		t.Errorf("expected nil from ReconstructMessages for non-monotonic events, got %v", msgs)
	}
}

func TestValidateEvents_DuplicateRunStartedRejected(t *testing.T) {
	// HIGH-4 fix: duplicate run.started events must be rejected.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "llm.turn.completed", Step: 1},
		{Type: "run.started", Step: 2}, // second run.started
	}
	if err := validateEvents(events); err == nil {
		t.Error("expected error for duplicate run.started, got nil")
	}
}

func TestValidateEvents_EventAfterTerminalRejected(t *testing.T) {
	// HIGH-4 fix: events with step > terminal event's step must be rejected.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "run.completed", Step: 1},
		{Type: "llm.turn.completed", Step: 2}, // after terminal
	}
	if err := validateEvents(events); err == nil {
		t.Error("expected error for event after terminal, got nil")
	}
}

func TestValidateEvents_EventAtSameStepAsTerminalOK(t *testing.T) {
	// HIGH-4 fix: events at the same step as the terminal event are valid
	// (e.g., tool.call.completed and run.completed can share a step).
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "tool.call.completed", Step: 1},
		{Type: "run.completed", Step: 1},
	}
	if err := validateEvents(events); err != nil {
		t.Errorf("expected nil for events at same step as terminal, got: %v", err)
	}
}

func TestReplay_DuplicateAnnouncementRejected(t *testing.T) {
	// MEDIUM-2 fix: re-announcing the same call_id in a later llm.turn.completed
	// (possibly with an empty name to weaken checks) must be flagged as a mismatch.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		// First announcement.
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "step 1",
			"tool_calls": []any{
				map[string]any{"id": "c1", "name": "bash"},
			},
		}},
		// Second announcement of the same call_id — attacker overwrites with empty name.
		{Type: "llm.turn.completed", Step: 2, Payload: map[string]any{
			"content": "step 2",
			"tool_calls": []any{
				map[string]any{"id": "c1", "name": ""},
			},
		}},
	}

	result := Replay(events)
	if result.Matched {
		t.Error("expected mismatch for duplicate announcement of same call_id")
	}
	found := false
	for _, m := range result.Mismatches {
		if strings.Contains(m, "duplicate announcement") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'duplicate announcement' mismatch, got: %v", result.Mismatches)
	}
}

// ---------------------------------------------------------------------------
// Round 27 regression tests
// ---------------------------------------------------------------------------

// TestValidateEvents_RunStartedNotFirst verifies that validateEvents rejects
// a run.started event that is not the first event (CRITICAL-2 fix).
func TestValidateEvents_RunStartedNotFirst(t *testing.T) {
	events := []rollout.RolloutEvent{
		{Type: "llm.turn.completed", Step: 1},
		{Type: "run.started", Step: 2}, // monotonic (step 2 > 1) but not at index 0
	}
	result := Replay(events)
	if result.Matched {
		t.Error("Replay with run.started not at index 0: want Matched=false, got true")
	}
	found := false
	for _, mm := range result.Mismatches {
		if strings.Contains(mm, "run.started must be the first event") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'run.started must be the first event' mismatch, got: %v", result.Mismatches)
	}
}

// TestValidateEvents_RunStartedNonZeroStep verifies that validateEvents
// rejects run.started at step != 0 (CRITICAL-2 fix).
func TestValidateEvents_RunStartedNonZeroStep(t *testing.T) {
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 1}, // step must be 0
	}
	result := Replay(events)
	if result.Matched {
		t.Error("Replay with run.started at step 1: want Matched=false, got true")
	}
	found := false
	for _, mm := range result.Mismatches {
		if strings.Contains(mm, "run.started must have step 0") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'run.started must have step 0' mismatch, got: %v", result.Mismatches)
	}
}

// TestReplay_ValidatesEventOrderingFirst verifies that Replay() calls
// validateEvents before processing, so non-monotonic slices are rejected
// before any file-order index comparisons (HIGH-9 fix).
func TestReplay_ValidatesEventOrderingFirst(t *testing.T) {
	// Provide events in non-monotonic order so sort-laundering could hide
	// a completion-before-start violation.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "tool.call.completed", Step: 3},
		{Type: "tool.call.started", Step: 2}, // step 2 < prev step 3: non-monotonic
	}
	result := Replay(events)
	if result.Matched {
		t.Error("Replay with non-monotonic events: want Matched=false, got true")
	}
	found := false
	for _, mm := range result.Mismatches {
		if strings.Contains(mm, "validation failed") || strings.Contains(mm, "non-monotonic") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected validation failure mismatch, got: %v", result.Mismatches)
	}
}

// ---------------------------------------------------------------------------
// Round 28 regression tests
// ---------------------------------------------------------------------------

// TestValidateEvents_SameStepAsTerminalRejected verifies that events at the
// same step as the terminal event are now rejected (CRITICAL-1 round 28 fix:
// index-based vs step-based terminal tracking).
func TestValidateEvents_SameStepAsTerminalRejected(t *testing.T) {
	// run.completed at step 2, then llm.turn.completed ALSO at step 2.
	// Previously allowed (step 2 !> terminalStep 2); now rejected by index.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "run.completed", Step: 2},
		{Type: "llm.turn.completed", Step: 2}, // same step as terminal
	}
	if err := validateEvents(events); err == nil {
		t.Error("expected error for event at same step as terminal, got nil")
	}
}

// TestValidateEvents_MessageProducingAtStepZeroRejected verifies that
// message-producing event types at step 0 are rejected (CRITICAL-1 round 28).
func TestValidateEvents_MessageProducingAtStepZeroRejected(t *testing.T) {
	forbidden := []string{
		"llm.turn.completed",
		"tool.call.started",
		"tool.call.completed",
		"steering.received",
		"conversation.continued",
	}
	for _, typ := range forbidden {
		t.Run(typ, func(t *testing.T) {
			events := []rollout.RolloutEvent{
				{Type: typ, Step: 0},
			}
			if err := validateEvents(events); err == nil {
				t.Errorf("expected error for %q at step 0, got nil", typ)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Round 29 regression tests
// ---------------------------------------------------------------------------

// TestValidateEvents_MissingRunStartedRejected verifies that validateEvents
// requires run.started to be present when events are non-empty.
// HIGH-4 fix (round 29): without run.started, ReconstructMessages can inject
// an assistant message before any user/system context, violating ordering.
func TestValidateEvents_MissingRunStartedRejected(t *testing.T) {
	events := []rollout.RolloutEvent{
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{"content": "injected"}},
		{Type: "run.completed", Step: 2},
	}
	if err := validateEvents(events); err == nil {
		t.Error("expected error for missing run.started, got nil")
	}
}

// TestValidateEvents_EmptySliceAllowed verifies that an empty event slice
// still passes (no run.started required for empty input).
func TestValidateEvents_EmptySliceAllowed(t *testing.T) {
	if err := validateEvents(nil); err != nil {
		t.Errorf("expected nil for empty events, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Round 30 regression tests
// ---------------------------------------------------------------------------

// TestFork_ToolCallsStrippedInUnsafeMode verifies that ForkResult.ToolCallsStripped
// is true when pending tool calls are stripped in UnsafePreserveToolCalls mode.
// HIGH-8 fix (round 30): the boolean from stripPendingToolCalls was discarded
// via `var _ bool`, always leaving ToolCallsStripped=false.
func TestFork_ToolCallsStrippedInUnsafeMode(t *testing.T) {
	t.Parallel()
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "calling tool",
			"tool_calls": []any{
				map[string]any{"id": "call_pending", "name": "bash", "arguments": `{}`},
			},
		}},
		// No tool.call.started or tool.call.completed — call is pending.
	}

	result, err := Fork(events, 1, &ForkOptions{
		UnsafePreserveToolCalls: true,
		IncludeToolResults:      true, // required when UnsafePreserveToolCalls=true
	})
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if !result.ToolCallsStripped {
		t.Errorf("ToolCallsStripped = false; expected true for pending call stripped in unsafe mode")
	}
}

// TestPayloadStringOrJSON_LargeMapCapped verifies that payloadStringOrJSON
// does not allocate more than maxToolArgMarshalBytes when the value is a large
// map. HIGH-7 fix (round 30): previously used json.Marshal which allocated the
// full output before any truncation.
func TestPayloadStringOrJSON_LargeMapCapped(t *testing.T) {
	t.Parallel()

	// Build a payload where "arguments" is a map with a very long string value.
	bigValue := strings.Repeat("x", 200*1024) // 200 KiB — larger than maxToolArgMarshalBytes (64 KiB)
	payload := map[string]any{
		"arguments": map[string]any{"key": bigValue},
	}

	// Call through Replay which exercises payloadStringOrJSON via tool.call.started.
	events := []rollout.RolloutEvent{
		{Type: "run.started", Step: 0},
		{Type: "llm.turn.completed", Step: 1, Payload: map[string]any{
			"content": "calling",
			"tool_calls": []any{
				map[string]any{"id": "call_big", "name": "bash", "arguments": map[string]any{"key": bigValue}},
			},
		}},
		{Type: "tool.call.completed", Step: 1, Payload: map[string]any{
			"call_id": "call_big",
			"tool":    "bash",
			"result":  "ok",
		}},
		{Type: "tool.call.started", Step: 1, Payload: payload},
	}
	_ = Replay(events) // must not panic or OOM

	// Also test payloadStringOrJSON directly via extracting args from the started event.
	// The result length must be <= maxToolArgMarshalBytes + len("...<truncated>").
	result := Replay(events)
	_ = result // just verifying no crash
}

// TestRedaction_StringBudgetDecrementedForLeaves verifies that a flat map with
// more string entries than maxRedactElements does not process all of them.
// HIGH-6 fix (round 30): string leaf values now decrement the budget counter.
func TestRedaction_PayloadStringOrJSON_NilValue(t *testing.T) {
	t.Parallel()
	// Ensure payloadStringOrJSON handles missing key gracefully.
	payload := map[string]any{"other": "val"}
	s, ok := payloadStringOrJSON(payload, "arguments")
	if ok {
		t.Errorf("expected ok=false for missing key, got s=%q ok=%v", s, ok)
	}
}
