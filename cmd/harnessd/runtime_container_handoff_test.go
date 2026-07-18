package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-agent-harness/internal/fakeprovider"
	"go-agent-harness/internal/harness"
	htools "go-agent-harness/internal/harness/tools"
)

const handoffTestContent = "handoff-pong"

// newHandoffTestRunner builds a real *harness.Runner backed by the scriptable
// fake provider (one content reply, repeated on exhaustion) and wires it into
// a subagentRunnerHandoff exactly the way buildHTTPRuntime does.
func newHandoffTestRunner(t *testing.T) (*subagentRunnerHandoff, *harness.Runner) {
	t.Helper()
	provider := fakeprovider.New(
		[]fakeprovider.Turn{{Content: handoffTestContent}},
		fakeprovider.WithExhaustedBehavior(fakeprovider.ExhaustRepeatLast),
	)
	runner := harness.NewRunner(provider, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     1,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := runner.Shutdown(ctx); err != nil {
			t.Logf("runner shutdown: %v", err)
		}
	})
	handoff := &subagentRunnerHandoff{}
	handoff.setRunner(runner)
	return handoff, runner
}

// waitForTerminalStatus polls the runner until the run reaches a terminal
// status, with a bounded deadline so a stuck run fails fast instead of
// hanging the test.
func waitForTerminalStatus(t *testing.T, runner *harness.Runner, runID string) harness.Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		run, ok := runner.GetRun(runID)
		if ok {
			switch run.Status {
			case harness.RunStatusCompleted, harness.RunStatusFailed, harness.RunStatusCancelled:
				return run
			}
		}
		if time.Now().After(deadline) {
			status := "unknown"
			if ok {
				status = string(run.Status)
			}
			t.Fatalf("run %s did not reach a terminal status within 5s (last: %s)", runID, status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSubagentRunnerHandoffStartRunGetRunSubscribe(t *testing.T) {
	t.Parallel()
	handoff, runner := newHandoffTestRunner(t)

	// StartRun delegates: the returned run is registered on the underlying runner.
	run, err := handoff.StartRun(harness.RunRequest{Prompt: "hello handoff"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if run.ID == "" {
		t.Fatal("StartRun returned a run with an empty ID")
	}

	// GetRun returns the same record as the underlying runner.
	got, ok := handoff.GetRun(run.ID)
	if !ok {
		t.Fatalf("GetRun(%s) via handoff: not found", run.ID)
	}
	direct, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("GetRun(%s) via runner: not found", run.ID)
	}
	if got.ID != direct.ID || got.Prompt != direct.Prompt {
		t.Fatalf("handoff GetRun = %+v, want the same record as the runner's %+v", got, direct)
	}
	if _, ok := handoff.GetRun("run-does-not-exist"); ok {
		t.Fatal("GetRun(unknown) via handoff: ok=true, want false")
	}

	// Subscribe on a completed run replays the recorded history and returns a
	// live channel plus a working cancel func, like the underlying runner.
	final := waitForTerminalStatus(t, runner, run.ID)
	if final.Status != harness.RunStatusCompleted {
		t.Fatalf("run status = %s, want completed (run error: %s)", final.Status, final.Error)
	}
	history, ch, cancelSub, err := handoff.Subscribe(run.ID)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if len(history) == 0 {
		t.Fatal("Subscribe returned an empty history for a completed run")
	}
	if ch == nil {
		t.Fatal("Subscribe returned a nil event channel")
	}
	cancelSub()

	if _, _, _, err := handoff.Subscribe("run-does-not-exist"); err == nil {
		t.Fatal("Subscribe(unknown) via handoff: nil error, want not-found error")
	}
}

func TestSubagentRunnerHandoffRunPrompt(t *testing.T) {
	t.Parallel()
	handoff, _ := newHandoffTestRunner(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := handoff.RunPrompt(ctx, "say pong")
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if out != handoffTestContent {
		t.Fatalf("RunPrompt output = %q, want %q", out, handoffTestContent)
	}

	out, err = handoff.RunPromptWithAllowedTools(ctx, "say pong again", nil)
	if err != nil {
		t.Fatalf("RunPromptWithAllowedTools(nil filter): %v", err)
	}
	if out != handoffTestContent {
		t.Fatalf("RunPromptWithAllowedTools(nil filter) output = %q, want %q", out, handoffTestContent)
	}

	out, err = handoff.RunPromptWithAllowedTools(ctx, "say pong with filter", []string{"read_file"})
	if err != nil {
		t.Fatalf("RunPromptWithAllowedTools(named tool): %v", err)
	}
	if out != handoffTestContent {
		t.Fatalf("RunPromptWithAllowedTools(named tool) output = %q, want %q", out, handoffTestContent)
	}
}

func TestSubagentRunnerHandoffCancelRun(t *testing.T) {
	t.Parallel()
	handoff, runner := newHandoffTestRunner(t)

	// Unknown run: the handoff surfaces the underlying runner's ErrRunNotFound.
	if err := runner.CancelRun("run-does-not-exist"); !errors.Is(err, harness.ErrRunNotFound) {
		t.Fatalf("runner.CancelRun(unknown) = %v, want ErrRunNotFound", err)
	}
	if err := handoff.CancelRun("run-does-not-exist"); !errors.Is(err, harness.ErrRunNotFound) {
		t.Fatalf("handoff.CancelRun(unknown) = %v, want ErrRunNotFound", err)
	}

	// Terminal run: cancellation is a no-op returning nil, same as the runner.
	run, err := handoff.StartRun(harness.RunRequest{Prompt: "cancel me after completion"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForTerminalStatus(t, runner, run.ID)
	if err := handoff.CancelRun(run.ID); err != nil {
		t.Fatalf("handoff.CancelRun(completed) = %v, want nil", err)
	}
	if err := runner.CancelRun(run.ID); err != nil {
		t.Fatalf("runner.CancelRun(completed) = %v, want nil", err)
	}
}

func TestSubagentRunnerHandoffSteerRun(t *testing.T) {
	t.Parallel()
	handoff, runner := newHandoffTestRunner(t)

	// Unknown run: ErrRunNotFound, matching the underlying runner.
	if err := handoff.SteerRun("run-does-not-exist", "steer"); !errors.Is(err, harness.ErrRunNotFound) {
		t.Fatalf("handoff.SteerRun(unknown) = %v, want ErrRunNotFound", err)
	}

	// Completed run: ErrRunNotActive, matching the underlying runner.
	run, err := handoff.StartRun(harness.RunRequest{Prompt: "steer me after completion"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForTerminalStatus(t, runner, run.ID)
	if err := handoff.SteerRun(run.ID, "too late"); !errors.Is(err, harness.ErrRunNotActive) {
		t.Fatalf("handoff.SteerRun(completed) = %v, want ErrRunNotActive", err)
	}
	if err := runner.SteerRun(run.ID, "too late"); !errors.Is(err, harness.ErrRunNotActive) {
		t.Fatalf("runner.SteerRun(completed) = %v, want ErrRunNotActive", err)
	}

	// Blank message: validation error regardless of run state.
	if err := handoff.SteerRun(run.ID, "   "); err == nil {
		t.Fatal("handoff.SteerRun(blank message) = nil, want validation error")
	}
}

func TestSubagentRunnerHandoffParentRunID(t *testing.T) {
	t.Parallel()
	handoff, _ := newHandoffTestRunner(t)

	// Run spawned with a parent handoff: ParentRunID surfaces the recorded parent.
	child, err := handoff.StartRun(harness.RunRequest{
		Prompt: "child run",
		ParentContextHandoff: &htools.ParentContextHandoff{
			ParentRunID: "parent-1",
			Messages:    []htools.ParentContextMessage{{Index: 0, Role: "user", Content: "root task"}},
		},
	})
	if err != nil {
		t.Fatalf("StartRun(child): %v", err)
	}
	if parentID, ok := handoff.ParentRunID(child.ID); parentID != "parent-1" || !ok {
		t.Fatalf("ParentRunID(child) = (%q, %v), want (%q, true)", parentID, ok, "parent-1")
	}

	// Run whose handoff carries only whitespace: treated as no parent.
	blank, err := handoff.StartRun(harness.RunRequest{
		Prompt:               "blank parent run",
		ParentContextHandoff: &htools.ParentContextHandoff{ParentRunID: "   "},
	})
	if err != nil {
		t.Fatalf("StartRun(blank parent): %v", err)
	}
	if parentID, ok := handoff.ParentRunID(blank.ID); parentID != "" || ok {
		t.Fatalf("ParentRunID(blank parent) = (%q, %v), want (%q, false)", parentID, ok, "")
	}

	// Run without a handoff: no parent.
	orphan, err := handoff.StartRun(harness.RunRequest{Prompt: "orphan run"})
	if err != nil {
		t.Fatalf("StartRun(orphan): %v", err)
	}
	if parentID, ok := handoff.ParentRunID(orphan.ID); parentID != "" || ok {
		t.Fatalf("ParentRunID(orphan) = (%q, %v), want (%q, false)", parentID, ok, "")
	}

	// Unknown run: no parent.
	if parentID, ok := handoff.ParentRunID("run-does-not-exist"); parentID != "" || ok {
		t.Fatalf("ParentRunID(unknown) = (%q, %v), want (%q, false)", parentID, ok, "")
	}
}
