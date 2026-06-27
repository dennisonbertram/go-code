package workflow

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestMaxConcurrencyMatchesDefaultBounds(t *testing.T) {
	t.Parallel()

	got := maxConcurrency()
	want := runtime.NumCPU() - 2
	if want < 1 {
		want = 1
	}
	if want > 16 {
		want = 16
	}
	if got != want {
		t.Fatalf("maxConcurrency = %d, want %d", got, want)
	}
}

func TestMemoryStoreGetRunReturnsCopyAndNilForMissing(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	now := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	run := &Run{
		ID:           "workflow-run-1",
		WorkflowName: "demo",
		Status:       RunStatusRunning,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	loaded, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected run")
	}
	if loaded.ID != run.ID || loaded.Status != RunStatusRunning {
		t.Fatalf("loaded run = %+v", loaded)
	}

	loaded.Status = RunStatusCompleted
	loadedAgain, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun again: %v", err)
	}
	if loadedAgain.Status != RunStatusRunning {
		t.Fatalf("stored run was mutated through returned pointer: %+v", loadedAgain)
	}

	missing, err := store.GetRun(context.Background(), "missing")
	if err != nil {
		t.Fatalf("GetRun missing: %v", err)
	}
	if missing != nil {
		t.Fatalf("missing run = %+v, want nil", missing)
	}
}
