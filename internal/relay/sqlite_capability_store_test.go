package relay_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go-agent-harness/internal/relay"
)

func TestSQLiteCapabilityStore(t *testing.T) {
	runCapabilityStoreContractTests(t, func(t *testing.T) relay.CapabilityStore {
		t.Helper()
		path := filepath.Join(t.TempDir(), "relay_cap.db")
		ws, err := relay.NewSQLiteWorkerStore(path)
		if err != nil {
			t.Fatalf("NewSQLiteWorkerStore: %v", err)
		}
		t.Cleanup(func() { ws.Close() })

		if err := ws.Migrate(context.Background()); err != nil {
			t.Fatalf("Migrate worker: %v", err)
		}

		cs, err := relay.NewSQLiteCapabilityStore(ws.DB())
		if err != nil {
			t.Fatalf("NewSQLiteCapabilityStore: %v", err)
		}
		if err := cs.Migrate(context.Background()); err != nil {
			t.Fatalf("Migrate capability: %v", err)
		}
		return cs
	})
}

func TestSQLiteCapabilityStoreRejectsNilDB(t *testing.T) {
	_, err := relay.NewSQLiteCapabilityStore(nil)
	if err == nil {
		t.Fatal("expected error for nil database")
	}
}

func TestSQLiteCapabilityStoreClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relay_cap_close.db")
	ws, err := relay.NewSQLiteWorkerStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteWorkerStore: %v", err)
	}
	t.Cleanup(func() { ws.Close() })
	cs, err := relay.NewSQLiteCapabilityStore(ws.DB())
	if err != nil {
		t.Fatalf("NewSQLiteCapabilityStore: %v", err)
	}
	if err := cs.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSQLiteCapabilityInventoryRemovedWithWorker(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relay_cap_cascade.db")
	ws, err := relay.NewSQLiteWorkerStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteWorkerStore: %v", err)
	}
	t.Cleanup(func() { ws.Close() })
	if err := ws.Migrate(ctx); err != nil {
		t.Fatalf("Migrate worker: %v", err)
	}
	cs, err := relay.NewSQLiteCapabilityStore(ws.DB())
	if err != nil {
		t.Fatalf("NewSQLiteCapabilityStore: %v", err)
	}
	if err := cs.Migrate(ctx); err != nil {
		t.Fatalf("Migrate capability: %v", err)
	}

	now := time.Now().UTC()
	if err := ws.RegisterWorker(ctx, &relay.Worker{
		ID: "w-reused", TenantID: "t1", Name: "Original",
		LocationType: relay.LocationLocal, Status: relay.WorkerStatusOnline,
		TrustTier: relay.TrustTierStandard, LastHeartbeat: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("RegisterWorker original: %v", err)
	}
	if err := cs.SetInventory(ctx, &relay.CapabilityInventory{
		WorkerID: "w-reused",
		Tools:    []relay.ToolCapability{{Name: "dangerous-tool"}},
	}); err != nil {
		t.Fatalf("SetInventory: %v", err)
	}
	if err := ws.DeleteWorker(ctx, "w-reused"); err != nil {
		t.Fatalf("DeleteWorker: %v", err)
	}
	if err := ws.RegisterWorker(ctx, &relay.Worker{
		ID: "w-reused", TenantID: "t2", Name: "Replacement",
		LocationType: relay.LocationLocal, Status: relay.WorkerStatusOnline,
		TrustTier: relay.TrustTierStandard, LastHeartbeat: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("RegisterWorker replacement: %v", err)
	}

	if _, err := cs.GetInventory(ctx, "w-reused"); err != relay.ErrCapabilityNotFound {
		t.Fatalf("expected stale inventory removed, got %v", err)
	}
}
