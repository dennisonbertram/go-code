package workflow

// Third-round follow-up review on fix/workflow-engine-concurrency found
// that the round-2 performance fix (Subscribe releasing e.mu before its
// O(history) store.GetEvents copy, and memoryStore moving to a per-run
// lock) left two real problems:
//
//   - REQUIRED 1: emit()'s fan-out send is non-blocking into a 64-slot
//     channel, and a Subscribe()-ing goroutine cannot drain that channel
//     until Subscribe() itself returns (it hasn't been given the channel
//     yet). So more than 64 events emitted DURING the now-unlocked
//     GetEvents copy overflow the channel send AND are excluded from the
//     history trim (their Seq > recordedSeq) -- reaching neither set.
//     Fixed by giving each initializing subscriber a `pending []Event`
//     buffer (see engine.go).
//
//   - REQUIRED 2: memoryStore.GetEvents held its per-run RLock for the
//     WHOLE O(history) copy. emit() on that SAME run takes e.mu and then
//     blocks on that per-run lock's Lock() behind the reader -- WHILE
//     HOLDING e.mu. Every other run's emit (and GetRun/List/Start/Resume)
//     then blocks on e.mu too, for the full duration of that one run's
//     history copy. Fixed by having GetEvents hold the per-run lock only
//     long enough to take an O(1) capped slice-header snapshot, then
//     copying outside any lock (see types.go).
//
// This file's tests need direct access to the unexported e.emit and
// e.subs to control exactly when events are emitted relative to a slow
// Subscribe, and so live in package workflow rather than workflow_test.

import (
	"context"
	"sync"
	"testing"
	"time"
)

// gatedRunEvents and gatedStore are a minimal, hand-rolled Store whose
// GetEvents can be held open on a test-controlled channel (never a
// wall-clock sleep) for exactly one targeted run, while still giving
// every run its own independent per-run lock -- mirroring memoryStore's
// real structure closely enough to reproduce (or fail to reproduce) the
// exact lock-contention mechanism under test, without depending on real
// hardware being slow enough to make a large copy measurably slow.
type gatedRunEvents struct {
	mu     sync.RWMutex
	events []Event
}

type gatedStore struct {
	mu     sync.Mutex // protects runs + perRun map structure only, never O(history) work
	runs   map[string]*Run
	perRun map[string]*gatedRunEvents

	blockRunID string        // GetEvents for this run blocks until release is closed
	entered    chan struct{} // closed the first time GetEvents(blockRunID) is called
	release    chan struct{} // GetEvents(blockRunID) blocks reading from this before proceeding
}

func newGatedStore(blockRunID string) *gatedStore {
	return &gatedStore{
		runs:       make(map[string]*Run),
		perRun:     make(map[string]*gatedRunEvents),
		blockRunID: blockRunID,
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
	}
}

func (s *gatedStore) runEventsFor(runID string) *gatedRunEvents {
	s.mu.Lock()
	defer s.mu.Unlock()
	re, ok := s.perRun[runID]
	if !ok {
		re = &gatedRunEvents{}
		s.perRun[runID] = re
	}
	return re
}

func (s *gatedStore) CreateRun(_ context.Context, run *Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *run
	s.runs[run.ID] = &cp
	return nil
}

func (s *gatedStore) UpdateRun(_ context.Context, run *Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *run
	s.runs[run.ID] = &cp
	return nil
}

