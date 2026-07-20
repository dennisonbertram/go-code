package harness

import (
	"strings"
	"testing"
)

// This file pins the model-facing plan-mode guidance (epic #819, slice 2):
// while a run is in plan mode, every outgoing provider request carries a
// plan-mode block that tells the model it is in read-only planning, names the
// resolved plan file as the only writable target, and explains how to present
// the plan (optionally with 1-3 labeled approaches). The block must be absent
// for non-plan runs. The denial-feedback message names the plan file.
//
// Note: these tests use the in-package capturingProvider rather than
// internal/fakeprovider because fakeprovider imports this package (an import
// cycle for in-package tests); capturingProvider records the same outgoing
// CompletionRequests.

// planModeSystemBlocks returns the contents of all system-role messages in an
// outgoing request that look like plan-mode guidance (they mention the plan
// file marker).
func planModeSystemBlocks(req CompletionRequest) []string {
	var out []string
	for _, m := range req.Messages {
		if m.Role == "system" && strings.Contains(m.Content, "designated plan file") {
			out = append(out, m.Content)
		}
	}
	return out
}

// TestPlanModeGuidanceInjectedIntoOutgoingMessages asserts that a plan-mode
// run's provider requests carry the guidance block in every turn, and that the
// block names the resolved default plan file and the key rules.
func TestPlanModeGuidanceInjectedIntoOutgoingMessages(t *testing.T) {
	provider := &capturingProvider{turns: []CompletionResult{{Content: "# proposed plan"}}}
	broker := NewInMemoryApprovalBroker()
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:        "test",
		DefaultSystemPrompt: "You are helpful.",
		ApprovalBroker:      broker,
	})
	run, err := runner.StartRun(RunRequest{Prompt: "plan the work", PlanMode: true})
	if err != nil {
		t.Fatal(err)
	}
	waitPlanBrokerPending(t, broker, run.ID)

	provider.mu.Lock()
	calls := append([]CompletionRequest(nil), provider.calls...)
	provider.mu.Unlock()
	if len(calls) == 0 {
		t.Fatal("provider never called")
	}
	for i, req := range calls {
		blocks := planModeSystemBlocks(req)
		if len(blocks) != 1 {
			t.Fatalf("request %d: want exactly one plan-mode guidance block, got %d; system messages: %#v", i, len(blocks), req.Messages)
		}
		block := blocks[0]
		for _, want := range []string{
			"plan mode",
			defaultPlanFile, // snapshot: the resolved plan file path appears verbatim
			"plan_mode_denied",
			"## Approaches",
		} {
			if !strings.Contains(strings.ToLower(block), strings.ToLower(want)) {
				t.Fatalf("request %d: guidance block missing %q; block:\n%s", i, want, block)
			}
		}
	}

	// Clean up: approve the exit so the run finishes.
	if err := broker.Approve(run.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)
}

// TestPlanModeGuidanceNamesCustomPlanFile asserts the guidance block names the
// run's resolved custom plan file instead of the default.
func TestPlanModeGuidanceNamesCustomPlanFile(t *testing.T) {
	provider := &capturingProvider{turns: []CompletionResult{{Content: "# proposed plan"}}}
	broker := NewInMemoryApprovalBroker()
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:        "test",
		DefaultSystemPrompt: "You are helpful.",
		ApprovalBroker:      broker,
	})
	run, err := runner.StartRun(RunRequest{Prompt: "plan the work", PlanMode: true, PlanFile: "docs/plans/custom-plan.md"})
	if err != nil {
		t.Fatal(err)
	}
	waitPlanBrokerPending(t, broker, run.ID)

	provider.mu.Lock()
	calls := append([]CompletionRequest(nil), provider.calls...)
	provider.mu.Unlock()
	if len(calls) == 0 {
		t.Fatal("provider never called")
	}
	blocks := planModeSystemBlocks(calls[0])
	if len(blocks) != 1 {
		t.Fatalf("want exactly one plan-mode guidance block, got %d", len(blocks))
	}
	if !strings.Contains(blocks[0], "docs/plans/custom-plan.md") {
		t.Fatalf("guidance block does not name the custom plan file; block:\n%s", blocks[0])
	}
	if strings.Contains(blocks[0], defaultPlanFile) {
		t.Fatalf("guidance block names the default plan file for a custom-plan run; block:\n%s", blocks[0])
	}

	if err := broker.Approve(run.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)
}

// TestPlanModeGuidanceAbsentWhenPlanModeDisabled is the regression guard: a
// run without plan mode must not carry the guidance block.
func TestPlanModeGuidanceAbsentWhenPlanModeDisabled(t *testing.T) {
	provider := &capturingProvider{turns: []CompletionResult{{Content: "done"}}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:        "test",
		DefaultSystemPrompt: "You are helpful.",
	})
	run, err := runner.StartRun(RunRequest{Prompt: "just do it"})
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	provider.mu.Lock()
	calls := append([]CompletionRequest(nil), provider.calls...)
	provider.mu.Unlock()
	if len(calls) == 0 {
		t.Fatal("provider never called")
	}
	for i, req := range calls {
		if blocks := planModeSystemBlocks(req); len(blocks) != 0 {
			t.Fatalf("request %d: plan-mode guidance block present in a non-plan run: %q", i, blocks[0])
		}
	}
}

// TestPlanModeDenialFeedbackNamesPlanFile asserts the operator-denial feedback
// message reminds the model which plan file to revise.
func TestPlanModeDenialFeedbackNamesPlanFile(t *testing.T) {
	provider := &capturingProvider{turns: []CompletionResult{{Content: "# plan v1"}, {Content: "# plan v2"}}}
	broker := NewInMemoryApprovalBroker()
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:        "test",
		DefaultSystemPrompt: "You are helpful.",
		ApprovalBroker:      broker,
	})
	run, err := runner.StartRun(RunRequest{Prompt: "plan the work", PlanMode: true})
	if err != nil {
		t.Fatal(err)
	}
	waitPlanBrokerPending(t, broker, run.ID)
	if err := broker.Deny(run.ID); err != nil {
		t.Fatalf("deny: %v", err)
	}
	waitForUserMessageContaining(t, runner, run.ID, "operator requested changes")

	found := false
	for _, m := range runner.GetRunMessages(run.ID) {
		if m.Role == "user" && strings.Contains(m.Content, "operator requested changes") && strings.Contains(m.Content, defaultPlanFile) {
			found = true
		}
	}
	if !found {
		t.Fatalf("denial feedback message does not name the plan file %q; messages: %#v", defaultPlanFile, runner.GetRunMessages(run.ID))
	}

	// Clean up: the revised plan is presented again; approve to finish.
	waitPlanBrokerPending(t, broker, run.ID)
	if err := broker.Approve(run.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)
}
