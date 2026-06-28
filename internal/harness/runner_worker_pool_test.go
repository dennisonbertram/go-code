package harness

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// heldProvider is a Provider that blocks each call until the per-call release
// channel is closed. It records start/finish counts for pool assertions.
type heldProvider struct {
	mu      sync.Mutex
	waitCh  chan struct{} // closed to unblock all waiting Complete calls
	entered chan struct{} // each started call sends a token here
}

func newHeldProvider() *heldProvider {
	return &heldProvider{
		waitCh:  make(chan struct{}),
		entered: make(chan struct{}, 64),
	}
}

func (h *heldProvider) unblockAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	select {
	case <-h.waitCh:
		// already closed
	default:
		close(h.waitCh)
	}
}

func (h *heldProvider) Complete(_ context.Context, _ CompletionRequest) (CompletionResult, error) {
	h.entered <- struct{}{} // signal: entered Complete
	<-h.waitCh              // block until released
	return CompletionResult{Content: "done"}, nil
}

// countingHeldProvider is like heldProvider but allows one call to use a
// one-shot "first" channel and subsequent calls a "rest" channel.
type countingHeldProvider struct {
	firstWait    chan struct{} // closed to release first call
	firstEntered chan struct{} // closed when first call has entered
	restWait     chan struct{} // closed to release subsequent calls
	callCount    atomic.Int32
	firstOnce    sync.Once
	restOnce     sync.Once
}

func newCountingHeldProvider() *countingHeldProvider {
	return &countingHeldProvider{
		firstWait:    make(chan struct{}),
		firstEntered: make(chan struct{}),
		restWait:     make(chan struct{}),
	}
}

func (p *countingHeldProvider) Complete(_ context.Context, _ CompletionRequest) (CompletionResult, error) {
	n := p.callCount.Add(1)
	if n == 1 {
		select {
		case p.firstEntered <- struct{}{}:
		default:
		}
		<-p.firstWait
	} else {
		<-p.restWait
	}
	return CompletionResult{Content: "done"}, nil
}

func (p *countingHeldProvider) releaseFirst() {
	p.firstOnce.Do(func() { close(p.firstWait) })
}

func (p *countingHeldProvider) releaseRest() {
	p.restOnce.Do(func() { close(p.restWait) })
}

// seqRecordingProvider records the order in which calls complete.
type seqRecordingProvider struct {
	completionOrder chan int
	callCount       atomic.Int32
}

func newSeqRecordingProvider() *seqRecordingProvider {
	return &seqRecordingProvider{completionOrder: make(chan int, 16)}
}

func (p *seqRecordingProvider) Complete(_ context.Context, _ CompletionRequest) (CompletionResult, error) {
	n := int(p.callCount.Add(1))
	p.completionOrder <- n
	return CompletionResult{Content: "done"}, nil
}

// TestWorkerPool_QueuedStatusWhenPoolFull verifies that when more runs are
// started than the pool size, the extras have RunStatusQueued.
func TestWorkerPool_QueuedStatusWhenPoolFull(t *testing.T) {
	t.Parallel()

	const poolSize = 2
	const totalRuns = 5

	prov := newHeldProvider()

	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:   "gpt-4.1-mini",
		MaxSteps:       1,
		WorkerPoolSize: poolSize,
		AskUserTimeout: time.Second,
	})

	runIDs := make([]string, 0, totalRuns)
	for i := 0; i < totalRuns; i++ {
		run, err := runner.StartRun(RunRequest{Prompt: "hello"})
		if err != nil {
			t.Fatalf("StartRun %d: %v", i, err)
		}
		runIDs = append(runIDs, run.ID)
	}

	// Wait for exactly poolSize workers to enter Complete.
	deadline := time.After(3 * time.Second)
	for len(prov.entered) < poolSize {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for %d runs to start; only %d started", poolSize, len(prov.entered))
		case <-time.After(5 * time.Millisecond):
		}
	}
	// Give any overeager goroutine a moment to (incorrectly) also start.
	time.Sleep(50 * time.Millisecond)

	running, queued := 0, 0
	for _, id := range runIDs {
		run, ok := runner.GetRun(id)
		if !ok {
			t.Fatalf("run %s not found", id)
		}
		switch run.Status {
		case RunStatusRunning:
			running++
		case RunStatusQueued:
			queued++
		}
	}

	if running != poolSize {
		t.Errorf("expected %d running, got %d", poolSize, running)
	}
	if queued != totalRuns-poolSize {
		t.Errorf("expected %d queued, got %d", totalRuns-poolSize, queued)
	}

	prov.unblockAll()
	for _, id := range runIDs {
		waitForRunStatus(t, runner, id, RunStatusCompleted, 5*time.Second)
	}
}

