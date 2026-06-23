package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newTestConversationStore(t *testing.T) *SQLiteConversationStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test-conv.db")
	store, err := NewSQLiteConversationStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteConversationStore: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestConversationStoreSaveAndLoadMessages(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!", ToolCalls: []ToolCall{
			{ID: "call_1", Name: "read_file", Arguments: `{"path":"main.go"}`},
		}},
		{Role: "tool", ToolCallID: "call_1", Name: "read_file", Content: `{"content":"package main"}`},
		{Role: "assistant", Content: "I see the file."},
	}

	if err := store.SaveConversation(ctx, "conv-1", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	loaded, err := store.LoadMessages(ctx, "conv-1")
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}

	if len(loaded) != len(msgs) {
		t.Fatalf("expected %d messages, got %d", len(msgs), len(loaded))
	}

	for i, m := range loaded {
		if m.Role != msgs[i].Role {
			t.Errorf("msg[%d] role: got %q, want %q", i, m.Role, msgs[i].Role)
		}
		if m.Content != msgs[i].Content {
			t.Errorf("msg[%d] content: got %q, want %q", i, m.Content, msgs[i].Content)
		}
		if m.ToolCallID != msgs[i].ToolCallID {
			t.Errorf("msg[%d] tool_call_id: got %q, want %q", i, m.ToolCallID, msgs[i].ToolCallID)
		}
		if m.Name != msgs[i].Name {
			t.Errorf("msg[%d] name: got %q, want %q", i, m.Name, msgs[i].Name)
		}
	}
}

func TestConversationStoreSaveOverwrites(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs1 := []Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi"},
	}
	if err := store.SaveConversation(ctx, "conv-overwrite", msgs1); err != nil {
		t.Fatalf("SaveConversation (1): %v", err)
	}

	msgs2 := []Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi"},
		{Role: "user", Content: "How are you?"},
		{Role: "assistant", Content: "Good, thanks!"},
	}
	if err := store.SaveConversation(ctx, "conv-overwrite", msgs2); err != nil {
		t.Fatalf("SaveConversation (2): %v", err)
	}

	loaded, err := store.LoadMessages(ctx, "conv-overwrite")
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}

	if len(loaded) != 4 {
		t.Fatalf("expected 4 messages after overwrite, got %d", len(loaded))
	}
	if loaded[3].Content != "Good, thanks!" {
		t.Errorf("expected last message content 'Good, thanks!', got %q", loaded[3].Content)
	}
}

func TestConversationStoreListConversations(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	// Save 3 conversations
	for i := 0; i < 3; i++ {
		msgs := []Message{{Role: "user", Content: fmt.Sprintf("msg-%d", i)}}
		if err := store.SaveConversation(ctx, fmt.Sprintf("conv-%d", i), msgs); err != nil {
			t.Fatalf("SaveConversation conv-%d: %v", i, err)
		}
		// Small sleep to ensure distinct updated_at values
		time.Sleep(10 * time.Millisecond)
	}

	// List all
	convs, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 3 {
		t.Fatalf("expected 3 conversations, got %d", len(convs))
	}

	// Should be ordered by updated_at DESC (most recent first)
	if convs[0].ID != "conv-2" {
		t.Errorf("expected first conversation 'conv-2', got %q", convs[0].ID)
	}

	// Test limit
	convs, err = store.ListConversations(ctx, ConversationFilter{}, 2, 0)
	if err != nil {
		t.Fatalf("ListConversations with limit: %v", err)
	}
	if len(convs) != 2 {
		t.Fatalf("expected 2 conversations with limit, got %d", len(convs))
	}

	// Test offset
	convs, err = store.ListConversations(ctx, ConversationFilter{}, 10, 2)
	if err != nil {
		t.Fatalf("ListConversations with offset: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation with offset 2, got %d", len(convs))
	}
}

func TestConversationStoreDeleteConversation(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{{Role: "user", Content: "Hello"}}
	if err := store.SaveConversation(ctx, "conv-del", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	if err := store.DeleteConversation(ctx, "conv-del"); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}

	// Messages should be gone (cascaded)
	loaded, err := store.LoadMessages(ctx, "conv-del")
	if err != nil {
		t.Fatalf("LoadMessages after delete: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected 0 messages after delete, got %d", len(loaded))
	}

	// Conversation should not appear in list
	convs, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations after delete: %v", err)
	}
	if len(convs) != 0 {
		t.Fatalf("expected 0 conversations after delete, got %d", len(convs))
	}
}

