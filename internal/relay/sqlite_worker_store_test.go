package relay_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go-agent-harness/internal/relay"
)

func TestSQLiteWorkerStore(t *testing.T) {
	runWorkerStoreContractTests(t, func(t *testing.T) relay.WorkerStore {
		t.Helper()
		path := filepath.Join(t.TempDir(), "relay_test.db")
		store, err := relay.NewSQLiteWorkerStore(path)
		if err != nil {
			t.Fatalf("NewSQLiteWorkerStore: %v", err)
		}
		if err := store.Migrate(context.Background()); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		t.Cleanup(func() { store.Close() })
		return store
	})
}

func TestSQLiteWorkerStoreSharedDB(t *testing.T) {
	// Verify that DB() returns a non-nil database handle.
	path := filepath.Join(t.TempDir(), "relay_shared.db")
	store, err := relay.NewSQLiteWorkerStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteWorkerStore: %v", err)
	}
	defer store.Close()

	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	db := store.DB()
	if db == nil {
		t.Fatal("DB() returned nil")
	}

	// Verify we can query through the shared handle.
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM relay_workers").Scan(&count); err != nil {
		t.Fatalf("QueryRow: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 workers, got %d", count)
	}
}

func TestSQLiteWorkerStoreClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relay_close.db")
	store, err := relay.NewSQLiteWorkerStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteWorkerStore: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Double close should be safe.
	if err := store.Close(); err != nil {
		t.Errorf("Double Close: %v", err)
	}
}

func TestSQLiteWorkerStoreRejectsEmptyPath(t *testing.T) {
	_, err := relay.NewSQLiteWorkerStore("")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestSQLiteWorkerStoreHeartbeatDoesNotRegressTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relay_heartbeat.db")
	store, err := relay.NewSQLiteWorkerStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteWorkerStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	now := time.Now().UTC()
	worker := &relay.Worker{
		ID: "w-heartbeat", TenantID: "t1", Name: "Heartbeat",
		LocationType: relay.LocationLocal, Status: relay.WorkerStatusOnline,
		TrustTier: relay.TrustTierStandard, Load: 0,
		LastHeartbeat: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.RegisterWorker(context.Background(), worker); err != nil {
		t.Fatalf("RegisterWorker: %v", err)
	}

	newer := now.Add(time.Minute)
	if err := store.RecordHeartbeat(context.Background(), relay.Heartbeat{
		WorkerID:  "w-heartbeat",
		Timestamp: newer,
		Load:      2,
		Status:    relay.WorkerStatusOnline,
	}); err != nil {
		t.Fatalf("RecordHeartbeat newer: %v", err)
	}
	if err := store.RecordHeartbeat(context.Background(), relay.Heartbeat{
		WorkerID:  "w-heartbeat",
		Timestamp: now.Add(-time.Minute),
		Load:      0,
		Status:    relay.WorkerStatusDraining,
	}); err != nil {
		t.Fatalf("RecordHeartbeat older: %v", err)
	}

	got, err := store.GetWorker(context.Background(), "w-heartbeat")
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if !got.LastHeartbeat.Equal(newer) {
		t.Fatalf("LastHeartbeat regressed: got %s, want %s", got.LastHeartbeat, newer)
	}
	if got.Load != 2 || got.Status != relay.WorkerStatusOnline {
		t.Fatalf("older heartbeat changed state: load=%d status=%s", got.Load, got.Status)
	}
}
