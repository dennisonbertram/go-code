package workflow

// Tests in this file exercise internal Engine/Context state directly
// (package workflow, not workflow_test) because reproducing each bug
// requires either unexported access (subagentPollInterval, e.emit,
// e.scripts) or precise timing control that the public API does not
// expose.
//
// Each test corresponds to one of the five bugs tracked for
// fix/workflow-engine-concurrency. Bugs are added test-by-test, in
// red-then-green order, matching the commit trail.
//
//   BUG 1 (P0): send on closed channel in emit() vs Subscribe cancel()
//   BUG 2 (P1): Context.Agent busy-loop polling

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// noopSubagentManager satisfies SubagentManager for tests that never call
// Context.Agent and just need a valid Engine.
type noopSubagentManager struct{}

func (noopSubagentManager) Create(context.Context, SubagentRequest) (SubagentResult, error) {
	return SubagentResult{}, fmt.Errorf("noopSubagentManager: Create not implemented")
}

func (noopSubagentManager) Get(context.Context, string) (SubagentResult, error) {
	return SubagentResult{}, fmt.Errorf("noopSubagentManager: Get not implemented")
}

// ---------------------------------------------------------------------
// BUG 1: send on closed channel crashes harnessd
// ---------------------------------------------------------------------
//
// emit() historically snapshotted subscriber channels under e.mu, released
// the lock, then sent on each channel outside the lock. Subscribe's cancel
// func takes the lock, deletes the channel from e.subs, and closes it. If
// emit captured the channel before cancel ran, but cancel's close() happens
// before emit's send loop reaches that channel, emit sends on a closed
// channel and panics. A `select`/`default` guard does NOT protect against
// this — it only guards a full channel, not a closed one.
//
// This test concurrently emits (continuously, up to a bound) while
// subscribing and immediately cancelling, many times, to force the
// interleaving that triggers the panic. A panic in this non-recovered
// emitter goroutine will crash the whole test binary — exactly the
// harnessd crash the bug causes in production when an SSE client
// disconnects mid-stream.
//
// The emitter is capped at maxEmits (rather than running unbounded until
// `stop` closes) as a deliberate, environment-independent bound on this
// run's event history. Earlier this test ran the emitter with NO cap: on
// a machine where the surrounding Subscribe/cancel loop is slow (e.g.
// under heavy contention or -race overhead), the emitter would keep
// emitting for however long that takes, so the run's history could grow
// completely unbounded — measured at over 9 MILLION events during
// investigation of a real regression this caused (see the follow-up
// review of fix/workflow-engine-concurrency: Subscribe's O(history)
// store read, even after being moved outside e.mu, still made each
// Subscribe call progressively more expensive as history grew, which in
// turn gave the emitter more time to emit even more between Subscribes —
// a runaway feedback loop). Capping the emitter keeps this a meaningful,
// bounded stress test of the BUG 1 race regardless of how fast or slow
// the machine running it is.
func TestEmitCancelRaceDoesNotPanic(t *testing.T) {
	e := NewEngine(EngineOptions{Subagents: noopSubagentManager{}})
	const runID = "race-run"
	const iterations = 2000
	const maxEmits = 20000

	stop := make(chan struct{})
	var emitterWG sync.WaitGroup
	emitterWG.Add(1)
	go func() {
		defer emitterWG.Done()
		for i := 0; i < maxEmits; i++ {
			select {
			case <-stop:
				return
			default:
			}
			e.emit(runID, EventWorkflowLog, map[string]any{"i": i})
		}
	}()

	for i := 0; i < iterations; i++ {
		_, _, cancel, err := e.Subscribe(runID)
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		if i%5 == 0 {
			// Occasionally yield so the emitter goroutine has a chance to
			// capture this channel before we cancel it.
			runtime.Gosched()
		}
		cancel()
	}

	close(stop)
	emitterWG.Wait()
}

// ---------------------------------------------------------------------
// BUG 2: busy-loop poll pegs a CPU core
// ---------------------------------------------------------------------
//
// Context.Agent's completion-wait loop selected on ctx.Done() with a
// `default:` branch, so it never blocked — it spun at 100% CPU polling
// Get() for the entire lifetime of every in-flight subagent.
//
// slowPollMgr simulates a subagent that stays "running" for a fixed
// duration before completing. We shrink subagentPollInterval so the test
// runs fast, then assert the number of Get() calls over that duration is
// bounded (roughly duration/interval), not the thousands/millions a
// busy-loop would produce.
type slowPollMgr struct {
	getCalls      atomic.Int64
	createdAt     time.Time
	completeAfter time.Duration
}

func (m *slowPollMgr) Create(context.Context, SubagentRequest) (SubagentResult, error) {
	m.createdAt = time.Now()
	return SubagentResult{ID: "agent-1", Status: "running"}, nil
}

