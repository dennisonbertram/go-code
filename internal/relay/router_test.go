package relay_test

import (
	"context"
	"testing"
	"time"

	"go-agent-harness/internal/relay"
)

// setupTestRouter creates a router with pre-registered workers for testing.
func setupTestRouter(t *testing.T) (*relay.PlacementRouter, relay.WorkerStore) {
	t.Helper()
	store := newTestWorkerStore()
	now := time.Now().UTC()

	workers := []*relay.Worker{
		{
			ID: "w-local", TenantID: "t1", Name: "Local Laptop",
			LocationType: relay.LocationLocal, Status: relay.WorkerStatusOnline,
			TrustTier: relay.TrustTierStandard, Load: 0,
			SupportedWorkspaceModes: []string{"local", "worktree"},
			LastHeartbeat:           now, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "w-local-dirty", TenantID: "t1", Name: "Local Dirty",
			LocationType: relay.LocationLocal, Status: relay.WorkerStatusOnline,
			TrustTier: relay.TrustTierStandard, Load: 2,
			SupportedWorkspaceModes: []string{"local"},
			LastHeartbeat:           now, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "w-container", TenantID: "t1", Name: "Container Worker",
			LocationType: relay.LocationContainer, Status: relay.WorkerStatusOnline,
			TrustTier: relay.TrustTierStandard, Load: 0,
			SupportedWorkspaceModes: []string{"container"},
			LastHeartbeat:           now, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "w-vm", TenantID: "t1", Name: "Cloud VM",
			LocationType: relay.LocationVM, Status: relay.WorkerStatusOnline,
			TrustTier: relay.TrustTierPrivileged, Load: 0,
			SupportedWorkspaceModes: []string{"vm"},
			LastHeartbeat:           now, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "w-offline", TenantID: "t1", Name: "Offline Worker",
			LocationType: relay.LocationLocal, Status: relay.WorkerStatusOffline,
			TrustTier: relay.TrustTierStandard, Load: 0,
			LastHeartbeat: now.Add(-time.Hour), CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "w-stale", TenantID: "t1", Name: "Stale Worker",
			LocationType: relay.LocationLocal, Status: relay.WorkerStatusStale,
			TrustTier: relay.TrustTierStandard, Load: 0,
			LastHeartbeat: now.Add(-time.Hour), CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "w-untrusted", TenantID: "t1", Name: "Untrusted Sandbox",
			LocationType: relay.LocationSandbox, Status: relay.WorkerStatusOnline,
			TrustTier: relay.TrustTierUntrusted, Load: 0,
			SupportedWorkspaceModes: []string{"sandbox"},
			LastHeartbeat:           now, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "w-other-tenant", TenantID: "t2", Name: "Other Tenant Worker",
			LocationType: relay.LocationLocal, Status: relay.WorkerStatusOnline,
			TrustTier: relay.TrustTierStandard, Load: 0,
			LastHeartbeat: now, CreatedAt: now, UpdatedAt: now,
		},
	}

	for _, w := range workers {
		if err := store.RegisterWorker(context.Background(), w); err != nil {
			t.Fatalf("RegisterWorker %s: %v", w.ID, err)
		}
	}

	router := relay.NewPlacementRouter(store, nil)
	return router, store
}

func TestPlacementSelectsLocalByDefault(t *testing.T) {
	router, _ := setupTestRouter(t)

	req := relay.PlacementRequest{
		RunID:    "run-1",
		TenantID: "t1",
	}

	record, err := router.Place(context.Background(), req)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if record.SelectedWorker == "" {
		t.Fatal("expected a selected worker")
	}
	// w-local has highest score (local + low load).
	if record.SelectedWorker != "w-local" {
		t.Errorf("expected w-local, got %s", record.SelectedWorker)
	}
	if len(record.EligibleWorkers) < 2 {
		t.Errorf("expected at least 2 eligible workers, got %d", len(record.EligibleWorkers))
	}
}

