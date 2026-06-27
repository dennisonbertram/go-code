package relay_test

import (
	"context"
	"path/filepath"
	"testing"

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