func TestConversationStoreConcurrentAccess(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			convID := fmt.Sprintf("concurrent-conv-%d", idx)
			msgs := []Message{
				{Role: "user", Content: fmt.Sprintf("question-%d", idx)},
				{Role: "assistant", Content: fmt.Sprintf("answer-%d", idx)},
			}
			if err := store.SaveConversation(ctx, convID, msgs); err != nil {
				errs <- fmt.Errorf("save %d: %w", idx, err)
				return
			}
			loaded, err := store.LoadMessages(ctx, convID)
			if err != nil {
				errs <- fmt.Errorf("load %d: %w", idx, err)
				return
			}
			if len(loaded) != 2 {
				errs <- fmt.Errorf("expected 2 messages for conv %d, got %d", idx, len(loaded))
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

func TestConversationStoreEmptyConversation(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	loaded, err := store.LoadMessages(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("LoadMessages for nonexistent: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected 0 messages for nonexistent conversation, got %d", len(loaded))
	}
}

func TestConversationStoreToolCallsSerialization(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	toolCalls := []ToolCall{
		{ID: "call_abc", Name: "bash", Arguments: `{"command":"ls -la"}`},
		{ID: "call_def", Name: "write_file", Arguments: `{"path":"test.go","content":"package test"}`},
	}

	msgs := []Message{
		{Role: "user", Content: "Do stuff"},
		{Role: "assistant", Content: "", ToolCalls: toolCalls},
		{Role: "tool", ToolCallID: "call_abc", Name: "bash", Content: "file.txt"},
		{Role: "tool", ToolCallID: "call_def", Name: "write_file", Content: "ok"},
		{Role: "assistant", Content: "Done"},
	}

	if err := store.SaveConversation(ctx, "conv-tools", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	loaded, err := store.LoadMessages(ctx, "conv-tools")
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}

	if len(loaded) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(loaded))
	}

	// Check tool calls round-trip
	if len(loaded[1].ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(loaded[1].ToolCalls))
	}

	for i, tc := range loaded[1].ToolCalls {
		if tc.ID != toolCalls[i].ID {
			t.Errorf("tool call %d ID: got %q, want %q", i, tc.ID, toolCalls[i].ID)
		}
		if tc.Name != toolCalls[i].Name {
			t.Errorf("tool call %d Name: got %q, want %q", i, tc.Name, toolCalls[i].Name)
		}

		// Parse arguments to compare as JSON (avoid whitespace issues)
		var gotArgs, wantArgs map[string]any
		if err := json.Unmarshal([]byte(tc.Arguments), &gotArgs); err != nil {
			t.Errorf("tool call %d: failed to parse got arguments: %v", i, err)
		}
		if err := json.Unmarshal([]byte(toolCalls[i].Arguments), &wantArgs); err != nil {
			t.Errorf("tool call %d: failed to parse want arguments: %v", i, err)
		}
		gotJSON, _ := json.Marshal(gotArgs)
		wantJSON, _ := json.Marshal(wantArgs)
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("tool call %d Arguments: got %q, want %q", i, tc.Arguments, toolCalls[i].Arguments)
		}
	}

	// Messages without tool calls should have nil/empty ToolCalls
	if len(loaded[0].ToolCalls) != 0 {
		t.Errorf("expected no tool calls on user message, got %d", len(loaded[0].ToolCalls))
	}
}

func TestConversationStoreMsgCount(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi"},
		{Role: "user", Content: "Bye"},
	}
	if err := store.SaveConversation(ctx, "conv-count", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	convs, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}
	if convs[0].MsgCount != 3 {
		t.Errorf("expected MsgCount 3, got %d", convs[0].MsgCount)
	}
}

func TestConversationStoreDeleteNonExistent(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	// Deleting a non-existent conversation should not error
	if err := store.DeleteConversation(ctx, "does-not-exist"); err != nil {
		t.Fatalf("DeleteConversation on non-existent: %v", err)
	}
}

func TestConversationStoreEmptyPath(t *testing.T) {
	t.Parallel()
	_, err := NewSQLiteConversationStore("")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

// --- Regression tests for conversation persistence ---

func TestConversationStoreConcurrentSavesSameConversation(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	const goroutines = 10
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			msgs := []Message{
				{Role: "user", Content: fmt.Sprintf("question-%d", idx)},
				{Role: "assistant", Content: fmt.Sprintf("answer-%d", idx)},
			}
			if err := store.SaveConversation(ctx, "same-conv", msgs); err != nil {
				errs <- fmt.Errorf("goroutine %d save: %w", idx, err)
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}

	// After all concurrent saves, the conversation should exist with 2 messages
	loaded, err := store.LoadMessages(ctx, "same-conv")
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded))
	}
}

func TestConversationStoreInvalidDBPath(t *testing.T) {
	t.Parallel()
	// Path to a file inside a non-writable location (root-level device path)
	_, err := NewSQLiteConversationStore("/dev/null/impossible/path.db")
	if err == nil {
		t.Fatal("expected error for invalid DB path")
	}
}

func TestConversationStoreClosedStoreOperations(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "closed.db")
	store, err := NewSQLiteConversationStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteConversationStore: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Close the store
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Operations on closed store should return errors
	ctx := context.Background()

	if err := store.SaveConversation(ctx, "conv-1", []Message{{Role: "user", Content: "hi"}}); err == nil {
		t.Error("expected error saving to closed store")
	}

	if _, err := store.LoadMessages(ctx, "conv-1"); err == nil {
		t.Error("expected error loading from closed store")
	}

	if _, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0); err == nil {
		t.Error("expected error listing from closed store")
	}

	if err := store.DeleteConversation(ctx, "conv-1"); err == nil {
		t.Error("expected error deleting from closed store")
	}
}

func TestConversationStoreEmptyMessages(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	// Save with empty message slice
	if err := store.SaveConversation(ctx, "conv-empty", []Message{}); err != nil {
		t.Fatalf("SaveConversation with empty messages: %v", err)
	}

	loaded, err := store.LoadMessages(ctx, "conv-empty")
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(loaded))
	}

	// Conversation should still appear in list with msg_count=0
	convs, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}
	if convs[0].MsgCount != 0 {
		t.Fatalf("expected MsgCount=0, got %d", convs[0].MsgCount)
	}
}

func TestConversationStoreNilMessages(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	// Save with nil message slice
	if err := store.SaveConversation(ctx, "conv-nil", nil); err != nil {
		t.Fatalf("SaveConversation with nil messages: %v", err)
	}

	loaded, err := store.LoadMessages(ctx, "conv-nil")
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(loaded))
	}
}