func TestPlacementRejectsOfflineStaleWorkers(t *testing.T) {
	router, _ := setupTestRouter(t)

	req := relay.PlacementRequest{
		RunID:    "run-2",
		TenantID: "t1",
	}

	record, err := router.Place(context.Background(), req)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}

	// w-offline is explicitly offline — should be in rejected list.
	rejectedIDs := make(map[string]bool)
	for _, r := range record.RejectedWorkers {
		rejectedIDs[r.WorkerID] = true
	}
	if !rejectedIDs["w-offline"] {
		t.Errorf("expected w-offline to be rejected")
	}

	// w-stale is filtered at ListWorkers level — should NOT appear in eligible or rejected.
	for _, id := range record.EligibleWorkers {
		if id == "w-stale" {
			t.Errorf("w-stale should not be eligible (filtered by ListWorkers)")
		}
	}
	for _, r := range record.RejectedWorkers {
		if r.WorkerID == "w-stale" {
			t.Errorf("w-stale should not appear in rejections (filtered by ListWorkers)")
		}
	}

	// w-other-tenant is filtered by tenant — should not appear at all.
	for _, r := range record.RejectedWorkers {
		if r.WorkerID == "w-other-tenant" {
			t.Errorf("w-other-tenant should not appear (filtered by tenant)")
		}
	}
}

func TestPlacementTrustTierConstraint(t *testing.T) {
	router, _ := setupTestRouter(t)

	req := relay.PlacementRequest{
		RunID:            "run-3",
		TenantID:         "t1",
		MinimumTrustTier: relay.TrustTierPrivileged,
	}

	record, err := router.Place(context.Background(), req)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if record.SelectedWorker != "w-vm" {
		t.Errorf("expected w-vm (only privileged), got %s", record.SelectedWorker)
	}
}

func TestPlacementLocalOnly(t *testing.T) {
	router, _ := setupTestRouter(t)

	req := relay.PlacementRequest{
		RunID:     "run-4",
		TenantID:  "t1",
		LocalOnly: true,
	}

	record, err := router.Place(context.Background(), req)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if record.SelectedWorker == "" {
		t.Fatal("expected a selected worker")
	}
	for _, eligibleID := range record.EligibleWorkers {
		// All eligible workers should be local.
		if eligibleID != "w-local" && eligibleID != "w-local-dirty" {
			t.Errorf("non-local worker %s should not be eligible for local-only", eligibleID)
		}
	}
}

func TestPlacementAllowedLocationTypes(t *testing.T) {
	router, _ := setupTestRouter(t)

	req := relay.PlacementRequest{
		RunID:                "run-5",
		TenantID:             "t1",
		AllowedLocationTypes: []relay.LocationType{relay.LocationVM, relay.LocationSandbox},
	}

	record, err := router.Place(context.Background(), req)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	for _, eligibleID := range record.EligibleWorkers {
		if eligibleID != "w-vm" && eligibleID != "w-untrusted" {
			t.Errorf("worker %s should not be eligible (not in allowed location types)", eligibleID)
		}
	}
}

func TestPlacementPreferLocal(t *testing.T) {
	router, _ := setupTestRouter(t)

	req := relay.PlacementRequest{
		RunID:       "run-6",
		TenantID:    "t1",
		PreferLocal: true,
	}

	record, err := router.Place(context.Background(), req)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if record.SelectedWorker != "w-local" {
		t.Errorf("with PreferLocal, expected w-local, got %s", record.SelectedWorker)
	}
}

func TestPlacementPreferCleanWorkspace(t *testing.T) {
	router, _ := setupTestRouter(t)

	req := relay.PlacementRequest{
		RunID:                "run-7",
		TenantID:             "t1",
		PreferCleanWorkspace: true,
	}

	record, err := router.Place(context.Background(), req)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	// With clean workspace preference, a non-local worker should win.
	if record.SelectedWorker == "w-local" {
		t.Errorf("PreferCleanWorkspace should select non-local worker, got local")
	}
}

func TestPlacementPreferCloudForLongRunning(t *testing.T) {
	router, _ := setupTestRouter(t)

	req := relay.PlacementRequest{
		RunID:                     "run-8",
		TenantID:                  "t1",
		PreferCloudForLongRunning: true,
	}

	record, err := router.Place(context.Background(), req)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	// With cloud preference, cloud workers should score higher.
	if record.SelectedWorker != "w-vm" && record.SelectedWorker != "w-container" {
		t.Errorf("with cloud preference, expected cloud worker, got %s", record.SelectedWorker)
	}
}