func (s *gatedStore) GetRun(_ context.Context, id string) (*Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[id]
	if !ok {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (s *gatedStore) AppendEvent(_ context.Context, event *Event) error {
	re := s.runEventsFor(event.RunID)
	re.mu.Lock()
	defer re.mu.Unlock()
	re.events = append(re.events, *event)
	return nil
}

// GetEvents, for the targeted run, holds the per-run RLock across the
// ENTIRE call -- exactly what pre-REQUIRED-2 memoryStore.GetEvents did --
// so this store can prove whether the Engine (via emit()'s AppendEvent
// call, still made under e.mu) is still vulnerable to that specific
// mechanism, independent of memoryStore's own fix.
func (s *gatedStore) GetEvents(_ context.Context, runID string, afterSeq int64) ([]Event, error) {
	re := s.runEventsFor(runID)
	if runID == s.blockRunID {
		re.mu.RLock()
		defer re.mu.RUnlock()
		select {
		case <-s.entered:
		default:
			close(s.entered)
		}
		<-s.release
		out := make([]Event, 0, len(re.events))
		for _, ev := range re.events {
			if ev.Seq > afterSeq {
				out = append(out, ev)
			}
		}
		return out, nil
	}
	re.mu.RLock()
	defer re.mu.RUnlock()
	out := make([]Event, 0, len(re.events))
	for _, ev := range re.events {
		if ev.Seq > afterSeq {
			out = append(out, ev)
		}
	}
	return out, nil
}

// TestSlowSubscribeOnOneRunDoesNotBlockEmitOnAnotherRun reproduces
// REQUIRED 2 precisely: it holds run A's GetEvents open (via
// gatedStore, RLock included, matching the real pre-fix
// memoryStore.GetEvents), fires emit() on run A itself (which -- via
// AppendEvent needing run A's write lock, held under e.mu -- transitively
// blocks e.mu for as long as GetEvents(A) is open), and then asserts
// emit() on a completely unrelated run B still completes promptly. If
// e.mu is transitively stuck behind run A's blocked GetEvents, emit(B)
// can't even acquire e.mu and will not complete in time.
func TestSlowSubscribeOnOneRunDoesNotBlockEmitOnAnotherRun(t *testing.T) {
	const runA = "run-a"
	const runB = "run-b"
	st := newGatedStore(runA)
	e := NewEngine(EngineOptions{Subagents: noopSubagentManager{}, Store: st})

	subDone := make(chan struct{})
	go func() {
		defer close(subDone)
		_, _, cancel, err := e.Subscribe(runA)
		if err == nil {
			cancel()
		}
	}()

	select {
	case <-st.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe's GetEvents for run A never started")
	}

	// emit() on run A itself: needs e.mu, then blocks trying to
	// AppendEvent(A), which needs run A's write lock -- held read-locked
	// by the still-open GetEvents(A) above. Since emit() holds e.mu the
	// whole time it's blocked there, this is the exact mechanism under
	// test.
	emitADone := make(chan struct{})
	go func() {
		defer close(emitADone)
		e.emit(runA, EventWorkflowLog, map[string]any{"x": 1})
	}()

	// Give emit(A) a moment to actually reach (and block inside)
	// AppendEvent, so it is genuinely holding e.mu when run B is
	// measured below.
	time.Sleep(50 * time.Millisecond)

	emitBDone := make(chan struct{})
	go func() {
		defer close(emitBDone)
		e.emit(runB, EventWorkflowLog, map[string]any{"x": 2})
	}()

	select {
	case <-emitBDone:
	case <-time.After(1 * time.Second):
		close(st.release) // unblock everything so goroutines don't leak past this test
		t.Fatal("emit() on an unrelated run B did not complete within 1s while run A's GetEvents was still deliberately blocked — e.mu appears to be transitively held for the duration of run A's history copy")
	}

	close(st.release)
	<-subDone
	<-emitADone
}

// blockedGetEventsStore gates GetEvents on a test-controlled channel with
// NO lock held during the wait, so a concurrent AppendEvent for the same
// run is never blocked by it. This isolates testing REQUIRED 1 (the
// pending-buffer mechanism in Subscribe/emit) from REQUIRED 2 (the
// per-run store lock, tested separately above by
// TestSlowSubscribeOnOneRunDoesNotBlockEmitOnAnotherRun using gatedStore,
// which DOES hold the lock to match the real pre-fix memoryStore): with
// gatedStore, the very first emit() in the burst below would itself block
// on AppendEvent (since it contends with GetEvents' held lock), which
// tests REQUIRED 2, not REQUIRED 1. This store lets the burst of emits
// complete immediately so the test isolates the channel-overflow /
// pending-buffer behavior specifically.
type blockedGetEventsStore struct {
	mu     sync.Mutex
	runs   map[string]*Run
	events map[string][]Event

	entered chan struct{}
	release chan struct{}
}

func newBlockedGetEventsStore() *blockedGetEventsStore {
	return &blockedGetEventsStore{
		runs:    make(map[string]*Run),
		events:  make(map[string][]Event),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (s *blockedGetEventsStore) CreateRun(_ context.Context, run *Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *run
	s.runs[run.ID] = &cp
	return nil
}

func (s *blockedGetEventsStore) UpdateRun(_ context.Context, run *Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *run
	s.runs[run.ID] = &cp
	return nil
}

func (s *blockedGetEventsStore) GetRun(_ context.Context, id string) (*Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[id]
	if !ok {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (s *blockedGetEventsStore) AppendEvent(_ context.Context, event *Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[event.RunID] = append(s.events[event.RunID], *event)
	return nil
}

func (s *blockedGetEventsStore) GetEvents(_ context.Context, runID string, afterSeq int64) ([]Event, error) {
	select {
	case <-s.entered:
	default:
		close(s.entered)
	}
	<-s.release
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, 0, len(s.events[runID]))
	for _, ev := range s.events[runID] {
		if ev.Seq > afterSeq {
			out = append(out, ev)
		}
	}
	return out, nil
}

// TestSubscribeCapturesBurstDuringSlowHistoryCopyExactlyOnce reproduces
// REQUIRED 1: it holds a Subscribe's GetEvents open (via
// blockedGetEventsStore) and emits far more than the channel's 64-slot
// buffer during that window, then asserts every one of those events
// appears EXACTLY ONCE across (returned history + live channel) once
// Subscribe returns -- never zero times (lost), never twice (duplicated).
func TestSubscribeCapturesBurstDuringSlowHistoryCopyExactlyOnce(t *testing.T) {
	const runID = "burst-run"
	st := newBlockedGetEventsStore()
	e := NewEngine(EngineOptions{Subagents: noopSubagentManager{}, Store: st})

	type subResult struct {
		history []Event
		ch      <-chan Event
		cancel  func()
		err     error
	}
	resultCh := make(chan subResult, 1)
	go func() {
		h, ch, cancel, err := e.Subscribe(runID)
		resultCh <- subResult{h, ch, cancel, err}
	}()

	select {
	case <-st.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe's GetEvents never started")
	}

	const burst = 300 // far more than the channel's 64-slot buffer
	for i := 0; i < burst; i++ {
		e.emit(runID, EventWorkflowLog, map[string]any{"i": i})
	}

	close(st.release)

	var result subResult
	select {
	case result = <-resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe did not return after GetEvents was released")
	}
	if result.err != nil {
		t.Fatalf("Subscribe: %v", result.err)
	}
	defer result.cancel()

	seen := make(map[int64]int)
	for _, ev := range result.history {
		seen[ev.Seq]++
	}
	draining := true
	for draining {
		select {
		case ev, ok := <-result.ch:
			if !ok {
				draining = false
				break
			}
			seen[ev.Seq]++
		case <-time.After(200 * time.Millisecond):
			draining = false
		}
	}

	if len(seen) != burst {
		t.Fatalf("expected exactly %d distinct events captured across history+live, got %d distinct seqs (history=%d)", burst, len(seen), len(result.history))
	}
	for seq, count := range seen {
		if count != 1 {
			t.Fatalf("event seq %d observed %d times (want exactly 1) — duplicated across history+live", seq, count)
		}
	}
}