func TestConversationStoreLargeConversation(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	// Build a large conversation with 500 messages
	const msgCount = 500
	msgs := make([]Message, msgCount)
	for i := 0; i < msgCount; i++ {
		if i%2 == 0 {
			msgs[i] = Message{Role: "user", Content: fmt.Sprintf("question %d with some padding content to increase size", i)}
		} else {
			msgs[i] = Message{Role: "assistant", Content: fmt.Sprintf("answer %d with some padding content to increase size", i)}
		}
	}

	if err := store.SaveConversation(ctx, "conv-large", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	loaded, err := store.LoadMessages(ctx, "conv-large")
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != msgCount {
		t.Fatalf("expected %d messages, got %d", msgCount, len(loaded))
	}

	// Verify first and last
	if loaded[0].Content != msgs[0].Content {
		t.Errorf("first message content mismatch")
	}
	if loaded[msgCount-1].Content != msgs[msgCount-1].Content {
		t.Errorf("last message content mismatch")
	}

	// Verify msg_count in listing
	convs, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if convs[0].MsgCount != msgCount {
		t.Fatalf("expected MsgCount=%d, got %d", msgCount, convs[0].MsgCount)
	}
}

func TestConversationStoreLargeContent(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	// Single message with very large content (100KB)
	largeContent := string(make([]byte, 100*1024))
	msgs := []Message{{Role: "user", Content: largeContent}}

	if err := store.SaveConversation(ctx, "conv-large-content", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	loaded, err := store.LoadMessages(ctx, "conv-large-content")
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 message, got %d", len(loaded))
	}
	if len(loaded[0].Content) != 100*1024 {
		t.Fatalf("expected content length %d, got %d", 100*1024, len(loaded[0].Content))
	}
}

func TestConversationStoreListConversationsPagination(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	// Save 5 conversations
	for i := 0; i < 5; i++ {
		msgs := []Message{{Role: "user", Content: fmt.Sprintf("msg-%d", i)}}
		if err := store.SaveConversation(ctx, fmt.Sprintf("pag-%d", i), msgs); err != nil {
			t.Fatalf("SaveConversation: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Offset beyond total should return empty
	convs, err := store.ListConversations(ctx, ConversationFilter{}, 10, 100)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 0 {
		t.Fatalf("expected 0 conversations for large offset, got %d", len(convs))
	}

	// Zero limit should default to 50 (per implementation)
	convs, err = store.ListConversations(ctx, ConversationFilter{}, 0, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 5 {
		t.Fatalf("expected 5 conversations with limit=0 (defaults to 50), got %d", len(convs))
	}
}

func TestConversationStoreSpecialCharactersInContent(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{
		{Role: "user", Content: "Hello 'world' \"quotes\" & <html> \x00\n\ttabs"},
		{Role: "assistant", Content: "Response with unicode: \u2603 \U0001F600"},
	}

	if err := store.SaveConversation(ctx, "conv-special", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	loaded, err := store.LoadMessages(ctx, "conv-special")
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded))
	}
	if loaded[0].Content != msgs[0].Content {
		t.Errorf("content mismatch: got %q, want %q", loaded[0].Content, msgs[0].Content)
	}
	if loaded[1].Content != msgs[1].Content {
		t.Errorf("content mismatch: got %q, want %q", loaded[1].Content, msgs[1].Content)
	}
}

func TestConversationStoreDoubleClose(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "double-close.db")
	store, err := NewSQLiteConversationStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteConversationStore: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// First close should succeed
	if err := store.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// Second close should not panic (may or may not error)
	_ = store.Close()
}

func TestConversationStoreSaveAndDeleteConcurrent(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	// Concurrently save and delete different conversations
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			convID := fmt.Sprintf("sd-conv-%d", idx)
			msgs := []Message{{Role: "user", Content: fmt.Sprintf("msg-%d", idx)}}
			if err := store.SaveConversation(ctx, convID, msgs); err != nil {
				errs <- fmt.Errorf("save %d: %w", idx, err)
				return
			}
			if err := store.DeleteConversation(ctx, convID); err != nil {
				errs <- fmt.Errorf("delete %d: %w", idx, err)
			}
		}(i)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// ---------------------------------------------------------------------------
// SearchMessages / FTS5 tests (Issue #37)
// ---------------------------------------------------------------------------

func TestSearchMessages_EmptyQuery(t *testing.T) {
	t.Parallel()

	db := newTestConversationStore(t)
	results, err := db.SearchMessages(context.Background(), "", "", 10)
	if err != nil {
		t.Fatalf("SearchMessages with empty query: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for empty query, got %d", len(results))
	}
}

func TestSearchMessages_NoMatch(t *testing.T) {
	t.Parallel()

	db := newTestConversationStore(t)
	msgs := []Message{
		{Role: "user", Content: "the quick brown fox"},
	}
	if err := db.SaveConversation(context.Background(), "conv-search-1", msgs); err != nil {
		t.Fatalf("save: %v", err)
	}

	results, err := db.SearchMessages(context.Background(), "", "elephant", 10)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestSearchMessages_Match(t *testing.T) {
	t.Parallel()

	db := newTestConversationStore(t)
	msgs := []Message{
		{Role: "user", Content: "the quick brown fox jumps"},
		{Role: "assistant", Content: "over the lazy dog"},
	}
	if err := db.SaveConversation(context.Background(), "conv-search-2", msgs); err != nil {
		t.Fatalf("save: %v", err)
	}

	results, err := db.SearchMessages(context.Background(), "", "fox", 10)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].ConversationID != "conv-search-2" {
		t.Fatalf("unexpected conversation_id: %s", results[0].ConversationID)
	}
	if results[0].Role != "user" {
		t.Fatalf("unexpected role: %s", results[0].Role)
	}
	if results[0].Snippet == "" {
		t.Fatal("expected non-empty snippet")
	}
}

func TestSearchMessages_LimitEnforced(t *testing.T) {
	t.Parallel()

	db := newTestConversationStore(t)
	// Save 5 conversations each with a matching message.
	for i := 0; i < 5; i++ {
		msgs := []Message{
			{Role: "user", Content: "needle in a haystack"},
		}
		convID := fmt.Sprintf("conv-limit-%d", i)
		if err := db.SaveConversation(context.Background(), convID, msgs); err != nil {
			t.Fatalf("save conv %d: %v", i, err)
		}
	}

	results, err := db.SearchMessages(context.Background(), "", "needle", 3)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results) > 3 {
		t.Fatalf("expected at most 3 results, got %d", len(results))
	}
}

func TestSearchMessages_DefaultLimit(t *testing.T) {
	t.Parallel()

	db := newTestConversationStore(t)
	msgs := []Message{
		{Role: "user", Content: "searchable content"},
	}
	if err := db.SaveConversation(context.Background(), "conv-default-limit", msgs); err != nil {
		t.Fatalf("save: %v", err)
	}

	// limit=0 should use the default (20).
	results, err := db.SearchMessages(context.Background(), "", "searchable", 0)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result with default limit")
	}
}