func TestPlacementRequiredWorkspaceModes(t *testing.T) {
	router, _ := setupTestRouter(t)

	req := relay.PlacementRequest{
		RunID:                  "run-9",
		TenantID:               "t1",
		RequiredWorkspaceModes: []string{"vm"},
	}

	record, err := router.Place(context.Background(), req)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if record.SelectedWorker != "w-vm" {
		t.Errorf("only w-vm supports vm mode, got %s", record.SelectedWorker)
	}
}

func TestPlacementRequiresCapabilityInventory(t *testing.T) {
	store := newTestWorkerStore()
	caps := newTestCapabilityStore()
	now := time.Now().UTC()

	for _, w := range []*relay.Worker{
		{
			ID: "w-a-missing", TenantID: "t1", Name: "Missing Capabilities",
			LocationType: relay.LocationLocal, Status: relay.WorkerStatusOnline,
			TrustTier: relay.TrustTierStandard, Load: 0,
			SupportedWorkspaceModes: []string{"local"},
			LastHeartbeat:           now, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "w-z-capable", TenantID: "t1", Name: "Capable Worker",
			LocationType: relay.LocationLocal, Status: relay.WorkerStatusOnline,
			TrustTier: relay.TrustTierStandard, Load: 0,
			SupportedWorkspaceModes: []string{"local"},
			LastHeartbeat:           now, CreatedAt: now, UpdatedAt: now,
		},
	} {
		if err := store.RegisterWorker(context.Background(), w); err != nil {
			t.Fatalf("RegisterWorker %s: %v", w.ID, err)
		}
	}
	if err := caps.SetInventory(context.Background(), &relay.CapabilityInventory{
		WorkerID: "w-z-capable",
		Tools: []relay.ToolCapability{
			{Name: "slack"},
		},
		MCPServers: []relay.MCPServerCapability{
			{Name: "github"},
		},
		Memories: []relay.MemoryCapability{
			{Name: "repo-memory"},
		},
		Repos: []relay.RepoCapability{
			{RepoURL: "https://github.com/dennisonbertram/go-code.git"},
		},
		Secrets: []relay.SecretCapability{
			{Name: "github-token", Ref: "secret/github", Scope: "repo"},
		},
		OutputSurfaces: []relay.OutputSurfaceCapability{
			{Type: "slack:reply"},
		},
		Browser: &relay.BrowserCapability{Available: true, Driver: "playwright"},
		Docker:  &relay.DockerCapability{Available: true, Runtimes: []string{"docker"}},
	}); err != nil {
		t.Fatalf("SetInventory: %v", err)
	}

	router := relay.NewPlacementRouter(store, caps)
	req := relay.PlacementRequest{
		RunID:           "run-capabilities",
		TenantID:        "t1",
		RequiredRepoURL: "https://github.com/dennisonbertram/go-code.git",
		RequireBrowser:  true,
		RequireDocker:   true,
		RequiredCapabilities: relay.CapabilityPack{
			Tools: []relay.ToolCapability{
				{Name: "slack"},
			},
			MCPServers: []relay.MCPServerCapability{
				{Name: "github"},
			},
			Memories: []relay.MemoryCapability{
				{Name: "repo-memory"},
			},
			Secrets: []relay.SecretCapability{
				{Ref: "secret/github"},
			},
			OutputSurfaces: []relay.OutputSurfaceCapability{
				{Type: "slack:reply"},
			},
		},
	}

	record, err := router.Place(context.Background(), req)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if record.SelectedWorker != "w-z-capable" {
		t.Fatalf("expected capable worker, got %q", record.SelectedWorker)
	}
	if !hasRejection(record, "w-a-missing", "capability") {
		t.Fatalf("expected missing-capability worker to be rejected, got %#v", record.RejectedWorkers)
	}
}

func TestPlacementRejectsCapabilityRequirementsWithoutCapabilityStore(t *testing.T) {
	router, _ := setupTestRouter(t)

	record, err := router.Place(context.Background(), relay.PlacementRequest{
		RunID:    "run-no-cap-store",
		TenantID: "t1",
		RequiredCapabilities: relay.CapabilityPack{
			Tools: []relay.ToolCapability{{Name: "slack"}},
		},
	})
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if record.SelectedWorker != "" {
		t.Fatalf("expected no worker without capability store, got %q", record.SelectedWorker)
	}
	foundCapabilityRejection := false
	for _, r := range record.RejectedWorkers {
		if r.Category == "capability" {
			foundCapabilityRejection = true
			break
		}
	}
	if !foundCapabilityRejection {
		t.Fatalf("expected capability rejections, got %#v", record.RejectedWorkers)
	}
}

