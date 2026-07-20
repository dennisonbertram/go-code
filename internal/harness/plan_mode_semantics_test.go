package harness

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// This file pins the plan-mode exit semantics that every later feature slice of
// epic #819 depends on:
//
//  1. Exiting plan mode always blocks on the approval broker — in every
//     ToolApprovalMode, including full_auto. Approval mode must never bypass the
//     plan-exit checkpoint.
//  2. Denial returns the run to PlanModeActive and feeds the operator's request
//     for changes back into the run as a user message; approval deactivates
//     plan mode and lets the run complete.
//  3. A nil approval broker is an explicit run failure
//     ("plan mode requires an approval broker") — fail closed, never skip.
//  4. A broker timeout is a defined outcome: the run fails with the approval
//     timeout error rather than hanging forever.
//
// These tests must fail if anyone ever gates plan exit on approval mode.

// planApprovalModes enumerates every tool approval mode. Plan-exit approval is
// required under all of them.
var planApprovalModes = []struct {
	name string
	mode ToolApprovalMode
}{
	{"full_auto", ToolApprovalModeFullAuto},
	{"permissions", ToolApprovalModePermissions},
	{"all", ToolApprovalModeAll},
}

// gatedProvider returns its first completion immediately and holds every later
// completion on a gate. It lets a test keep a run parked in PlanModeActive
// (post-denial) deterministically instead of racing the step loop.
type gatedProvider struct {
	mu         sync.Mutex
	turns      []CompletionResult
	calls      int
	gate       chan struct{}
	releaseOne sync.Once
}

func (g *gatedProvider) Complete(_ context.Context, _ CompletionRequest) (CompletionResult, error) {
	g.mu.Lock()
	call := g.calls
	g.calls++
	var turn CompletionResult
	if call < len(g.turns) {
		turn = g.turns[call]
	}
	gate := g.gate
	g.mu.Unlock()
	if call > 0 {
		<-gate
	}
	return turn, nil
}

func (g *gatedProvider) release() {
	g.releaseOne.Do(func() { close(g.gate) })
}

