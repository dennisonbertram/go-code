package workflow

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStoreGetRunAndMaxConcurrency(t *testing.T) {
	t.Parallel()

	limit := maxConcurrency()
	if limit < 1 || limit > 16 {
		t.Fatalf("maxConcurrency = %d, want between 1 and 16", limit)
	}

	ctx := context.Background()
	store := newMemoryStore()
	run := &Run{
		ID:           "workflow-run-1",
		WorkflowName: "smoke",
		Status:       RunStatusRunning,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	got, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got == nil || got.ID != run.ID {
		t.Fatalf("got run %#v, want id %q", got, run.ID)
	}
	missing, err := store.GetRun(ctx, "missing")
	if err != nil {
		t.Fatalf("GetRun missing: %v", err)
	}
	if missing != nil {
		t.Fatalf("expected nil missing run, got %#v", missing)
	}
}
