package workflow_test

// Strongly recommended by third-round review of fix/workflow-engine-
// concurrency: Context.Phase() writes c.phase under c.mu, but
// Context.Agent() reads c.phase with NO lock. Parallel()/Pipeline()
// goroutines share a single *Context, so a script that calls ctx.Phase()
// in one thunk and ctx.Agent() in another is a genuine data race,
// confirmed under -race. This is byte-identical at the fork point (not a
// regression introduced by this branch), but since this IS the
// concurrency-hardening branch for this package, it's folded in here.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"go-agent-harness/internal/workflow"
)

// TestParallelPhaseAndAgentDoNotRace calls ctx.Phase() and ctx.Agent()
// concurrently from two Parallel() thunks sharing the same *Context. Run
// with -race, this must not report a data race.
func TestParallelPhaseAndAgentDoNotRace(t *testing.T) {
	eng, _ := newEngine(t)
	eng.Register("phase-agent-race", func(ctx *workflow.Context) (any, error) {
		_, _ = ctx.Parallel([]func() (any, error){
			func() (any, error) {
				for i := 0; i < 200; i++ {
					ctx.Phase("phase")
				}
				return nil, nil
			},
			func() (any, error) {
				for i := 0; i < 200; i++ {
					if _, err := ctx.Agent("hello", nil); err != nil {
						return nil, err
					}
				}
				return nil, nil
			},
		})
		return nil, nil
	})

	run, err := eng.Start(context.Background(), "phase-agent-race", nil)
	require.NoError(t, err)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)
}
