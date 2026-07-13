package relay

import (
	"context"
	"path/filepath"
	"testing"
)

func TestNewControlPlane_WiresAllComponents(t *testing.T) {
	ctx := context.Background()
	ws, err := NewSQLiteWorkerStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatalf("worker store: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	if err := ws.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cp, err := NewControlPlane(ctx, ws)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	if cp.Capabilities == nil || cp.Events == nil || cp.Router == nil ||
		cp.Composer == nil || cp.Policy == nil || cp.Operator == nil {
		t.Fatalf("control plane has nil components: %+v", cp)
	}

	// The capability and event stores share the worker store's DB, so they are
	// usable immediately (their schemas were migrated by NewControlPlane).
	if _, err := cp.Capabilities.GetInventory(ctx, "nonexistent"); err == nil {
		t.Error("expected ErrCapabilityNotFound for unknown worker inventory")
	}
}

func TestNewControlPlane_NilWorkerStore(t *testing.T) {
	if _, err := NewControlPlane(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil worker store")
	}
}
