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

// twoTenantFixture wires up a single server with auth enabled and two tenants,
// each with a distinct API key carrying runs:read + runs:write. A run started
// by tenant A must never be readable or controllable by tenant B.
type twoTenantFixture struct {
	ts        *httptest.Server
	prov      *fakeprovider.Provider
	tokenA    string
	tenantA   string
	tokenB    string
	tenantB   string
	runStore  store.Store
	convStore *harness.SQLiteConversationStore
}

// newTwoTenantFixture builds the fixture. The provider hangs on its first turn
// so any run stays active (and resident in the runner's in-memory map) until
// explicitly released, which keeps cross-tenant by-ID assertions deterministic.
// A SQLite ConversationStore is wired in so tests can seed conversation data and
// verify messages-endpoint isolation with a real positive control.
func newTwoTenantFixture(t *testing.T) *twoTenantFixture {
	t.Helper()

	ms := store.NewMemoryStore()

	tenantA := "tenant-alpha"
	tokenA, keyA := generateFastAPIKey(t, tenantA, "key A", []string{
		store.ScopeRunsRead,
		store.ScopeRunsWrite,
	})
	if err := ms.CreateAPIKey(context.Background(), keyA); err != nil {
		t.Fatalf("CreateAPIKey A: %v", err)
	}

	tenantB := "tenant-bravo"
	tokenB, keyB := generateFastAPIKey(t, tenantB, "key B", []string{
		store.ScopeRunsRead,
		store.ScopeRunsWrite,
	})
	if err := ms.CreateAPIKey(context.Background(), keyB); err != nil {
		t.Fatalf("CreateAPIKey B: %v", err)
	}

	prov := fakeprovider.New([]fakeprovider.Turn{{Hang: true}})

	// SQLite conversation store enables the messages-endpoint positive control:
	// tests can seed a conversation after the run is in-flight so that tenant A
	// gets 200 (proves the gate is real) rather than 404 (no messages yet).
	cs, err := harness.NewSQLiteConversationStore(filepath.Join(t.TempDir(), "two-tenant-convs.db"))
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
		MaxSteps:            2,
		Store:               ms,
		ConversationStore:   cs,
	})

	h := server.NewWithOptions(server.ServerOptions{
		Store:  ms,
		Runner: runner,
		// AuthDisabled NOT set -- auth is enabled.
	})
	ts := httptest.NewServer(h)
	t.Cleanup(func() {
		prov.Release()
		ts.Close()
		runner.Shutdown(context.Background())
	})

	return &twoTenantFixture{
		ts:        ts,
		prov:      prov,
		tokenA:    tokenA,
		tenantA:   tenantA,
		tokenB:    tokenB,
		tenantB:   tenantB,
		runStore:  ms,
		convStore: cs,
	}
}

// startRun POSTs /v1/runs as the given tenant token and returns the new run ID.
func (f *twoTenantFixture) startRun(t *testing.T, token string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"prompt": "hello"})
	req, _ := http.NewRequest(http.MethodPost, f.ts.URL+"/v1/runs", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/runs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /v1/runs: status %d, body %s", resp.StatusCode, body)
	}
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.RunID == "" {
		t.Fatal("expected non-empty run_id")
	}
	return created.RunID
}

// waitInFlight blocks until the hanging provider has been entered at least once,
// proving the run is active and resident in memory.
func (f *twoTenantFixture) waitInFlight(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f.prov.Calls() >= 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("provider never went in-flight")
}

// doByID issues an HTTP request to a by-ID route with the given token and
// returns the status code and body. A short per-request timeout guards against
// the events route's SSE stream blocking the read indefinitely (it streams a
// 200 while the bug is present; it returns a 404 once the fix lands).
func (f *twoTenantFixture) doByID(t *testing.T, method, token, path string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, method, f.ts.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// currentStatus reads the run's status as the owning tenant.
func (f *twoTenantFixture) currentStatus(t *testing.T, runID string) string {
	t.Helper()
	code, body := f.doByID(t, http.MethodGet, f.tokenA, "/v1/runs/"+runID)
	if code != http.StatusOK {
		t.Fatalf("owner GET /v1/runs/%s: status %d, body %s", runID, code, body)
	}
	var st struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatalf("decode status: %v (%s)", err, body)
	}
	return st.Status
}

