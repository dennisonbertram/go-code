package relay_test

import (
	"context"
	"testing"
	"time"

	"go-agent-harness/internal/relay"
)

// Cloud worker tests.
func TestIsCloudWorker(t *testing.T) {
	if relay.IsCloudWorker(relay.LocationLocal) {
		t.Error("local should not be cloud")
	}
	if !relay.IsCloudWorker(relay.LocationVM) {
		t.Error("VM should be cloud")
	}
	if !relay.IsCloudWorker(relay.LocationContainer) {
		t.Error("container should be cloud")
	}
	if !relay.IsCloudWorker(relay.LocationSandbox) {
		t.Error("sandbox should be cloud")
	}
}

func TestIsLocalWorkerType(t *testing.T) {
	if !relay.IsLocalWorkerType(relay.LocationLocal) {
		t.Error("local should be local worker type")
	}
	if !relay.IsLocalWorkerType(relay.LocationWorktree) {
		t.Error("worktree should be local worker type")
	}
	if relay.IsLocalWorkerType(relay.LocationVM) {
		t.Error("VM should not be local worker type")
	}
}

func TestValidateCloudPlacement(t *testing.T) {
	err := relay.ValidateCloudPlacement(true, nil, relay.LocationVM)
	if err == nil {
		t.Error("local-only should prevent cloud placement")
	}

	err = relay.ValidateCloudPlacement(false, []relay.LocationType{relay.LocationVM}, relay.LocationVM)
	if err != nil {
		t.Errorf("matching location type should be allowed: %v", err)
	}

	err = relay.ValidateCloudPlacement(false, []relay.LocationType{relay.LocationLocal}, relay.LocationVM)
	if err == nil {
		t.Error("mismatched location type should be rejected")
	}
}

func TestWorkspaceModeForLocation(t *testing.T) {
	tests := map[relay.LocationType]string{
		relay.LocationLocal:     "local",
		relay.LocationWorktree:  "worktree",
		relay.LocationContainer: "container",
		relay.LocationVM:        "vm",
		relay.LocationSandbox:   "sandbox",
	}
	for lt, expected := range tests {
		if got := relay.WorkspaceModeForLocation(lt); got != expected {
			t.Errorf("WorkspaceModeForLocation(%q) = %q, want %q", lt, got, expected)
		}
	}
}

func TestCostRiskSummary(t *testing.T) {
	for _, lt := range []relay.LocationType{
		relay.LocationLocal, relay.LocationWorktree,
		relay.LocationContainer, relay.LocationVM, relay.LocationSandbox,
	} {
		cost, risk := relay.CostRiskSummary(lt)
		if cost == "unknown" || risk == "unknown" {
			t.Errorf("CostRiskSummary(%q): cost=%q risk=%q", lt, cost, risk)
		}
	}
}

func TestCloudWorkerPoolStoresRegisteredConfig(t *testing.T) {
	store := newTestWorkerStore()
	pool := relay.NewCloudWorkerPool(store)

	cfg := relay.CloudWorkerConfig{
		WorkerID:     "w-cloud",
		LocationType: relay.LocationVM,
		Provider:     "hetzner",
		Region:       "fsn1",
	}
	if err := pool.RegisterCloudWorker(cfg); err != nil {
		t.Fatalf("RegisterCloudWorker: %v", err)
	}

	got, ok := pool.Config("w-cloud")
	if !ok {
		t.Fatal("expected stored config")
	}
	if got.MaxConcurrentRuns != 5 {
		t.Fatalf("MaxConcurrentRuns: got %d, want 5", got.MaxConcurrentRuns)
	}
	if got.LocationType != relay.LocationVM {
		t.Fatalf("LocationType: got %q, want vm", got.LocationType)
	}
	if _, err := store.GetWorker(context.Background(), "w-cloud"); err != nil {
		t.Fatalf("expected worker registered in store: %v", err)
	}
}