// TestWorkerPool_QueuedTransitionsToRunning verifies that when a worker slot
// frees up, the next queued run transitions to running.
func TestWorkerPool_QueuedTransitionsToRunning(t *testing.T) {
	t.Parallel()

	const poolSize = 1

	prov := newCountingHeldProvider()
	t.Cleanup(func() {
		prov.releaseFirst()
		prov.releaseRest()
	})

	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:   "gpt-4.1-mini",
		MaxSteps:       1,
		WorkerPoolSize: poolSize,
		AskUserTimeout: time.Second,
	})

	// Start run1 — occupies the single slot.
	run1, err := runner.StartRun(RunRequest{Prompt: "first"})
	if err != nil {
		t.Fatalf("StartRun 1: %v", err)
	}

	// Wait for run1 to be in the provider.
	select {
	case <-prov.firstEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for first run to start")
	}

	// Start run2 — pool is full, should be queued.
	run2, err := runner.StartRun(RunRequest{Prompt: "second"})
	if err != nil {
		t.Fatalf("StartRun 2: %v", err)
	}

	time.Sleep(30 * time.Millisecond)

	r2, _ := runner.GetRun(run2.ID)
	if r2.Status != RunStatusQueued {
		t.Errorf("run2: expected queued, got %q", r2.Status)
	}

	// Release run1.
	prov.releaseFirst()
	// Unblock subsequent (run2) calls too.
	prov.releaseRest()

	waitForRunStatus(t, runner, run1.ID, RunStatusCompleted, 5*time.Second)
	waitForRunStatus(t, runner, run2.ID, RunStatusCompleted, 5*time.Second)
}

// TestWorkerPool_ConfigurablePoolSize verifies the pool size is honoured for
// several different pool sizes.
func TestWorkerPool_ConfigurablePoolSize(t *testing.T) {
	t.Parallel()

	for _, poolSize := range []int{1, 3, 5} {
		poolSize := poolSize
		t.Run(poolSizeName(poolSize), func(t *testing.T) {
			t.Parallel()

			total := poolSize + 2
			prov := newHeldProvider()

			runner := NewRunner(prov, NewRegistry(), RunnerConfig{
				DefaultModel:   "gpt-4.1-mini",
				MaxSteps:       1,
				WorkerPoolSize: poolSize,
				AskUserTimeout: time.Second,
			})

			runIDs := make([]string, total)
			for i := 0; i < total; i++ {
				run, err := runner.StartRun(RunRequest{Prompt: "hello"})
				if err != nil {
					t.Fatalf("StartRun %d: %v", i, err)
				}
				runIDs[i] = run.ID
			}

			deadline := time.After(3 * time.Second)
			for len(prov.entered) < poolSize {
				select {
				case <-deadline:
					t.Fatalf("timeout waiting for %d runs to start", poolSize)
				case <-time.After(5 * time.Millisecond):
				}
			}
			time.Sleep(40 * time.Millisecond)

			running, queued := 0, 0
			for _, id := range runIDs {
				run, _ := runner.GetRun(id)
				switch run.Status {
				case RunStatusRunning:
					running++
				case RunStatusQueued:
					queued++
				}
			}

			if running != poolSize {
				t.Errorf("poolSize=%d: expected %d running, got %d", poolSize, poolSize, running)
			}
			if queued != 2 {
				t.Errorf("poolSize=%d: expected 2 queued, got %d", poolSize, queued)
			}

			prov.unblockAll()
			for _, id := range runIDs {
				waitForRunStatus(t, runner, id, RunStatusCompleted, 5*time.Second)
			}
		})
	}
}

// TestWorkerPool_ZeroMeansUnlimited verifies that WorkerPoolSize=0 launches
// all runs immediately without queuing.
func TestWorkerPool_ZeroMeansUnlimited(t *testing.T) {
	t.Parallel()

	const total = 5
	prov := newHeldProvider()

	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:   "gpt-4.1-mini",
		MaxSteps:       1,
		WorkerPoolSize: 0, // unlimited
		AskUserTimeout: time.Second,
	})

	runIDs := make([]string, total)
	for i := 0; i < total; i++ {
		run, err := runner.StartRun(RunRequest{Prompt: "hello"})
		if err != nil {
			t.Fatalf("StartRun %d: %v", i, err)
		}
		runIDs[i] = run.ID
	}

	// All runs should start without waiting.
	deadline := time.After(3 * time.Second)
	for len(prov.entered) < total {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for all %d runs to start; only %d started", total, len(prov.entered))
		case <-time.After(5 * time.Millisecond):
		}
	}

	for _, id := range runIDs {
		run, ok := runner.GetRun(id)
		if !ok {
			t.Fatalf("run %s not found", id)
		}
		if run.Status == RunStatusQueued {
			t.Errorf("run %s: expected running (pool=0 is unlimited), got queued", id)
		}
	}

	prov.unblockAll()
	for _, id := range runIDs {
		waitForRunStatus(t, runner, id, RunStatusCompleted, 5*time.Second)
	}
}