// TestTenantIsolation_ByID_CrossTenantDenied (T-PFIX-2): a run owned by tenant A
// must be invisible and uncontrollable by tenant B over the by-ID routes.
func TestTenantIsolation_ByID_CrossTenantDenied(t *testing.T) {
	t.Parallel()

	f := newTwoTenantFixture(t)
	runID := f.startRun(t, f.tokenA)
	f.waitInFlight(t)

	// Tenant B GET /v1/runs/{id} -> 404 (must not reveal existence).
	if code, body := f.doByID(t, http.MethodGet, f.tokenB, "/v1/runs/"+runID); code != http.StatusNotFound {
		t.Errorf("tenant B GET /v1/runs/%s: got %d, want 404; body %s", runID, code, body)
	}

	// Tenant B GET /v1/runs/{id}/events -> 404.
	if code, _ := f.doByID(t, http.MethodGet, f.tokenB, "/v1/runs/"+runID+"/events"); code != http.StatusNotFound {
		t.Errorf("tenant B GET events: got %d, want 404", code)
	}

	// Tenant B GET /v1/runs/{id}/summary -> 404.
	if code, _ := f.doByID(t, http.MethodGet, f.tokenB, "/v1/runs/"+runID+"/summary"); code != http.StatusNotFound {
		t.Errorf("tenant B GET summary: got %d, want 404", code)
	}

	// Tenant B POST /v1/runs/{id}/cancel -> 404 AND must NOT cancel the run.
	if code, body := f.doByID(t, http.MethodPost, f.tokenB, "/v1/runs/"+runID+"/cancel"); code != http.StatusNotFound {
		t.Errorf("tenant B POST cancel: got %d, want 404; body %s", code, body)
	}

	// The run must remain active: tenant B's cancel must NOT have taken effect.
	if got := f.currentStatus(t, runID); got == "cancelled" || got == "cancelling" {
		t.Errorf("cross-tenant cancel affected the run: status=%q", got)
	}

	// Owner (tenant A) still sees and controls the run.
	if code, body := f.doByID(t, http.MethodGet, f.tokenA, "/v1/runs/"+runID); code != http.StatusOK {
		t.Errorf("owner GET /v1/runs/%s: got %d, want 200; body %s", runID, code, body)
	}
	if code, body := f.doByID(t, http.MethodPost, f.tokenA, "/v1/runs/"+runID+"/cancel"); code != http.StatusOK {
		t.Errorf("owner POST cancel: got %d, want 200; body %s", code, body)
	}
}

