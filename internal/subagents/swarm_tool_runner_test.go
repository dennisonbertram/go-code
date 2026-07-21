package subagents

import (
	"context"
	"testing"

	"go-agent-harness/internal/harness"
	tools "go-agent-harness/internal/harness/tools"
)

// TestToolSwarmRunnerMapsRequestIntoSwarm verifies the tools.SwarmRunner
// adapter translates every request field into the subagents swarm and that
// members are created with the mapped overrides.
func TestToolSwarmRunnerMapsRequestIntoSwarm(t *testing.T) {
	t.Parallel()

	fake := newFakeSwarmManager()
	fake.release()
	steerer := &fakeRunSteerer{}
	swarm := NewSwarm(fake, WithSwarmSteerer(steerer))
	runner := NewToolSwarmRunner(swarm)

	report, err := runner.RunSwarm(context.Background(), tools.SwarmRequest{
		PromptTemplate:  "do {{item}}",
		Items:           []string{"a", "b"},
		Model:           "gpt-swarm",
		MaxSteps:        7,
		MaxCostUSD:      1.5,
		ReasoningEffort: "high",
		AllowedTools:    []string{"read"},
		ProfileName:     "explorer",
		IsolationMode:   "inline",
		CleanupPolicy:   "delete",
		BaseRef:         "main",
		ResultMode:      "summary",
	})
	if err != nil {
		t.Fatalf("RunSwarm error = %v, want nil", err)
	}
	if fake.startCount() != 2 {
		t.Fatalf("started %d members, want 2", fake.startCount())
	}
	for i := 0; i < 2; i++ {
		req := fake.started[i]
		if req.Model != "gpt-swarm" || req.MaxSteps != 7 || req.MaxCostUSD != 1.5 {
			t.Errorf("member %d overrides = model:%q steps:%d cost:%v", i, req.Model, req.MaxSteps, req.MaxCostUSD)
		}
		if req.ReasoningEffort != "high" || req.ProfileName != "explorer" {
			t.Errorf("member %d effort/profile = %q/%q", i, req.ReasoningEffort, req.ProfileName)
		}
		if len(req.AllowedTools) != 1 || req.AllowedTools[0] != "read" {
			t.Errorf("member %d AllowedTools = %v, want [read]", i, req.AllowedTools)
		}
		if req.IsolationMode != "inline" || req.CleanupPolicy != "delete" || req.BaseRef != "main" || req.ResultMode != "summary" {
			t.Errorf("member %d runtime fields = %q/%q/%q/%q", i, req.IsolationMode, req.CleanupPolicy, req.BaseRef, req.ResultMode)
		}
	}

	if report.Total != 2 || report.Completed != 2 {
		t.Fatalf("report counts = total:%d completed:%d, want 2/2", report.Total, report.Completed)
	}
	if len(report.Members) != 2 {
		t.Fatalf("report members = %d, want 2", len(report.Members))
	}
	for i, item := range []string{"a", "b"} {
		m := report.Members[i]
		if m.Item != item || m.Status != string(harness.RunStatusCompleted) {
			t.Errorf("member %d = item:%q status:%q, want %q completed", i, m.Item, m.Status, item)
		}
		if m.Resumed {
			t.Errorf("member %d Resumed = true, want false (new member)", i)
		}
	}
}

// TestToolSwarmRunnerMapsResume verifies resume_agent_ids pass through the
// adapter into steering and that the resumed marker survives the round trip.
func TestToolSwarmRunnerMapsResume(t *testing.T) {
	t.Parallel()

	fake := newFakeSwarmManager()
	fake.seed("sub-1", "run-1", string(harness.RunStatusRunning))
	fake.release()
	steerer := &fakeRunSteerer{}
	swarm := NewSwarm(fake, WithSwarmSteerer(steerer))
	runner := NewToolSwarmRunner(swarm)

	report, err := runner.RunSwarm(context.Background(), tools.SwarmRequest{
		PromptTemplate: "do {{item}}",
		Items:          []string{"a", "b"},
		ResumeAgentIDs: []string{"sub-1"},
	})
	if err != nil {
		t.Fatalf("RunSwarm error = %v, want nil", err)
	}
	if steerer.callCount() != 1 || steerer.call(0).message != "do a" {
		t.Fatalf("steer calls = %d, want 1 with expanded prompt", steerer.callCount())
	}
	if len(report.Members) != 2 {
		t.Fatalf("report members = %d, want 2", len(report.Members))
	}
	resumed := report.Members[1]
	if !resumed.Resumed || resumed.ID != "sub-1" || resumed.Item != "a" {
		t.Fatalf("resumed member = id:%q resumed:%v item:%q, want sub-1/true/a", resumed.ID, resumed.Resumed, resumed.Item)
	}
	if report.Members[0].Resumed {
		t.Fatal("new member marked resumed, want false")
	}
}

// TestToolSwarmRunnerPropagatesValidationError verifies swarm validation
// failures surface through the adapter untouched.
func TestToolSwarmRunnerPropagatesValidationError(t *testing.T) {
	t.Parallel()

	swarm := NewSwarm(newFakeSwarmManager())
	runner := NewToolSwarmRunner(swarm)
	_, err := runner.RunSwarm(context.Background(), tools.SwarmRequest{
		PromptTemplate: "no placeholder",
		Items:          []string{"a"},
	})
	if err == nil {
		t.Fatal("RunSwarm error = nil, want validation error")
	}
}
