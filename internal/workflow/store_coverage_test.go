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

// TestMemoryStorePerRunEventLockIsIndependentPerRun pins the structural
// property behind the fix for the follow-up-review performance
// regression: memoryStore.AppendEvent/GetEvents used to share ONE
// RWMutex across every run's events, so a slow GetEvents (O(history),
// unbounded for a long-running/high-frequency run) for run A would force
// AppendEvent for a completely unrelated run B to wait too --
// sync.RWMutex.Lock() must wait for all current readers regardless of
// which run they're reading. In production this meant one long-running
// workflow's Subscribe traffic could stall event delivery for every
// other concurrently-running workflow.
//
// This is deliberately a structural assertion, not a timing test (timing
// tests on shared/loaded CI hardware are exactly what caused the
// regression this fix addresses to go unnoticed -- see the
// fix/workflow-engine-concurrency branch history). Two different runs
// must get two DIFFERENT *runEvents (and therefore two independent
// locks, making cross-run blocking structurally impossible); the same
// run must always get back the SAME *runEvents instance (otherwise the
// per-run lock would be meaningless -- a fresh lock every call protects
// nothing).
func TestMemoryStorePerRunEventLockIsIndependentPerRun(t *testing.T) {
	m := newMemoryStore()

	reA := m.runEventsFor("run-a")
	reB := m.runEventsFor("run-b")
	if reA == reB {
		t.Fatal("expected different runs to get independent *runEvents (and therefore independent locks); got the same instance for run-a and run-b")
	}

	reAAgain := m.runEventsFor("run-a")
	if reA != reAAgain {
		t.Fatal("expected repeated runEventsFor calls for the same run to return the same *runEvents instance (a fresh lock every call would protect nothing)")
	}
}

// TestMemoryStoreGetEventsReleasesLockBeforeCopy is REQUIRED 2's
// deterministic proof: third-round review found that
// TestMemoryStorePerRunEventLockIsIndependentPerRun above, while true,
// does not actually establish that GetEvents releases its per-run lock
// before the O(history) copy -- it passed even when GetEvents held the
// RLock for the whole call (the actual bug: emit() calls AppendEvent
// while holding e.mu, so a concurrent AppendEvent for the SAME run
// blocked behind that RLock transitively held e.mu for every other run
// too).
//
// Using memoryStoreGetEventsPostSnapshotHook (a test-only seam, nil/
// no-op in production, mirroring the same pattern used elsewhere in this
// package for subagentPollInterval and resumePreTransitionHook), this
// test holds GetEvents open AFTER its snapshot step and asserts a
// concurrent AppendEvent for the SAME run completes near-instantly --
// which is only possible if the per-run RLock was already released
// before the hook fired.
func TestMemoryStoreGetEventsReleasesLockBeforeCopy(t *testing.T) {
	m := newMemoryStore()
	ctx := context.Background()
	const runID = "gated-run"

	if err := m.AppendEvent(ctx, &Event{RunID: runID, Seq: 1}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	memoryStoreGetEventsPostSnapshotHook = func() {
		close(entered)
		<-release
	}
	t.Cleanup(func() { memoryStoreGetEventsPostSnapshotHook = nil })

	getEventsDone := make(chan struct{})
	go func() {
		defer close(getEventsDone)
		if _, err := m.GetEvents(ctx, runID, -1); err != nil {
			t.Errorf("GetEvents: %v", err)
		}
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("GetEvents never reached the post-snapshot hook")
	}

	// The per-run RLock must already be released by this point (the hook
	// fires strictly after re.mu.RUnlock() in the fixed implementation).
	// A concurrent AppendEvent for the SAME run should therefore complete
	// near-instantly, not block for as long as the hook is held.
	appendDone := make(chan struct{})
	go func() {
		defer close(appendDone)
		if err := m.AppendEvent(ctx, &Event{RunID: runID, Seq: 2}); err != nil {
			t.Errorf("AppendEvent: %v", err)
		}
	}()

	select {
	case <-appendDone:
	case <-time.After(200 * time.Millisecond):
		close(release)
		t.Fatal("AppendEvent for the same run did not complete within 200ms while GetEvents was mid-copy — the per-run lock appears to still be held during the copy")
	}

	close(release)
	<-getEventsDone
}
