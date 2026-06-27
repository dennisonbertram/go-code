package workflow

import (
	"context"
	"testing"
	"time"
)

func TestMaxConcurrencyWithinDocumentedBounds(t *testing.T) {
	t.Parallel()

	got := maxConcurrency()
	if got < 1 {
		t.Fatalf("maxConcurrency = %d, want at least 1", got)
	}
	if got > 16 {
		t.Fatalf("maxConcurrency = %d, want at most 16", got)
	}
}

func TestMemoryStoreGetRunReturnsCopyAndNilForMissing(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	if run, err := store.GetRun(context.Background(), "missing"); err != nil || run != nil {
		t.Fatalf("missing GetRun = (%#v, %v), want nil nil", run, err)
	}

	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	if err := store.CreateRun(context.Background(), &Run{
		ID:           "workflow-run",
		WorkflowName: "coverage",
		Status:       RunStatusRunning,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	run, err := store.GetRun(context.Background(), "workflow-run")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	run.Status = RunStatusFailed

	again, err := store.GetRun(context.Background(), "workflow-run")
	if err != nil {
		t.Fatalf("GetRun again: %v", err)
	}
	if again.Status != RunStatusRunning {
		t.Fatalf("stored status = %q, want running copy isolation", again.Status)
	}
}