// TestTenantIsolation_CreateListAuth (T-D-create-list-auth): locks in the
// existing correct create/list tenant behavior under auth.
func TestTenantIsolation_CreateListAuth(t *testing.T) {
	t.Parallel()

	f := newTwoTenantFixture(t)

	post := func(token string, body map[string]any) (int, string) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, f.ts.URL+"/v1/runs", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /v1/runs: %v", err)
		}
		defer resp.Body.Close()
		rb, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(rb)
	}

	// Conflicting tenant_id -> 400 invalid_request.
	if code, body := post(f.tokenA, map[string]any{"prompt": "x", "tenant_id": f.tenantB}); code != http.StatusBadRequest {
		t.Errorf("conflicting tenant_id: got %d, want 400; body %s", code, body)
	}

	// Matching tenant_id -> 202.
	if code, body := post(f.tokenA, map[string]any{"prompt": "x", "tenant_id": f.tenantA}); code != http.StatusAccepted {
		t.Errorf("matching tenant_id: got %d, want 202; body %s", code, body)
	}

	// Empty tenant_id -> 202 (filled from auth).
	if code, body := post(f.tokenA, map[string]any{"prompt": "x"}); code != http.StatusAccepted {
		t.Errorf("empty tenant_id: got %d, want 202; body %s", code, body)
	}

	// Tenant B starts a run too.
	if code, body := post(f.tokenB, map[string]any{"prompt": "y"}); code != http.StatusAccepted {
		t.Errorf("tenant B create: got %d, want 202; body %s", code, body)
	}

	// GET /v1/runs as tenant A must list ONLY tenant A's runs.
	listReq, _ := http.NewRequest(http.MethodGet, f.ts.URL+"/v1/runs", nil)
	listReq.Header.Set("Authorization", "Bearer "+f.tokenA)
	listResp, err := http.DefaultClient.Do(listReq)
	if err != nil {
		t.Fatalf("GET /v1/runs: %v", err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(listResp.Body)
		t.Fatalf("GET /v1/runs: status %d, body %s", listResp.StatusCode, body)
	}
	var listed struct {
		Runs []struct {
			ID       string `json:"id"`
			TenantID string `json:"tenant_id"`
		} `json:"runs"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Runs) == 0 {
		t.Fatal("expected tenant A to have at least one run listed")
	}
	for _, r := range listed.Runs {
		if r.TenantID != f.tenantA {
			t.Errorf("GET /v1/runs leaked a run for tenant %q (run %s); caller is %q", r.TenantID, r.ID, f.tenantA)
		}
	}
}

// convIDFromRun fetches GET /v1/runs/{id} as tenant A and extracts conversation_id.
func (f *twoTenantFixture) convIDFromRun(t *testing.T, runID string) string {
	t.Helper()
	code, body := f.doByID(t, http.MethodGet, f.tokenA, "/v1/runs/"+runID)
	if code != http.StatusOK {
		t.Fatalf("GET /v1/runs/%s as owner: status %d, body %s", runID, code, body)
	}
	var out struct {
		ConversationID string `json:"conversation_id"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode run response: %v (%s)", err, body)
	}
	if out.ConversationID == "" {
		t.Fatalf("run %s has empty conversation_id", runID)
	}
	return out.ConversationID
}