func TestSearchMessages_CrossConversation(t *testing.T) {
	t.Parallel()

	db := newTestConversationStore(t)
	for i, content := range []string{
		"apple banana cherry",
		"cherry dragon fruit",
		"elderberry fig grape",
	} {
		msgs := []Message{{Role: "user", Content: content}}
		if err := db.SaveConversation(context.Background(), fmt.Sprintf("conv-cross-%d", i), msgs); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}

	results, err := db.SearchMessages(context.Background(), "", "cherry", 10)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results (cherry appears in 2 convs), got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// Token/cost tracking tests (Issue #32)
// ---------------------------------------------------------------------------

func TestConversationStoreSaveConversationWithCost_Basic(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
	}
	cost := ConversationTokenCost{
		PromptTokens:     100,
		CompletionTokens: 50,
		CostUSD:          0.00123,
	}

	if err := store.SaveConversationWithCost(ctx, "conv-cost-1", msgs, cost); err != nil {
		t.Fatalf("SaveConversationWithCost: %v", err)
	}

	convs, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}

	c := convs[0]
	if c.PromptTokens != 100 {
		t.Errorf("expected PromptTokens=100, got %d", c.PromptTokens)
	}
	if c.CompletionTokens != 50 {
		t.Errorf("expected CompletionTokens=50, got %d", c.CompletionTokens)
	}
	if c.CostUSD < 0.001 || c.CostUSD > 0.002 {
		t.Errorf("expected CostUSD~0.00123, got %f", c.CostUSD)
	}
}

func TestConversationStoreSaveConversationWithCost_ZeroCost(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{{Role: "user", Content: "Hello"}}
	cost := ConversationTokenCost{} // zero values

	if err := store.SaveConversationWithCost(ctx, "conv-zero-cost", msgs, cost); err != nil {
		t.Fatalf("SaveConversationWithCost: %v", err)
	}

	convs, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}

	c := convs[0]
	if c.PromptTokens != 0 {
		t.Errorf("expected PromptTokens=0, got %d", c.PromptTokens)
	}
	if c.CompletionTokens != 0 {
		t.Errorf("expected CompletionTokens=0, got %d", c.CompletionTokens)
	}
	if c.CostUSD != 0 {
		t.Errorf("expected CostUSD=0, got %f", c.CostUSD)
	}
}

func TestConversationStoreSaveConversationWithCost_Accumulates(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	// First save
	msgs1 := []Message{{Role: "user", Content: "Hello"}, {Role: "assistant", Content: "Hi"}}
	cost1 := ConversationTokenCost{PromptTokens: 100, CompletionTokens: 50, CostUSD: 0.001}
	if err := store.SaveConversationWithCost(ctx, "conv-accum", msgs1, cost1); err != nil {
		t.Fatalf("SaveConversationWithCost (1): %v", err)
	}

	// Second save (updates/overwrites with new totals)
	msgs2 := append(msgs1,
		Message{Role: "user", Content: "What's up?"},
		Message{Role: "assistant", Content: "All good!"},
	)
	cost2 := ConversationTokenCost{PromptTokens: 250, CompletionTokens: 120, CostUSD: 0.003}
	if err := store.SaveConversationWithCost(ctx, "conv-accum", msgs2, cost2); err != nil {
		t.Fatalf("SaveConversationWithCost (2): %v", err)
	}

	convs, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}

	c := convs[0]
	// Second save should overwrite with the new (cumulative run total) cost
	if c.PromptTokens != 250 {
		t.Errorf("expected PromptTokens=250, got %d", c.PromptTokens)
	}
	if c.CompletionTokens != 120 {
		t.Errorf("expected CompletionTokens=120, got %d", c.CompletionTokens)
	}
	if c.CostUSD < 0.002 || c.CostUSD > 0.004 {
		t.Errorf("expected CostUSD~0.003, got %f", c.CostUSD)
	}
}

func TestConversationStoreSaveConversation_BackwardsCompat(t *testing.T) {
	t.Parallel()
	// Ensure that SaveConversation (without cost) still works and leaves
	// token/cost fields at their zero values.
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{{Role: "user", Content: "Hello"}, {Role: "assistant", Content: "Hi"}}
	if err := store.SaveConversation(ctx, "conv-compat", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	convs, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}

	c := convs[0]
	if c.PromptTokens != 0 {
		t.Errorf("expected PromptTokens=0, got %d", c.PromptTokens)
	}
	if c.CompletionTokens != 0 {
		t.Errorf("expected CompletionTokens=0, got %d", c.CompletionTokens)
	}
	if c.CostUSD != 0 {
		t.Errorf("expected CostUSD=0, got %f", c.CostUSD)
	}
}

func TestConversationStoreSaveConversationWithCost_LargeValues(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{{Role: "user", Content: "Hello"}}
	cost := ConversationTokenCost{
		PromptTokens:     1_000_000,
		CompletionTokens: 500_000,
		CostUSD:          9999.99,
	}

	if err := store.SaveConversationWithCost(ctx, "conv-large-cost", msgs, cost); err != nil {
		t.Fatalf("SaveConversationWithCost: %v", err)
	}

	convs, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}

	c := convs[0]
	if c.PromptTokens != 1_000_000 {
		t.Errorf("expected PromptTokens=1000000, got %d", c.PromptTokens)
	}
	if c.CompletionTokens != 500_000 {
		t.Errorf("expected CompletionTokens=500000, got %d", c.CompletionTokens)
	}
	if c.CostUSD < 9999 || c.CostUSD > 10001 {
		t.Errorf("expected CostUSD~9999.99, got %f", c.CostUSD)
	}
}

