package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-agent-harness/internal/store"
)

// newTenantScriptWorkflowServer wires a real HTTP server (auth ENABLED, two
// tenants) around a mock script-workflow manager. Used by the S3 (tenant
// isolation) and C2 (unknown-run 404, not hang) attack tests below.
func newTenantScriptWorkflowServer(t *testing.T) (ts *httptest.Server, mgr *mockScriptWorkflowMgr, tokenA, tokenB string) {
	t.Helper()

	ms := store.NewMemoryStore()
	tokenA, keyA := cronTestAPIKey(t, "tenant-alpha", "key A", []string{store.ScopeRunsRead, store.ScopeRunsWrite})
	if err := ms.CreateAPIKey(context.Background(), keyA); err != nil {
		t.Fatalf("CreateAPIKey A: %v", err)
	}
	tokenB, keyB := cronTestAPIKey(t, "tenant-bravo", "key B", []string{store.ScopeRunsRead, store.ScopeRunsWrite})
	if err := ms.CreateAPIKey(context.Background(), keyB); err != nil {
		t.Fatalf("CreateAPIKey B: %v", err)
	}

	mgr = newMockScriptWorkflowMgr()
	h := NewWithOptions(ServerOptions{
		Store:           ms,
		ScriptWorkflows: mgr,
		// AuthDisabled NOT set — auth is enabled, so TenantIDFromContext is populated.
	})
	ts = httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts, mgr, tokenA, tokenB
}

// startScriptWorkflowRunAs starts a run as the given bearer token and returns
// its run ID.
func startScriptWorkflowRunAs(t *testing.T, ts *httptest.Server, token string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/script-workflows/test-workflow/runs", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("start run: expected 202, got %d", res.StatusCode)
	}
	var resp struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if resp.RunID == "" {
		t.Fatal("expected non-empty run_id")
	}
	return resp.RunID
}

// TestS3_ScriptWorkflowGetRun_CrossTenantReturns404 is an ATTACK test: tenant
// A starts a script-workflow run; tenant B must NOT be able to read it by ID.
// 404 (not 403) so tenant B cannot distinguish "not mine" from "does not
// exist" and enumerate tenant A's run IDs.
func TestS3_ScriptWorkflowGetRun_CrossTenantReturns404(t *testing.T) {
	t.Parallel()
	ts, _, tokenA, tokenB := newTenantScriptWorkflowServer(t)
	runID := startScriptWorkflowRunAs(t, ts, tokenA)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/script-workflow-runs/"+runID, nil)
	req.Header.Set("Authorization", "Bearer "+tokenB)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get run as tenant B: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant GET run: expected 404, got %d", res.StatusCode)
	}

	// Sanity: the owning tenant can still read it.
	req2, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/script-workflow-runs/"+runID, nil)
	req2.Header.Set("Authorization", "Bearer "+tokenA)
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("get run as tenant A: %v", err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("owning tenant GET run: expected 200, got %d", res2.StatusCode)
	}
}

// TestS3_ScriptWorkflowResume_CrossTenantReturns404 is an ATTACK test: tenant
// B must not be able to resume tenant A's failed script-workflow run.
func TestS3_ScriptWorkflowResume_CrossTenantReturns404(t *testing.T) {
	t.Parallel()
	ts, mgr, tokenA, tokenB := newTenantScriptWorkflowServer(t)
	runID := startScriptWorkflowRunAs(t, ts, tokenA)

	// Force the run into a failed state so Resume would otherwise be valid.
	mgr.mu.Lock()
	if r, ok := mgr.runs[runID]; ok {
		r.Status = "failed"
	}
	mgr.mu.Unlock()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/script-workflow-runs/"+runID+"/resume", nil)
	req.Header.Set("Authorization", "Bearer "+tokenB)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("resume as tenant B: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant resume: expected 404, got %d", res.StatusCode)
	}
}

// TestS3_ScriptWorkflowEvents_CrossTenantReturns404 is an ATTACK test: tenant
// B must not be able to stream tenant A's script-workflow run events.
func TestS3_ScriptWorkflowEvents_CrossTenantReturns404(t *testing.T) {
	t.Parallel()
	ts, _, tokenA, tokenB := newTenantScriptWorkflowServer(t)
	runID := startScriptWorkflowRunAs(t, ts, tokenA)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/v1/script-workflow-runs/"+runID+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+tokenB)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("cross-tenant events request errored (possible hang/timeout): %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant events: expected 404, got %d", res.StatusCode)
	}
}

// TestC2_ScriptWorkflowEvents_UnknownRunReturns404 is a regression test for a
// hang: streaming events for a run ID that was never started must return 404
// immediately, not block forever waiting for events that will never arrive.
// A bounded client-side context timeout turns "hangs forever" into a clear
// test failure (context deadline exceeded) instead of a test-suite stall.
func TestC2_ScriptWorkflowEvents_UnknownRunReturns404(t *testing.T) {
	t.Parallel()
	ts, mgr, tokenA, _ := newTenantScriptWorkflowServer(t)
	_ = mgr

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/v1/script-workflow-runs/does-not-exist/events", nil)
	req.Header.Set("Authorization", "Bearer "+tokenA)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unknown-run events request errored (possible hang/timeout): %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown run events: expected 404, got %d", res.StatusCode)
	}
}