func TestCloudWorkerPoolRejectsLocalLocation(t *testing.T) {
	pool := relay.NewCloudWorkerPool(newTestWorkerStore())
	err := pool.RegisterCloudWorker(relay.CloudWorkerConfig{
		WorkerID:     "w-local",
		LocationType: relay.LocationLocal,
		Provider:     "local",
	})
	if err == nil {
		t.Fatal("expected local cloud worker registration to fail")
	}
}

// Handoff tests.
func TestHandoffCanHandoff(t *testing.T) {
	hm := relay.NewHandoffManager(nil)

	if err := hm.CanHandoff(relay.MobilityPinned); err == nil {
		t.Error("pinned runs should not be movable")
	}
	if err := hm.CanHandoff(relay.MobilityEphemeral); err == nil {
		t.Error("ephemeral runs should not be movable")
	}
	if err := hm.CanHandoff(relay.MobilityResumable); err != nil {
		t.Errorf("resumable runs should be movable: %v", err)
	}
	if err := hm.CanHandoff(relay.MobilityCloneable); err != nil {
		t.Errorf("cloneable runs should be movable: %v", err)
	}
}

func TestHandoffCreatePackage(t *testing.T) {
	hm := relay.NewHandoffManager(nil)

	contract := &relay.RunContract{
		ID:     "rc-handoff",
		Prompt: "test handoff",
		Workspace: relay.WorkspaceTarget{
			Mode:    "container",
			RepoURL: "https://github.com/org/repo.git",
		},
		Mobility: relay.MobilityResumable,
		Metadata: relay.RunMetadata{
			TenantID:  "t1",
			CreatedAt: time.Now(),
		},
	}

	checkpoint := relay.HandoffCheckpoint{
		Boundary:         "after_llm_turn",
		TurnNumber:       5,
		LastToolCall:     "bash",
		PendingApprovals: []string{"git:push"},
	}

	pkg, err := hm.CreateHandoffPackage(contract, "w-1", checkpoint, "worker draining")
	if err != nil {
		t.Fatalf("CreateHandoffPackage: %v", err)
	}
	if pkg.Status != relay.HandoffPending {
		t.Errorf("Status: got %q, want pending", pkg.Status)
	}
	if pkg.SourceWorker != "w-1" {
		t.Errorf("SourceWorker: got %q, want w-1", pkg.SourceWorker)
	}
	if len(pkg.Lineage) != 1 {
		t.Errorf("Lineage: got %d entries, want 1", len(pkg.Lineage))
	}
}

func TestHandoffValidateTarget(t *testing.T) {
	store := newTestWorkerStore()
	now := time.Now().UTC()

	store.RegisterWorker(context.Background(), &relay.Worker{
		ID: "w-target", TenantID: "t1", Name: "Target",
		LocationType: relay.LocationContainer, Status: relay.WorkerStatusOnline,
		TrustTier:               relay.TrustTierStandard,
		SupportedWorkspaceModes: []string{"container"},
		LastHeartbeat:           now, CreatedAt: now, UpdatedAt: now,
	})

	hm := relay.NewHandoffManager(store)

	contract := &relay.RunContract{
		ID: "rc-h2", Prompt: "test",
		Workspace: relay.WorkspaceTarget{Mode: "container"},
		Mobility:  relay.MobilityResumable,
		Metadata:  relay.RunMetadata{CreatedAt: now},
	}

	pkg, _ := hm.CreateHandoffPackage(contract, "w-1", relay.HandoffCheckpoint{Boundary: "after_llm_turn"}, "test")

	// Valid target.
	err := hm.ValidateHandoffTarget(context.Background(), "w-target", pkg)
	if err != nil {
		t.Errorf("valid target should be accepted: %v", err)
	}

	// Same worker should be rejected.
	err = hm.ValidateHandoffTarget(context.Background(), "w-1", pkg)
	if err == nil {
		t.Error("same worker should be rejected")
	}
}