func TestConversationStoreSaveConversationWithCost_ConcurrentSafety(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	const goroutines = 10
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			convID := fmt.Sprintf("concurrent-cost-%d", idx)
			msgs := []Message{
				{Role: "user", Content: fmt.Sprintf("question-%d", idx)},
				{Role: "assistant", Content: fmt.Sprintf("answer-%d", idx)},
			}
			cost := ConversationTokenCost{
				PromptTokens:     idx * 100,
				CompletionTokens: idx * 50,
				CostUSD:          float64(idx) * 0.001,
			}
			if err := store.SaveConversationWithCost(ctx, convID, msgs, cost); err != nil {
				errs <- fmt.Errorf("goroutine %d: %w", idx, err)
			}
		}(i)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	convs, err := store.ListConversations(ctx, ConversationFilter{}, 20, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != goroutines {
		t.Fatalf("expected %d conversations, got %d", goroutines, len(convs))
	}
}

func TestConversationStoreSaveConversationWithCost_MigrationIdempotent(t *testing.T) {
	t.Parallel()
	// Running Migrate twice on the same store should not fail (idempotent).
	path := filepath.Join(t.TempDir(), "idempotent.db")
	store, err := NewSQLiteConversationStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteConversationStore: %v", err)
	}
	defer store.Close()

	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("second Migrate (idempotent check): %v", err)
	}

	// Should work after double migration
	msgs := []Message{{Role: "user", Content: "ok"}}
	cost := ConversationTokenCost{PromptTokens: 10, CompletionTokens: 5, CostUSD: 0.0001}
	if err := store.SaveConversationWithCost(context.Background(), "conv-idem", msgs, cost); err != nil {
		t.Fatalf("SaveConversationWithCost after double migrate: %v", err)
	}
}

// Issue #35: workspace/tenant scoping tests
// ---------------------------------------------------------------------------

func TestConversationStoreUpdateConversationMeta(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{{Role: "user", Content: "Hello"}}
	if err := store.SaveConversation(ctx, "conv-meta-update", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	// Set workspace and tenant_id
	if err := store.UpdateConversationMeta(ctx, "conv-meta-update", "ws1", "tenant-abc"); err != nil {
		t.Fatalf("UpdateConversationMeta: %v", err)
	}

	// Verify fields appear in listing
	convs, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}
	if convs[0].Workspace != "ws1" {
		t.Errorf("Workspace: got %q, want %q", convs[0].Workspace, "ws1")
	}
	if convs[0].TenantID != "tenant-abc" {
		t.Errorf("TenantID: got %q, want %q", convs[0].TenantID, "tenant-abc")
	}
}

func TestConversationStoreUpdateConversationMetaIdempotent(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{{Role: "user", Content: "Hello"}}
	if err := store.SaveConversation(ctx, "conv-meta-idem", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	// Call UpdateConversationMeta multiple times -- should not error
	for i := 0; i < 3; i++ {
		if err := store.UpdateConversationMeta(ctx, "conv-meta-idem", "ws-x", "tenant-y"); err != nil {
			t.Fatalf("UpdateConversationMeta (call %d): %v", i, err)
		}
	}

	convs, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}
	if convs[0].Workspace != "ws-x" || convs[0].TenantID != "tenant-y" {
		t.Errorf("unexpected meta: workspace=%q tenant_id=%q", convs[0].Workspace, convs[0].TenantID)
	}
}

func TestConversationStoreUpdateConversationMetaNonExistent(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	// UpdateConversationMeta on non-existent conversation should not error
	if err := store.UpdateConversationMeta(ctx, "does-not-exist", "ws", "tenant"); err != nil {
		t.Fatalf("UpdateConversationMeta on non-existent should not error: %v", err)
	}
}

