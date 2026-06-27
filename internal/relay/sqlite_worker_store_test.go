package relay_test

import (
	"context"
	"path/filepath"
	"testing"

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
