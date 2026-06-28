package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/relay"
	"go-agent-harness/internal/server"
	istore "go-agent-harness/internal/store"
)

// mockWorkerStore is an in-memory mock for relay.WorkerStore.
type mockWorkerStore struct {
	mu      sync.Mutex
	workers map[string]*relay.Worker
}

func newMockWorkerStore() *mockWorkerStore {
	return &mockWorkerStore{
		workers: make(map[string]*relay.Worker),
	}
}

func (m *mockWorkerStore) RegisterWorker(_ context.Context, w *relay.Worker) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w.ID == "" {
		return relay.ErrInvalidWorkerID
	}
	if _, ok := m.workers[w.ID]; ok {
		return relay.ErrWorkerAlreadyExists
	}
	cp := *w
	m.workers[w.ID] = &cp
	return nil
}

func (m *mockWorkerStore) UpdateWorker(_ context.Context, w *relay.Worker) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.workers[w.ID]; !ok {
		return relay.ErrWorkerNotFound
	}
	cp := *w
	m.workers[w.ID] = &cp
	return nil
}

func (m *mockWorkerStore) GetWorker(_ context.Context, id string) (*relay.Worker, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.workers[id]
	if !ok {
		return nil, relay.ErrWorkerNotFound
	}
	cp := *w
	return &cp, nil
}

func (m *mockWorkerStore) ListWorkers(_ context.Context, filter relay.WorkerFilter) ([]*relay.Worker, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*relay.Worker
	for _, w := range m.workers {
		if filter.TenantID != "" && w.TenantID != filter.TenantID {
			continue
		}
		if filter.Status != "" && w.Status != filter.Status {
			continue
		}
		if filter.Status == "" && w.Status == relay.WorkerStatusStale {
			continue
		}
		if filter.LocationType != "" && w.LocationType != filter.LocationType {
			continue
		}
		if filter.TrustTier != "" && w.TrustTier != filter.TrustTier {
			continue
		}
		cp := *w
		result = append(result, &cp)
	}
	if result == nil {
		result = []*relay.Worker{}
	}
	return result, nil
}

func (m *mockWorkerStore) DeleteWorker(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.workers[id]; !ok {
		return relay.ErrWorkerNotFound
	}
	delete(m.workers, id)
	return nil
}

func (m *mockWorkerStore) RecordHeartbeat(_ context.Context, hb relay.Heartbeat) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.workers[hb.WorkerID]
	if !ok {
		return relay.ErrWorkerNotFound
	}
	w.LastHeartbeat = hb.Timestamp
	w.Load = hb.Load
	w.Status = hb.Status
	w.UpdatedAt = time.Now()
	return nil
}

func (m *mockWorkerStore) MarkStaleWorkers(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	staleBefore := time.Now().Add(-relay.StaleDuration)
	count := 0
	for _, w := range m.workers {
		if (w.Status == relay.WorkerStatusOnline || w.Status == relay.WorkerStatusDraining) &&
			w.LastHeartbeat.Before(staleBefore) {
			w.Status = relay.WorkerStatusStale
			w.UpdatedAt = time.Now()
			count++
		}
	}
	return count, nil
}

func (m *mockWorkerStore) Close() error { return nil }

func newRelayServer(store relay.WorkerStore) http.Handler {
	return server.NewWithOptions(server.ServerOptions{
		AuthDisabled:     true,
		RelayWorkerStore: store,
	})
}

func newAuthenticatedRelayServer(t *testing.T, workerStore relay.WorkerStore) (http.Handler, map[string]string) {
	t.Helper()

	runStore := istore.NewMemoryStore()
	tokens := make(map[string]string)
	for _, tc := range []struct {
		name     string
		tenantID string
		scopes   []string
	}{
		{"t1_read", "t1", []string{istore.ScopeRunsRead}},
		{"t1_write", "t1", []string{istore.ScopeRunsWrite}},
		{"t2_read", "t2", []string{istore.ScopeRunsRead}},
		{"t2_write", "t2", []string{istore.ScopeRunsWrite}},
	} {
		raw, key := generateFastAPIKey(t, tc.tenantID, tc.name, tc.scopes)
		if err := runStore.CreateAPIKey(context.Background(), key); err != nil {
			t.Fatalf("CreateAPIKey(%s): %v", tc.name, err)
		}
		tokens[tc.name] = raw
	}

	return server.NewWithOptions(server.ServerOptions{
		Store:            runStore,
		RelayWorkerStore: workerStore,
	}), tokens
}