func TestConversationStoreListFilterByTenantID(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{{Role: "user", Content: "hi"}}

	// Save 3 conversations with different tenant IDs
	for _, id := range []string{"conv-t1", "conv-t2", "conv-t3"} {
		if err := store.SaveConversation(ctx, id, msgs); err != nil {
			t.Fatalf("SaveConversation %s: %v", id, err)
		}
	}

	if err := store.UpdateConversationMeta(ctx, "conv-t1", "", "tenant-alpha"); err != nil {
		t.Fatalf("UpdateConversationMeta conv-t1: %v", err)
	}
	if err := store.UpdateConversationMeta(ctx, "conv-t2", "", "tenant-alpha"); err != nil {
		t.Fatalf("UpdateConversationMeta conv-t2: %v", err)
	}
	if err := store.UpdateConversationMeta(ctx, "conv-t3", "", "tenant-beta"); err != nil {
		t.Fatalf("UpdateConversationMeta conv-t3: %v", err)
	}

	// Filter by tenant-alpha
	convs, err := store.ListConversations(ctx, ConversationFilter{TenantID: "tenant-alpha"}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 2 {
		t.Fatalf("expected 2 conversations for tenant-alpha, got %d", len(convs))
	}
	for _, c := range convs {
		if c.TenantID != "tenant-alpha" {
			t.Errorf("unexpected tenant_id %q in filtered results", c.TenantID)
		}
	}

	// Filter by tenant-beta
	convs, err = store.ListConversations(ctx, ConversationFilter{TenantID: "tenant-beta"}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation for tenant-beta, got %d", len(convs))
	}

	// Filter by non-existent tenant returns empty
	convs, err = store.ListConversations(ctx, ConversationFilter{TenantID: "tenant-none"}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 0 {
		t.Fatalf("expected 0 conversations for unknown tenant, got %d", len(convs))
	}
}

func TestConversationStoreListFilterByWorkspace(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{{Role: "user", Content: "hi"}}

	for _, id := range []string{"conv-w1", "conv-w2", "conv-w3"} {
		if err := store.SaveConversation(ctx, id, msgs); err != nil {
			t.Fatalf("SaveConversation %s: %v", id, err)
		}
	}

	if err := store.UpdateConversationMeta(ctx, "conv-w1", "workspace-A", ""); err != nil {
		t.Fatalf("UpdateConversationMeta: %v", err)
	}
	if err := store.UpdateConversationMeta(ctx, "conv-w2", "workspace-A", ""); err != nil {
		t.Fatalf("UpdateConversationMeta: %v", err)
	}
	if err := store.UpdateConversationMeta(ctx, "conv-w3", "workspace-B", ""); err != nil {
		t.Fatalf("UpdateConversationMeta: %v", err)
	}

	convs, err := store.ListConversations(ctx, ConversationFilter{Workspace: "workspace-A"}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 2 {
		t.Fatalf("expected 2 conversations for workspace-A, got %d", len(convs))
	}
	for _, c := range convs {
		if c.Workspace != "workspace-A" {
			t.Errorf("unexpected workspace %q in filtered results", c.Workspace)
		}
	}
}

func TestConversationStoreListFilterBothWorkspaceAndTenant(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{{Role: "user", Content: "hi"}}

	for _, id := range []string{"conv-wt1", "conv-wt2", "conv-wt3", "conv-wt4"} {
		if err := store.SaveConversation(ctx, id, msgs); err != nil {
			t.Fatalf("SaveConversation %s: %v", id, err)
		}
	}

	if err := store.UpdateConversationMeta(ctx, "conv-wt1", "ws-X", "t-1"); err != nil {
		t.Fatalf("UpdateConversationMeta conv-wt1: %v", err)
	}
	if err := store.UpdateConversationMeta(ctx, "conv-wt2", "ws-X", "t-2"); err != nil {
		t.Fatalf("UpdateConversationMeta conv-wt2: %v", err)
	}
	if err := store.UpdateConversationMeta(ctx, "conv-wt3", "ws-Y", "t-1"); err != nil {
		t.Fatalf("UpdateConversationMeta conv-wt3: %v", err)
	}
	if err := store.UpdateConversationMeta(ctx, "conv-wt4", "ws-Y", "t-2"); err != nil {
		t.Fatalf("UpdateConversationMeta conv-wt4: %v", err)
	}

	// Only ws-X AND t-1 should match conv-wt1
	convs, err := store.ListConversations(ctx, ConversationFilter{Workspace: "ws-X", TenantID: "t-1"}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation matching both filters, got %d", len(convs))
	}
	if convs[0].ID != "conv-wt1" {
		t.Errorf("expected conv-wt1, got %q", convs[0].ID)
	}
}

func TestConversationStoreTenantScopingMigrationIdempotent(t *testing.T) {
	t.Parallel()

	// Create and migrate a fresh DB
	dbPath := filepath.Join(t.TempDir(), "tenant-migration.db")
	store, err := NewSQLiteConversationStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteConversationStore: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}

	// Save a conversation
	ctx := context.Background()
	msgs := []Message{{Role: "user", Content: "Hello"}}
	if err := store.SaveConversation(ctx, "conv-migrate", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}
	store.Close()

	// Reopen and re-migrate (idempotent)
	store2, err := NewSQLiteConversationStore(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer store2.Close()

	// Migrate again — should not fail even though columns already exist
	if err := store2.Migrate(context.Background()); err != nil {
		t.Fatalf("second Migrate (idempotent): %v", err)
	}

	// Data should still be intact
	loaded, err := store2.LoadMessages(ctx, "conv-migrate")
	if err != nil {
		t.Fatalf("LoadMessages after re-migrate: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 message after re-migrate, got %d", len(loaded))
	}

	// workspace and tenant_id should be empty string (default)
	convs, err := store2.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations after re-migrate: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}
	if convs[0].Workspace != "" {
		t.Errorf("expected empty workspace, got %q", convs[0].Workspace)
	}
	if convs[0].TenantID != "" {
		t.Errorf("expected empty tenant_id, got %q", convs[0].TenantID)
	}
}

// ============================================================
// Issue #34: Retention policy tests
// ============================================================

func TestConversationStoreDeleteOldConversations_DeletesOld(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{{Role: "user", Content: "hello"}}

	// Save two conversations
	if err := store.SaveConversation(ctx, "old-conv", msgs); err != nil {
		t.Fatalf("SaveConversation old-conv: %v", err)
	}
	if err := store.SaveConversation(ctx, "new-conv", msgs); err != nil {
		t.Fatalf("SaveConversation new-conv: %v", err)
	}

	// Manually backdate old-conv to 40 days ago
	cutoff := time.Now().UTC().Add(-40 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := store.db.ExecContext(ctx, `UPDATE conversations SET updated_at = ? WHERE id = ?`, cutoff, "old-conv"); err != nil {
		t.Fatalf("backdate old-conv: %v", err)
	}

	// Delete conversations older than 30 days
	threshold := time.Now().UTC().Add(-30 * 24 * time.Hour)
	deleted, err := store.DeleteOldConversations(ctx, threshold)
	if err != nil {
		t.Fatalf("DeleteOldConversations: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	convs, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 remaining conversation, got %d", len(convs))
	}
	if convs[0].ID != "new-conv" {
		t.Errorf("expected new-conv to remain, got %q", convs[0].ID)
	}
}

func TestConversationStoreDeleteOldConversations_SparesPinned(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{{Role: "user", Content: "pinned message"}}

	if err := store.SaveConversation(ctx, "pinned-conv", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	// Backdate pinned-conv to 60 days ago
	cutoff := time.Now().UTC().Add(-60 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := store.db.ExecContext(ctx, `UPDATE conversations SET updated_at = ? WHERE id = ?`, cutoff, "pinned-conv"); err != nil {
		t.Fatalf("backdate pinned-conv: %v", err)
	}

	// Pin it
	if err := store.PinConversation(ctx, "pinned-conv", true); err != nil {
		t.Fatalf("PinConversation: %v", err)
	}

	// Delete conversations older than 30 days
	threshold := time.Now().UTC().Add(-30 * 24 * time.Hour)
	deleted, err := store.DeleteOldConversations(ctx, threshold)
	if err != nil {
		t.Fatalf("DeleteOldConversations: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted (pinned should be spared), got %d", deleted)
	}

	convs, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected pinned-conv to survive, got %d conversations", len(convs))
	}
}

func TestConversationStoreDeleteOldConversations_ZeroThreshold(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{{Role: "user", Content: "msg"}}
	if err := store.SaveConversation(ctx, "conv-a", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	// Zero time threshold: nothing should be deleted (defensive check)
	deleted, err := store.DeleteOldConversations(ctx, time.Time{})
	if err != nil {
		t.Fatalf("DeleteOldConversations with zero time: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted with zero threshold, got %d", deleted)
	}
}

func TestConversationStorePinConversation_TogglePin(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{{Role: "user", Content: "hi"}}
	if err := store.SaveConversation(ctx, "pin-test", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	// Pin it
	if err := store.PinConversation(ctx, "pin-test", true); err != nil {
		t.Fatalf("PinConversation(true): %v", err)
	}

	convs, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 1 || !convs[0].Pinned {
		t.Errorf("expected pinned=true, got %+v", convs[0])
	}

	// Unpin it
	if err := store.PinConversation(ctx, "pin-test", false); err != nil {
		t.Fatalf("PinConversation(false): %v", err)
	}

	convs2, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations after unpin: %v", err)
	}
	if len(convs2) != 1 || convs2[0].Pinned {
		t.Errorf("expected pinned=false after unpin, got %+v", convs2[0])
	}
}

func TestConversationStorePinConversation_NotFound(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	// Pinning a non-existent conversation should return an error
	err := store.PinConversation(ctx, "does-not-exist", true)
	if err == nil {
		t.Error("expected error when pinning non-existent conversation, got nil")
	}
}

func TestConversationStoreDeleteOldConversations_NoneOldEnough(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{{Role: "user", Content: "recent"}}
	if err := store.SaveConversation(ctx, "recent-conv", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	// Threshold is 30 days ago — conversation is brand-new
	threshold := time.Now().UTC().Add(-30 * 24 * time.Hour)
	deleted, err := store.DeleteOldConversations(ctx, threshold)
	if err != nil {
		t.Fatalf("DeleteOldConversations: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted, got %d", deleted)
	}
}

func TestConversationStoreDeleteOldConversations_Concurrent(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	// Create 20 conversations
	msgs := []Message{{Role: "user", Content: "msg"}}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("concurrent-retention-%02d", i)
		if err := store.SaveConversation(ctx, id, msgs); err != nil {
			t.Fatalf("SaveConversation %s: %v", id, err)
		}
	}

	// Backdate the first 10 to 40 days ago
	old := time.Now().UTC().Add(-40 * 24 * time.Hour).Format(time.RFC3339Nano)
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("concurrent-retention-%02d", i)
		if _, err := store.db.ExecContext(ctx, `UPDATE conversations SET updated_at = ? WHERE id = ?`, old, id); err != nil {
			t.Fatalf("backdate %s: %v", id, err)
		}
	}

	threshold := time.Now().UTC().Add(-30 * 24 * time.Hour)

	var wg sync.WaitGroup
	deletedTotal := make(chan int, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := store.DeleteOldConversations(ctx, threshold)
			if err != nil {
				t.Errorf("concurrent DeleteOldConversations: %v", err)
				return
			}
			deletedTotal <- n
		}()
	}
	wg.Wait()
	close(deletedTotal)

	total := 0
	for n := range deletedTotal {
		total += n
	}
	// Exactly 10 should have been deleted in total across all goroutines
	if total != 10 {
		t.Errorf("expected 10 total deleted, got %d", total)
	}

	remaining, err := store.ListConversations(ctx, ConversationFilter{}, 30, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(remaining) != 10 {
		t.Errorf("expected 10 remaining, got %d", len(remaining))
	}
}

// TestConversationCleanerRun verifies ConversationCleaner.RunOnce deletes old conversations.
func TestConversationCleanerRunOnce(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{{Role: "user", Content: "cleanup test"}}
	for _, id := range []string{"old-a", "old-b", "new-c"} {
		if err := store.SaveConversation(ctx, id, msgs); err != nil {
			t.Fatalf("SaveConversation %s: %v", id, err)
		}
	}

	// Backdate old-a and old-b to 45 days ago
	oldDate := time.Now().UTC().Add(-45 * 24 * time.Hour).Format(time.RFC3339Nano)
	for _, id := range []string{"old-a", "old-b"} {
		if _, err := store.db.ExecContext(ctx, `UPDATE conversations SET updated_at = ? WHERE id = ?`, oldDate, id); err != nil {
			t.Fatalf("backdate %s: %v", id, err)
		}
	}

	cleaner := NewConversationCleaner(store, 30)
	deleted, err := cleaner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if deleted != 2 {
		t.Errorf("expected 2 deleted, got %d", deleted)
	}

	remaining, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != "new-c" {
		t.Errorf("expected only new-c to remain, got %+v", remaining)
	}
}

// TestConversationCleanerRunOnce_ZeroRetentionDisabled verifies that 0 days means disabled.
func TestConversationCleanerRunOnce_ZeroRetentionDisabled(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{{Role: "user", Content: "should not be deleted"}}
	if err := store.SaveConversation(ctx, "to-keep", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}
	// Backdate to ancient past
	old := time.Now().UTC().Add(-1000 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := store.db.ExecContext(ctx, `UPDATE conversations SET updated_at = ? WHERE id = ?`, old, "to-keep"); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	// retentionDays=0 means disabled — nothing should be deleted
	cleaner := NewConversationCleaner(store, 0)
	deleted, err := cleaner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce with 0 days: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted with 0 retention days, got %d", deleted)
	}
}

// ---- Auto-title tests (issue #38) ----

func TestExtractTitleFirstSentence(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		{Role: "user", Content: "What is the meaning of life? This is more text."},
	}
	got := extractTitle(msgs)
	want := "What is the meaning of life?"
	if got != want {
		t.Errorf("extractTitle = %q, want %q", got, want)
	}
}

func TestExtractTitleTruncate(t *testing.T) {
	t.Parallel()
	long := "This is a very long message without any sentence boundary that exceeds the eighty character limit for sure"
	msgs := []Message{{Role: "user", Content: long}}
	got := extractTitle(msgs)
	if len([]rune(got)) > 81 { // 80 + ellipsis
		t.Errorf("extractTitle too long: %d runes in %q", len([]rune(got)), got)
	}
	if !func() bool {
		for _, r := range got {
			if r == '…' {
				return true
			}
		}
		return false
	}() {
		t.Errorf("expected ellipsis in truncated title %q", got)
	}
}

func TestExtractTitleNoUserMessage(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		{Role: "assistant", Content: "Hello"},
	}
	if got := extractTitle(msgs); got != "" {
		t.Errorf("expected empty title, got %q", got)
	}
}

func TestExtractTitleSkipsMeta(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		{Role: "user", Content: "Meta message", IsMeta: true},
		{Role: "user", Content: "Real message."},
	}
	got := extractTitle(msgs)
	if got != "Real message." {
		t.Errorf("extractTitle = %q, want %q", got, "Real message.")
	}
}

func TestExtractTitleFirstLineOnly(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		{Role: "user", Content: "First line\nSecond line"},
	}
	got := extractTitle(msgs)
	if got != "First line" {
		t.Errorf("extractTitle = %q, want %q", got, "First line")
	}
}

