package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/store"
)

// TestCleanupEndpoint_RequiresAdminScope locks in that POST
// /v1/conversations/cleanup deletes across every tenant's conversations
// (DeleteOldConversations has no tenant parameter), so it must be gated to
// admin scope rather than runs:write: a non-admin caller with only
// runs:write is rejected, and an admin caller succeeds.
func TestCleanupEndpoint_RequiresAdminScope(t *testing.T) {
	t.Parallel()

	convStore := &mockConversationStore{deleteOldCount: 3}
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{
		ConversationStore: convStore,
	})

	ms := store.NewMemoryStore()
	writeToken, writeKey, err := store.GenerateAPIKey("tenant-a", "runs-write key", []string{store.ScopeRunsWrite})
	if err != nil {
		t.Fatalf("GenerateAPIKey(runs:write): %v", err)
	}
	writeKey = minCostRehash(t, writeToken, writeKey)
	if err := ms.CreateAPIKey(context.Background(), writeKey); err != nil {
		t.Fatalf("CreateAPIKey(runs:write): %v", err)
	}
	adminToken, adminKey, err := store.GenerateAPIKey("tenant-a", "admin key", []string{store.ScopeAdmin})
	if err != nil {
		t.Fatalf("GenerateAPIKey(admin): %v", err)
	}
	adminKey = minCostRehash(t, adminToken, adminKey)
	if err := ms.CreateAPIKey(context.Background(), adminKey); err != nil {
		t.Fatalf("CreateAPIKey(admin): %v", err)
	}

	handler := NewWithOptions(ServerOptions{Runner: runner, Store: ms})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	post := func(token string) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/conversations/cleanup", bytes.NewBufferString(`{"max_age_days":30}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST cleanup: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	// A runs:write-only caller must be rejected — it must not be able to
	// bulk-delete every tenant's conversations.
	if code, body := post(writeToken); code != http.StatusForbidden {
		t.Fatalf("runs:write caller: got %d, want 403; body %s", code, body)
	}
	if convStore.deleteOldCalled {
		t.Fatal("DeleteOldConversations must not be called for a rejected runs:write caller")
	}

	// An admin caller succeeds.
	code, body := post(adminToken)
	if code != http.StatusOK {
		t.Fatalf("admin caller: got %d, want 200; body %s", code, body)
	}
	var resp struct {
		Deleted int `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Deleted != 3 {
		t.Errorf("deleted = %d, want 3", resp.Deleted)
	}
	if !convStore.deleteOldCalled {
		t.Error("expected DeleteOldConversations to be called for admin caller")
	}
}