func TestPlacementNoEligibleWorkers(t *testing.T) {
	router, _ := setupTestRouter(t)

	req := relay.PlacementRequest{
		RunID:                  "run-10",
		TenantID:               "t1",
		MinimumTrustTier:       relay.TrustTierPrivileged,
		RequiredWorkspaceModes: []string{"local"},
	}

	record, err := router.Place(context.Background(), req)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if record.SelectedWorker != "" {
		t.Errorf("expected no selected worker (privileged + local impossible), got %s", record.SelectedWorker)
	}
}

func TestPlacementEmptyTenant(t *testing.T) {
	router, _ := setupTestRouter(t)

	req := relay.PlacementRequest{
		RunID:    "run-11",
		TenantID: "empty-tenant",
	}

	record, err := router.Place(context.Background(), req)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if record.SelectedWorker != "" {
		t.Errorf("expected no workers in empty tenant, got %s", record.SelectedWorker)
	}
}

func TestPlacementDeterminism(t *testing.T) {
	router, _ := setupTestRouter(t)

	req := relay.PlacementRequest{
		RunID:    "run-12",
		TenantID: "t1",
	}

	// Two placements with identical inputs should produce identical results.
	record1, err := router.Place(context.Background(), req)
	if err != nil {
		t.Fatalf("Place 1: %v", err)
	}
	record2, err := router.Place(context.Background(), req)
	if err != nil {
		t.Fatalf("Place 2: %v", err)
	}
	if record1.SelectedWorker != record2.SelectedWorker {
		t.Errorf("non-deterministic placement: %s vs %s", record1.SelectedWorker, record2.SelectedWorker)
	}
}

func TestPlacementExplanation(t *testing.T) {
	router, _ := setupTestRouter(t)

	req := relay.PlacementRequest{
		RunID:    "run-13",
		TenantID: "t1",
	}

	record, err := router.Place(context.Background(), req)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if record.RoutingReason == "" {
		t.Error("routing reason should not be empty")
	}
	if len(record.EligibleWorkers) == 0 {
		t.Error("should have eligible workers")
	}
	if record.Timestamp.IsZero() {
		t.Error("timestamp should be set")
	}
}

func TestPlacementDrainingWorkerPenalized(t *testing.T) {
	store := newTestWorkerStore()
	now := time.Now().UTC()

	// Register a draining worker and an online worker.
	w1 := &relay.Worker{
		ID: "w-draining", TenantID: "t1", Name: "Draining",
		LocationType: relay.LocationLocal, Status: relay.WorkerStatusDraining,
		TrustTier: relay.TrustTierStandard, Load: 0,
		LastHeartbeat: now, CreatedAt: now, UpdatedAt: now,
	}
	w2 := &relay.Worker{
		ID: "w-online", TenantID: "t1", Name: "Online",
		LocationType: relay.LocationLocal, Status: relay.WorkerStatusOnline,
		TrustTier: relay.TrustTierStandard, Load: 0,
		LastHeartbeat: now, CreatedAt: now, UpdatedAt: now,
	}
	store.RegisterWorker(context.Background(), w1)
	store.RegisterWorker(context.Background(), w2)

	router := relay.NewPlacementRouter(store, nil)

	req := relay.PlacementRequest{RunID: "run-14", TenantID: "t1"}
	record, err := router.Place(context.Background(), req)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	// Online worker should be preferred over draining.
	if record.SelectedWorker != "w-online" {
		t.Errorf("expected w-online over draining worker, got %s", record.SelectedWorker)
	}
}