func TestConversationStoreAutoTitle(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{
		{Role: "user", Content: "How do I deploy to Railway? Extra text here."},
		{Role: "assistant", Content: "You can use railway up."},
	}
	if err := store.SaveConversation(ctx, "conv-autotitle", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	convs, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}
	if convs[0].Title != "How do I deploy to Railway?" {
		t.Errorf("Title = %q, want %q", convs[0].Title, "How do I deploy to Railway?")
	}
}

// TestConversationCleanup is an end-to-end test that creates old and new
// conversations in the SQLite store, runs cleanup via DeleteOldConversations,
// and verifies only the old non-pinned conversations are deleted.
func TestConversationCleanup(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx := context.Background()

	msgs := []Message{{Role: "user", Content: "test message"}}

	// Save three conversations: two old and one recent.
	for _, id := range []string{"old-1", "old-2", "recent-1"} {
		if err := store.SaveConversation(ctx, id, msgs); err != nil {
			t.Fatalf("SaveConversation(%s): %v", id, err)
		}
	}

	// Backdate old-1 and old-2 to 40 days ago.
	oldDate := time.Now().UTC().Add(-40 * 24 * time.Hour).Format(time.RFC3339Nano)
	for _, id := range []string{"old-1", "old-2"} {
		if _, err := store.db.ExecContext(ctx, `UPDATE conversations SET updated_at = ? WHERE id = ?`, oldDate, id); err != nil {
			t.Fatalf("backdate %s: %v", id, err)
		}
	}

	// Cleanup with 30-day TTL.
	threshold := time.Now().UTC().Add(-30 * 24 * time.Hour)
	deleted, err := store.DeleteOldConversations(ctx, threshold)
	if err != nil {
		t.Fatalf("DeleteOldConversations: %v", err)
	}
	if deleted != 2 {
		t.Errorf("expected 2 deleted, got %d", deleted)
	}

	// Only recent-1 should remain.
	remaining, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(remaining))
	}
	if remaining[0].ID != "recent-1" {
		t.Errorf("expected remaining conversation to be recent-1, got %s", remaining[0].ID)
	}
}

