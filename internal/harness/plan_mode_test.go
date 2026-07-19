package harness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	htools "go-agent-harness/internal/harness/tools"
)

func TestStartRunInitializesPlanModeState(t *testing.T) {
	runner := NewRunner(&stubProvider{}, NewRegistry(), RunnerConfig{})
	run, err := runner.StartRun(RunRequest{Prompt: "plan this", PlanMode: true})
	if err != nil {
		t.Fatal(err)
	}
	runner.mu.RLock()
	state := runner.runs[run.ID]
	runner.mu.RUnlock()
	if state.planMode != PlanModeActive {
		t.Fatalf("plan state = %q, want active", state.planMode)
	}
	if state.planFile != defaultPlanFile {
		t.Fatalf("plan file = %q", state.planFile)
	}
}

func TestPlanModeRealPolicyWrapperDeniesEditOutsidePlanFile(t *testing.T) {
	called := false
	wrapped := htools.ApplyPolicy(htools.Definition{Name: "write", Action: htools.ActionWrite, Mutating: true}, htools.ApprovalModeFullAuto, nil,
		func(_ context.Context, _ json.RawMessage) (string, error) { called = true; return "ok", nil })
	runner := NewRunner(&stubProvider{}, NewRegistry(), RunnerConfig{})
	run, err := runner.StartRun(RunRequest{Prompt: "plan", PlanMode: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), htools.ContextKeyPlanModeGate, runPlanModeGate{runner: runner, runID: run.ID})
	out, err := wrapped(ctx, json.RawMessage(`{"path":"main.go","content":"bad"}`))
	if err != nil {
		t.Fatal(err)
	}
	if called || !strings.Contains(out, "plan_mode_denied") {
		t.Fatalf("real policy wrapper result=%s called=%v", out, called)
	}
}
