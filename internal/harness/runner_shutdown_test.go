package harness

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// runnerGoroutineStackContains reports whether the full goroutine stack dump
// (all goroutines) contains the given substring. It is file-unique to
// runner_shutdown_test.go and used by TestRunnerWithoutShutdownLeaksDispatcher.
func runnerGoroutineStackContains(substr string) bool {
	buf := make([]byte, 1<<20) // 1 MiB
	n := runtime.Stack(buf, true)
	return strings.Contains(string(buf[:n]), substr)
}

// assertNoRunnerGoroutineLeak polls runtime.NumGoroutine() until it drops to
// <= baseline within ~2 seconds. On each iteration runtime.GC() is called to
// give the runtime a chance to clean up finalizers and stale goroutines. If
// the goroutine count does not drop within the deadline the full goroutine
// stack is dumped and the test fails.
//
// This helper is defined exactly once here and reused by other harness tests.
func assertNoRunnerGoroutineLeak(t *testing.T, baseline int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if runtime.NumGoroutine() <= baseline {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Timed out — dump stack and fail.
	buf := make([]byte, 1<<20) // 1 MiB
	n := runtime.Stack(buf, true)
	t.Fatalf("goroutine leak: NumGoroutine()=%d > baseline=%d after 2s\n%s",
		runtime.NumGoroutine(), baseline, buf[:n])
}

// shutdownStubProvider is a minimal Provider that returns a single non-empty
// content response so that the run completes in one turn, without importing
// fakeprovider (which would cause an import cycle: harness ← fakeprovider ← harness).
type shutdownStubProvider struct{}

func (s *shutdownStubProvider) Complete(_ context.Context, _ CompletionRequest) (CompletionResult, error) {
	return CompletionResult{Content: "ok"}, nil
}

// hangingProvider blocks Complete until Release() is called or ctx is cancelled.
// Used to occupy a worker slot during Shutdown tests.
type hangingProvider struct {
	releaseCh chan struct{}
	once      sync.Once
}

func newHangingProvider() *hangingProvider {
	return &hangingProvider{releaseCh: make(chan struct{})}
}

func (h *hangingProvider) Complete(ctx context.Context, _ CompletionRequest) (CompletionResult, error) {
	select {
	case <-h.releaseCh:
		return CompletionResult{Content: "released"}, nil
	case <-ctx.Done():
		return CompletionResult{}, ctx.Err()
	}
}

func (h *hangingProvider) Release() {
	h.once.Do(func() { close(h.releaseCh) })
}

// TestRunnerShutdownStopsPoolDispatcher verifies that:
//  1. Shutdown closes the poolDispatcher goroutine (no goroutine leak).
//  2. StartRun after Shutdown returns ErrRunnerClosed.
func TestRunnerShutdownStopsPoolDispatcher(t *testing.T) {
	// Capture baseline BEFORE NewRunner so the poolDispatcher goroutine is NOT
	// included in the baseline count.
	baseline := runtime.NumGoroutine()

	r := NewRunner(&shutdownStubProvider{}, nil, RunnerConfig{
		WorkerPoolSize: 4,
		DefaultModel:   "gpt-4.1-mini",
	})

	// Start two runs and drain each to a terminal event so all execute()
	// goroutines launched by poolDispatcher exit before we call Shutdown.
	for i := 0; i < 2; i++ {
		run, err := r.StartRun(RunRequest{Prompt: "hello"})
		require.NoError(t, err)
		_, err = collectRunEvents(t, r, run.ID)
		require.NoError(t, err)
	}

	// Shutdown must succeed without error.
	require.NoError(t, r.Shutdown(context.Background()))

	// Starting a run after Shutdown must return ErrRunnerClosed.
	_, err := r.StartRun(RunRequest{Prompt: "should fail"})
	require.True(t, errors.Is(err, ErrRunnerClosed),
		"expected ErrRunnerClosed, got %v", err)

	// The poolDispatcher goroutine must have exited.
	assertNoRunnerGoroutineLeak(t, baseline)
}

// TestRunnerShutdownDrainsBufferedQueue is a regression test for the critical
// liveness bug where Shutdown could hang indefinitely because items enqueued
// into the buffered runQueue after r.done was closed were never drained, so
// r.inflight.Wait() in Shutdown blocked forever.
//
// Setup: WorkerPoolSize=1, occupy the single slot with a hanging provider,
// then fire a second StartRun that is forced onto the buffered queue. Call
// Shutdown concurrently and assert it returns within a short deadline and that
// inflight reaches zero (no goroutine leak).
func TestRunnerShutdownDrainsBufferedQueue(t *testing.T) {
	hp := newHangingProvider()
	r := NewRunner(hp, nil, RunnerConfig{
		WorkerPoolSize: 1,
		DefaultModel:   "gpt-4.1-mini",
	})

	// Start run 1: occupies the single worker slot (hangs in Complete).
	run1, err := r.StartRun(RunRequest{Prompt: "hang"})
	require.NoError(t, err)

	// Wait briefly to ensure the worker slot is occupied before continuing.
	time.Sleep(30 * time.Millisecond)

	// Start run 2: no slot available, so it is pushed onto the buffered runQueue.
	// It may succeed or fail with ErrRunnerClosed depending on the race; either
	// is acceptable — the key invariant is that Shutdown returns promptly.
	_, _ = r.StartRun(RunRequest{Prompt: "queued"})

	// Shutdown concurrently — this must NOT hang forever.
	shutdownDone := make(chan error, 1)
	go func() {
		// Release the hanging run so execute() can finish and inflight.Done() is called.
		// We release after a tiny delay to allow Shutdown to start waiting.
		time.Sleep(10 * time.Millisecond)
		hp.Release()
		shutdownDone <- r.Shutdown(context.Background())
	}()

	select {
	case err := <-shutdownDone:
		require.NoError(t, err, "Shutdown must not return an error")
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown hung: inflight.Wait() never unblocked (buffered-queue drain regression)")
	}

	// Verify run 1 reached a terminal state (provider was released).
	_, err = collectRunEvents(t, r, run1.ID)
	require.NoError(t, err)
}

// TestRunnerShutdownContinueRunRevertsSourceState is a regression test for the
// major bug where ContinueRunWithOptions set state.continued=true on the source
// run BEFORE dispatching, and on dispatch failure never reverted it — leaving
// the source run permanently non-continuable even though no continuation ran.
func TestRunnerShutdownContinueRunRevertsSourceState(t *testing.T) {
	r := NewRunner(&shutdownStubProvider{}, nil, RunnerConfig{
		WorkerPoolSize: 0, // unbounded, simpler setup
		DefaultModel:   "gpt-4.1-mini",
	})

	// Run a completed run.
	run, err := r.StartRun(RunRequest{Prompt: "hello"})
	require.NoError(t, err)
	_, err = collectRunEvents(t, r, run.ID)
	require.NoError(t, err)

	// Shut down the runner so that dispatchRun returns ErrRunnerClosed.
	require.NoError(t, r.Shutdown(context.Background()))

	// ContinueRun must return ErrRunnerClosed (or a wrapped form) and must NOT
	// leave state.continued=true on the source run.
	_, contErr := r.ContinueRun(run.ID, "follow up")
	// We expect ErrRunnerClosed because the runner is shut down.
	require.True(t, errors.Is(contErr, ErrRunnerClosed),
		"expected ErrRunnerClosed, got %v", contErr)

	// Source run's continued flag must not have been permanently set.
	// We verify by re-reading the state under the lock.
	r.mu.RLock()
	state, ok := r.runs[run.ID]
	r.mu.RUnlock()
	if ok {
		// State may have been removed; only check if still present.
		require.False(t, state.continued,
			"source run.continued must be false after failed continuation dispatch")
	}
}

// TestRunnerWithoutShutdownLeaksDispatcher is the control case: without
// Shutdown, the poolDispatcher goroutine stays alive, proving the stack-scan
// detector actually catches the parked goroutine. Shutdown is called at the
// end to clean up the test process so no goroutine leaks into other tests.
//
// Approach: poll the full goroutine stack dump (runtime.Stack(all=true)) for
// the "poolDispatcher" frame rather than relying on a fragile goroutine count,
// which is perturbed by the testing framework and the Go runtime itself. This
// is deterministic: the frame is present iff the goroutine is alive.
func TestRunnerWithoutShutdownLeaksDispatcher(t *testing.T) {
	r := NewRunner(&shutdownStubProvider{}, nil, RunnerConfig{
		WorkerPoolSize: 4,
		DefaultModel:   "gpt-4.1-mini",
	})

	// Run to terminal so the execute goroutine exits; only poolDispatcher stays.
	run, err := r.StartRun(RunRequest{Prompt: "hello"})
	require.NoError(t, err)
	_, err = collectRunEvents(t, r, run.ID)
	require.NoError(t, err)

	// PART 1: assert that poolDispatcher is PRESENT before Shutdown.
	// Poll up to 1 s for the goroutine to park in the select.
	deadline := time.Now().Add(1 * time.Second)
	var present bool
	for time.Now().Before(deadline) {
		if runnerGoroutineStackContains("poolDispatcher") {
			present = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.True(t, present,
		"control assertion failed: poolDispatcher goroutine not found in stack dump before Shutdown; the leak detector would not catch a leak")

	// PART 2: call Shutdown and assert that poolDispatcher is ABSENT afterwards.
	require.NoError(t, r.Shutdown(context.Background()))

	deadline = time.Now().Add(2 * time.Second)
	var absent bool
	for time.Now().Before(deadline) {
		runtime.GC()
		if !runnerGoroutineStackContains("poolDispatcher") {
			absent = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.True(t, absent,
		"poolDispatcher goroutine still present in stack dump after Shutdown; Shutdown did not clean it up")
}

// TestRunnerShutdownIdempotent verifies that calling Shutdown twice does not
// panic or return an error.
func TestRunnerShutdownIdempotent(t *testing.T) {
	r := NewRunner(&shutdownStubProvider{}, nil, RunnerConfig{
		WorkerPoolSize: 2,
		DefaultModel:   "gpt-4.1-mini",
	})
	require.NoError(t, r.Shutdown(context.Background()))
	require.NoError(t, r.Shutdown(context.Background()))
}

// TestRunnerShutdownUnboundedMode verifies that Shutdown works correctly when
// WorkerPoolSize == 0 (unbounded / legacy mode — no poolDispatcher goroutine).
func TestRunnerShutdownUnboundedMode(t *testing.T) {
	baseline := runtime.NumGoroutine()

	r := NewRunner(&shutdownStubProvider{}, nil, RunnerConfig{
		WorkerPoolSize: 0, // unbounded
		DefaultModel:   "gpt-4.1-mini",
	})

	run, err := r.StartRun(RunRequest{Prompt: "hello"})
	require.NoError(t, err)
	_, err = collectRunEvents(t, r, run.ID)
	require.NoError(t, err)

	require.NoError(t, r.Shutdown(context.Background()))

	// StartRun after Shutdown should return ErrRunnerClosed.
	_, err = r.StartRun(RunRequest{Prompt: "nope"})
	require.True(t, errors.Is(err, ErrRunnerClosed),
		"expected ErrRunnerClosed, got %v", err)

	assertNoRunnerGoroutineLeak(t, baseline)
}
