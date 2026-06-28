package server_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-agent-harness/internal/checkpoints"
	"go-agent-harness/internal/fakeprovider"
	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/server"
	"go-agent-harness/internal/store"
)

// checkpointTenantFixture wires a server with auth enabled, two tenants, a run
// store, and a checkpoint service. A checkpoint created for a run belonging to
// tenant A must be invisible and unresumable by tenant B (GAP-2).
type checkpointTenantFixture struct {
	ts           *httptest.Server
	tokenA       string
	tokenB       string
	checkpointID string
}

func newCheckpointTenantFixture(t *testing.T) *checkpointTenantFixture {
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

	// Persist a run owned by tenant A directly into the store (no live runner
	// involvement needed — we only need the tenant ownership record).
	runA := &store.Run{
		ID:       "run-tenant-alpha-001",
		TenantID: tenantA,
		Status:   store.RunStatusCompleted,
	}
	if err := ms.CreateRun(context.Background(), runA); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	// Create a checkpoint service and a checkpoint that references tenant A's run.
	now := time.Now
	cpStore := checkpoints.NewMemoryStore()
	checkpointSvc := checkpoints.NewService(cpStore, now)
	cp, err := checkpointSvc.Create(context.Background(), checkpoints.CreateRequest{
		Kind:  checkpoints.KindExternalResume,
		RunID: runA.ID,
	})
	if err != nil {
		t.Fatalf("checkpoints.Create: %v", err)
	}

	runner := harness.NewRunner(
		fakeprovider.New(nil),
		harness.NewRegistry(),
		harness.RunnerConfig{
			DefaultModel: "test-model",
			Store:        ms,
		},
	)

	h := server.NewWithOptions(server.ServerOptions{
		Store:       ms,
		Runner:      runner,
		Checkpoints: checkpointSvc,
		// AuthDisabled NOT set — auth is enabled.
	})
	ts := httptest.NewServer(h)
	t.Cleanup(func() {
		ts.Close()
		runner.Shutdown(context.Background())
	})

	return &checkpointTenantFixture{
		ts:           ts,
		tokenA:       tokenA,
		tokenB:       tokenB,
		checkpointID: cp.ID,
	}
}

func (f *checkpointTenantFixture) doCheckpoint(t *testing.T, method, token, path string, body []byte) (int, string) {
	t.Helper()
	var req *http.Request
	if body != nil {
		req, _ = http.NewRequest(method, f.ts.URL+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, _ = http.NewRequest(method, f.ts.URL+path, nil)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestTenantIsolation_Checkpoint_UnresolvableRunFailsClosed proves that a
// checkpoint whose RunID references a run that exists in neither the in-memory
// runner state nor the persistent store is NOT served to an authenticated
// (non-default) tenant. This is the fail-closed path: when tenant ownership
// cannot be resolved, access is denied rather than granted.
func TestTenantIsolation_Checkpoint_UnresolvableRunFailsClosed(t *testing.T) {
	t.Parallel()

	ms := store.NewMemoryStore()

	tenantA := "tenant-alpha-unresolvable"
	tokenA, keyA := generateFastAPIKey(t, tenantA, "key A unresolvable", []string{
		store.ScopeRunsRead,
		store.ScopeRunsWrite,
	})
	if err := ms.CreateAPIKey(context.Background(), keyA); err != nil {
		t.Fatalf("CreateAPIKey A: %v", err)
	}

	// Create a checkpoint that references a run ID that does NOT exist in the
	// store or in-memory runner state. This simulates a run that was purged or
	// never recorded. The gate must fail CLOSED for the authenticated caller.
	now := time.Now
	cpStore := checkpoints.NewMemoryStore()
	checkpointSvc := checkpoints.NewService(cpStore, now)
	cp, err := checkpointSvc.Create(context.Background(), checkpoints.CreateRequest{
		Kind:  checkpoints.KindExternalResume,
		RunID: "run-that-does-not-exist-anywhere",
	})
	if err != nil {
		t.Fatalf("checkpoints.Create: %v", err)
	}

	runner := harness.NewRunner(
		fakeprovider.New(nil),
		harness.NewRegistry(),
		harness.RunnerConfig{
			DefaultModel: "test-model",
			Store:        ms,
		},
	)

	h := server.NewWithOptions(server.ServerOptions{
		Store:       ms,
		Runner:      runner,
		Checkpoints: checkpointSvc,
		// AuthDisabled NOT set — auth is enabled.
	})
	ts := httptest.NewServer(h)
	t.Cleanup(func() {
		ts.Close()
		runner.Shutdown(context.Background())
	})

	cpPath := "/v1/checkpoints/" + cp.ID

	doReq := func(method, token, path string, body []byte) (int, string) {
		t.Helper()
		var req *http.Request
		if body != nil {
			req, _ = http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req, _ = http.NewRequest(method, ts.URL+path, nil)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// GET: authenticated tenant must NOT be able to read a checkpoint whose run
	// cannot be resolved — fail closed → 404.
	if code, body := doReq(http.MethodGet, tokenA, cpPath, nil); code != http.StatusNotFound {
		t.Errorf("GET checkpoint with unresolvable run: got %d (body: %s), want 404 — fail-closed required", code, body)
	}

	// POST /resume: same gate must apply to mutation.
	if code, body := doReq(http.MethodPost, tokenA, cpPath+"/resume",
		[]byte(`{"payload":{}}`)); code != http.StatusNotFound {
		t.Errorf("POST checkpoint/resume with unresolvable run: got %d (body: %s), want 404 — fail-closed required", code, body)
	}
}

// TestTenantIsolation_Checkpoint_CrossTenantDenied (GAP-2):
// GET /v1/checkpoints/{id} and POST /v1/checkpoints/{id}/resume enforce scope
// but NOT tenant ownership. Tenant B currently reads/resumes a checkpoint
// created by tenant A's run (BUG). After the fix, both must return 404.
func TestTenantIsolation_Checkpoint_CrossTenantDenied(t *testing.T) {
	t.Parallel()

	f := newCheckpointTenantFixture(t)
	cpPath := "/v1/checkpoints/" + f.checkpointID

	// Sanity: tenant A can read their own checkpoint.
	if code, body := f.doCheckpoint(t, http.MethodGet, f.tokenA, cpPath, nil); code != http.StatusOK {
		t.Errorf("tenant A GET checkpoint: got %d, want 200; body %s", code, body)
	}

	// GAP-2 regression: tenant B must NOT be able to read tenant A's checkpoint.
	// Currently returns 200 (BUG — no tenant ownership check in http_checkpoints.go).
	if code, body := f.doCheckpoint(t, http.MethodGet, f.tokenB, cpPath, nil); code != http.StatusNotFound {
		t.Errorf("tenant B GET checkpoint: got %d (body: %s), want 404 — checkpoint tenant ownership not enforced", code, body)
	}

	// GAP-2 regression: tenant B must NOT be able to resume tenant A's checkpoint.
	if code, body := f.doCheckpoint(t, http.MethodPost, f.tokenB, cpPath+"/resume",
		[]byte(`{"payload":{}}`)); code != http.StatusNotFound {
		t.Errorf("tenant B POST checkpoint/resume: got %d (body: %s), want 404 — checkpoint tenant ownership not enforced", code, body)
	}

	// Tenant A's resume should still work (owner access must be preserved).
	if code, body := f.doCheckpoint(t, http.MethodPost, f.tokenA, cpPath+"/resume",
		[]byte(`{"payload":{"decision":"ok"}}`)); code != http.StatusOK {
		t.Errorf("tenant A POST checkpoint/resume: got %d, want 200; body %s", code, body)
	}
}
