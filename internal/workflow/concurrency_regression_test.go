package workflow_test

// This is a black-box regression test for the fix/workflow-engine-concurrency
// bug batch (BUG 1-5 in internal/workflow/concurrency_bugs_internal_test.go).
// Unlike the bug-specific tests, which each isolate a single race with
// direct/internal access, this test drives the Engine purely through its
// public API the way the HTTP layer (internal/server/http_script_workflows.go)
// actually does: Start a workflow, have many SSE-style subscribers
// connect and disconnect at random points while it runs (mirroring an SSE
// client disconnecting mid-stream), run nested Workflow() calls
// concurrently with new Register() calls, and fire concurrent Resume
// calls against a failed run.
//
// If any of the BUG 1-5 fixes are reverted, this test:
//   - panics the whole test binary (BUG 1: send on closed channel),
//   - times out or takes drastically longer (BUG 2: busy-loop poll),
//   - crashes with a fatal Go runtime error under -race (BUG 3: unlocked
//     map read/write),
//   - observes more than one successful concurrent Resume / more than the
//     expected number of script executions (BUG 4: TOCTOU double exec),
//   - or fails to observe the terminal event for a subscriber connected
//     at exactly the wrong moment (BUG 5: history/live gap).

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"go-agent-harness/internal/workflow"
)

func TestWorkflowEngineSurvivesConcurrentSubscribersRegistrationAndResume(t *testing.T) {
	mgr := newMockMgr()
	mgr.delay = 2 * time.Millisecond // force Context.Agent's poll loop to iterate at least once
	eng := workflow.NewEngine(workflow.EngineOptions{Subagents: mgr, MaxConcurrency: 4})

	eng.Register("nested-leaf", func(ctx *workflow.Context) (any, error) {
		return "leaf-ok", nil
	})

	// "main" exercises: real agent() polling (BUG 2), several log/emit
	// calls that concurrent subscribers race against (BUG 1 + BUG 5), and
	// repeated nested workflow() lookups racing against concurrent
	// Register() calls from outside (BUG 3).
	eng.Register("main", func(ctx *workflow.Context) (any, error) {
		for i := 0; i < 20; i++ {
			ctx.Log(fmt.Sprintf("step %d", i))
			if _, err := ctx.Workflow("nested-leaf", nil); err != nil {
				return nil, err
			}
		}
		if _, err := ctx.Agent("do work", nil); err != nil {
			return nil, err
		}
		return "main-ok", nil
	})

	run, err := eng.Start(context.Background(), "main", nil)
	require.NoError(t, err)

	// SSE-style subscribers: connect, maybe read a bit, then disconnect
	// at a randomized moment — this is exactly the pattern that triggers
	// BUG 1 (panic) and stresses BUG 5 (lost terminal event) in
	// production when a browser tab closes mid-stream.
	var subWG sync.WaitGroup
	stopSubs := make(chan struct{})
	for s := 0; s < 20; s++ {
		subWG.Add(1)
		go func(id int) {
			defer subWG.Done()
			for {
				select {
				case <-stopSubs:
					return
				default:
				}
				_, ch, cancel, err := eng.Subscribe(run.ID)
				if err != nil {
					return
				}
				// Randomized dwell time before disconnecting, to hit a
				// wide spread of race windows against emit().
				select {
				case <-ch:
				case <-time.After(time.Duration(rand.Intn(2)) * time.Millisecond):
				}
				cancel()
			}
		}(s)
	}

	// Concurrently register new (unrelated) workflows while "main" is
	// repeatedly calling ctx.Workflow("nested-leaf", ...) — races
	// Engine.Register's map write against Context.Workflow's map read.
	var registerWG sync.WaitGroup
	stopRegister := make(chan struct{})
	registerWG.Add(1)
	go func() {
		defer registerWG.Done()
		i := 0
		for {
			select {
			case <-stopRegister:
				return
			default:
			}
			eng.RegisterWithMeta(workflow.Meta{Name: fmt.Sprintf("extra-%d", i)}, func(*workflow.Context) (any, error) {
				return nil, nil
			})
			i++
		}
	}()

	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	close(stopRegister)
	registerWG.Wait()
	close(stopSubs)
	subWG.Wait()

	final, err := eng.GetRun(run.ID)
	require.NoError(t, err)
	require.Equal(t, workflow.RunStatusCompleted, final.Status)
	require.Contains(t, final.ResultJSON, "main-ok")

	// A subscriber connecting strictly after completion must still see
	// the terminal event in history — proves BUG 5 stays fixed even
	// after the run has settled.
	history, _, cancel, err := eng.Subscribe(run.ID)
	require.NoError(t, err)
	defer cancel()
	foundTerminal := false
	for _, ev := range history {
		if ev.Type == workflow.EventWorkflowCompleted {
			foundTerminal = true
			break
		}
	}
	require.True(t, foundTerminal, "terminal event missing from history after completion")

	// --- Concurrent Resume regression (BUG 4) ---
	var executions atomic.Int64
	release := make(chan struct{})
	eng.Register("flaky-regression", func(*workflow.Context) (any, error) {
		n := executions.Add(1)
		if n == 1 {
			return nil, fmt.Errorf("boom")
		}
		<-release
		return "resumed-ok", nil
	})
	flakyRun, err := eng.Start(context.Background(), "flaky-regression", nil)
	require.NoError(t, err)
	waitForRun(t, eng, flakyRun.ID, workflow.RunStatusFailed)

	const concurrentResumes = 6
	var resumeWG sync.WaitGroup
	resumeErrs := make([]error, concurrentResumes)
	begin := make(chan struct{})
	for i := 0; i < concurrentResumes; i++ {
		resumeWG.Add(1)
		go func(idx int) {
			defer resumeWG.Done()
			<-begin
			_, resumeErr := eng.Resume(context.Background(), flakyRun.ID, nil)
			resumeErrs[idx] = resumeErr
		}(i)
	}
	close(begin)
	resumeWG.Wait()

	successes := 0
	for _, resumeErr := range resumeErrs {
		if resumeErr == nil {
			successes++
		}
	}
	require.Equal(t, 1, successes, "exactly one concurrent Resume should succeed, errs=%v", resumeErrs)

	close(release)
	waitForRun(t, eng, flakyRun.ID, workflow.RunStatusCompleted)
	require.Equal(t, int64(2), executions.Load(), "flaky-regression script should execute exactly twice total")
}
