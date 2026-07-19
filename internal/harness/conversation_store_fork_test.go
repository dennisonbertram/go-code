package harness

import (
	"context"
	"testing"
)

// forkSeedMessages returns a representative message history covering plain
// turns, tool calls (with tool_call_id/name), and a meta message, so the fork
// copy can be verified field-by-field.
func forkSeedMessages() []Message {
	return []Message{
		{Role: "user", Content: "Refactor the parser."},
		{Role: "assistant", Content: "Reading the parser first.", ToolCalls: []ToolCall{
			{ID: "call_1", Name: "read_file", Arguments: `{"path":"parser.go"}`},
		}},
		{Role: "tool", ToolCallID: "call_1", Name: "read_file", Content: `{"content":"package parser"}`},
		{Role: "system", Content: "<skill name=\"go\">Go instructions</skill>", IsMeta: true},
		{Role: "assistant", Content: "Done, parser refactored."},
	}
}

func assertMessagesEqual(t *testing.T, want, got []Message) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("message count: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Role != want[i].Role {
			t.Errorf("msg[%d] role: got %q, want %q", i, got[i].Role, want[i].Role)
		}
		if got[i].Content != want[i].Content {
			t.Errorf("msg[%d] content: got %q, want %q", i, got[i].Content, want[i].Content)
		}
		if got[i].ToolCallID != want[i].ToolCallID {
			t.Errorf("msg[%d] tool_call_id: got %q, want %q", i, got[i].ToolCallID, want[i].ToolCallID)
		}
		if got[i].Name != want[i].Name {
			t.Errorf("msg[%d] name: got %q, want %q", i, got[i].Name, want[i].Name)
		}
		if got[i].IsMeta != want[i].IsMeta {
			t.Errorf("msg[%d] is_meta: got %v, want %v", i, got[i].IsMeta, want[i].IsMeta)
		}
		if len(got[i].ToolCalls) != len(want[i].ToolCalls) {
			t.Errorf("msg[%d] tool_calls count: got %d, want %d", i, len(got[i].ToolCalls), len(want[i].ToolCalls))
			continue
		}
		for j := range want[i].ToolCalls {
			if got[i].ToolCalls[j] != want[i].ToolCalls[j] {
				t.Errorf("msg[%d] tool_calls[%d]: got %+v, want %+v", i, j, got[i].ToolCalls[j], want[i].ToolCalls[j])
			}
		}
	}
}

// TestForkConversationCopiesFullHistory verifies that forking a persisted
// conversation produces a new conversation whose messages equal the source in
// count, order, role, and content.
func TestForkConversationCopiesFullHistory(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := forkSeedMessages()
	if err := store.SaveConversation(ctx, "conv-src", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	fork, err := store.ForkConversation(ctx, "conv-src", "conv-fork")
	if err != nil {
		t.Fatalf("ForkConversation: %v", err)
	}
	if fork == nil {
		t.Fatal("ForkConversation returned nil conversation")
	}
	if fork.ID != "conv-fork" {
		t.Errorf("fork ID: got %q, want %q", fork.ID, "conv-fork")
	}
	if fork.MsgCount != len(msgs) {
		t.Errorf("fork MsgCount: got %d, want %d", fork.MsgCount, len(msgs))
	}

	loaded, err := store.LoadMessages(ctx, "conv-fork")
	if err != nil {
		t.Fatalf("LoadMessages on fork: %v", err)
	}
	assertMessagesEqual(t, msgs, loaded)

	// Source must be untouched by the fork.
	srcLoaded, err := store.LoadMessages(ctx, "conv-src")
	if err != nil {
		t.Fatalf("LoadMessages on source: %v", err)
	}
	assertMessagesEqual(t, msgs, srcLoaded)
}

// TestForkConversationDivergenceIsolation verifies that after a fork, writes
// to either conversation are invisible to the other.
func TestForkConversationDivergenceIsolation(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	base := []Message{
		{Role: "user", Content: "Start."},
		{Role: "assistant", Content: "Acknowledged."},
	}
	if err := store.SaveConversation(ctx, "conv-src", base); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}
	if _, err := store.ForkConversation(ctx, "conv-src", "conv-fork"); err != nil {
		t.Fatalf("ForkConversation: %v", err)
	}

	// Append to the source only.
	srcNext := append(append([]Message{}, base...), Message{Role: "user", Content: "Source-only turn."})
	if err := store.SaveConversation(ctx, "conv-src", srcNext); err != nil {
		t.Fatalf("SaveConversation on source: %v", err)
	}
	forkLoaded, err := store.LoadMessages(ctx, "conv-fork")
	if err != nil {
		t.Fatalf("LoadMessages on fork: %v", err)
	}
	assertMessagesEqual(t, base, forkLoaded)

	// Append to the fork only.
	forkNext := append(append([]Message{}, base...), Message{Role: "user", Content: "Fork-only turn."})
	if err := store.SaveConversation(ctx, "conv-fork", forkNext); err != nil {
		t.Fatalf("SaveConversation on fork: %v", err)
	}
	srcLoaded, err := store.LoadMessages(ctx, "conv-src")
	if err != nil {
		t.Fatalf("LoadMessages on source: %v", err)
	}
	assertMessagesEqual(t, srcNext, srcLoaded)
}