func TestHandoffValidateTargetRejectsCrossTenantWorker(t *testing.T) {
	store := newTestWorkerStore()
	now := time.Now().UTC()
	if err := store.RegisterWorker(context.Background(), &relay.Worker{
		ID: "w-other-tenant", TenantID: "t2", Name: "Other Tenant",
		LocationType: relay.LocationContainer, Status: relay.WorkerStatusOnline,
		TrustTier:               relay.TrustTierStandard,
		SupportedWorkspaceModes: []string{"container"},
		LastHeartbeat:           now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("RegisterWorker: %v", err)
	}

	hm := relay.NewHandoffManager(store)
	contract := &relay.RunContract{
		ID: "rc-cross-tenant", Prompt: "test",
		Workspace: relay.WorkspaceTarget{Mode: "container"},
		Mobility:  relay.MobilityResumable,
		Metadata:  relay.RunMetadata{TenantID: "t1", CreatedAt: now},
	}
	pkg, err := hm.CreateHandoffPackage(contract, "w-source", relay.HandoffCheckpoint{Boundary: "after_llm_turn"}, "test")
	if err != nil {
		t.Fatalf("CreateHandoffPackage: %v", err)
	}

	if err := hm.ValidateHandoffTarget(context.Background(), "w-other-tenant", pkg); err == nil {
		t.Fatal("expected cross-tenant handoff target to be rejected")
	}
}

func TestHandoffStatusTracking(t *testing.T) {
	hm := relay.NewHandoffManager(nil)
	contract := &relay.RunContract{
		ID: "rc-status", Prompt: "test",
		Mobility: relay.MobilityResumable,
		Metadata: relay.RunMetadata{CreatedAt: time.Now()},
	}

	// Initially no handoff.
	if status := hm.GetHandoffStatus("rc-status"); status != relay.HandoffNone {
		t.Errorf("initial status: got %q, want none", status)
	}

	pkg, _ := hm.CreateHandoffPackage(contract, "w-1", relay.HandoffCheckpoint{Boundary: "after_llm_turn"}, "test")
	if status := hm.GetHandoffStatus("rc-status"); status != relay.HandoffPending {
		t.Errorf("after create: got %q, want pending", status)
	}

	pkg.TargetWorker = "w-2"
	hm.CompleteHandoff("rc-status")
	if status := hm.GetHandoffStatus("rc-status"); status != relay.HandoffCompleted {
		t.Errorf("after complete: got %q, want completed", status)
	}

	if len(pkg.Lineage) != 2 {
		t.Errorf("lineage after complete: got %d, want 2", len(pkg.Lineage))
	}
}

func TestHandoffAssignTargetAndGetPackage(t *testing.T) {
	store := newTestWorkerStore()
	now := time.Now().UTC()
	if err := store.RegisterWorker(context.Background(), &relay.Worker{
		ID: "w-target", TenantID: "t1", Name: "Target",
		LocationType: relay.LocationContainer, Status: relay.WorkerStatusOnline,
		TrustTier:               relay.TrustTierStandard,
		SupportedWorkspaceModes: []string{"container"},
		LastHeartbeat:           now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("RegisterWorker: %v", err)
	}

	hm := relay.NewHandoffManager(store)
	contract := &relay.RunContract{
		ID: "rc-assign", Prompt: "test",
		Workspace: relay.WorkspaceTarget{Mode: "container"},
		Mobility:  relay.MobilityResumable,
		Metadata:  relay.RunMetadata{TenantID: "t1", CreatedAt: now},
	}
	if _, err := hm.CreateHandoffPackage(contract, "w-source", relay.HandoffCheckpoint{Boundary: "after_llm_turn"}, "test"); err != nil {
		t.Fatalf("CreateHandoffPackage: %v", err)
	}
	if err := hm.AssignTarget(context.Background(), "rc-assign", "w-target"); err != nil {
		t.Fatalf("AssignTarget: %v", err)
	}
	pkg, err := hm.GetHandoffPackage("rc-assign")
	if err != nil {
		t.Fatalf("GetHandoffPackage: %v", err)
	}
	if pkg.TargetWorker != "w-target" || pkg.Status != relay.HandoffInProgress {
		t.Fatalf("package not updated: %#v", pkg)
	}
	if _, err := hm.GetHandoffPackage("missing"); err == nil {
		t.Fatal("expected missing package error")
	}
}