func addBearer(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
}

func TestRelayRegisterWorker(t *testing.T) {
	store := newMockWorkerStore()
	h := newRelayServer(store)

	body := `{"id":"w-1","name":"Test Worker","tenant_id":"t1","location_type":"local","trust_tier":"standard","labels":{"env":"test"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/relay/workers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp relay.Worker
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID != "w-1" {
		t.Errorf("ID: got %q, want w-1", resp.ID)
	}
	if resp.Status != relay.WorkerStatusOnline {
		t.Errorf("Status: got %q, want online", resp.Status)
	}
}

func TestRelayRegisterWorkerDuplicate(t *testing.T) {
	store := newMockWorkerStore()
	h := newRelayServer(store)

	body := `{"id":"w-dup","name":"Dup","tenant_id":"t1","location_type":"local"}`

	req1 := httptest.NewRequest(http.MethodPost, "/v1/relay/workers", strings.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, req1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first register: expected 201, got %d", w1.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/v1/relay/workers", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusConflict {
		t.Errorf("duplicate register: expected 409, got %d: %s", w2.Code, w2.Body.String())
	}
}

func TestRelayRegisterWorkerInvalidLocationType(t *testing.T) {
	store := newMockWorkerStore()
	h := newRelayServer(store)

	body := `{"id":"w-bad","name":"Bad","tenant_id":"t1","location_type":"cloud"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/relay/workers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid location type, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRelayRegisterWorkerMissingID(t *testing.T) {
	store := newMockWorkerStore()
	h := newRelayServer(store)

	body := `{"name":"NoID","tenant_id":"t1","location_type":"local"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/relay/workers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing id, got %d", w.Code)
	}
}

func TestRelayListWorkers(t *testing.T) {
	store := newMockWorkerStore()
	h := newRelayServer(store)

	// Pre-register some workers.
	now := time.Now()
	for _, w := range []*relay.Worker{
		{ID: "w-1", TenantID: "t1", Name: "W1", LocationType: relay.LocationLocal, Status: relay.WorkerStatusOnline, TrustTier: relay.TrustTierStandard, LastHeartbeat: now, CreatedAt: now, UpdatedAt: now},
		{ID: "w-2", TenantID: "t1", Name: "W2", LocationType: relay.LocationContainer, Status: relay.WorkerStatusOnline, TrustTier: relay.TrustTierPrivileged, LastHeartbeat: now, CreatedAt: now, UpdatedAt: now},
		{ID: "w-3", TenantID: "t2", Name: "W3", LocationType: relay.LocationLocal, Status: relay.WorkerStatusOffline, TrustTier: relay.TrustTierUntrusted, LastHeartbeat: now, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.RegisterWorker(context.Background(), w); err != nil {
			t.Fatalf("RegisterWorker %s: %v", w.ID, err)
		}
	}

	t.Run("list all", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/relay/workers", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var resp struct {
			Workers []*relay.Worker `json:"workers"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Workers) != 3 {
			t.Errorf("expected 3 workers, got %d", len(resp.Workers))
		}
	})

	t.Run("filter by tenant", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/relay/workers?tenant_id=t1", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		var resp struct {
			Workers []*relay.Worker `json:"workers"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Workers) != 2 {
			t.Errorf("tenant t1: expected 2, got %d", len(resp.Workers))
		}
		for _, rw := range resp.Workers {
			if rw.TenantID != "t1" {
				t.Errorf("cross-tenant leak: worker %s has tenant %q", rw.ID, rw.TenantID)
			}
		}
	})
}

func TestRelayWorkersUseAuthenticatedTenant(t *testing.T) {
	workerStore := newMockWorkerStore()
	h, tokens := newAuthenticatedRelayServer(t, workerStore)

	now := time.Now()
	for _, w := range []*relay.Worker{
		{ID: "w-t1", TenantID: "t1", Name: "Tenant 1", LocationType: relay.LocationLocal, Status: relay.WorkerStatusOnline, TrustTier: relay.TrustTierStandard, LastHeartbeat: now, CreatedAt: now, UpdatedAt: now},
		{ID: "w-t2", TenantID: "t2", Name: "Tenant 2", LocationType: relay.LocationLocal, Status: relay.WorkerStatusOnline, TrustTier: relay.TrustTierStandard, LastHeartbeat: now, CreatedAt: now, UpdatedAt: now},
	} {
		if err := workerStore.RegisterWorker(context.Background(), w); err != nil {
			t.Fatalf("RegisterWorker %s: %v", w.ID, err)
		}
	}

	t.Run("list defaults to auth tenant", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/relay/workers", nil)
		addBearer(req, tokens["t1_read"])
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp struct {
			Workers []*relay.Worker `json:"workers"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Workers) != 1 || resp.Workers[0].TenantID != "t1" {
			t.Fatalf("expected only tenant t1 workers, got %#v", resp.Workers)
		}
	})

	t.Run("list rejects mismatched tenant query", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/relay/workers?tenant_id=t2", nil)
		addBearer(req, tokens["t1_read"])
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("get hides other tenant worker", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/relay/workers/w-t2", nil)
		addBearer(req, tokens["t1_read"])
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("register assigns auth tenant by default", func(t *testing.T) {
		body := `{"id":"w-new","name":"New Worker","location_type":"local"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/relay/workers", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		addBearer(req, tokens["t1_write"])
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
		}

		got, err := workerStore.GetWorker(context.Background(), "w-new")
		if err != nil {
			t.Fatalf("GetWorker: %v", err)
		}
		if got.TenantID != "t1" {
			t.Fatalf("tenant: got %q, want t1", got.TenantID)
		}
	})

	t.Run("register rejects mismatched tenant body", func(t *testing.T) {
		body := `{"id":"w-bad-tenant","name":"Bad Tenant","tenant_id":"t2","location_type":"local"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/relay/workers", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		addBearer(req, tokens["t1_write"])
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("update hides other tenant worker", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/v1/relay/workers/w-t2", strings.NewReader(`{"name":"cross"}`))
		req.Header.Set("Content-Type", "application/json")
		addBearer(req, tokens["t1_write"])
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("delete hides other tenant worker", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/v1/relay/workers/w-t2", nil)
		addBearer(req, tokens["t1_write"])
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
		}
		if _, err := workerStore.GetWorker(context.Background(), "w-t2"); err != nil {
			t.Fatalf("other tenant worker should remain: %v", err)
		}
	})

	t.Run("heartbeat hides other tenant worker", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/relay/workers/w-t2/heartbeat", strings.NewReader(`{"load":1,"status":"online"}`))
		req.Header.Set("Content-Type", "application/json")
		addBearer(req, tokens["t1_write"])
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}

func TestRelayGetWorker(t *testing.T) {
	store := newMockWorkerStore()
	h := newRelayServer(store)

	now := time.Now()
	w := &relay.Worker{
		ID:            "w-get",
		TenantID:      "t1",
		Name:          "GetMe",
		LocationType:  relay.LocationLocal,
		Status:        relay.WorkerStatusOnline,
		TrustTier:     relay.TrustTierStandard,
		LastHeartbeat: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.RegisterWorker(context.Background(), w); err != nil {
		t.Fatalf("RegisterWorker: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/relay/workers/w-get", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp relay.Worker
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != "w-get" {
		t.Errorf("ID: got %q, want w-get", resp.ID)
	}
	if resp.Name != "GetMe" {
		t.Errorf("Name: got %q, want GetMe", resp.Name)
	}
}

func TestRelayGetWorkerNotFound(t *testing.T) {
	store := newMockWorkerStore()
	h := newRelayServer(store)

	req := httptest.NewRequest(http.MethodGet, "/v1/relay/workers/nonexistent", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestRelayWorkerHeartbeat(t *testing.T) {
	store := newMockWorkerStore()
	h := newRelayServer(store)

	now := time.Now()
	w := &relay.Worker{
		ID:            "w-hb",
		TenantID:      "t1",
		Name:          "HBWorker",
		LocationType:  relay.LocationLocal,
		Status:        relay.WorkerStatusOnline,
		TrustTier:     relay.TrustTierStandard,
		Load:          0,
		LastHeartbeat: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.RegisterWorker(context.Background(), w); err != nil {
		t.Fatalf("RegisterWorker: %v", err)
	}

	body := `{"load":3,"status":"online"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/relay/workers/w-hb/heartbeat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify the worker was updated.
	got, err := store.GetWorker(context.Background(), "w-hb")
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if got.Load != 3 {
		t.Errorf("Load: got %d, want 3", got.Load)
	}
}

func TestRelayWorkerHeartbeatNotFound(t *testing.T) {
	store := newMockWorkerStore()
	h := newRelayServer(store)

	body := `{"load":0,"status":"online"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/relay/workers/nonexistent/heartbeat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestRelayUpdateWorker(t *testing.T) {
	store := newMockWorkerStore()
	h := newRelayServer(store)

	now := time.Now()
	w := &relay.Worker{
		ID:            "w-update",
		TenantID:      "t1",
		Name:          "Original",
		LocationType:  relay.LocationLocal,
		Status:        relay.WorkerStatusOnline,
		TrustTier:     relay.TrustTierStandard,
		LastHeartbeat: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.RegisterWorker(context.Background(), w); err != nil {
		t.Fatalf("RegisterWorker: %v", err)
	}

	body := `{"name":"Updated","status":"draining","trust_tier":"privileged"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/relay/workers/w-update", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp relay.Worker
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Name != "Updated" {
		t.Errorf("Name: got %q, want Updated", resp.Name)
	}
	if resp.Status != relay.WorkerStatusDraining {
		t.Errorf("Status: got %q, want draining", resp.Status)
	}
	if resp.TrustTier != relay.TrustTierPrivileged {
		t.Errorf("TrustTier: got %q, want privileged", resp.TrustTier)
	}
}

func TestRelayDeleteWorker(t *testing.T) {
	store := newMockWorkerStore()
	h := newRelayServer(store)

	now := time.Now()
	w := &relay.Worker{
		ID:            "w-del",
		TenantID:      "t1",
		Name:          "DeleteMe",
		LocationType:  relay.LocationLocal,
		Status:        relay.WorkerStatusOnline,
		TrustTier:     relay.TrustTierStandard,
		LastHeartbeat: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.RegisterWorker(context.Background(), w); err != nil {
		t.Fatalf("RegisterWorker: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/v1/relay/workers/w-del", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	_, err := store.GetWorker(context.Background(), "w-del")
	if err != relay.ErrWorkerNotFound {
		t.Errorf("worker should be deleted, got err: %v", err)
	}
}

func TestRelayWorkersNotConfigured(t *testing.T) {
	h := server.NewWithOptions(server.ServerOptions{AuthDisabled: true})

	req := httptest.NewRequest(http.MethodGet, "/v1/relay/workers", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Errorf("expected 501 when store not configured, got %d", w.Code)
	}
}

func TestRelayWorkerMethodsNotAllowed(t *testing.T) {
	store := newMockWorkerStore()
	h := newRelayServer(store)

	// POST to a specific worker by ID (without /heartbeat suffix) is not allowed.
	req := httptest.NewRequest(http.MethodPost, "/v1/relay/workers/w-1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for POST to specific worker, got %d", w.Code)
	}
}

func TestRelayWorkerHeartbeatInvalidStatus(t *testing.T) {
	store := newMockWorkerStore()
	h := newRelayServer(store)

	now := time.Now()
	w := &relay.Worker{
		ID:            "w-bad-hb",
		TenantID:      "t1",
		Name:          "BadHB",
		LocationType:  relay.LocationLocal,
		Status:        relay.WorkerStatusOnline,
		TrustTier:     relay.TrustTierStandard,
		LastHeartbeat: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.RegisterWorker(context.Background(), w); err != nil {
		t.Fatalf("RegisterWorker: %v", err)
	}

	body := `{"load":0,"status":"stale"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/relay/workers/w-bad-hb/heartbeat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid heartbeat status, got %d", rec.Code)
	}
}

func TestRelayRegisterWorkerDefaults(t *testing.T) {
	store := newMockWorkerStore()
	h := newRelayServer(store)

	// Omit location_type and trust_tier — should get defaults.
	body := `{"id":"w-defaults","name":"Defaults"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/relay/workers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp relay.Worker
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.LocationType != relay.LocationLocal {
		t.Errorf("default LocationType: got %q, want local", resp.LocationType)
	}
	if resp.TrustTier != relay.TrustTierStandard {
		t.Errorf("default TrustTier: got %q, want standard", resp.TrustTier)
	}
	if resp.TenantID != "default" {
		t.Errorf("default TenantID: got %q, want default", resp.TenantID)
	}
	if resp.Status != relay.WorkerStatusOnline {
		t.Errorf("initial Status: got %q, want online", resp.Status)
	}
}

func TestRelayListWorkersQuery(t *testing.T) {
	store := newMockWorkerStore()
	h := newRelayServer(store)

	now := time.Now()
	for _, w := range []*relay.Worker{
		{ID: "q-1", TenantID: "qa", Name: "Q1", LocationType: relay.LocationLocal, Status: relay.WorkerStatusOnline, TrustTier: relay.TrustTierStandard, LastHeartbeat: now, CreatedAt: now, UpdatedAt: now},
		{ID: "q-2", TenantID: "qa", Name: "Q2", LocationType: relay.LocationVM, Status: relay.WorkerStatusOnline, TrustTier: relay.TrustTierPrivileged, LastHeartbeat: now, CreatedAt: now, UpdatedAt: now},
		{ID: "q-3", TenantID: "qb", Name: "Q3", LocationType: relay.LocationLocal, Status: relay.WorkerStatusOnline, TrustTier: relay.TrustTierStandard, LastHeartbeat: now, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.RegisterWorker(context.Background(), w); err != nil {
			t.Fatalf("RegisterWorker %s: %v", w.ID, err)
		}
	}

	t.Run("filter by location_type", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/relay/workers?location_type=vm", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		var resp struct {
			Workers []*relay.Worker `json:"workers"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Workers) != 1 {
			t.Errorf("vm filter: expected 1, got %d", len(resp.Workers))
		}
	})

	t.Run("filter by trust_tier", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/relay/workers?trust_tier=privileged", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		var resp struct {
			Workers []*relay.Worker `json:"workers"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Workers) != 1 {
			t.Errorf("privileged filter: expected 1, got %d", len(resp.Workers))
		}
		if resp.Workers[0].ID != "q-2" {
			t.Errorf("expected q-2, got %s", resp.Workers[0].ID)
		}
	})

	t.Run("combined filter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/relay/workers?tenant_id=qa&trust_tier=privileged", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		var resp struct {
			Workers []*relay.Worker `json:"workers"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Workers) != 1 {
			t.Errorf("combined filter: expected 1, got %d", len(resp.Workers))
		}
	})
}

func TestRelayListWorkersEmpty(t *testing.T) {
	store := newMockWorkerStore()
	h := newRelayServer(store)

	req := httptest.NewRequest(http.MethodGet, "/v1/relay/workers", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Workers []*relay.Worker `json:"workers"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Workers) != 0 {
		t.Errorf("expected 0 workers, got %d", len(resp.Workers))
	}
}

func TestRelayRegisterWorkerInvalidJSON(t *testing.T) {
	store := newMockWorkerStore()
	h := newRelayServer(store)

	body := `not-json`
	req := httptest.NewRequest(http.MethodPost, "/v1/relay/workers", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", w.Code)
	}
}