// TestWorkerPool_PoolSize1Serializes verifies pool=1 forces runs to execute
// sequentially.
func TestWorkerPool_PoolSize1Serializes(t *testing.T) {
	t.Parallel()

	prov := newSeqRecordingProvider()

	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:   "gpt-4.1-mini",
		MaxSteps:       1,
		WorkerPoolSize: 1,
		AskUserTimeout: time.Second,
	})

	const n = 3
	runIDs := make([]string, n)
	for i := 0; i < n; i++ {
		run, err := runner.StartRun(RunRequest{Prompt: "hello"})
		if err != nil {
			t.Fatalf("StartRun %d: %v", i, err)
		}
		runIDs[i] = run.ID
	}

	for _, id := range runIDs {
		waitForRunStatus(t, runner, id, RunStatusCompleted, 8*time.Second)
	}

	// Collect completion order.
	close(prov.completionOrder)
	got := make([]int, 0, n)
	for v := range prov.completionOrder {
		got = append(got, v)
	}
	if len(got) != n {
		t.Fatalf("expected %d completions, got %d", n, len(got))
	}
	// With pool=1, calls must be sequential: 1, 2, 3.
	for i, v := range got {
		if v != i+1 {
			t.Errorf("completion[%d]=%d, want %d (pool=1 must serialize)", i, v, i+1)
		}
	}
}

// TestWorkerPool_RunQueuedEventEmitted verifies that a run.queued SSE event
// appears in a queued run's event history.
func TestWorkerPool_RunQueuedEventEmitted(t *testing.T) {
	t.Parallel()

	prov := newCountingHeldProvider()
	t.Cleanup(func() {
		prov.releaseFirst()
		prov.releaseRest()
	})

	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:   "gpt-4.1-mini",
		MaxSteps:       1,
		WorkerPoolSize: 1,
		AskUserTimeout: time.Second,
	})

	// Fill the single slot.
	run1, err := runner.StartRun(RunRequest{Prompt: "first"})
	if err != nil {
		t.Fatalf("StartRun 1: %v", err)
	}

	select {
	case <-prov.firstEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for first run to start")
	}

	// run2 must be queued.
	run2, err := runner.StartRun(RunRequest{Prompt: "second"})
	if err != nil {
		t.Fatalf("StartRun 2: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	r2, _ := runner.GetRun(run2.ID)
	if r2.Status != RunStatusQueued {
		t.Errorf("run2: expected queued status, got %q", r2.Status)
	}

	// Check run.queued event in history.
	history, _, cancel, err := runner.Subscribe(run2.ID)
	if err != nil {
		t.Fatalf("Subscribe run2: %v", err)
	}
	cancel()

	foundQueued := false
	for _, ev := range history {
		if ev.Type == EventRunQueued {
			foundQueued = true
			break
		}
	}
	if !foundQueued {
		types := make([]string, 0, len(history))
		for _, ev := range history {
			types = append(types, string(ev.Type))
		}
		t.Errorf("run2: expected run.queued event; got events: %v", types)
	}

	prov.releaseFirst()
	prov.releaseRest()
	waitForRunStatus(t, runner, run1.ID, RunStatusCompleted, 5*time.Second)
	waitForRunStatus(t, runner, run2.ID, RunStatusCompleted, 5*time.Second)
}

// --- helpers ---

func poolSizeName(n int) string {
	if n == 0 {
		return "poolSize=0"
	}
	b := make([]byte, 0, 12)
	b = append(b, []byte("poolSize=")...)
	tmp := make([]byte, 0, 4)
	for n > 0 {
		tmp = append([]byte{byte('0' + n%10)}, tmp...)
		n /= 10
	}
	return string(append(b, tmp...))
}

// waitForRunStatus polls until the run reaches the expected status or timeout.
func waitForRunStatus(t *testing.T, runner *Runner, runID string, want RunStatus, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		run, ok := runner.GetRun(runID)
		if ok && run.Status == want {
			return
		}
		select {
		case <-deadline:
			var status RunStatus
			if ok {
				status = run.Status
			}
			t.Fatalf("timeout waiting for run %s to reach %q; current: %q", runID, want, status)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
