package harness

import (
	"context"
	"encoding/json"
	"testing"

	htools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/core"
)

// TestCompactionTranscriptRoundTripPreservesToolCalls verifies that an
// assistant message with ToolCalls survives a harness.Message ->
// htools.TranscriptMessage -> harness.Message round-trip. This is the path
// used by CompactRun/autoCompactMessages when the tail is preserved.
func TestCompactionTranscriptRoundTripPreservesToolCalls(t *testing.T) {
	t.Parallel()

	original := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Read file.txt"},
		{
			Role:    "assistant",
			Content: "I'll read it.",
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: "read", Arguments: `{"path":"file.txt"}`},
			},
		},
		{Role: "tool", ToolCallID: "call_1", Content: "file contents"},
	}

	snap := messagesAsTranscriptSnapshot(original)
	got := transcriptMessagesToHarness(snap)

	if len(got) != len(original) {
		t.Fatalf("expected %d messages, got %d", len(original), len(got))
	}

	assistant := got[2]
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call on assistant message, got %d", len(assistant.ToolCalls))
	}
	tc := assistant.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "read" || tc.Arguments != `{"path":"file.txt"}` {
		t.Errorf("tool call not preserved: got %+v", tc)
	}

	if got[3].ToolCallID != "call_1" || got[3].Content != "file contents" {
		t.Errorf("tool result message not preserved: got %+v", got[3])
	}
}

type testToolCallTranscriptReader struct {
	messages []htools.TranscriptMessage
}

func (r testToolCallTranscriptReader) Snapshot(limit int, includeTools bool) htools.TranscriptSnapshot {
	msgs := r.messages
	if !includeTools {
		var filtered []htools.TranscriptMessage
		for _, m := range msgs {
			if m.Role != "tool" {
				filtered = append(filtered, m)
			}
		}
		msgs = filtered
	}
	if limit > 0 && len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	return htools.TranscriptSnapshot{Messages: msgs}
}

// TestCompactHistoryMapRoundTripPreservesToolCalls verifies that the
// compact_history tool's map encoding (transcriptMsgsToMaps) and the
// messageReplacer's decoding (messageMapToMessage) preserve assistant ToolCalls.
func TestCompactHistoryMapRoundTripPreservesToolCalls(t *testing.T) {
	t.Parallel()

	msgs := []htools.TranscriptMessage{
		{Index: 0, Role: "system", Content: "You are helpful."},
		{Index: 1, Role: "user", Content: "step1"},
		{
			Index:   2,
			Role:    "assistant",
			Content: "call echo",
			ToolCalls: []htools.ToolCall{
				{ID: "call_1", Name: "echo", Arguments: `{"m":"x"}`},
			},
		},
		{Index: 3, Role: "tool", ToolCallID: "call_1", Content: "echoed"},
		{Index: 4, Role: "user", Content: "step2"},
		{
			Index:   5,
			Role:    "assistant",
			Content: "compact now",
			ToolCalls: []htools.ToolCall{
				{ID: "call_2", Name: "compact_history", Arguments: `{"mode":"strip","keep_last":2}`},
			},
		},
	}

	reader := testToolCallTranscriptReader{messages: msgs}
	var gotMaps []map[string]any
	replacer := func(maps []map[string]any) {
		gotMaps = maps
	}

	ctx := context.Background()
	ctx = context.WithValue(ctx, htools.ContextKeyTranscriptReader, reader)
	ctx = context.WithValue(ctx, htools.ContextKeyMessageReplacer, replacer)

	tool := core.CompactHistoryTool(nil)
	if _, err := tool.Handler(ctx, json.RawMessage(`{"mode":"strip","keep_last":2}`)); err != nil {
		t.Fatalf("compact_history handler: %v", err)
	}

	if len(gotMaps) == 0 {
		t.Fatal("replacer was not called")
	}

	found := false
	for _, m := range gotMaps {
		if role, _ := m["role"].(string); role == "assistant" {
			if content, _ := m["content"].(string); content == "compact now" {
				found = true
				msg := messageMapToMessage(m)
				if len(msg.ToolCalls) != 1 {
					t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
				}
				tc := msg.ToolCalls[0]
				if tc.ID != "call_2" || tc.Name != "compact_history" || tc.Arguments != `{"mode":"strip","keep_last":2}` {
					t.Errorf("tool call mismatch: got %+v", tc)
				}
			}
		}
	}
	if !found {
		t.Fatal("tail assistant message with ToolCalls not found in replacer output")
	}
}
