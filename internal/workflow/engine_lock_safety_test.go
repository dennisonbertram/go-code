package workflow_test

// This file covers a follow-up review round on fix/workflow-engine-concurrency
// that found the BUG1 defense-in-depth recover() (added in ff7c96c) traded a
// loud, restartable crash for a silent, permanent wedge of the whole Engine.
// See the individual tests below for the specific failure modes.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"go-agent-harness/internal/workflow"
)

// panickyMarshaler's MarshalJSON panics with a genuine Go runtime panic
// (assignment to entry in a nil map) rather than returning an error.
// encoding/json's top-level recover only converts its OWN internal
// sentinel error type back into a normal error; any other panic value
// (like this one) is re-panicked out of json.Marshal, exactly as it would
// be for any real user-supplied MarshalJSON that misbehaves.
type panickyMarshaler struct{}

func (panickyMarshaler) MarshalJSON() ([]byte, error) {
	var m map[string]int
	m["x"] = 1 // panics: assignment to entry in nil map
	return nil, nil
}

// TestExecuteScriptAsyncPanicDuringMarshalDoesNotWedgeEngine reproduces
// BLOCKING 1 + BLOCKING 2 from the follow-up review: executeScriptAsync
// used to hold e.mu across json.Marshal(result), which can invoke
// arbitrary user-supplied MarshalJSON code and panic. The BUG1
// defense-in-depth recover() at the top of execute() then swallowed that
// panic silently -- with e.mu still locked (manual Unlock(), no defer) and
// with no status transition, no persisted error, and no terminal event.
// Net effect: the run stays "running" forever, and every subsequent
// engine call that needs e.mu (Start, Resume, Subscribe, GetRun, List,
// emit) blocks forever too.
//
// Every blocking wait below is guarded by a short select/timeout so this
// test fails fast and cleanly instead of hanging the whole `go test`
// process if the engine is genuinely wedged.
func TestExecuteScriptAsyncPanicDuringMarshalDoesNotWedgeEngine(t *testing.T) {
	mgr := newMockMgr()
	eng := workflow.NewEngine(workflow.EngineOptions{Subagents: mgr})

	eng.Register("panicky-marshal", func(*workflow.Context) (any, error) {
		return panickyMarshaler{}, nil
	})

	run, err := eng.Start(context.Background(), "panicky-marshal", nil)
	require.NoError(t, err)

	// (i) The engine must NOT wedge: the run must reach a terminal status
	// promptly, and it must be Failed (a panic anywhere in the
	// execution/emit path is a failure, not a silent success).
	terminalStatus := make(chan workflow.RunStatus, 1)
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			r, gerr := eng.GetRun(run.ID)
			if gerr == nil && (r.Status == workflow.RunStatusCompleted || r.Status == workflow.RunStatusFailed) {
				terminalStatus <- r.Status
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		// Give up quietly; the select below has its own timeout and will
		// report the failure from the main test goroutine.
	}()

	select {
	case status := <-terminalStatus:
		require.Equal(t, workflow.RunStatusFailed, status,
			"a panic anywhere in the execution/emit path must leave the run Failed, not stuck non-terminal")
	case <-time.After(3 * time.Second):
		t.Fatal("engine appears wedged: run did not reach a terminal status within 3s of a panicking MarshalJSON")
	}

	// (i, continued) The engine's lock itself must not be leaked: a
	// totally unrelated call must also return promptly afterward.
	listDone := make(chan struct{})
	go func() {
		eng.List()
		close(listDone)
	}()
	select {
	case <-listDone:
	case <-time.After(2 * time.Second):
		t.Fatal("engine appears wedged: List() did not return within 2s after the panicking marshal")
	}

	startDone := make(chan struct{})
	eng.Register("noop", func(*workflow.Context) (any, error) { return "ok", nil })
	go func() {
		_, _ = eng.Start(context.Background(), "noop", nil)
		close(startDone)
	}()
	select {
	case <-startDone:
	case <-time.After(2 * time.Second):
		t.Fatal("engine appears wedged: Start() did not return within 2s after the panicking marshal")
	}

	// (ii) The run must be observably terminal: status Failed with a
	// descriptive error, AND a workflow.failed event actually emitted
	// (not just the status flipped silently).
	final, err := eng.GetRun(run.ID)
	require.NoError(t, err)
	require.Equal(t, workflow.RunStatusFailed, final.Status)
	require.Contains(t, final.Error, "panic", "expected the run's Error to mention the panic, got %q", final.Error)

	history, _, cancel, err := eng.Subscribe(run.ID)
	require.NoError(t, err)
	defer cancel()
	sawFailed := false
	for _, ev := range history {
		if ev.Type == workflow.EventWorkflowFailed {
			sawFailed = true
		}
	}
	require.True(t, sawFailed, "expected a workflow.failed terminal event in history, got %+v", history)
}

