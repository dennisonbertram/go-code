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
	"time"

	"go-agent-harness/internal/fakeprovider"
	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/server"
	"go-agent-harness/internal/store"
)

// forkTenantFixture wires an auth-enabled server with two tenants (runs:read +
// runs:write each), a completing fake provider, a memory run store, and a
// SQLite conversation store, for fork cross-tenant tests.
type forkTenantFixture struct {
	ts        *httptest.Server
	tokenA    string
	tenantA   string
	tokenB    string
	tenantB   string
	convStore *harness.SQLiteConversationStore
}

func newForkTenantFixture(t *testing.T) *forkTenantFixture {
	t.Helper()

	ms := store.NewMemoryStore()

	tenantA := "tenant-alpha"
	tokenA, keyA := generateFastAPIKey(t, tenantA, "key A", []string{store.ScopeRunsRead, store.ScopeRunsWrite})
	if err := ms.CreateAPIKey(context.Background(), keyA); err != nil {
		t.Fatalf("CreateAPIKey A: %v", err)
	}

	tenantB := "tenant-bravo"
	tokenB, keyB := generateFastAPIKey(t, tenantB, "key B", []string{store.ScopeRunsRead, store.ScopeRunsWrite})
	if err := ms.CreateAPIKey(context.Background(), keyB); err != nil {
		t.Fatalf("CreateAPIKey B: %v", err)
	}

	prov := fakeprovider.New([]fakeprovider.Turn{{Content: "completed reply"}})

	cs, err := harness.NewSQLiteConversationStore(filepath.Join(t.TempDir(), "fork-tenant-convs.db"))
	if err != nil {
		t.Fatalf("NewSQLiteConversationStore: %v", err)
	}
	if err := cs.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	runner := harness.NewRunner(prov, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "test",
		MaxSteps:            1,
		Store:               ms,
		ConversationStore:   cs,
	})

	h := server.NewWithOptions(server.ServerOptions{
		Store:  ms,
		Runner: runner,
		// Auth enabled (AuthDisabled not set).
	})
	ts := httptest.NewServer(h)
	t.Cleanup(func() {
		ts.Close()
		runner.Shutdown(context.Background())
	})

	return &forkTenantFixture{ts: ts, tokenA: tokenA, tenantA: tenantA, tokenB: tokenB, tenantB: tenantB, convStore: cs}
}

// do auths and performs an HTTP request, returning status and body.
func (f *forkTenantFixture) do(t *testing.T, token, method, path string, body []byte) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rdr)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// TestForkEndpoint_CrossTenantRejected verifies that tenant B cannot fork a
// conversation owned by tenant A (404, same as any cross-tenant by-ID route),
// that tenant A can (positive control), and that the fork itself remains
// tenant-scoped so B cannot read the copy either.
func TestForkEndpoint_CrossTenantRejected(t *testing.T) {
	t.Parallel()
	f := newForkTenantFixture(t)

	// Seed a conversation owned by tenant A directly in the store.
	msgs := []harness.Message{
		{Role: "user", Content: "tenant A secret question"},
		{Role: "assistant", Content: "tenant A secret answer"},
	}
	if err := f.convStore.SaveConversation(context.Background(), "conv-a", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}
	if err := f.convStore.UpdateConversationMeta(context.Background(), "conv-a", "", f.tenantA); err != nil {
		t.Fatalf("UpdateConversationMeta: %v", err)
	}

	// Tenant B must not fork tenant A's conversation.
	code, body := f.do(t, f.tokenB, http.MethodPost, "/v1/conversations/conv-a/fork", nil)
	if code != http.StatusNotFound {
		t.Fatalf("tenant B fork: got %d, want 404; body %s", code, body)
	}

	// Positive control: tenant A forks its own conversation.
	code, body = f.do(t, f.tokenA, http.MethodPost, "/v1/conversations/conv-a/fork", nil)
	if code != http.StatusOK {
		t.Fatalf("tenant A fork: got %d, want 200; body %s", code, body)
	}
	var fork struct {
		ConversationID string `json:"conversation_id"`
		ForkedFrom     string `json:"forked_from"`
		MessageCount   int    `json:"message_count"`
	}
	if err := json.Unmarshal(body, &fork); err != nil {
		t.Fatalf("decode fork response: %v", err)
	}
	if fork.ConversationID == "" || fork.ForkedFrom != "conv-a" || fork.MessageCount != len(msgs) {
		t.Fatalf("bad fork response: %+v", fork)
	}

	// The fork inherits tenant A's ownership: B cannot read the copy.
	code, body = f.do(t, f.tokenB, http.MethodGet, "/v1/conversations/"+fork.ConversationID+"/messages", nil)
	if code != http.StatusNotFound {
		t.Fatalf("tenant B reading fork: got %d, want 404; body %s", code, body)
	}

	// Positive control: A reads its fork and the history matches the source.
	code, body = f.do(t, f.tokenA, http.MethodGet, "/v1/conversations/"+fork.ConversationID+"/messages", nil)
	if code != http.StatusOK {
		t.Fatalf("tenant A reading fork: got %d, want 200; body %s", code, body)
	}
	var gotMsgs struct {
		Messages []harness.Message `json:"messages"`
	}
	if err := json.Unmarshal(body, &gotMsgs); err != nil {
		t.Fatalf("decode fork messages: %v", err)
	}
	if len(gotMsgs.Messages) != len(msgs) {
		t.Fatalf("fork message count: got %d, want %d", len(gotMsgs.Messages), len(msgs))
	}
	for i := range msgs {
		if gotMsgs.Messages[i].Role != msgs[i].Role || gotMsgs.Messages[i].Content != msgs[i].Content {
			t.Errorf("msg[%d]: got %s: %q, want %s: %q", i,
				gotMsgs.Messages[i].Role, gotMsgs.Messages[i].Content, msgs[i].Role, msgs[i].Content)
		}
	}
}

