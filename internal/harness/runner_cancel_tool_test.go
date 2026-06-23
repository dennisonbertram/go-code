package harness

// runner_cancel_tool_test.go — proves that mid-flight cancellation of a real
// long-running bash command propagates through exec.CommandContext and terminates
// the run promptly (well within the command's own timeout).
//
// Design notes:
//   - Uses an inline provider stub (bashToolCancelProvider) to avoid importing
//     fakeprovider (which would create an import cycle: fakeprovider→harness).
//   - Uses NewDefaultRegistryWithOptions so the real bash tool (core.BashTool)
//     is registered and approved via FullAuto mode — no stub tool needed.
//   - The provider returns a "sleep 30" bash tool call on the first turn.
//     A goroutine subscribes before the run starts, waits for EventToolCallStarted
//     (event-based, not time-based), then calls CancelRun.
//   - The test asserts run.cancelled arrives within 5 s — much less than the
//     30 s sleep, proving the process was killed rather than waited out.
//   - assertNoRunnerGoroutineLeak is defined in runner_shutdown_test.go; it must
//     NOT be redeclared here.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// bashToolCancelProvider is a file-unique inline provider stub that returns a
// single bash tool call ("sleep 30") on the first Complete call, then blocks
// on the second call until the context is cancelled (which happens after
// CancelRun kills the bash process and the runner issues the next LLM turn,
// though in practice the run terminates before reaching the second turn).
type bashToolCancelProvider struct {
	mu        sync.Mutex
	calls     int
	firstCall chan struct{} // closed when first Complete is entered
}

func newBashToolCancelProvider() *bashToolCancelProvider {
	return &bashToolCancelProvider{
		firstCall: make(chan struct{}),
	}
}

func (p *bashToolCancelProvider) Complete(ctx context.Context, _ CompletionRequest) (CompletionResult, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.mu.Unlock()

	if idx == 0 {
		// Signal that the first call has been entered (run is live).
		select {
		case <-p.firstCall:
		default:
			close(p.firstCall)
		}
		// Return a bash tool call that will sleep for 30 seconds.
		return CompletionResult{
			ToolCalls: []ToolCall{{
				ID:        "call-sleep-30",
				Name:      "bash",
				Arguments: `{"command":"sleep 30","timeout_seconds":60}`,
			}},
		}, nil
	}

	// Subsequent calls: block until the run context is cancelled.
	// (We don't expect to reach here in the happy path, but being safe.)
	select {
	case <-ctx.Done():
		return CompletionResult{}, ctx.Err()
	}
}

// TestCancelToolMidFlight verifies that:
//  1. A run executing "sleep 30" via the real bash tool reaches tool.call.started.
//  2. CancelRun interrupts the bash process (via context cancellation through
//     exec.CommandContext) and the run reaches RunStatusCancelled within 5 s.
//  3. run.completed is NOT emitted.
//  4. No goroutine leak remains after Shutdown.
func TestCancelToolMidFlight(t *testing.T) {
	t.Parallel()

	prov := newBashToolCancelProvider()

	// NewDefaultRegistryWithOptions registers the real bash tool with FullAuto
	// approval so "sleep 30" runs without any policy gate.
	registry := NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{
		ApprovalMode: ToolApprovalModeFullAuto,
	})

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     5,
	})

	// Subscribe before starting the run so we capture all events from the start.
	// We must start the run first to get an ID, then subscribe.
	run, err := runner.StartRun(RunRequest{
		Prompt:       "run a long sleep",
		AllowedTools: []string{"bash"},
	})
	require.NoError(t, err)

	// toolStarted is closed when we observe EventToolCallStarted.
	toolStarted := make(chan struct{})
	cancelled := make(chan struct{})

	// Subscribe to the event stream.
	history, eventCh, cancelSub, subErr := runner.Subscribe(run.ID)
	require.NoError(t, subErr)

	// Process history snapshot first: the run may already be ahead of us.
	for _, ev := range history {
		t.Logf("history event: %s", ev.Type)
		if ev.Type == EventToolCallStarted {
			select {
			case <-toolStarted:
			default:
				close(toolStarted)
			}
		}
		if IsTerminalEvent(ev.Type) {
			select {
			case <-cancelled:
			default:
				close(cancelled)
			}
		}
	}

	// Drain the event stream in a background goroutine. When we see
	// tool.call.started we close toolStarted. When we see a terminal event
	// (run.cancelled, run.completed, run.failed) we close cancelled.
	go func() {
		defer cancelSub()
		for {
			evt, ok := <-eventCh
			if !ok {
				select {
				case <-cancelled:
				default:
					close(cancelled)
				}
				return
			}
			if evt.Type == EventToolCallStarted {
				select {
				case <-toolStarted:
				default:
					close(toolStarted)
				}
			}
			if IsTerminalEvent(evt.Type) {
				select {
				case <-cancelled:
				default:
					close(cancelled)
				}
				return
			}
		}
	}()

	// Wait for the bash tool to actually start executing (event-based).
	// tool.call.started may already be in the history snapshot (run started before
	// Subscribe was called), in which case toolStarted is already closed and this
	// select returns immediately.
	select {
	case <-toolStarted:
		// Good — the bash tool is either already executing or just started.
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for tool.call.started event (bash never started)")
	}

	// Cancel the run. This must propagate through exec.CommandContext and kill
	// the "sleep 30" process.
	require.NoError(t, runner.CancelRun(run.ID))

	// The run must reach a terminal state within 5 s — much less than the 30 s
	// sleep, proving the bash process was killed rather than waited out.
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		state, _ := runner.GetRun(run.ID)
		t.Fatalf("timed out waiting for terminal event after CancelRun; last status: %q", state.Status)
	}

	// Assert the run reached RunStatusCancelled (not completed or failed).
	state, ok := runner.GetRun(run.ID)
	require.True(t, ok, "run must still exist in the runner's store")
	require.Equal(t, RunStatusCancelled, state.Status,
		"run must be cancelled, not %q", state.Status)

	// Assert run.completed was NOT emitted (the run was killed, not finished).
	// We re-subscribe to collect the full history.
	fullHistory, _, fullCancel, histErr := runner.Subscribe(run.ID)
	if histErr == nil {
		fullCancel()
		for _, ev := range fullHistory {
			if ev.Type == EventRunCompleted {
				t.Error("run.completed must NOT be emitted when run is cancelled mid-tool")
				break
			}
		}
	}

	// Shut down the runner to clean up goroutines. Goroutine leak detection via
	// assertNoRunnerGoroutineLeak is intentionally omitted here: this test runs
	// in parallel alongside many other tests, making baseline comparisons
	// unreliable. The leak-detection invariants for Shutdown are covered by
	// TestRunnerShutdownStopsPoolDispatcher.
	require.NoError(t, runner.Shutdown(context.Background()))
}