func TestPlacementRejectsDrainingWorkersForNewRuns(t *testing.T) {
	store := newTestWorkerStore()
	now := time.Now().UTC()
	if err := store.RegisterWorker(context.Background(), &relay.Worker{
		ID: "w-draining-only", TenantID: "t1", Name: "Draining",
		LocationType: relay.LocationLocal, Status: relay.WorkerStatusDraining,
		TrustTier: relay.TrustTierStandard, Load: 0,
		SupportedWorkspaceModes: []string{"local"},
		LastHeartbeat:           now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("RegisterWorker: %v", err)
	}

	router := relay.NewPlacementRouter(store, nil)
	record, err := router.Place(context.Background(), relay.PlacementRequest{
		RunID:    "run-draining-rejected",
		TenantID: "t1",
	})
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if record.SelectedWorker != "" {
		t.Fatalf("expected no selected worker, got %q", record.SelectedWorker)
	}
	if !hasRejection(record, "w-draining-only", "offline") {
		t.Fatalf("expected draining worker rejection, got %#v", record.RejectedWorkers)
	}
}

// newTestWorkerStore is an in-memory worker store for router tests.
type testWorkerStore struct {
	workers map[string]*relay.Worker
}

func newTestWorkerStore() *testWorkerStore {
	return &testWorkerStore{workers: make(map[string]*relay.Worker)}
}

func (s *testWorkerStore) RegisterWorker(_ context.Context, w *relay.Worker) error {
	if w.ID == "" {
		return relay.ErrInvalidWorkerID
	}
	s.workers[w.ID] = w
	return nil
}

func (s *testWorkerStore) UpdateWorker(_ context.Context, w *relay.Worker) error {
	s.workers[w.ID] = w
	return nil
}

func (s *testWorkerStore) GetWorker(_ context.Context, id string) (*relay.Worker, error) {
	w, ok := s.workers[id]
	if !ok {
		return nil, relay.ErrWorkerNotFound
	}
	return w, nil
}

func (s *testWorkerStore) ListWorkers(_ context.Context, filter relay.WorkerFilter) ([]*relay.Worker, error) {
	var result []*relay.Worker
	for _, w := range s.workers {
		if filter.TenantID != "" && w.TenantID != filter.TenantID {
			continue
		}
		// Default: exclude stale unless specifically requested.
		if filter.Status == "" && w.Status == relay.WorkerStatusStale {
			continue
		}
		if filter.Status != "" && w.Status != filter.Status {
			continue
		}
		result = append(result, w)
	}
	return result, nil
}

func (s *testWorkerStore) DeleteWorker(_ context.Context, id string) error {
	delete(s.workers, id)
	return nil
}

func (s *testWorkerStore) RecordHeartbeat(_ context.Context, hb relay.Heartbeat) error {
	return nil
}

func (s *testWorkerStore) MarkStaleWorkers(_ context.Context) (int, error) {
	return 0, nil
}

func (s *testWorkerStore) Close() error { return nil }

func hasRejection(record *relay.PlacementRecord, workerID, category string) bool {
	for _, r := range record.RejectedWorkers {
		if r.WorkerID == workerID && r.Category == category {
			return true
		}
	}
	return false
}

type testCapabilityStore struct {
	inventories map[string]*relay.CapabilityInventory
	packs       map[string]*relay.CapabilityPack
}

func newTestCapabilityStore() *testCapabilityStore {
	return &testCapabilityStore{
		inventories: make(map[string]*relay.CapabilityInventory),
		packs:       make(map[string]*relay.CapabilityPack),
	}
}

func (s *testCapabilityStore) SetInventory(_ context.Context, inv *relay.CapabilityInventory) error {
	cp := *inv
	s.inventories[inv.WorkerID] = &cp
	return nil
}

func (s *testCapabilityStore) GetInventory(_ context.Context, workerID string) (*relay.CapabilityInventory, error) {
	inv, ok := s.inventories[workerID]
	if !ok {
		return nil, relay.ErrCapabilityNotFound
	}
	cp := *inv
	return &cp, nil
}

func (s *testCapabilityStore) DeleteInventory(_ context.Context, workerID string) error {
	delete(s.inventories, workerID)
	return nil
}

func (s *testCapabilityStore) SetPack(_ context.Context, pack *relay.CapabilityPack) error {
	cp := *pack
	s.packs[pack.RunID] = &cp
	return nil
}

func (s *testCapabilityStore) GetPack(_ context.Context, runID string) (*relay.CapabilityPack, error) {
	pack, ok := s.packs[runID]
	if !ok {
		return nil, relay.ErrCapabilityNotFound
	}
	cp := *pack
	return &cp, nil
}

func (s *testCapabilityStore) DeletePack(_ context.Context, runID string) error {
	delete(s.packs, runID)
	return nil
}

func (s *testCapabilityStore) Close() error { return nil }