// raceStore is a minimal workflow.Store implementation whose UpdateRun
// call sleeps briefly before reading the *Run it was given. This widens
// (without needing thousands of iterations) the race window described in
// BLOCKING 3: executeScriptAsync used to release e.mu and then pass the
// SHARED *Run pointer (still reachable and mutable by a concurrent
// Resume, under e.mu) into store.UpdateRun, which reads it unsynchronized
// (memoryStore.UpdateRun does `cp := *run`). If executeScriptAsync passes
// a private copy taken under the lock instead, this is race-free
// regardless of how long UpdateRun takes to get around to reading it.
type raceStore struct {
	mu          sync.Mutex
	runs        map[string]*workflow.Run
	events      map[string][]workflow.Event
	updateDelay time.Duration
}

func newRaceStore(delay time.Duration) *raceStore {
	return &raceStore{
		runs:        make(map[string]*workflow.Run),
		events:      make(map[string][]workflow.Event),
		updateDelay: delay,
	}
}

func (s *raceStore) CreateRun(_ context.Context, run *workflow.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *run
	s.runs[run.ID] = &cp
	return nil
}

func (s *raceStore) UpdateRun(_ context.Context, run *workflow.Run) error {
	if s.updateDelay > 0 {
		time.Sleep(s.updateDelay)
	}
	// The unsynchronized read: if `run` is still a pointer some other
	// goroutine can concurrently mutate under e.mu, -race will catch it
	// here.
	cp := *run
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[run.ID] = &cp
	return nil
}

func (s *raceStore) GetRun(_ context.Context, id string) (*workflow.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[id]
	if !ok {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (s *raceStore) AppendEvent(_ context.Context, ev *workflow.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[ev.RunID] = append(s.events[ev.RunID], *ev)
	return nil
}

func (s *raceStore) GetEvents(_ context.Context, runID string, afterSeq int64) ([]workflow.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]workflow.Event, 0)
	for _, ev := range s.events[runID] {
		if ev.Seq > afterSeq {
			out = append(out, ev)
		}
	}
	return out, nil
}

// TestExecuteScriptAsyncNeverPassesSharedRunPointerToStore reproduces
// BLOCKING 3: it starts a run that fails immediately, then races a
// GetRun-poll-then-Resume loop against executeScriptAsync's own
// (artificially delayed) store.UpdateRun call for that same failure. Must
// be run with -race.
func TestExecuteScriptAsyncNeverPassesSharedRunPointerToStore(t *testing.T) {
	mgr := newMockMgr()
	st := newRaceStore(20 * time.Millisecond)
	eng := workflow.NewEngine(workflow.EngineOptions{Subagents: mgr, Store: st, MaxConcurrency: 4})

	eng.Register("flaky-race", func(*workflow.Context) (any, error) {
		return nil, fmt.Errorf("boom")
	})

	run, err := eng.Start(context.Background(), "flaky-race", nil)
	require.NoError(t, err)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r, gerr := eng.GetRun(run.ID)
		if gerr == nil && r.Status == workflow.RunStatusFailed {
			_, _ = eng.Resume(context.Background(), run.ID, nil)
			break
		}
		time.Sleep(time.Millisecond)
	}

	// Drain to a terminal status so the test doesn't leave a dangling
	// goroutine; the interesting assertion here is "did -race fire",
	// which the test runner reports independently of this poll.
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r, gerr := eng.GetRun(run.ID)
		if gerr == nil && (r.Status == workflow.RunStatusCompleted || r.Status == workflow.RunStatusFailed) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("run did not reach a terminal status in time")
}
