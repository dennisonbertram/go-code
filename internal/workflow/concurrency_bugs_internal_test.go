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

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
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
