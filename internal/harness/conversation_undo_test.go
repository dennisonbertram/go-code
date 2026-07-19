package harness

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Epic #805 Slice 1: ConversationStore.UndoPrompts tests
// ---------------------------------------------------------------------------

func TestUndoPrompts_RemovesLastPromptAndTail(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2", ToolCalls: []ToolCall{{ID: "call_1", Name: "read_file", Arguments: `{"path":"x"}`}}},
		{Role: "tool", ToolCallID: "call_1", Name: "read_file", Content: "file body"},
		{Role: "assistant", Content: "a3"},
	}
	if err := store.SaveConversation(ctx, "conv-undo-basic", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	removedFromStep, err := store.UndoPrompts(ctx, "conv-undo-basic", 1)
	if err != nil {
		t.Fatalf("UndoPrompts: %v", err)
	}
	if removedFromStep != 2 {
		t.Errorf("removedFromStep: got %d, want %d (step of prompt q2)", removedFromStep, 2)
	}

	loaded, err := store.LoadMessages(ctx, "conv-undo-basic")
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	// q1, a1 remain; the undo-boundary marker is appended at the removed step.
	if len(loaded) != 3 {
		t.Fatalf("expected 3 messages after undo (2 kept + marker), got %d: %+v", len(loaded), loaded)
	}
	if loaded[0].Role != "user" || loaded[0].Content != "q1" {
		t.Errorf("message[0]: got %+v, want user q1", loaded[0])
	}
	if loaded[1].Role != "assistant" || loaded[1].Content != "a1" {
		t.Errorf("message[1]: got %+v, want assistant a1", loaded[1])
	}
}

func TestUndoPrompts_WalksBackNUserPromptsSkippingMeta(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "system", Content: "skill instructions", IsMeta: true},
		{Role: "user", Content: "hidden meta note", IsMeta: true},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "q3"},
		{Role: "assistant", Content: "a3"},
	}
	if err := store.SaveConversation(ctx, "conv-undo-n", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	// count=2 must target q2 (step 4): the is_meta user-role message at step 3
	// is not a prompt and must not be counted.
	removedFromStep, err := store.UndoPrompts(ctx, "conv-undo-n", 2)
	if err != nil {
		t.Fatalf("UndoPrompts: %v", err)
	}
	if removedFromStep != 4 {
		t.Errorf("removedFromStep: got %d, want %d (step of prompt q2)", removedFromStep, 4)
	}

	loaded, err := store.LoadMessages(ctx, "conv-undo-n")
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	// Steps 0..3 kept (including both meta messages) plus the marker at step 4.
	if len(loaded) != 5 {
		t.Fatalf("expected 5 messages after undo (4 kept + marker), got %d: %+v", len(loaded), loaded)
	}
	wantContents := []string{"q1", "a1", "skill instructions", "hidden meta note"}
	for i, want := range wantContents {
		if loaded[i].Content != want {
			t.Errorf("message[%d].Content: got %q, want %q", i, loaded[i].Content, want)
		}
	}
	if !loaded[2].IsMeta || !loaded[3].IsMeta {
		t.Errorf("meta flags must survive undo: msg[2].IsMeta=%v msg[3].IsMeta=%v", loaded[2].IsMeta, loaded[3].IsMeta)
	}
}

func TestUndoPrompts_CountOutOfRange(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2"},
	}
	if err := store.SaveConversation(ctx, "conv-undo-range", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	cases := map[string]int{
		"zero count":               0,
		"negative count":           -2,
		"more counts than prompts": 3,
	}
	for name, count := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := store.UndoPrompts(ctx, "conv-undo-range", count)
			if err == nil {
				t.Fatalf("UndoPrompts(count=%d): expected error, got nil", count)
			}
			if !errors.Is(err, ErrUndoCountOutOfRange) {
				t.Errorf("UndoPrompts(count=%d): got %v, want errors.Is ErrUndoCountOutOfRange", count, err)
			}
		})
	}

	// A failed undo must leave the conversation untouched.
	loaded, err := store.LoadMessages(ctx, "conv-undo-range")
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != len(msgs) {
		t.Fatalf("conversation mutated by failed undo: got %d messages, want %d", len(loaded), len(msgs))
	}
	for i, m := range msgs {
		if loaded[i].Content != m.Content {
			t.Errorf("message[%d].Content: got %q, want %q", i, loaded[i].Content, m.Content)
		}
	}
}