func (m *slowPollMgr) Get(_ context.Context, id string) (SubagentResult, error) {
	m.getCalls.Add(1)
	if time.Since(m.createdAt) >= m.completeAfter {
		return SubagentResult{ID: id, Status: "completed", Output: "done"}, nil
	}
	return SubagentResult{ID: id, Status: "running"}, nil
}

func TestAgentPollDoesNotBusyLoop(t *testing.T) {
	orig := subagentPollInterval
	subagentPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { subagentPollInterval = orig })

	mgr := &slowPollMgr{completeAfter: 60 * time.Millisecond}
	e := NewEngine(EngineOptions{Subagents: mgr, MaxConcurrency: 1})
	wfCtx := newContext(context.Background(), e, "run-poll", nil, newBudget(0))

	_, err := wfCtx.Agent("do work", nil)
	if err != nil {
		t.Fatalf("Agent: %v", err)
	}

	gets := mgr.getCalls.Load()
	// Busy-looping would produce many thousands of Get() calls in 60ms.
	// A bounded poll at 5ms intervals over ~60ms should produce roughly
	// 12 calls; allow generous headroom for scheduling jitter, but reject
	// anything that looks like a spin.
	if gets > 100 {
		t.Fatalf("expected a bounded number of poll calls, got %d (looks like a busy-loop)", gets)
	}
	if gets < 2 {
		t.Fatalf("expected at least a couple of poll calls, got %d", gets)
	}
}

// TestAgentPollBlocksBetweenPolls is a second, more direct proof of the
// fix: it uses the un-shrunk default subagentPollInterval and bounds the
// Context's own ctx with a short timeout while the subagent never
// completes. With a properly-blocking poll, at most 2 Get() calls happen
// in that window (the immediate first check, plus possibly one more right
// at the boundary). A busy-loop would produce orders of magnitude more.
func TestAgentPollBlocksBetweenPolls(t *testing.T) {
	mgr := &countingRunningMgr{}
	e := NewEngine(EngineOptions{Subagents: mgr, MaxConcurrency: 1})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	wfCtx := newContext(ctx, e, "run-poll-blocking", nil, newBudget(0))

	_, err := wfCtx.Agent("do work", nil)
	if err == nil {
		t.Fatalf("expected Agent to return an error when its context deadline expires")
	}

	gets := mgr.getCalls.Load()
	if gets > 1000 {
		t.Fatalf("expected a bounded number of poll calls within ~50ms, got %d (looks like a busy-loop)", gets)
	}
}

// countingRunningMgr is a subagent manager whose subagent never completes;
// used by TestAgentPollBlocksBetweenPolls to count how many times Get() is
// called within a bounded time window.
type countingRunningMgr struct {
	getCalls atomic.Int64
}

func (m *countingRunningMgr) Create(context.Context, SubagentRequest) (SubagentResult, error) {
	return SubagentResult{ID: "agent-1", Status: "running"}, nil
}

func (m *countingRunningMgr) Get(_ context.Context, id string) (SubagentResult, error) {
	m.getCalls.Add(1)
	return SubagentResult{ID: id, Status: "running"}, nil
}

// ---------------------------------------------------------------------
// BUG 3: unlocked map read races registration
// ---------------------------------------------------------------------
//
// Context.Workflow reads c.engine.scripts[name] without holding
// c.engine.mu, while Engine.Register writes that same map. Concurrent
// unsynchronized map read/write is a FATAL Go runtime error (not a
// recoverable panic) -- this test, run with -race, must not trigger it.
func TestConcurrentRegisterAndContextWorkflowNoRace(t *testing.T) {
	e := NewEngine(EngineOptions{Subagents: noopSubagentManager{}})
	e.Register("base", func(*Context) (any, error) { return "ok", nil })

	wfCtx := newContext(context.Background(), e, "run-workflow-race", nil, newBudget(0))

	stop := make(chan struct{})
	var registerWG sync.WaitGroup
	registerWG.Add(1)
	go func() {
		defer registerWG.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			name := fmt.Sprintf("dyn-%d", i)
			e.Register(name, func(*Context) (any, error) { return nil, nil })
			i++
		}
	}()

	const iterations = 2000
	for i := 0; i < iterations; i++ {
		result, err := wfCtx.Workflow("base", nil)
		if err != nil {
			close(stop)
			registerWG.Wait()
			t.Fatalf("Workflow: %v", err)
		}
		if result != "ok" {
			close(stop)
			registerWG.Wait()
			t.Fatalf("Workflow result = %v, want %q", result, "ok")
		}
	}

	close(stop)
	registerWG.Wait()
}