// TestTenantIsolation_ConversationRuns_CrossTenantDenied (T-PFIX-3):
//   - GET /v1/conversations/{id}/runs built a RunFilter with no TenantID ->
//     tenant B could enumerate runs belonging to tenant A's conversation.
//   - The conversation sub-resource routes (messages, export) must also return
//     404 when the conversation belongs to another tenant.
func TestTenantIsolation_ConversationRuns_CrossTenantDenied(t *testing.T) {
	t.Parallel()

	f := newTwoTenantFixture(t)
	runID := f.startRun(t, f.tokenA)
	f.waitInFlight(t)

	// Get the conversation ID from the run (as tenant A, who owns it).
	convID := f.convIDFromRun(t, runID)

	// --- runs sub-resource ---
	// Tenant A must see their own run in the conversation.
	if code, body := f.doByID(t, http.MethodGet, f.tokenA, "/v1/conversations/"+convID+"/runs"); code != http.StatusOK {
		t.Errorf("owner GET /v1/conversations/%s/runs: status %d, body %s", convID, code, body)
	} else {
		var resp struct {
			Runs []struct {
				ID       string `json:"id"`
				TenantID string `json:"tenant_id"`
			} `json:"runs"`
		}
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatalf("decode conversation runs response: %v (%s)", err, body)
		}
		if len(resp.Runs) == 0 {
			t.Errorf("owner GET /v1/conversations/%s/runs: expected at least 1 run, got 0", convID)
		}
	}

	// Tenant B must NOT see tenant A's runs in the conversation -- cross-tenant leak.
	// Before fix: tenant B gets a 200 with A's run. After fix: 200 with empty list.
	code, body := f.doByID(t, http.MethodGet, f.tokenB, "/v1/conversations/"+convID+"/runs")
	if code != http.StatusOK {
		t.Errorf("tenant B GET /v1/conversations/%s/runs: want 200 (empty), got %d; body %s", convID, code, body)
	} else {
		var resp struct {
			Runs []struct {
				ID       string `json:"id"`
				TenantID string `json:"tenant_id"`
			} `json:"runs"`
		}
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatalf("decode cross-tenant conversation runs response: %v (%s)", err, body)
		}
		if len(resp.Runs) != 0 {
			t.Errorf("cross-tenant GET /v1/conversations/%s/runs leaked %d run(s) to tenant B; want 0",
				convID, len(resp.Runs))
		}
	}

	// --- messages sub-resource ---
	// Seed the SQLite conversation store with a message owned by tenant A.
	// This ensures the messages endpoint can return 200 for tenant A (positive
	// control), so a blanket-404 implementation could not pass this test.
	// The run record (persisted to runStore synchronously at StartRun) is
	// authoritative for conversationTenantMismatch; the seeded conversation row
	// additionally satisfies ConversationMessages' fallback path for in-flight runs.
	ctx := context.Background()
	if err := f.convStore.SaveConversation(ctx, convID, []harness.Message{
		{Role: "user", Content: "tenant A probe message"},
	}); err != nil {
		t.Fatalf("seed SaveConversation: %v", err)
	}
	if err := f.convStore.UpdateConversationMeta(ctx, convID, "", f.tenantA); err != nil {
		t.Fatalf("seed UpdateConversationMeta: %v", err)
	}

	// Positive control: tenant A must see their own conversation (200 + ≥1 message).
	// If this is 404, the seeding or server wiring is broken — not a tenant-gate issue.
	if code, body := f.doByID(t, http.MethodGet, f.tokenA, "/v1/conversations/"+convID+"/messages"); code != http.StatusOK {
		t.Errorf("owner GET /v1/conversations/%s/messages: want 200, got %d; body %s", convID, code, body)
	}

	// Negative control: tenant B must NOT see tenant A's conversation.
	// Because tenant A DID get 200 above, a 404 here can only mean the gate fired,
	// not that the conversation simply had no messages.
	if code, _ := f.doByID(t, http.MethodGet, f.tokenB, "/v1/conversations/"+convID+"/messages"); code != http.StatusNotFound {
		t.Errorf("tenant B GET /v1/conversations/%s/messages: want 404, got %d", convID, code)
	}

	// --- export sub-resource ---
	// Positive control: tenant A can export their own conversation.
	if code, body := f.doByID(t, http.MethodGet, f.tokenA, "/v1/conversations/"+convID+"/export"); code != http.StatusOK {
		t.Errorf("owner GET /v1/conversations/%s/export: want 200, got %d; body %s", convID, code, body)
	}
	// Negative control: tenant B must NOT export tenant A's conversation.
	if code, _ := f.doByID(t, http.MethodGet, f.tokenB, "/v1/conversations/"+convID+"/export"); code != http.StatusNotFound {
		t.Errorf("tenant B GET /v1/conversations/%s/export: want 404, got %d", convID, code)
	}
}

// runlessConversationFixture sets up a server with auth + a SQLite conversation
// store containing a conversation owned by tenant A that has messages but NO
// persisted run in the run store. This is the GAP-1 scenario: conversationTenantMismatch
// resolves ownership via runStore.ListRuns, which returns empty → no gate → cross-tenant
// access to a runless conversation's messages is allowed (the bug).
type runlessConversationFixture struct {
	ts     *httptest.Server
	tokenA string
	tokenB string
	convID string
}