// waitPlanBrokerPending waits until the broker has a pending approval for the
// run — the observable proof that plan exit paused for operator approval.
func waitPlanBrokerPending(t *testing.T, b *InMemoryApprovalBroker, runID string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		if _, ok := b.Pending(runID); ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %q never registered a pending plan-exit approval with the broker", runID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitPlanModeState polls PlanModeState until the run reaches the target state.
func waitPlanModeState(t *testing.T, r *Runner, runID string, want PlanModeState) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		state, ok := r.PlanModeState(runID)
		if ok && state == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for plan mode state %q, last state %q (ok=%v)", want, state, ok)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitForUserMessageContaining polls the run transcript for a user message
// containing want.
func waitForUserMessageContaining(t *testing.T, r *Runner, runID, want string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		for _, m := range r.GetRunMessages(runID) {
			if m.Role == "user" && strings.Contains(m.Content, want) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for user message containing %q; messages: %#v", want, r.GetRunMessages(runID))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPlanModeExitApprovalBlocksInEveryApprovalMode pins the core invariant:
// a plan-mode run that is ready to complete must pause on the approval broker
// and emit plan.approval_required no matter which tool approval mode is
// configured — full_auto included. Approving deactivates plan mode, emits
// plan.approval_granted, and lets the run complete.
func TestPlanModeExitApprovalBlocksInEveryApprovalMode(t *testing.T) {
	for _, tc := range planApprovalModes {
		t.Run(tc.name, func(t *testing.T) {
			broker := NewInMemoryApprovalBroker()
			runner := NewRunner(&stubProvider{turns: []CompletionResult{{Content: "# proposed plan"}}}, NewRegistry(), RunnerConfig{
				DefaultModel:     "test",
				ToolApprovalMode: tc.mode,
				ApprovalBroker:   broker,
			})
			run, err := runner.StartRun(RunRequest{Prompt: "plan", PlanMode: true})
			if err != nil {
				t.Fatal(err)
			}

			// The run must reach the broker instead of completing on its own.
			// If plan exit were ever keyed on approval mode, full_auto would
			// complete without ever registering here and this wait would fail.
			waitPlanBrokerPending(t, broker, run.ID)

			if got, ok := runner.GetRun(run.ID); !ok {
				t.Fatalf("run %q not found", run.ID)
			} else if got.Status != RunStatusWaitingForApproval {
				t.Fatalf("status=%q, want %q: plan exit must block on the broker in %s mode", got.Status, RunStatusWaitingForApproval, tc.name)
			}
			waitPlanModeState(t, runner, run.ID, PlanModeExitPending)

			required := findEventByType(collectEvents(t, runner, run.ID), EventPlanApprovalRequired)
			if required == nil {
				t.Fatalf("plan.approval_required not emitted in %s mode", tc.name)
			}
			if plan, _ := required.Payload["plan"].(string); plan != "# proposed plan" {
				t.Fatalf("plan.approval_required payload plan=%q, want the presented plan", plan)
			}

			if err := broker.Approve(run.ID); err != nil {
				t.Fatalf("approve: %v", err)
			}
			if got := waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed); got != RunStatusCompleted {
				t.Fatalf("status=%q after approve, want completed", got)
			}
			waitPlanModeState(t, runner, run.ID, PlanModeInactive)
			if findEventByType(collectEvents(t, runner, run.ID), EventPlanApprovalGranted) == nil {
				t.Fatalf("plan.approval_granted not emitted after approve in %s mode", tc.name)
			}
		})
	}
}

// TestPlanModeExitDenyReentersActiveWithFeedback pins the denial path in every
// approval mode: denial returns the run to PlanModeActive, emits
// plan.approval_denied, and appends the operator-feedback user message so the
// model revises the plan; the run then continues and the next exit attempt
// blocks on the broker again.
func TestPlanModeExitDenyReentersActiveWithFeedback(t *testing.T) {
	for _, tc := range planApprovalModes {
		t.Run(tc.name, func(t *testing.T) {
			provider := &gatedProvider{
				turns: []CompletionResult{{Content: "# plan v1"}, {Content: "# plan v2"}},
				gate:  make(chan struct{}),
			}
			t.Cleanup(provider.release)
			broker := NewInMemoryApprovalBroker()
			runner := NewRunner(provider, NewRegistry(), RunnerConfig{
				DefaultModel:     "test",
				ToolApprovalMode: tc.mode,
				ApprovalBroker:   broker,
			})
			run, err := runner.StartRun(RunRequest{Prompt: "plan", PlanMode: true})
			if err != nil {
				t.Fatal(err)
			}
			waitPlanBrokerPending(t, broker, run.ID)

			if err := broker.Deny(run.ID); err != nil {
				t.Fatalf("deny: %v", err)
			}

			// Denial re-enters PlanModeActive and the operator's request for
			// changes is fed back into the run as a user message.
			waitPlanModeState(t, runner, run.ID, PlanModeActive)
			waitForUserMessageContaining(t, runner, run.ID, "operator requested changes")
			if findEventByType(collectEvents(t, runner, run.ID), EventPlanApprovalDenied) == nil {
				t.Fatalf("plan.approval_denied not emitted after deny in %s mode", tc.name)
			}
			if got, ok := runner.GetRun(run.ID); !ok {
				t.Fatalf("run %q not found", run.ID)
			} else if got.Status == RunStatusCompleted || got.Status == RunStatusFailed {
				t.Fatalf("status=%q after deny, want the run to continue in plan mode", got.Status)
			}

			// The run continues; the revised plan is presented for approval again.
			provider.release()
			waitPlanBrokerPending(t, broker, run.ID)
			waitPlanModeState(t, runner, run.ID, PlanModeExitPending)
			if err := broker.Approve(run.ID); err != nil {
				t.Fatalf("approve after revision: %v", err)
			}
			if got := waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed); got != RunStatusCompleted {
				t.Fatalf("status=%q after approve, want completed", got)
			}
			waitPlanModeState(t, runner, run.ID, PlanModeInactive)
		})
	}
}

// TestPlanModeExitNilBrokerFailsRun pins the fail-closed contract: plan mode
// without an approval broker is a configuration error, and the run must fail
// loudly rather than silently skip the exit checkpoint.
func TestPlanModeExitNilBrokerFailsRun(t *testing.T) {
	runner := NewRunner(&stubProvider{turns: []CompletionResult{{Content: "# plan"}}}, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
	})
	run, err := runner.StartRun(RunRequest{Prompt: "plan", PlanMode: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitForStatus(t, runner, run.ID, RunStatusFailed, RunStatusCompleted); got != RunStatusFailed {
		t.Fatalf("status=%q, want failed: nil broker must fail the run, never bypass plan exit", got)
	}
	got, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("run %q not found", run.ID)
	}
	if !strings.Contains(got.Error, "plan mode requires an approval broker") {
		t.Fatalf("run error=%q, want the explicit nil-broker failure", got.Error)
	}
}

// TestPlanModeExitBrokerTimeoutFailsRun pins the defined timeout outcome: if
// the operator never decides before the broker deadline, the run fails with
// the approval timeout error instead of waiting forever.
func TestPlanModeExitBrokerTimeoutFailsRun(t *testing.T) {
	broker := NewInMemoryApprovalBroker()
	runner := NewRunner(&stubProvider{turns: []CompletionResult{{Content: "# plan"}}}, NewRegistry(), RunnerConfig{
		DefaultModel:   "test",
		ApprovalBroker: broker,
		AskUserTimeout: 50 * time.Millisecond,
	})
	run, err := runner.StartRun(RunRequest{Prompt: "plan", PlanMode: true})
	if err != nil {
		t.Fatal(err)
	}
	waitPlanBrokerPending(t, broker, run.ID)
	if got := waitForStatus(t, runner, run.ID, RunStatusFailed); got != RunStatusFailed {
		t.Fatalf("status=%q, want failed after broker timeout", got)
	}
	got, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("run %q not found", run.ID)
	}
	if !strings.Contains(got.Error, "approval timeout") {
		t.Fatalf("run error=%q, want the approval timeout failure", got.Error)
	}
}