// TestForkConversationErrors covers the two failure paths: forking a source
// that does not exist, and forking onto a target ID that is already taken.
func TestForkConversationErrors(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	if _, err := store.ForkConversation(ctx, "conv-missing", "conv-new"); err == nil {
		t.Fatal("expected error forking nonexistent source, got nil")
	}
	// The failed fork must not leave a target row behind.
	if owner, err := store.GetConversationOwner(ctx, "conv-new"); err != nil {
		t.Fatalf("GetConversationOwner: %v", err)
	} else if owner != nil {
		t.Fatalf("failed fork left target conversation behind: %+v", owner)
	}

	if err := store.SaveConversation(ctx, "conv-src", []Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("SaveConversation conv-src: %v", err)
	}
	if err := store.SaveConversation(ctx, "conv-taken", []Message{{Role: "user", Content: "occupied"}}); err != nil {
		t.Fatalf("SaveConversation conv-taken: %v", err)
	}
	if _, err := store.ForkConversation(ctx, "conv-src", "conv-taken"); err == nil {
		t.Fatal("expected error forking onto existing target ID, got nil")
	}
	// The existing target must be unmodified.
	loaded, err := store.LoadMessages(ctx, "conv-taken")
	if err != nil {
		t.Fatalf("LoadMessages conv-taken: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Content != "occupied" {
		t.Fatalf("fork onto existing ID clobbered the target: %+v", loaded)
	}
}

// TestForkConversationInheritsWorkspaceAndTenant verifies that the fork row
// inherits workspace and tenant_id from the source, while pinned and the
// token/cost counters reset to zero-values and timestamps are fresh.
func TestForkConversationInheritsWorkspaceAndTenant(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	cost := ConversationTokenCost{PromptTokens: 123, CompletionTokens: 45, CostUSD: 0.67}
	if err := store.SaveConversationWithCost(ctx, "conv-src", msgs, cost); err != nil {
		t.Fatalf("SaveConversationWithCost: %v", err)
	}
	if err := store.UpdateConversationMeta(ctx, "conv-src", "/ws/path", "tenant-1"); err != nil {
		t.Fatalf("UpdateConversationMeta: %v", err)
	}
	if err := store.PinConversation(ctx, "conv-src", true); err != nil {
		t.Fatalf("PinConversation: %v", err)
	}
	src, err := store.GetConversationOwner(ctx, "conv-src")
	if err != nil {
		t.Fatalf("GetConversationOwner on source: %v", err)
	}
	if src == nil {
		t.Fatal("source conversation not found")
	}

	fork, err := store.ForkConversation(ctx, "conv-src", "conv-fork")
	if err != nil {
		t.Fatalf("ForkConversation: %v", err)
	}
	if fork == nil {
		t.Fatal("ForkConversation returned nil conversation")
	}

	if fork.Workspace != src.Workspace {
		t.Errorf("fork workspace: got %q, want %q", fork.Workspace, src.Workspace)
	}
	if fork.TenantID != src.TenantID {
		t.Errorf("fork tenant_id: got %q, want %q", fork.TenantID, src.TenantID)
	}
	if fork.Pinned {
		t.Error("fork must not inherit the pinned flag")
	}
	if fork.PromptTokens != 0 || fork.CompletionTokens != 0 || fork.CostUSD != 0 {
		t.Errorf("fork token/cost counters must start at zero, got prompt=%d completion=%d cost=%f",
			fork.PromptTokens, fork.CompletionTokens, fork.CostUSD)
	}
	if fork.CreatedAt.Before(src.CreatedAt) || fork.UpdatedAt.Before(src.UpdatedAt) {
		t.Errorf("fork timestamps must be fresh (>= source): created %v vs %v, updated %v vs %v",
			fork.CreatedAt, src.CreatedAt, fork.UpdatedAt, src.UpdatedAt)
	}

	// The owner row read back from the store must agree with the returned fork.
	owner, err := store.GetConversationOwner(ctx, "conv-fork")
	if err != nil {
		t.Fatalf("GetConversationOwner on fork: %v", err)
	}
	if owner == nil {
		t.Fatal("fork conversation not found via GetConversationOwner")
	}
	if owner.Workspace != src.Workspace || owner.TenantID != src.TenantID {
		t.Errorf("persisted fork meta: workspace=%q tenant=%q, want workspace=%q tenant=%q",
			owner.Workspace, owner.TenantID, src.Workspace, src.TenantID)
	}
	if owner.MsgCount != len(msgs) {
		t.Errorf("persisted fork msg_count: got %d, want %d", owner.MsgCount, len(msgs))
	}
}

// TestForkConversationEmptySource verifies that forking a conversation with
// zero messages succeeds and yields an empty fork.
func TestForkConversationEmptySource(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	if err := store.SaveConversation(ctx, "conv-empty", []Message{}); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}
	fork, err := store.ForkConversation(ctx, "conv-empty", "conv-empty-fork")
	if err != nil {
		t.Fatalf("ForkConversation: %v", err)
	}
	if fork.MsgCount != 0 {
		t.Errorf("fork MsgCount: got %d, want 0", fork.MsgCount)
	}
	loaded, err := store.LoadMessages(ctx, "conv-empty-fork")
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("expected empty fork, got %d messages", len(loaded))
	}
}