func newRunlessConversationFixture(t *testing.T) *runlessConversationFixture {
	t.Helper()

	ms := store.NewMemoryStore()

	tenantA := "tenant-alpha"
	tokenA, keyA := generateFastAPIKey(t, tenantA, "key A", []string{
		store.ScopeRunsRead,
		store.ScopeRunsWrite,
	})
	if err := ms.CreateAPIKey(context.Background(), keyA); err != nil {
		t.Fatalf("CreateAPIKey A: %v", err)
	}

	tenantB := "tenant-bravo"
	tokenB, keyB := generateFastAPIKey(t, tenantB, "key B", []string{
		store.ScopeRunsRead,
		store.ScopeRunsWrite,
	})
	if err := ms.CreateAPIKey(context.Background(), keyB); err != nil {
		t.Fatalf("CreateAPIKey B: %v", err)
	}

	// SQLite conversation store with tenant A's conversation — no run record anywhere.
	path := filepath.Join(t.TempDir(), "runless-conv.db")
	cs, err := harness.NewSQLiteConversationStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteConversationStore: %v", err)
	}
	if err := cs.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	convID := "conv-no-run-tenant-alpha"
	ctx := context.Background()
	if err := cs.SaveConversation(ctx, convID, []harness.Message{
		{Role: "user", Content: "tenant A secret: alpha-top-secret-42"},
		{Role: "assistant", Content: "understood, alpha-top-secret-42"},
	}); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}
	// stamp the conversation with tenant A's tenant_id (no run created anywhere).
	if err := cs.UpdateConversationMeta(ctx, convID, "", tenantA); err != nil {
		t.Fatalf("UpdateConversationMeta: %v", err)
	}

	runner := harness.NewRunner(
		fakeprovider.New(nil),
		harness.NewRegistry(),
		harness.RunnerConfig{
			DefaultModel:      "test-model",
			Store:             ms,
			ConversationStore: cs,
		},
	)

	h := server.NewWithOptions(server.ServerOptions{
		Store:  ms,
		Runner: runner,
		// AuthDisabled NOT set — auth is enabled.
	})
	ts := httptest.NewServer(h)
	t.Cleanup(func() {
		ts.Close()
		runner.Shutdown(context.Background())
	})

	return &runlessConversationFixture{
		ts:     ts,
		tokenA: tokenA,
		tokenB: tokenB,
		convID: convID,
	}
}

func (f *runlessConversationFixture) get(t *testing.T, token, path string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, f.ts.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// TestTenantIsolation_RunlessConversation_CrossTenantDenied (GAP-1):
// A conversation that exists in the conversation store (with messages and a
// tenant_id) but has NO persisted run must still be gated. Before the fix,
// conversationTenantMismatch falls back to runStore.ListRuns (returns empty) →
// no gate → tenant B reads tenant A's messages. After the fix it must return 404.
func TestTenantIsolation_RunlessConversation_CrossTenantDenied(t *testing.T) {
	t.Parallel()

	f := newRunlessConversationFixture(t)

	// Sanity: tenant A can access their own conversation's messages.
	if code, body := f.get(t, f.tokenA, "/v1/conversations/"+f.convID+"/messages"); code != http.StatusOK {
		t.Errorf("tenant A GET messages: got %d, want 200; body %s", code, body)
	}

	// GAP-1 regression: tenant B must NOT be able to access tenant A's runless
	// conversation messages. Currently returns 200 (BUG — no gate when 0 runs).
	if code, body := f.get(t, f.tokenB, "/v1/conversations/"+f.convID+"/messages"); code != http.StatusNotFound {
		t.Errorf("tenant B GET messages: got %d (body: %s), want 404 — cross-tenant runless conversation is ungated", code, body)
	}

	// Same for the export endpoint.
	if code, body := f.get(t, f.tokenB, "/v1/conversations/"+f.convID+"/export"); code != http.StatusNotFound {
		t.Errorf("tenant B GET export: got %d (body: %s), want 404 — cross-tenant runless conversation is ungated", code, body)
	}
}