// ---------------------------------------------------------------------
// BUG 4: Resume TOCTOU allows double execution
// ---------------------------------------------------------------------
//
// Resume checked run.Status != RunStatusFailed, unlocked, THEN mutated the
// shared Run and spawned execution. Two concurrent Resume calls on the
// same failed run both pass the check before either mutates state, so
// both spawn `go e.execute` -- the script runs twice for one Resume
// "event".
func TestConcurrentResumeExecutesScriptExactlyOnce(t *testing.T) {
	e := NewEngine(EngineOptions{Subagents: noopSubagentManager{}})

	// The first execution (triggered by Start) fails immediately, putting
	// the run into RunStatusFailed. Any subsequent execution (triggered by
	// a successful Resume) blocks on `release` until the test has
	// collected results from ALL concurrent Resume attempts. This keeps
	// the run's status at RunStatusRunning for the whole race window, so
	// a genuine (fast) re-failure can never masquerade as a second
	// legitimate Resume — isolating the TOCTOU bug specifically.
	var executions atomic.Int64
	release := make(chan struct{})
	e.Register("flaky", func(*Context) (any, error) {
		n := executions.Add(1)
		if n == 1 {
			return nil, fmt.Errorf("boom")
		}
		<-release
		return nil, fmt.Errorf("boom again")
	})

	run, err := e.Start(context.Background(), "flaky", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForStatus(t, e, run.ID, RunStatusFailed)
	if got := executions.Load(); got != 1 {
		t.Fatalf("initial execution count = %d, want 1", got)
	}

	const concurrentResumes = 8
	var wg sync.WaitGroup
	errs := make([]error, concurrentResumes)
	var ready sync.WaitGroup
	ready.Add(concurrentResumes)
	start := make(chan struct{})
	for i := 0; i < concurrentResumes; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ready.Done()
			<-start
			_, resumeErr := e.Resume(context.Background(), run.ID, nil)
			errs[idx] = resumeErr
		}(i)
	}
	ready.Wait()
	close(start)
	wg.Wait()

	// Exactly one Resume call should have succeeded in transitioning the
	// run; the rest must observe it's no longer Failed and error out.
	successCount := 0
	for _, resumeErr := range errs {
		if resumeErr == nil {
			successCount++
		}
	}
	if successCount != 1 {
		close(release)
		t.Fatalf("expected exactly 1 successful Resume, got %d (errs=%v)", successCount, errs)
	}

	// All 8 concurrent Resume calls have now returned. Only the winner's
	// `go e.execute` is (or could be) in flight, blocked on `release`.
	// Let it proceed and reach a terminal status.
	close(release)
	waitForTerminal(t, e, run.ID)

	// executions started at 1 (the initial failed Start). Exactly one more
	// execution should have happened from the single successful Resume.
	if got := executions.Load(); got != 2 {
		t.Fatalf("total script executions = %d, want 2 (1 initial + 1 resume)", got)
	}
}

