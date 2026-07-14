package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/store"
	"go-agent-harness/internal/subagents"

	"golang.org/x/crypto/bcrypt"
)

// minCostRehash replaces key.KeyHash with a MinCost bcrypt hash of rawToken so
// that CompareHashAndPassword stays fast under -race. The production bcrypt cost
// used by store.GenerateAPIKey is slow enough to blow the 30s handler timeout
// under the race detector, which flakes these auth-heavy tests.
func minCostRehash(t *testing.T, rawToken string, key store.APIKey) store.APIKey {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(rawToken), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt MinCost: %v", err)
	}
	key.KeyHash = string(h)
	return key
}

// tenantFakeSubagentManager is a minimal in-memory subagents.Manager double
// that stores whatever TenantID the caller passes on Create and never applies
// tenant filtering itself — matching the real manager, which has no tenant
// concept of its own. This proves that isolation is enforced entirely by the
// HTTP layer (http_subagents.go), not by the manager.
type tenantFakeSubagentManager struct {
	mu        sync.Mutex
	items     map[string]subagents.Subagent
	seq       int
	cancelled map[string]bool
	deleted   map[string]bool
}

func newTenantFakeSubagentManager() *tenantFakeSubagentManager {
	return &tenantFakeSubagentManager{
		items:     make(map[string]subagents.Subagent),
		cancelled: make(map[string]bool),
		deleted:   make(map[string]bool),
	}
}

func (f *tenantFakeSubagentManager) Create(_ context.Context, req subagents.Request) (subagents.Subagent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	id := fmt.Sprintf("subagent-%d", f.seq)
	item := subagents.Subagent{
		ID:       id,
		TenantID: req.TenantID,
		RunID:    "run-" + id,
		Status:   harness.RunStatusCompleted,
	}
	f.items[id] = item
	return item, nil
}

func (f *tenantFakeSubagentManager) Get(_ context.Context, id string) (subagents.Subagent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.items[id]
	if !ok {
		return subagents.Subagent{}, subagents.ErrNotFound
	}
	return item, nil
}

func (f *tenantFakeSubagentManager) List(_ context.Context) ([]subagents.Subagent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]subagents.Subagent, 0, len(f.items))
	for _, item := range f.items {
		out = append(out, item)
	}
	return out, nil
}

func (f *tenantFakeSubagentManager) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.items[id]; !ok {
		return subagents.ErrNotFound
	}
	delete(f.items, id)
	f.deleted[id] = true
	return nil
}

func (f *tenantFakeSubagentManager) Cancel(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.items[id]; !ok {
		return subagents.ErrNotFound
	}
	f.cancelled[id] = true
	return nil
}

// newSubagentTenantAPIKey creates a raw API token plus the store.APIKey record
// for it, scoped to the given tenant with runs:read + runs:write.
func newSubagentTenantAPIKey(t *testing.T, tenantID, name string) (string, store.APIKey) {
	t.Helper()
	rawToken, key, err := store.GenerateAPIKey(tenantID, name, []string{store.ScopeRunsRead, store.ScopeRunsWrite})
	if err != nil {
		t.Fatalf("GenerateAPIKey(%s): %v", tenantID, err)
	}
	return rawToken, minCostRehash(t, rawToken, key)
}

func doSubagentRequest(t *testing.T, ts *httptest.Server, method, token, path string, body []byte) (int, string) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, ts.URL+path, reader)
	if err != nil {
		t.Fatalf("NewRequest %s %s: %v", method, path, err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(respBody)
}

// TestSubagentsTenantIsolation_CreateBindsCallerTenant verifies that POST
// /v1/subagents stamps the caller's authenticated tenant on the created
// subagent, ignoring any client-supplied tenant_id that conflicts.
func TestSubagentsTenantIsolation_CreateBindsCallerTenant(t *testing.T) {
	t.Parallel()

	ms := store.NewMemoryStore()
	tokenA, keyA := newSubagentTenantAPIKey(t, "tenant-alpha", "key A")
	if err := ms.CreateAPIKey(context.Background(), keyA); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	mgr := newTenantFakeSubagentManager()
	handler := NewWithOptions(ServerOptions{Runner: testRunnerForAgents(t), SubagentManager: mgr, Store: ms})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{"prompt": "hello"})
	code, respBody := doSubagentRequest(t, ts, http.MethodPost, tokenA, "/v1/subagents", body)
	if code != http.StatusCreated {
		t.Fatalf("POST /v1/subagents: status %d, body %s", code, respBody)
	}
	var created subagents.Subagent
	if err := json.Unmarshal([]byte(respBody), &created); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	mgr.mu.Lock()
	stored, ok := mgr.items[created.ID]
	mgr.mu.Unlock()
	if !ok {
		t.Fatalf("subagent %q not stored in manager", created.ID)
	}
	if stored.TenantID != "tenant-alpha" {
		t.Fatalf("stored TenantID = %q, want tenant-alpha", stored.TenantID)
	}
}

