package harness

import "testing"

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