// TestConcurrentResumeCriticalSectionIsAtomic hardens BUG 4's coverage.
// Follow-up review found TestConcurrentResumeExecutesScriptExactlyOnce
// above passes even against the TOCTOU-buggy Resume when run WITHOUT
// -race: the buggy window is a few instructions wide, and 8 goroutines
// mostly serialize on e.mu anyway, so successCount==1 by luck, not by
// proof. It only goes red under -race, and even then via "race detected
// during execution of test", not via its own assertions -- so a
// regression that reintroduces the TOCTOU without a literal data race
// (e.g. an atomic-typed Status field checked-then-unlocked-then-set)
// would ship silently.
//
// This test uses resumePreTransitionHook (a test-only seam, nil/no-op in
// production) to deterministically pause the WINNING Resume call
// mid-critical-section -- after it has passed the status check but
// before it transitions the run -- and asserts that NONE of several
// concurrently-racing Resume calls can complete while it's paused there.
// That is only true if the check-and-transition genuinely holds e.mu for
// its entire span; it does not depend on -race, timing luck, or how many
// goroutines happen to serialize on the mutex by chance.
func TestConcurrentResumeCriticalSectionIsAtomic(t *testing.T) {
	e := NewEngine(EngineOptions{Subagents: noopSubagentManager{}})

	var executions atomic.Int64
	release := make(chan struct{})
	e.Register("flaky-det", func(*Context) (any, error) {
		n := executions.Add(1)
		if n == 1 {
			return nil, fmt.Errorf("boom")
		}
		<-release
		return "resumed-ok", nil
	})

	run, err := e.Start(context.Background(), "flaky-det", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForStatus(t, e, run.ID, RunStatusFailed)

	hookEntered := make(chan struct{})
	hookRelease := make(chan struct{})
	var hookCalls atomic.Int64
	var hookEnteredOnce sync.Once
	resumePreTransitionHook = func() {
		// sync.Once here guards ONLY against a spurious double-close
		// panic if this hook is somehow entered concurrently by more
		// than one goroutine (which is exactly what happens against a
		// non-atomic/buggy Resume) -- it does not affect the load-bearing
		// assertions below (hookCalls, finishedCount, successCount),
		// which still observe and report every entry.
		hookCalls.Add(1)
		hookEnteredOnce.Do(func() { close(hookEntered) })
		<-hookRelease
	}
	t.Cleanup(func() { resumePreTransitionHook = nil })

	const concurrentResumes = 8
	var wg sync.WaitGroup
	errs := make([]error, concurrentResumes)
	var finishedCount atomic.Int64
	begin := make(chan struct{})
	for i := 0; i < concurrentResumes; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-begin
			_, resumeErr := e.Resume(context.Background(), run.ID, nil)
			finishedCount.Add(1)
			errs[idx] = resumeErr
		}(i)
	}
	close(begin)

	select {
	case <-hookEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("no Resume call reached resumePreTransitionHook within 2s")
	}

	// While the winner is paused inside the hook (holding e.mu, if the
	// critical section is genuinely atomic), give the other goroutines
	// ample time to attempt Resume. None of them can have finished yet --
	// they're all blocked trying to acquire e.mu, which the winner still
	// holds.
	time.Sleep(150 * time.Millisecond)
	if got := finishedCount.Load(); got != 0 {
		t.Fatalf("expected 0 Resume calls to finish while the winner is paused mid-critical-section, got %d -- check-and-transition is not atomic", got)
	}

	close(hookRelease)
	wg.Wait()

	if got := hookCalls.Load(); got != 1 {
		t.Fatalf("expected resumePreTransitionHook to fire exactly once, got %d", got)
	}

	successCount := 0
	for _, resumeErr := range errs {
		if resumeErr == nil {
			successCount++
		}
	}
	if successCount != 1 {
		close(release)
		t.Fatalf("expected exactly 1 successful Resume, got %d (errs=%v)", successCount, errs)
	}

	close(release)
	waitForTerminal(t, e, run.ID)
	if got := executions.Load(); got != 2 {
		t.Fatalf("total script executions = %d, want 2 (1 initial + 1 resume)", got)
	}
}

// ---------------------------------------------------------------------
// BUG 5: Subscribe history/live gap loses terminal events
// ---------------------------------------------------------------------
//
// Subscribe read history via store.GetEvents and only THEN registered its
// channel in e.subs. An event emitted in that gap is in neither the
// returned history nor delivered on the channel -- permanently lost. If
// the lost event is the terminal workflow.completed/failed event, an SSE
// client hangs forever.
//
// This test races emit() against Subscribe() for the same runID many
// times (alternating which starts first) and asserts the event is always
// observed either in history or on the live channel -- never neither.
func TestSubscribeNeverMissesConcurrentEmit(t *testing.T) {
	e := NewEngine(EngineOptions{Subagents: noopSubagentManager{}})
	const iterations = 500

	for i := 0; i < iterations; i++ {
		runID := fmt.Sprintf("run-%d", i)

		var emitWG sync.WaitGroup
		emitWG.Add(1)
		fireEmit := func() {
			defer emitWG.Done()
			e.emit(runID, EventWorkflowCompleted, map[string]any{"i": i})
		}

		var history []Event
		var ch <-chan Event
		var cancel func()
		var subErr error

		if i%2 == 0 {
			go fireEmit()
			history, ch, cancel, subErr = e.Subscribe(runID)
		} else {
			history, ch, cancel, subErr = e.Subscribe(runID)
			go fireEmit()
		}
		if subErr != nil {
			t.Fatalf("iteration %d: Subscribe: %v", i, subErr)
		}

		emitWG.Wait()

		found := false
		for _, ev := range history {
			if ev.Type == EventWorkflowCompleted {
				found = true
				break
			}
		}
		if !found {
			select {
			case ev := <-ch:
				if ev.Type == EventWorkflowCompleted {
					found = true
				}
			case <-time.After(200 * time.Millisecond):
			}
		}
		cancel()

		if !found {
			t.Fatalf("iteration %d: terminal event lost — not in history and not delivered live", i)
		}
	}
}

// waitForStatus polls GetRun until the run reaches want or the deadline
// expires.
func waitForStatus(t *testing.T, e *Engine, runID string, want RunStatus) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		run, err := e.GetRun(runID)
		if err == nil && run.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach status %s in time", runID, want)
}

// waitForTerminal polls GetRun until the run reaches a terminal status.
func waitForTerminal(t *testing.T, e *Engine, runID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		run, err := e.GetRun(runID)
		if err == nil && (run.Status == RunStatusCompleted || run.Status == RunStatusFailed) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach a terminal status in time", runID)
}