// TestConversationCleanerStart verifies Start launches the background goroutine
// and deletes old conversations. Uses a very short interval to avoid test delays.
func TestConversationCleanerStart(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msgs := []Message{{Role: "user", Content: "bg test"}}
	for _, id := range []string{"bg-old", "bg-new"} {
		if err := store.SaveConversation(ctx, id, msgs); err != nil {
			t.Fatalf("SaveConversation(%s): %v", id, err)
		}
	}

	// Backdate bg-old to 40 days ago.
	oldDate := time.Now().UTC().Add(-40 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := store.db.ExecContext(ctx, `UPDATE conversations SET updated_at = ? WHERE id = ?`, oldDate, "bg-old"); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	cleaner := NewConversationCleaner(store, 30)
	// Use a very long interval; we rely on the startup sweep.
	cleaner.Start(ctx, 24*time.Hour)

	// Poll until bg-old is gone or we time out.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		convs, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
		if err != nil {
			t.Fatalf("ListConversations: %v", err)
		}
		if len(convs) == 1 && convs[0].ID == "bg-new" {
			return // success
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("timed out waiting for background cleaner to delete old conversation")
}

// TestConversationCleanerStart_ZeroRetentionNoOp verifies Start is a no-op when retentionDays=0.
func TestConversationCleanerStart_ZeroRetentionNoOp(t *testing.T) {
	t.Parallel()
	store := newTestConversationStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Zero retentionDays — Start should be a no-op.
	cleaner := NewConversationCleaner(store, 0)
	cleaner.Start(ctx, 1*time.Millisecond) // would delete fast if active
	// Give it a moment to potentially (incorrectly) trigger
	time.Sleep(20 * time.Millisecond)

	// No conversations to delete anyway, just verify no panic/error.
	convs, err := store.ListConversations(ctx, ConversationFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 0 {
		t.Errorf("expected 0 conversations, got %d", len(convs))
	}
}
