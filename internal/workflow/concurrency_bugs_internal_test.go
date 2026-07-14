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