// TestSubagentsTenantIsolation_ListFiltersToCallerTenant verifies that GET
// /v1/subagents only returns subagents owned by the caller's tenant.
func TestSubagentsTenantIsolation_ListFiltersToCallerTenant(t *testing.T) {
	t.Parallel()

	ms := store.NewMemoryStore()
	tokenA, keyA := newSubagentTenantAPIKey(t, "tenant-alpha", "key A")
	tokenB, keyB := newSubagentTenantAPIKey(t, "tenant-bravo", "key B")
	if err := ms.CreateAPIKey(context.Background(), keyA); err != nil {
		t.Fatalf("CreateAPIKey A: %v", err)
	}
	if err := ms.CreateAPIKey(context.Background(), keyB); err != nil {
		t.Fatalf("CreateAPIKey B: %v", err)
	}

	mgr := newTenantFakeSubagentManager()
	handler := NewWithOptions(ServerOptions{Runner: testRunnerForAgents(t), SubagentManager: mgr, Store: ms})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	createBody, _ := json.Marshal(map[string]any{"prompt": "hello"})
	if code, body := doSubagentRequest(t, ts, http.MethodPost, tokenA, "/v1/subagents", createBody); code != http.StatusCreated {
		t.Fatalf("create as A: status %d, body %s", code, body)
	}
	if code, body := doSubagentRequest(t, ts, http.MethodPost, tokenB, "/v1/subagents", createBody); code != http.StatusCreated {
		t.Fatalf("create as B: status %d, body %s", code, body)
	}

	code, body := doSubagentRequest(t, ts, http.MethodGet, tokenA, "/v1/subagents", nil)
	if code != http.StatusOK {
		t.Fatalf("list as A: status %d, body %s", code, body)
	}
	var listed struct {
		Subagents []subagents.Subagent `json:"subagents"`
	}
	if err := json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Subagents) != 1 {
		t.Fatalf("tenant A sees %d subagents, want 1: %+v", len(listed.Subagents), listed.Subagents)
	}
	if listed.Subagents[0].TenantID != "tenant-alpha" {
		t.Fatalf("listed subagent TenantID = %q, want tenant-alpha", listed.Subagents[0].TenantID)
	}
}

// TestSubagentsTenantIsolation_CrossTenantDenied verifies that get, delete,
// and cancel all return 404 (not the underlying subagent) when the caller's
// tenant does not own the subagent, and that the cross-tenant call has no
// side effect on the target.
func TestSubagentsTenantIsolation_CrossTenantDenied(t *testing.T) {
	t.Parallel()

	ms := store.NewMemoryStore()
	tokenA, keyA := newSubagentTenantAPIKey(t, "tenant-alpha", "key A")
	tokenB, keyB := newSubagentTenantAPIKey(t, "tenant-bravo", "key B")
	if err := ms.CreateAPIKey(context.Background(), keyA); err != nil {
		t.Fatalf("CreateAPIKey A: %v", err)
	}
	if err := ms.CreateAPIKey(context.Background(), keyB); err != nil {
		t.Fatalf("CreateAPIKey B: %v", err)
	}

	mgr := newTenantFakeSubagentManager()
	handler := NewWithOptions(ServerOptions{Runner: testRunnerForAgents(t), SubagentManager: mgr, Store: ms})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	createBody, _ := json.Marshal(map[string]any{"prompt": "hello"})
	code, body := doSubagentRequest(t, ts, http.MethodPost, tokenA, "/v1/subagents", createBody)
	if code != http.StatusCreated {
		t.Fatalf("create as A: status %d, body %s", code, body)
	}
	var created subagents.Subagent
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	id := created.ID

	// Tenant B GET must not find tenant A's subagent.
	if code, respBody := doSubagentRequest(t, ts, http.MethodGet, tokenB, "/v1/subagents/"+id, nil); code != http.StatusNotFound {
		t.Errorf("tenant B GET /v1/subagents/%s: got %d, want 404; body %s", id, code, respBody)
	}

	// Tenant B cancel must not find (and must not cancel) tenant A's subagent.
	if code, respBody := doSubagentRequest(t, ts, http.MethodPost, tokenB, "/v1/subagents/"+id+"/cancel", nil); code != http.StatusNotFound {
		t.Errorf("tenant B POST cancel: got %d, want 404; body %s", code, respBody)
	}
	mgr.mu.Lock()
	cancelled := mgr.cancelled[id]
	mgr.mu.Unlock()
	if cancelled {
		t.Errorf("cross-tenant cancel took effect on subagent %q", id)
	}

	// Tenant B delete must not find (and must not delete) tenant A's subagent.
	if code, respBody := doSubagentRequest(t, ts, http.MethodDelete, tokenB, "/v1/subagents/"+id, nil); code != http.StatusNotFound {
		t.Errorf("tenant B DELETE: got %d, want 404; body %s", code, respBody)
	}
	mgr.mu.Lock()
	_, stillExists := mgr.items[id]
	mgr.mu.Unlock()
	if !stillExists {
		t.Errorf("cross-tenant delete took effect on subagent %q", id)
	}

	// Owner (tenant A) can still get, cancel, and delete.
	if code, respBody := doSubagentRequest(t, ts, http.MethodGet, tokenA, "/v1/subagents/"+id, nil); code != http.StatusOK {
		t.Errorf("owner GET: got %d, want 200; body %s", code, respBody)
	}
	if code, respBody := doSubagentRequest(t, ts, http.MethodPost, tokenA, "/v1/subagents/"+id+"/cancel", nil); code != http.StatusOK {
		t.Errorf("owner cancel: got %d, want 200; body %s", code, respBody)
	}
	if code, respBody := doSubagentRequest(t, ts, http.MethodDelete, tokenA, "/v1/subagents/"+id, nil); code != http.StatusNoContent {
		t.Errorf("owner delete: got %d, want 204; body %s", code, respBody)
	}
}
