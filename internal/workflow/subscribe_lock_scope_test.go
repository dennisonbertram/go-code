package workflow_test

// Follow-up review (second round) on fix/workflow-engine-concurrency found
// a NEW, REPRODUCIBLE performance regression introduced by the BUG 5 fix
// (commit a4e2b34): Subscribe() calls store.GetEvents(ctx, runID, -1) --
// an O(history) copy of the run's ENTIRE event history -- WHILE HOLDING
// e.mu, the engine's single global lock. Since emit() also needs e.mu for
// every event (any run, not just this one), a Subscribe against a run
// with a large or growing history blocks ALL emits engine-wide for the
// duration of the copy. Repeated Subscribes against a run whose history
// keeps growing (e.g. TestEmitCancelRaceDoesNotPanic's 2000 iterations
// against a continuously-emitting run) is O(n^2) overall, and manifested
// as `go test ./internal/workflow/... -race -count=5` timing out (it
// completed in ~56s before this regression).
//
// The fix: Subscribe now holds e.mu only long enough to (a) register the
// subscriber channel and (b) record the current sequence number, then
// releases the lock and does the O(history) copy OUTSIDE it, trimming the
// result to events at-or-before the recorded sequence number. Events
// after that point arrive live on the channel (already registered before
// the lock was released) instead. See TestSubscribeNeverMissesConcurrentEmit
// in concurrency_bugs_internal_test.go for the invariant this must not
// reintroduce a gap in.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"go-agent-harness/internal/workflow"
)

// slowHistoryStore is a minimal workflow.Store whose GetEvents call
// sleeps before returning, simulating a large/slow history copy. Used to
// prove Subscribe does NOT hold e.mu for the duration of that copy: if it
// did, a concurrent, otherwise-trivial engine call (List) would also be
// blocked for the same duration.
type slowHistoryStore struct {
	mu             sync.Mutex
	runs           map[string]*workflow.Run
	events         map[string][]workflow.Event
	getEventsDelay time.Duration
}

func newSlowHistoryStore(delay time.Duration) *slowHistoryStore {
	return &slowHistoryStore{
		runs:           make(map[string]*workflow.Run),
		events:         make(map[string][]workflow.Event),
		getEventsDelay: delay,
	}
}

func (s *slowHistoryStore) CreateRun(_ context.Context, run *workflow.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *run
	s.runs[run.ID] = &cp
	return nil
}

func (s *slowHistoryStore) UpdateRun(_ context.Context, run *workflow.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *run
	s.runs[run.ID] = &cp
	return nil
}

func (s *slowHistoryStore) GetRun(_ context.Context, id string) (*workflow.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[id]
	if !ok {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (s *slowHistoryStore) AppendEvent(_ context.Context, ev *workflow.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[ev.RunID] = append(s.events[ev.RunID], *ev)
	return nil
}

func (s *slowHistoryStore) GetEvents(_ context.Context, runID string, afterSeq int64) ([]workflow.Event, error) {
	// Simulate an expensive/slow history copy -- e.g. a large in-memory
	// history, or a real persistence backend.
	if s.getEventsDelay > 0 {
		time.Sleep(s.getEventsDelay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]workflow.Event, 0, len(s.events[runID]))
	for _, ev := range s.events[runID] {
		if ev.Seq > afterSeq {
			out = append(out, ev)
		}
	}
	return out, nil
}

// TestSubscribeDoesNotHoldEngineLockDuringHistoryCopy asserts that a slow
// Subscribe (its store.GetEvents call takes 300ms) does not block an
// unrelated, otherwise-instant engine call (List) for anywhere close to
// that duration. If Subscribe still holds e.mu across the store read,
// List -- which also needs e.mu -- would be forced to wait for the full
// 300ms too.
func TestSubscribeDoesNotHoldEngineLockDuringHistoryCopy(t *testing.T) {
	mgr := newMockMgr()
	st := newSlowHistoryStore(300 * time.Millisecond)
	eng := workflow.NewEngine(workflow.EngineOptions{Subagents: mgr, Store: st})
	eng.Register("noop", func(*workflow.Context) (any, error) { return "ok", nil })

	run, err := eng.Start(context.Background(), "noop", nil)
	require.NoError(t, err)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	subDone := make(chan struct{})
	go func() {
		defer close(subDone)
		_, _, cancel, subErr := eng.Subscribe(run.ID)
		require.NoError(t, subErr)
		cancel()
	}()

	// Give the Subscribe goroutine a moment to start (and, if buggy,
	// acquire and hold e.mu across its slow store call).
	time.Sleep(20 * time.Millisecond)

	start := time.Now()
	eng.List()
	elapsed := time.Since(start)

	require.Less(t, elapsed, 150*time.Millisecond,
		"List() took %v while a concurrent Subscribe's 300ms history copy was in flight — "+
			"Subscribe appears to still be holding the engine lock across the store read", elapsed)

	<-subDone
}
