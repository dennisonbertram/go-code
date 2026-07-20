package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"go-agent-harness/internal/fakeprovider"
	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/server"
	"go-agent-harness/internal/store"
)

// postJSON issues a POST with a JSON body as the given bearer token.
func postJSON(t *testing.T, ts *httptest.Server, token, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+path, bytes.NewBufferString(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestTenantIsolation_UndoConversation_CrossTenantDenied (epic #805 Slice 2):
// tenant B must not be able to undo prompts in tenant A's conversation — the
// cross-tenant gate fires with 404 before the handler runs, and the failed
// attempt must not mutate the conversation. The owner's undo still works
// (positive control proving the 404 is the gate, not a broken route).
func TestTenantIsolation_UndoConversation_CrossTenantDenied(t *testing.T) {
	t.Parallel()

	f := newRunlessConversationFixture(t)
	undoPath := "/v1/conversations/" + f.convID + "/undo"

	// Negative control first: tenant B POST undo -> 404 (must not reveal or mutate).
	if code, body := postJSON(t, f.ts, f.tokenB, undoPath, `{"count":1}`); code != http.StatusNotFound {
		t.Errorf("tenant B POST undo: got %d, want 404; body %s", code, body)
	}

	// Tenant B's attempt must NOT have truncated tenant A's conversation.
	if code, body := f.get(t, f.tokenA, "/v1/conversations/"+f.convID+"/messages"); code != http.StatusOK {
		t.Fatalf("tenant A GET messages after B's undo attempt: got %d, want 200; body %s", code, body)
	} else {
		var resp struct {
			Messages []harness.Message `json:"messages"`
		}
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatalf("decode messages: %v (%s)", err, body)
		}
		if len(resp.Messages) != 2 {
			t.Errorf("cross-tenant undo mutated the conversation: got %d messages, want 2", len(resp.Messages))
		}
	}

	// Positive control: tenant A (owner) can undo; without this a 404 above
	// could mean a broken route rather than a real tenant gate.
	code, body := postJSON(t, f.ts, f.tokenA, undoPath, `{"count":1}`)
	if code != http.StatusOK {
		t.Fatalf("tenant A POST undo: got %d, want 200; body %s", code, body)
	}
	var resp struct {
		Undone            bool `json:"undone"`
		RemovedFromStep   int  `json:"removed_from_step"`
		RemainingMessages int  `json:"remaining_messages"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode undo response: %v (%s)", err, body)
	}
	if !resp.Undone || resp.RemainingMessages != 1 {
		t.Errorf("unexpected undo response: %+v (want undone=true, remaining_messages=1 marker)", resp)
	}
}

// TestUndoConversationEndpoint_RequiresRunsWrite (epic #805 Slice 2): the undo
// route is destructive, so a key carrying only runs:read must be rejected with
// 403, while a runs:write key on the owning tenant succeeds.
func TestUndoConversationEndpoint_RequiresRunsWrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ms := store.NewMemoryStore()

	tenantW := "tenant-writer"
	tokenW, keyW := generateFastAPIKey(t, tenantW, "writer key", []string{
		store.ScopeRunsRead,
		store.ScopeRunsWrite,
	})
	if err := ms.CreateAPIKey(ctx, keyW); err != nil {
		t.Fatalf("CreateAPIKey writer: %v", err)
	}

	tokenR, keyR := generateFastAPIKey(t, tenantW, "reader key", []string{
		store.ScopeRunsRead,
	})
	if err := ms.CreateAPIKey(ctx, keyR); err != nil {
		t.Fatalf("CreateAPIKey reader: %v", err)
	}

	cs, err := harness.NewSQLiteConversationStore(filepath.Join(t.TempDir(), "undo-scope.db"))
	if err != nil {
		t.Fatalf("NewSQLiteConversationStore: %v", err)
	}
	if err := cs.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	convID := "conv-undo-scope"
	if err := cs.SaveConversation(ctx, convID, []harness.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
	}); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}
	if err := cs.UpdateConversationMeta(ctx, convID, "", tenantW); err != nil {
		t.Fatalf("UpdateConversationMeta: %v", err)
	}

	runner := harness.NewRunner(
		fakeprovider.New(nil),
		harness.NewRegistry(),
		harness.RunnerConfig{DefaultModel: "test-model", Store: ms, ConversationStore: cs},
	)
	h := server.NewWithOptions(server.ServerOptions{Store: ms, Runner: runner})
	ts := httptest.NewServer(h)
	t.Cleanup(func() {
		ts.Close()
		runner.Shutdown(context.Background())
	})

	undoPath := "/v1/conversations/" + convID + "/undo"

	// runs:read-only key -> 403 insufficient_scope, and no mutation.
	if code, body := postJSON(t, ts, tokenR, undoPath, `{"count":1}`); code != http.StatusForbidden {
		t.Errorf("read-only POST undo: got %d, want 403; body %s", code, body)
	}
	loaded, err := cs.LoadMessages(ctx, convID)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != 2 {
		t.Errorf("read-only undo mutated the conversation: got %d messages, want 2", len(loaded))
	}

	// runs:write key on the owning tenant -> 200 (proves the 403 is the scope gate).
	if code, body := postJSON(t, ts, tokenW, undoPath, `{"count":1}`); code != http.StatusOK {
		t.Errorf("writer POST undo: got %d, want 200; body %s", code, body)
	}
}
