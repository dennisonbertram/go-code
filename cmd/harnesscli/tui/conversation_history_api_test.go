package tui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestFetchConversationMessagesCmd_Success verifies that fetchConversationMessagesCmd
// hits the correct path (GET /v1/conversations/{id}/messages), sends no
// Authorization header when no API key is configured (preserving
// unauthenticated-local behavior — see TestAllHarnessdCallsAuthenticate in
// api_auth_test.go for the header-present case shared across every
// harnessd-targeting call), and decodes the message list into
// ConversationHistoryMsg.
func TestFetchConversationMessagesCmd_Success(t *testing.T) {
	t.Parallel()

	var gotMethod, gotPath, gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"}]}`))
	}))
	defer ts.Close()

	msg := fetchConversationMessagesCmd(ts.URL, "conv-history-1", "")()

	if gotMethod != http.MethodGet {
		t.Errorf("method: want GET, got %q", gotMethod)
	}
	if gotPath != "/v1/conversations/conv-history-1/messages" {
		t.Errorf("path: want /v1/conversations/conv-history-1/messages, got %q", gotPath)
	}
	if gotAuth != "" {
		t.Errorf("Authorization header: want empty when no API key is configured, got %q", gotAuth)
	}

	got, ok := msg.(ConversationHistoryMsg)
	if !ok {
		t.Fatalf("expected ConversationHistoryMsg, got %T", msg)
	}
	if got.ConversationID != "conv-history-1" {
		t.Errorf("ConversationID: want %q, got %q", "conv-history-1", got.ConversationID)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("Messages length: want 2, got %d", len(got.Messages))
	}
	if got.Messages[0].Role != "user" || got.Messages[0].Content != "hi" {
		t.Errorf("Messages[0]: want {user hi}, got %+v", got.Messages[0])
	}
	if got.Messages[1].Role != "assistant" || got.Messages[1].Content != "hello" {
		t.Errorf("Messages[1]: want {assistant hello}, got %+v", got.Messages[1])
	}
}

// TestFetchConversationMessagesCmd_ErrorStatus verifies that a non-200 response
// causes fetchConversationMessagesCmd to emit ConversationHistoryErrorMsg rather
// than a decode panic.
func TestFetchConversationMessagesCmd_ErrorStatus(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "conversation not found", http.StatusNotFound)
	}))
	defer ts.Close()

	msg := fetchConversationMessagesCmd(ts.URL, "conv-missing", "")()
	got, ok := msg.(ConversationHistoryErrorMsg)
	if !ok {
		t.Fatalf("expected ConversationHistoryErrorMsg, got %T", msg)
	}
	if got.ConversationID != "conv-missing" {
		t.Errorf("ConversationID: want %q, got %q", "conv-missing", got.ConversationID)
	}
	if got.Err == "" {
		t.Error("expected a non-empty error message")
	}
}

// TestFetchConversationMessagesCmd_NetworkError verifies that an unreachable
// server causes fetchConversationMessagesCmd to emit ConversationHistoryErrorMsg.
func TestFetchConversationMessagesCmd_NetworkError(t *testing.T) {
	t.Parallel()

	msg := fetchConversationMessagesCmd("http://127.0.0.1:1", "conv-err", "")()
	got, ok := msg.(ConversationHistoryErrorMsg)
	if !ok {
		t.Fatalf("expected ConversationHistoryErrorMsg, got %T", msg)
	}
	if got.Err == "" {
		t.Error("expected a non-empty error message")
	}
}
