package relay_test

import (
	"context"
	"testing"
	"time"

	"go-agent-harness/internal/relay"
)

// workerStoreFactory creates a fresh WorkerStore for testing.
type workerStoreFactory func(t *testing.T) relay.WorkerStore

// runWorkerStoreContractTests runs the full contract test suite.
func runWorkerStoreContractTests(t *testing.T, factory workerStoreFactory) {
	t.Helper()

	t.Run("RegisterAndGetWorker", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		now := time.Now().UTC().Truncate(time.Second)

		w := &relay.Worker{
			ID:                      "worker-1",
			TenantID:                "tenant-1",
			Name:                    "Test Worker",
			LocationType:            relay.LocationLocal,
			Status:                  relay.WorkerStatusOnline,
			TrustTier:               relay.TrustTierStandard,
			Load:                    0,
			Labels:                  map[string]string{"env": "test"},
			SupportedWorkspaceModes: []string{"local", "worktree"},
			LastHeartbeat:           now,
			CreatedAt:               now,
			UpdatedAt:               now,
		}

		if err := s.RegisterWorker(ctx, w); err != nil {
			t.Fatalf("RegisterWorker: %v", err)
		}

		got, err := s.GetWorker(ctx, "worker-1")
		if err != nil {
			t.Fatalf("GetWorker: %v", err)
		}
		if got.ID != w.ID {
			t.Errorf("ID: got %q, want %q", got.ID, w.ID)
		}
		if got.TenantID != w.TenantID {
			t.Errorf("TenantID: got %q, want %q", got.TenantID, w.TenantID)
		}
		if got.Name != w.Name {
			t.Errorf("Name: got %q, want %q", got.Name, w.Name)
		}
		if got.LocationType != w.LocationType {
			t.Errorf("LocationType: got %q, want %q", got.LocationType, w.LocationType)
		}
		if got.Status != w.Status {
			t.Errorf("Status: got %q, want %q", got.Status, w.Status)
		}
		if got.TrustTier != w.TrustTier {
			t.Errorf("TrustTier: got %q, want %q", got.TrustTier, w.TrustTier)
		}
		if got.Labels["env"] != "test" {
			t.Errorf("Labels: got %v, want env=test", got.Labels)
		}
		if len(got.SupportedWorkspaceModes) != 2 {
			t.Errorf("SupportedWorkspaceModes: got %d items, want 2", len(got.SupportedWorkspaceModes))
		}
	})

	t.Run("RegisterDuplicateFails", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		now := time.Now().UTC()

		w := &relay.Worker{
			ID:            "worker-dup",
			TenantID:      "tenant-1",
			Name:          "Dup",
			LocationType:  relay.LocationLocal,
			Status:        relay.WorkerStatusOnline,
			TrustTier:     relay.TrustTierStandard,
			LastHeartbeat: now,
			CreatedAt:     now,
			UpdatedAt:     now,
		}

		if err := s.RegisterWorker(ctx, w); err != nil {
			t.Fatalf("first RegisterWorker: %v", err)
		}
		if err := s.RegisterWorker(ctx, w); err != relay.ErrWorkerAlreadyExists {
			t.Errorf("duplicate RegisterWorker: got %v, want ErrWorkerAlreadyExists", err)
		}
	})

	t.Run("RegisterEmptyIDFails", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		w := &relay.Worker{
			ID:        "",
			Name:      "bad",
			TenantID:  "t1",
			Status:    relay.WorkerStatusOnline,
			TrustTier: relay.TrustTierStandard,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := s.RegisterWorker(ctx, w); err != relay.ErrInvalidWorkerID {
			t.Errorf("RegisterWorker with empty ID: got %v, want ErrInvalidWorkerID", err)
		}
	})

	t.Run("UpdateWorker", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		now := time.Now().UTC().Truncate(time.Second)

		w := &relay.Worker{
			ID:            "worker-update",
			TenantID:      "tenant-1",
			Name:          "Original",
			LocationType:  relay.LocationLocal,
			Status:        relay.WorkerStatusOnline,
			TrustTier:     relay.TrustTierStandard,
			LastHeartbeat: now,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if err := s.RegisterWorker(ctx, w); err != nil {
			t.Fatalf("RegisterWorker: %v", err)
		}

		w.Name = "Updated"
		w.Status = relay.WorkerStatusDraining
		w.TrustTier = relay.TrustTierPrivileged
		w.Load = 5
		w.Labels = map[string]string{"updated": "true"}
		w.UpdatedAt = time.Now()

		if err := s.UpdateWorker(ctx, w); err != nil {
			t.Fatalf("UpdateWorker: %v", err)
		}

		got, err := s.GetWorker(ctx, "worker-update")
		if err != nil {
			t.Fatalf("GetWorker after update: %v", err)
		}
		if got.Name != "Updated" {
			t.Errorf("Name: got %q, want %q", got.Name, "Updated")
		}
		if got.Status != relay.WorkerStatusDraining {
			t.Errorf("Status: got %q, want %q", got.Status, relay.WorkerStatusDraining)
		}
		if got.TrustTier != relay.TrustTierPrivileged {
			t.Errorf("TrustTier: got %q, want %q", got.TrustTier, relay.TrustTierPrivileged)
		}
		if got.Load != 5 {
			t.Errorf("Load: got %d, want 5", got.Load)
		}
		if got.Labels["updated"] != "true" {
			t.Errorf("Labels: got %v, want updated=true", got.Labels)
		}
	})

	t.Run("UpdateNonExistentFails", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		w := &relay.Worker{
			ID:   "nonexistent",
			Name: "ghost",
		}
		if err := s.UpdateWorker(ctx, w); err != relay.ErrWorkerNotFound {
			t.Errorf("UpdateWorker nonexistent: got %v, want ErrWorkerNotFound", err)
		}
	})

	t.Run("GetNonExistentFails", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		_, err := s.GetWorker(ctx, "nonexistent")
		if err != relay.ErrWorkerNotFound {
			t.Errorf("GetWorker nonexistent: got %v, want ErrWorkerNotFound", err)
		}
	})

	t.Run("ListWorkers", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		now := time.Now().UTC()

		// Register workers in different tenants.
		for i, w := range []*relay.Worker{
			{ID: "w-1", TenantID: "tenant-a", Name: "A1", LocationType: relay.LocationLocal, Status: relay.WorkerStatusOnline, TrustTier: relay.TrustTierStandard, LastHeartbeat: now, CreatedAt: now, UpdatedAt: now},
			{ID: "w-2", TenantID: "tenant-a", Name: "A2", LocationType: relay.LocationContainer, Status: relay.WorkerStatusOnline, TrustTier: relay.TrustTierPrivileged, LastHeartbeat: now, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)},
			{ID: "w-3", TenantID: "tenant-b", Name: "B1", LocationType: relay.LocationLocal, Status: relay.WorkerStatusOffline, TrustTier: relay.TrustTierUntrusted, LastHeartbeat: now, CreatedAt: now, UpdatedAt: now},
		} {
			_ = i
			if err := s.RegisterWorker(ctx, w); err != nil {
				t.Fatalf("RegisterWorker %s: %v", w.ID, err)
			}
		}

		// List all (excludes stale by default).
		all, err := s.ListWorkers(ctx, relay.WorkerFilter{})
		if err != nil {
			t.Fatalf("ListWorkers all: %v", err)
		}
		if len(all) != 3 {
			t.Errorf("ListWorkers all: got %d, want 3", len(all))
		}

		// Filter by tenant.
		a, err := s.ListWorkers(ctx, relay.WorkerFilter{TenantID: "tenant-a"})
		if err != nil {
			t.Fatalf("ListWorkers tenant-a: %v", err)
		}
		if len(a) != 2 {
			t.Errorf("ListWorkers tenant-a: got %d, want 2", len(a))
		}
		for _, w := range a {
			if w.TenantID != "tenant-a" {
				t.Errorf("cross-tenant leak: worker %s has tenant %q", w.ID, w.TenantID)
			}
		}

		// Filter by status.
		offline, err := s.ListWorkers(ctx, relay.WorkerFilter{Status: relay.WorkerStatusOffline})
		if err != nil {
			t.Fatalf("ListWorkers offline: %v", err)
		}
		if len(offline) != 1 {
			t.Errorf("ListWorkers offline: got %d, want 1", len(offline))
		}

		// Filter by location type.
		local, err := s.ListWorkers(ctx, relay.WorkerFilter{LocationType: relay.LocationLocal})
		if err != nil {
			t.Fatalf("ListWorkers local: %v", err)
		}
		if len(local) != 2 {
			t.Errorf("ListWorkers local: got %d, want 2", len(local))
		}
	})

	t.Run("ListWorkersExcludesStale", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		now := time.Now().UTC()

		// Register an online and a stale worker.
		w1 := &relay.Worker{
			ID: "w-online", TenantID: "t1", Name: "Online",
			LocationType: relay.LocationLocal, Status: relay.WorkerStatusOnline,
			TrustTier:     relay.TrustTierStandard,
			LastHeartbeat: now, CreatedAt: now, UpdatedAt: now,
		}
		w2 := &relay.Worker{
			ID: "w-stale", TenantID: "t1", Name: "Stale",
			LocationType: relay.LocationLocal, Status: relay.WorkerStatusStale,
			TrustTier:     relay.TrustTierStandard,
			LastHeartbeat: now.Add(-time.Hour), CreatedAt: now, UpdatedAt: now,
		}

		if err := s.RegisterWorker(ctx, w1); err != nil {
			t.Fatalf("RegisterWorker w1: %v", err)
		}
		if err := s.RegisterWorker(ctx, w2); err != nil {
			t.Fatalf("RegisterWorker w2: %v", err)
		}

		// Default list should exclude stale.
		workers, err := s.ListWorkers(ctx, relay.WorkerFilter{TenantID: "t1"})
		if err != nil {
			t.Fatalf("ListWorkers: %v", err)
		}
		if len(workers) != 1 {
			t.Errorf("ListWorkers: got %d, want 1 (stale excluded)", len(workers))
		}
		if len(workers) > 0 && workers[0].ID != "w-online" {
			t.Errorf("expected w-online, got %s", workers[0].ID)
		}

		// Explicitly requesting stale should include them.
		staleList, err := s.ListWorkers(ctx, relay.WorkerFilter{Status: relay.WorkerStatusStale})
		if err != nil {
			t.Fatalf("ListWorkers stale: %v", err)
		}
		if len(staleList) != 1 {
			t.Errorf("ListWorkers stale: got %d, want 1", len(staleList))
		}
	})

	t.Run("DeleteWorker", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		now := time.Now().UTC()

		w := &relay.Worker{
			ID:            "worker-del",
			TenantID:      "t1",
			Name:          "ToDelete",
			LocationType:  relay.LocationLocal,
			Status:        relay.WorkerStatusOnline,
			TrustTier:     relay.TrustTierStandard,
			LastHeartbeat: now,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if err := s.RegisterWorker(ctx, w); err != nil {
			t.Fatalf("RegisterWorker: %v", err)
		}
		if err := s.DeleteWorker(ctx, "worker-del"); err != nil {
			t.Fatalf("DeleteWorker: %v", err)
		}
		_, err := s.GetWorker(ctx, "worker-del")
		if err != relay.ErrWorkerNotFound {
			t.Errorf("GetWorker after delete: got %v, want ErrWorkerNotFound", err)
		}
	})

	t.Run("DeleteNonExistentFails", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		if err := s.DeleteWorker(ctx, "nonexistent"); err != relay.ErrWorkerNotFound {
			t.Errorf("DeleteWorker nonexistent: got %v, want ErrWorkerNotFound", err)
		}
	})

	t.Run("RecordHeartbeat", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		now := time.Now().UTC()

		w := &relay.Worker{
			ID:            "worker-hb",
			TenantID:      "t1",
			Name:          "HeartbeatWorker",
			LocationType:  relay.LocationLocal,
			Status:        relay.WorkerStatusOnline,
			TrustTier:     relay.TrustTierStandard,
			LastHeartbeat: now,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if err := s.RegisterWorker(ctx, w); err != nil {
			t.Fatalf("RegisterWorker: %v", err)
		}

		hbTime := now.Add(10 * time.Second)
		hb := relay.Heartbeat{
			WorkerID:  "worker-hb",
			Timestamp: hbTime,
			Load:      3,
			Status:    relay.WorkerStatusOnline,
		}
		if err := s.RecordHeartbeat(ctx, hb); err != nil {
			t.Fatalf("RecordHeartbeat: %v", err)
		}

		got, err := s.GetWorker(ctx, "worker-hb")
		if err != nil {
			t.Fatalf("GetWorker after heartbeat: %v", err)
		}
		if got.Load != 3 {
			t.Errorf("Load: got %d, want 3", got.Load)
		}
		if got.Status != relay.WorkerStatusOnline {
			t.Errorf("Status: got %q, want online", got.Status)
		}
		if got.LastHeartbeat.Before(now) {
			t.Errorf("LastHeartbeat should be after registration time")
		}
	})

	t.Run("RecordHeartbeatNonExistentFails", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		hb := relay.Heartbeat{
			WorkerID:  "nonexistent",
			Timestamp: time.Now(),
			Status:    relay.WorkerStatusOnline,
		}
		if err := s.RecordHeartbeat(ctx, hb); err != relay.ErrWorkerNotFound {
			t.Errorf("RecordHeartbeat nonexistent: got %v, want ErrWorkerNotFound", err)
		}
	})

	t.Run("MarkStaleWorkers", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		now := time.Now().UTC()

		// Worker that heartbeated recently should stay online.
		w1 := &relay.Worker{
			ID: "w-fresh", TenantID: "t1", Name: "Fresh",
			LocationType: relay.LocationLocal, Status: relay.WorkerStatusOnline,
			TrustTier:     relay.TrustTierStandard,
			LastHeartbeat: now, CreatedAt: now, UpdatedAt: now,
		}
		// Worker that heartbeated long ago should become stale.
		w2 := &relay.Worker{
			ID: "w-old", TenantID: "t1", Name: "Old",
			LocationType: relay.LocationLocal, Status: relay.WorkerStatusOnline,
			TrustTier:     relay.TrustTierStandard,
			LastHeartbeat: now.Add(-2 * relay.StaleDuration), CreatedAt: now, UpdatedAt: now,
		}
		// Worker already offline should not be affected.
		w3 := &relay.Worker{
			ID: "w-offline", TenantID: "t1", Name: "Offline",
			LocationType: relay.LocationLocal, Status: relay.WorkerStatusOffline,
			TrustTier:     relay.TrustTierStandard,
			LastHeartbeat: now.Add(-2 * relay.StaleDuration), CreatedAt: now, UpdatedAt: now,
		}
		for _, w := range []*relay.Worker{w1, w2, w3} {
			if err := s.RegisterWorker(ctx, w); err != nil {
				t.Fatalf("RegisterWorker %s: %v", w.ID, err)
			}
		}

		count, err := s.MarkStaleWorkers(ctx)
		if err != nil {
			t.Fatalf("MarkStaleWorkers: %v", err)
		}
		if count != 1 {
			t.Errorf("MarkStaleWorkers count: got %d, want 1", count)
		}

		fresh, err := s.GetWorker(ctx, "w-fresh")
		if err != nil {
			t.Fatalf("GetWorker w-fresh: %v", err)
		}
		if fresh.Status != relay.WorkerStatusOnline {
			t.Errorf("fresh worker should be online, got %q", fresh.Status)
		}

		stale, err := s.GetWorker(ctx, "w-old")
		if err != nil {
			t.Fatalf("GetWorker w-old: %v", err)
		}
		if stale.Status != relay.WorkerStatusStale {
			t.Errorf("old worker should be stale, got %q", stale.Status)
		}

		offline, err := s.GetWorker(ctx, "w-offline")
		if err != nil {
			t.Fatalf("GetWorker w-offline: %v", err)
		}
		if offline.Status != relay.WorkerStatusOffline {
			t.Errorf("offline worker should remain offline, got %q", offline.Status)
		}
	})

	t.Run("CrossTenantIsolation", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		now := time.Now().UTC()

		w := &relay.Worker{
			ID:            "worker-iso",
			TenantID:      "tenant-private",
			Name:          "Private",
			LocationType:  relay.LocationLocal,
			Status:        relay.WorkerStatusOnline,
			TrustTier:     relay.TrustTierStandard,
			LastHeartbeat: now,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if err := s.RegisterWorker(ctx, w); err != nil {
			t.Fatalf("RegisterWorker: %v", err)
		}

		// Querying with a different tenant should not return this worker.
		others, err := s.ListWorkers(ctx, relay.WorkerFilter{TenantID: "tenant-other"})
		if err != nil {
			t.Fatalf("ListWorkers: %v", err)
		}
		for _, w := range others {
			if w.ID == "worker-iso" {
				t.Errorf("cross-tenant leak: worker-iso visible in tenant-other")
			}
		}
	})
}
