package tui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestForkConversationCmd_Success verifies forkConversationCmd POSTs to
// /v1/conversations/{id}/fork and decodes the new conversation ID, source ID,
// and message count.
func TestForkConversationCmd_Success(t *testing.T) {
	t.Parallel()

	var gotMethod, gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"conversation_id":"conv-new-9","forked_from":"conv-1","message_count":7}`))
	}))
	defer ts.Close()

	msg := forkConversationCmd(ts.URL, "conv-1", "")()

	if gotMethod != http.MethodPost {
		t.Errorf("method: want POST, got %q", gotMethod)
	}
	if gotPath != "/v1/conversations/conv-1/fork" {
		t.Errorf("path: want /v1/conversations/conv-1/fork, got %q", gotPath)
	}

	got, ok := msg.(ForkResultMsg)
	if !ok {
		t.Fatalf("expected ForkResultMsg, got %T", msg)
	}
	if got.Err != "" {
		t.Fatalf("unexpected error: %q", got.Err)
	}
	if got.NewID != "conv-new-9" {
		t.Errorf("NewID: want %q, got %q", "conv-new-9", got.NewID)
	}
	if got.SrcID != "conv-1" {
		t.Errorf("SrcID: want %q, got %q", "conv-1", got.SrcID)
	}
	if got.MessageCount != 7 {
		t.Errorf("MessageCount: want 7, got %d", got.MessageCount)
	}
}

// TestForkConversationCmd_EscapesConversationID verifies the conversation ID
// is percent-escaped in the request path (mirrors fetchConversationMessagesCmd).
func TestForkConversationCmd_EscapesConversationID(t *testing.T) {
	t.Parallel()

	var gotEscapedPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscapedPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"conversation_id":"n","forked_from":"s","message_count":0}`))
	}))
	defer ts.Close()

	msg := forkConversationCmd(ts.URL, "conv/x", "")()
	if got, ok := msg.(ForkResultMsg); !ok || got.Err != "" {
		t.Fatalf("expected successful ForkResultMsg, got %+v", msg)
	}
	if !strings.Contains(gotEscapedPath, "conv%2Fx") {
		t.Errorf("escaped path must contain conv%%2Fx, got %q", gotEscapedPath)
	}
}

// TestForkConversationCmd_ErrorStatus verifies a non-200 response (404 for an
// unknown conversation, 501 without persistence) yields ForkResultMsg.Err.
func TestForkConversationCmd_ErrorStatus(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"conversation not found"}`, http.StatusNotFound)
	}))
	defer ts.Close()

	msg := forkConversationCmd(ts.URL, "conv-missing", "")()
	got, ok := msg.(ForkResultMsg)
	if !ok {
		t.Fatalf("expected ForkResultMsg, got %T", msg)
	}
	if got.Err == "" {
		t.Error("expected a non-empty error message")
	}
	if !strings.Contains(got.Err, "404") {
		t.Errorf("error should mention the status code, got %q", got.Err)
	}
	if got.SrcID != "conv-missing" {
		t.Errorf("SrcID must be preserved on error, got %q", got.SrcID)
	}
}

// TestForkConversationCmd_NetworkError verifies an unreachable server yields
// ForkResultMsg.Err rather than a panic.
func TestForkConversationCmd_NetworkError(t *testing.T) {
	t.Parallel()

	msg := forkConversationCmd("http://127.0.0.1:1", "conv-err", "")()
	got, ok := msg.(ForkResultMsg)
	if !ok {
		t.Fatalf("expected ForkResultMsg, got %T", msg)
	}
	if got.Err == "" {
		t.Error("expected a non-empty error message")
	}
}
