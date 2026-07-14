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
// This test concurrently emits (continuously) while subscribing and
// immediately cancelling, many times, to force the interleaving that
// triggers the panic. A panic in this non-recovered emitter goroutine will
// crash the whole test binary — exactly the harnessd crash the bug causes
// in production when an SSE client disconnects mid-stream.
func TestEmitCancelRaceDoesNotPanic(t *testing.T) {
	e := NewEngine(EngineOptions{Subagents: noopSubagentManager{}})
	const runID = "race-run"
	const iterations = 2000

	stop := make(chan struct{})
	var emitterWG sync.WaitGroup
	emitterWG.Add(1)
	go func() {
		defer emitterWG.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			e.emit(runID, EventWorkflowLog, map[string]any{"i": i})
			i++
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
// countingRunningMgr's subagent never completes. We bound the Context's
// own ctx with a short timeout (instead of relying on the poll interval,
// which the fix has not introduced yet) and assert the number of Get()
// calls made in that window is small. A busy-loop produces many hundreds
// of thousands of calls in even a few milliseconds; a properly-blocking
// poll makes only a handful.
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

func TestAgentPollDoesNotBusyLoop(t *testing.T) {
	mgr := &countingRunningMgr{}
	e := NewEngine(EngineOptions{Subagents: mgr, MaxConcurrency: 1})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	wfCtx := newContext(ctx, e, "run-poll", nil, newBudget(0))

	_, err := wfCtx.Agent("do work", nil)
	if err == nil {
		t.Fatalf("expected Agent to return an error when its context deadline expires")
	}

	gets := mgr.getCalls.Load()
	// A busy-loop with no blocking will issue an enormous number of Get()
	// calls in 50ms (typically well over a million on modern hardware). A
	// properly-blocking poll should issue only a handful in that window.
	if gets > 1000 {
		t.Fatalf("expected a bounded number of poll calls within ~50ms, got %d (looks like a busy-loop)", gets)
	}
}
