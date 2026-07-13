package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"go-agent-harness/internal/relay"
	"go-agent-harness/internal/server"
)

// newRelayControlServer builds an auth-disabled server backed by a real SQLite
// relay worker store and its control plane, plus one online worker in tenant t1.
func newRelayControlServer(t *testing.T) (http.Handler, *relay.SQLiteWorkerStore) {
	t.Helper()
	ctx := context.Background()
	ws, err := relay.NewSQLiteWorkerStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatalf("worker store: %v", err)
	}
	if err := ws.Migrate(ctx); err != nil {
		t.Fatalf("migrate worker store: %v", err)
	}
	cp, err := relay.NewControlPlane(ctx, ws)
	if err != nil {
		t.Fatalf("control plane: %v", err)
	}
	if err := ws.RegisterWorker(ctx, &relay.Worker{
		ID:           "w1",
		TenantID:     "t1",
		Name:         "worker-1",
		LocationType: relay.LocationLocal,
		Status:       relay.WorkerStatusOnline,
		TrustTier:    relay.TrustTierStandard,
	}); err != nil {
		t.Fatalf("register worker: %v", err)
	}
	h := server.NewWithOptions(server.ServerOptions{
		AuthDisabled:     true,
		RelayWorkerStore: ws,
		RelayControl:     cp,
	})
	return h, ws
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRelayControl_NotConfigured_Returns501(t *testing.T) {
	// Auth disabled, worker store present, but NO control plane wired.
	h := server.NewWithOptions(server.ServerOptions{AuthDisabled: true})
	for _, path := range []string{
		"/v1/relay/contracts",
		"/v1/relay/placements",
		"/v1/relay/policy/check",
	} {
		rec := doJSON(t, h, http.MethodPost, path, map[string]any{})
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s: status = %d, want 501", path, rec.Code)
		}
	}
}

func TestRelayControl_ComposeContract(t *testing.T) {
	h, _ := newRelayControlServer(t)
	rec := doJSON(t, h, http.MethodPost, "/v1/relay/contracts", map[string]any{
		"Prompt":   "investigate the failing test",
		"TenantID": "t1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var contract map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &contract); err != nil {
		t.Fatalf("decode contract: %v", err)
	}
	if id, _ := contract["id"].(string); id == "" {
		t.Errorf("expected a composed contract id, got: %s", rec.Body.String())
	}
}

func TestRelayControl_Placement(t *testing.T) {
	h, _ := newRelayControlServer(t)
	rec := doJSON(t, h, http.MethodPost, "/v1/relay/placements", map[string]any{
		"RunID":    "run-1",
		"TenantID": "t1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var record map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &record); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if record["run_id"] != "run-1" {
		t.Errorf("run_id = %v, want run-1", record["run_id"])
	}
}

func TestRelayControl_PolicyCheck(t *testing.T) {
	h, _ := newRelayControlServer(t)
	rec := doJSON(t, h, http.MethodPost, "/v1/relay/policy/check", map[string]any{
		"pack":           map[string]any{},
		"policy_context": map[string]any{"TenantID": "t1"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode policy result: %v", err)
	}
	if _, ok := result["allowed"]; !ok {
		t.Errorf("expected an 'allowed' field in policy result: %s", rec.Body.String())
	}
}

func TestRelayControl_OperatorWorkers(t *testing.T) {
	h, _ := newRelayControlServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/relay/operator/workers?tenant_id=t1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Workers []map[string]any `json:"workers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Workers) != 1 {
		t.Errorf("expected 1 operator worker summary, got %d: %s", len(resp.Workers), rec.Body.String())
	}
}

func TestRelayControl_CapabilityCRUD(t *testing.T) {
	h, _ := newRelayControlServer(t)
	const path = "/v1/relay/capabilities/w1"

	// PUT an inventory.
	put := doJSON(t, h, http.MethodPut, path, map[string]any{
		"tools": []map[string]any{{"name": "bash"}},
	})
	if put.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200; body=%s", put.Code, put.Body.String())
	}

	// GET it back (sanitized).
	getReq := httptest.NewRequest(http.MethodGet, path, nil)
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body=%s", getRec.Code, getRec.Body.String())
	}
	var inv map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &inv); err != nil {
		t.Fatalf("decode inventory: %v", err)
	}
	if inv["worker_id"] != "w1" {
		t.Errorf("worker_id = %v, want w1", inv["worker_id"])
	}

	// DELETE it.
	delReq := httptest.NewRequest(http.MethodDelete, path, nil)
	delRec := httptest.NewRecorder()
	h.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200; body=%s", delRec.Code, delRec.Body.String())
	}
}

func TestRelayControl_CapabilityUnknownWorker404(t *testing.T) {
	h, _ := newRelayControlServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/relay/capabilities/does-not-exist", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
