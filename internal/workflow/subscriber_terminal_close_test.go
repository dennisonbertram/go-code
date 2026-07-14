package workflow_test

// Optional hardening (follow-up review, "strongly recommended"): the
// fan-out in emit() is `select { case ch <- event: default: }` on a
// 64-slot buffered channel. A slow subscriber whose buffer is already
// full silently LOSES the send -- including the terminal
// workflow.completed/workflow.failed event -- and the engine never
// closes the channel either, so that subscriber hangs forever waiting
// for a completion that already happened. This is a second route to the
// exact "SSE client hangs forever" failure mode BUG 5 fixed for the
// history/live gap, via a different mechanism (a full buffer instead of
// a registration-timing gap).
//
// The fix: on a terminal event, emit() closes and deregisters every
// subscriber channel for the run, in the same critical section, right
// after the (possibly-dropped) send. A closed channel read
// (`ev, ok := <-ch`) returns immediately with ok=false regardless of
// buffer state, which every subscriber (e.g. the SSE handler in
// internal/server/http_script_workflows.go, which already does
// `case ev, ok := <-stream: if !ok { return }`) already treats as
// "stream ended". This requires zero changes to internal/server.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"go-agent-harness/internal/workflow"
)

// TestSubscriberChannelClosesOnTerminalEventEvenWithFullBuffer subscribes
// to a run BEFORE starting it, then never drains the channel while the
// run emits far more than 64 (the channel's buffer capacity) events,
// guaranteeing some sends are dropped. It then asserts that draining the
// channel after the run reaches a terminal status eventually yields
// ok=false (closed), rather than hanging.
func TestSubscriberChannelClosesOnTerminalEventEvenWithFullBuffer(t *testing.T) {
	mgr := newMockMgr()
	eng := workflow.NewEngine(workflow.EngineOptions{Subagents: mgr})

	eng.Register("chatty", func(ctx *workflow.Context) (any, error) {
		for i := 0; i < 100; i++ {
			ctx.Log(fmt.Sprintf("line %d", i))
		}
		return "done", nil
	})

	run, err := eng.Start(context.Background(), "chatty", nil)
	require.NoError(t, err)

	_, ch, cancel, err := eng.Subscribe(run.ID)
	require.NoError(t, err)
	defer cancel()

	// Deliberately never drain ch while the run is in flight, so its
	// 64-slot buffer fills and later sends -- including, with high
	// probability, the terminal one -- are dropped by design.
	waitForRun(t, eng, run.ID, "")

	// Now drain whatever is left. Regardless of how many (if any)
	// buffered events remain, this must eventually observe the channel
	// closed -- not hang.
	closed := false
	deadline := time.Now().Add(2 * time.Second)
	for !closed && time.Now().Before(deadline) {
		select {
		case _, ok := <-ch:
			if !ok {
				closed = true
			}
		case <-time.After(200 * time.Millisecond):
		}
	}
	require.True(t, closed, "subscriber channel never closed after the run reached a terminal state — a slow subscriber with a full buffer would hang forever waiting for a completion that already happened")
}

// TestCancelAfterTerminalCloseIsSafe verifies that calling cancel() after
// emit() has already closed the channel on a terminal event does not
// panic (double-close).
func TestCancelAfterTerminalCloseIsSafe(t *testing.T) {
	mgr := newMockMgr()
	eng := workflow.NewEngine(workflow.EngineOptions{Subagents: mgr})

	eng.Register("quick", func(*workflow.Context) (any, error) {
		return "ok", nil
	})

	run, err := eng.Start(context.Background(), "quick", nil)
	require.NoError(t, err)

	_, _, cancel, err := eng.Subscribe(run.ID)
	require.NoError(t, err)

	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	// By now emit() has (per the fix) already closed and deregistered
	// this subscriber's channel on the terminal event. Calling cancel()
	// afterward must be a safe no-op, not a "close of closed channel"
	// panic.
	require.NotPanics(t, func() { cancel() })
}