func TestUndoPrompts_RefusesToCrossCompactionBoundary(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	// A compaction summary can sit mid-history when the runner persists its
	// in-memory compacted context via SaveConversation.
	msgs := []Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "system", Content: "summary of earlier context", IsCompactSummary: true},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "q3"},
		{Role: "assistant", Content: "a3"},
	}
	if err := store.SaveConversation(ctx, "conv-undo-compact", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	// count=3 targets the oldest prompt q1 at step 0, which is at/below the
	// compaction summary at step 2 — must be refused and leave history untouched.
	_, err := store.UndoPrompts(ctx, "conv-undo-compact", 3)
	if err == nil {
		t.Fatal("UndoPrompts across compaction boundary: expected error, got nil")
	}
	if !errors.Is(err, ErrUndoCrossesCompaction) {
		t.Errorf("got %v, want errors.Is ErrUndoCrossesCompaction", err)
	}

	loaded, err := store.LoadMessages(ctx, "conv-undo-compact")
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != len(msgs) {
		t.Fatalf("conversation mutated by refused undo: got %d messages, want %d", len(loaded), len(msgs))
	}

	// count=1 targets q3 at step 5, above the boundary — must succeed.
	removedFromStep, err := store.UndoPrompts(ctx, "conv-undo-compact", 1)
	if err != nil {
		t.Fatalf("UndoPrompts above boundary: %v", err)
	}
	if removedFromStep != 5 {
		t.Errorf("removedFromStep: got %d, want %d (step of prompt q3)", removedFromStep, 5)
	}

	// Undo is allowed right up to the boundary: q2 at step 3 is still above the
	// summary at step 2.
	secondStep, err := store.UndoPrompts(ctx, "conv-undo-compact", 1)
	if err != nil {
		t.Fatalf("UndoPrompts up to boundary: %v", err)
	}
	if secondStep != 3 {
		t.Errorf("second removedFromStep: got %d, want %d (step of prompt q2)", secondStep, 3)
	}

	// Now only q1 (step 0) remains, at/below the boundary: every further undo
	// must hit the boundary guard, not the range guard.
	_, err = store.UndoPrompts(ctx, "conv-undo-compact", 1)
	if !errors.Is(err, ErrUndoCrossesCompaction) {
		t.Errorf("post-undo boundary check: got %v, want errors.Is ErrUndoCrossesCompaction", err)
	}
}

func TestUndoPrompts_PersistsBoundaryMarker(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2"},
	}
	if err := store.SaveConversation(ctx, "conv-undo-marker", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	removedFromStep, err := store.UndoPrompts(ctx, "conv-undo-marker", 1)
	if err != nil {
		t.Fatalf("UndoPrompts: %v", err)
	}

	loaded, err := store.LoadMessages(ctx, "conv-undo-marker")
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 messages (2 kept + marker), got %d: %+v", len(loaded), loaded)
	}
	marker := loaded[2]
	if !marker.IsMeta {
		t.Error("undo-boundary marker must be persisted with IsMeta=true")
	}
	if marker.Content == "" {
		t.Error("undo-boundary marker must carry human-readable content")
	}
	if marker.IsCompactSummary {
		t.Error("undo-boundary marker must not be flagged as a compaction summary")
	}
	// The marker occupies the step the removed prompt held, keeping steps contiguous.
	if removedFromStep != 2 {
		t.Errorf("removedFromStep: got %d, want 2", removedFromStep)
	}

	// Conversation metadata stays consistent with the truncated history.
	convs, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}
	if convs[0].MsgCount != len(loaded) {
		t.Errorf("msg_count: got %d, want %d (including marker)", convs[0].MsgCount, len(loaded))
	}

	// A subsequent undo skips the marker and targets the next real prompt (q1).
	secondStep, err := store.UndoPrompts(ctx, "conv-undo-marker", 1)
	if err != nil {
		t.Fatalf("second UndoPrompts: %v", err)
	}
	if secondStep != 0 {
		t.Errorf("second removedFromStep: got %d, want 0 (step of prompt q1)", secondStep)
	}
	loaded, err = store.LoadMessages(ctx, "conv-undo-marker")
	if err != nil {
		t.Fatalf("LoadMessages after second undo: %v", err)
	}
	if len(loaded) != 1 || !loaded[0].IsMeta {
		t.Fatalf("expected only the new marker to remain, got %+v", loaded)
	}
}

func TestUndoPrompts_ConversationNotFound(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	_, err := store.UndoPrompts(ctx, "no-such-conversation", 1)
	if err == nil {
		t.Fatal("expected error for unknown conversation, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention the conversation was not found, got: %v", err)
	}
}