// TestForkEndpoint_InMemoryForkKeepsTenant covers the in-memory resolution
// path under auth: the source conversation exists only in the runner's mirror
// (its store row was deleted). The fork must be stamped with the source run's
// tenant so the copy is not world-readable until the next run persists it.
func TestForkEndpoint_InMemoryForkKeepsTenant(t *testing.T) {
	t.Parallel()
	f := newForkTenantFixture(t)

	// Tenant A completes a run; the conversation (ID == run ID) is mirrored in
	// memory and persisted with A's tenant stamp.
	code, body := f.do(t, f.tokenA, http.MethodPost, "/v1/runs", []byte(`{"prompt":"hello tenant world"}`))
	if code != http.StatusAccepted {
		t.Fatalf("POST /v1/runs: got %d, want 202; body %s", code, body)
	}
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	convID := created.RunID

	// Wait until the run's history is visible (mirror + store persisted).
	deadline := time.Now().Add(10 * time.Second)
	for {
		code, _ = f.do(t, f.tokenA, http.MethodGet, "/v1/conversations/"+convID+"/messages", nil)
		if code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for conversation %q to become readable (last status %d)", convID, code)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Delete the store row; the in-memory mirror remains.
	code, body = f.do(t, f.tokenA, http.MethodDelete, "/v1/conversations/"+convID, nil)
	if code != http.StatusOK {
		t.Fatalf("DELETE conversation: got %d, want 200; body %s", code, body)
	}

	// Tenant A forks the memory-only conversation.
	code, body = f.do(t, f.tokenA, http.MethodPost, "/v1/conversations/"+convID+"/fork", nil)
	if code != http.StatusOK {
		t.Fatalf("tenant A fork of memory-only conversation: got %d, want 200; body %s", code, body)
	}
	var fork struct {
		ConversationID string `json:"conversation_id"`
		MessageCount   int    `json:"message_count"`
	}
	if err := json.Unmarshal(body, &fork); err != nil {
		t.Fatalf("decode fork response: %v", err)
	}
	if fork.ConversationID == "" || fork.MessageCount < 2 {
		t.Fatalf("bad fork response: %+v (want new ID and >= 2 messages)", fork)
	}

	// The fork must be stamped with tenant A: tenant B gets 404, not the copy.
	code, body = f.do(t, f.tokenB, http.MethodGet, "/v1/conversations/"+fork.ConversationID+"/messages", nil)
	if code != http.StatusNotFound {
		t.Fatalf("tenant B reading memory-only fork: got %d, want 404 (cross-tenant leak); body %s", code, body)
	}

	// Positive control: tenant A still reads the fork.
	code, body = f.do(t, f.tokenA, http.MethodGet, "/v1/conversations/"+fork.ConversationID+"/messages", nil)
	if code != http.StatusOK {
		t.Fatalf("tenant A reading its fork: got %d, want 200; body %s", code, body)
	}
}
