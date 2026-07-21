package harness

import (
	"strings"
	"testing"
	"time"
)

// This file pins the plan-exit approach options flow (epic #819, slice 3):
// the agent may offer 1-3 approach options in a trailing "## Approaches"
// section of the presented plan; awaitPlanApproval extracts them into the
// plan.approval_required payload and the broker request; the operator may
// approve with a chosen option, which is echoed in plan.approval_granted and
// relayed to the model as the operator's choice. Plans without a valid
// Approaches section behave exactly as before (no options anywhere).

func TestParsePlanApproaches(t *testing.T) {
	for _, tc := range []struct {
		name  string
		plan  string
		want  []PlanApproachOption
		wantN int // expected len; want may be nil
	}{
		{
			name: "numbered list with em-dash descriptions",
			plan: "# Plan\n\nDo the thing.\n\n## Approaches\n\n1. Incremental — migrate piece by piece\n2. Big bang — rewrite in one pass\n",
			want: []PlanApproachOption{
				{ID: "a", Label: "Incremental", Description: "migrate piece by piece"},
				{ID: "b", Label: "Big bang", Description: "rewrite in one pass"},
			},
			wantN: 2,
		},
		{
			name: "bullets with colon and bold labels",
			plan: "## Approaches\n- **Safe**: keep the old path\n- **Fast**: cut over now\n- **Hybrid**: both\n",
			want: []PlanApproachOption{
				{ID: "a", Label: "Safe", Description: "keep the old path"},
				{ID: "b", Label: "Fast", Description: "cut over now"},
				{ID: "c", Label: "Hybrid", Description: "both"},
			},
			wantN: 3,
		},
		{
			name:  "label only items get empty description",
			plan:  "## Approaches\n1. Minimal\n",
			want:  []PlanApproachOption{{ID: "a", Label: "Minimal"}},
			wantN: 1,
		},
		{
			name:  "content after the next heading is ignored",
			plan:  "## Approaches\n1. A — first\n\n## Risks\n1. Not an option\n",
			want:  []PlanApproachOption{{ID: "a", Label: "A", Description: "first"}},
			wantN: 1,
		},
		{name: "no approaches section", plan: "# Plan\n\nJust do it.\n", wantN: 0},
		{name: "empty approaches section", plan: "# Plan\n\n## Approaches\n\nNothing here.\n", wantN: 0},
		{name: "more than three items rejected", plan: "## Approaches\n1. A\n2. B\n3. C\n4. D\n", wantN: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parsePlanApproaches(tc.plan)
			if len(got) != tc.wantN {
				t.Fatalf("parsePlanApproaches() returned %d options %#v, want %d", len(got), got, tc.wantN)
			}
			for i, want := range tc.want {
				if got[i] != want {
					t.Errorf("option %d = %#v, want %#v", i, got[i], want)
				}
			}
		})
	}
}

// TestPlanApprovalRequiredCarriesOptions asserts the options flow from the
// presented plan into the plan.approval_required event payload and the
// broker's pending approval.
func TestPlanApprovalRequiredCarriesOptions(t *testing.T) {
	plan := "# Plan\n\nBuild it.\n\n## Approaches\n\n1. Incremental — migrate piece by piece\n2. Big bang — rewrite in one pass\n"
	provider := &capturingProvider{turns: []CompletionResult{{Content: plan}}}
	broker := NewInMemoryApprovalBroker()
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:   "test",
		ApprovalBroker: broker,
	})
	run, err := runner.StartRun(RunRequest{Prompt: "plan", PlanMode: true})
	if err != nil {
		t.Fatal(err)
	}
	waitPlanBrokerPending(t, broker, run.ID)

	pending, ok := broker.Pending(run.ID)
	if !ok {
		t.Fatal("no pending approval")
	}
	if len(pending.Options) != 2 {
		t.Fatalf("pending options = %#v, want 2", pending.Options)
	}
	if pending.Options[0].ID != "a" || pending.Options[0].Label != "Incremental" || pending.Options[1].ID != "b" {
		t.Fatalf("pending options malformed: %#v", pending.Options)
	}

	required := findEventByType(collectEvents(t, runner, run.ID), EventPlanApprovalRequired)
	if required == nil {
		t.Fatal("plan.approval_required not emitted")
	}
	raw, ok := required.Payload["options"].([]PlanApproachOption)
	if !ok || len(raw) != 2 {
		t.Fatalf("plan.approval_required options = %#v, want 2 PlanApproachOption", required.Payload["options"])
	}
	if raw[0].Label != "Incremental" || raw[1].Description != "rewrite in one pass" {
		t.Fatalf("payload options malformed: %#v", raw)
	}

	if err := broker.Approve(run.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)
}

// TestPlanApprovalRequiredOmitsOptionsWithoutApproaches is the regression
// guard: a plan with no Approaches section must produce no options key at all,
// so a no-option plan works exactly as before slice 3.
func TestPlanApprovalRequiredOmitsOptionsWithoutApproaches(t *testing.T) {
	provider := &capturingProvider{turns: []CompletionResult{{Content: "# Plan\n\nJust do it.\n"}}}
	broker := NewInMemoryApprovalBroker()
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:   "test",
		ApprovalBroker: broker,
	})
	run, err := runner.StartRun(RunRequest{Prompt: "plan", PlanMode: true})
	if err != nil {
		t.Fatal(err)
	}
	waitPlanBrokerPending(t, broker, run.ID)

	pending, ok := broker.Pending(run.ID)
	if !ok {
		t.Fatal("no pending approval")
	}
	if len(pending.Options) != 0 {
		t.Fatalf("pending options = %#v, want none", pending.Options)
	}
	required := findEventByType(collectEvents(t, runner, run.ID), EventPlanApprovalRequired)
	if required == nil {
		t.Fatal("plan.approval_required not emitted")
	}
	if opts, present := required.Payload["options"]; present {
		t.Fatalf("plan.approval_required carried options %#v for a no-option plan", opts)
	}

	if err := broker.Approve(run.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)
	granted := findEventByType(collectEvents(t, runner, run.ID), EventPlanApprovalGranted)
	if granted == nil {
		t.Fatal("plan.approval_granted not emitted")
	}
	if opt, present := granted.Payload["option"]; present {
		t.Fatalf("plan.approval_granted carried option %q for a plain approve", opt)
	}
}

// TestPlanExitApproveWithOptionRoundTrip asserts the operator's selected
// option comes back through the broker Ask, is echoed in
// plan.approval_granted, and is relayed to the model in the transcript.
func TestPlanExitApproveWithOptionRoundTrip(t *testing.T) {
	plan := "# Plan\n\nBuild it.\n\n## Approaches\n\n1. Incremental — migrate piece by piece\n2. Big bang — rewrite in one pass\n"
	provider := &capturingProvider{turns: []CompletionResult{{Content: plan}}}
	broker := NewInMemoryApprovalBroker()
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:   "test",
		ApprovalBroker: broker,
	})
	run, err := runner.StartRun(RunRequest{Prompt: "plan", PlanMode: true})
	if err != nil {
		t.Fatal(err)
	}
	waitPlanBrokerPending(t, broker, run.ID)

	if err := broker.ApproveWithOption(run.ID, "b"); err != nil {
		t.Fatalf("ApproveWithOption: %v", err)
	}
	if got := waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed); got != RunStatusCompleted {
		t.Fatalf("status=%q after approve-with-option, want completed", got)
	}
	waitPlanModeState(t, runner, run.ID, PlanModeInactive)

	granted := findEventByType(collectEvents(t, runner, run.ID), EventPlanApprovalGranted)
	if granted == nil {
		t.Fatal("plan.approval_granted not emitted")
	}
	if got, _ := granted.Payload["option"].(string); got != "b" {
		t.Fatalf("plan.approval_granted option = %q, want %q", got, "b")
	}
	if got, _ := granted.Payload["option_label"].(string); got != "Big bang" {
		t.Fatalf("plan.approval_granted option_label = %q, want %q", got, "Big bang")
	}

	// The operator's choice is relayed to the model in the transcript so a
	// continuation knows which approach to follow.
	relayed := false
	for _, m := range runner.GetRunMessages(run.ID) {
		if m.Role == "user" && strings.Contains(m.Content, "Big bang") && strings.Contains(m.Content, "selected approach") {
			relayed = true
		}
	}
	if !relayed {
		t.Fatalf("selected approach not relayed to the model; messages: %#v", runner.GetRunMessages(run.ID))
	}
}

// TestInMemoryApprovalBrokerOptionsRoundTrip is the broker-level pin: options
// travel from the Ask request to Pending, and the selected option from
// ApproveWithOption back to the blocked Ask caller.
func TestInMemoryApprovalBrokerOptionsRoundTrip(t *testing.T) {
	broker := NewInMemoryApprovalBroker()
	options := []PlanApproachOption{{ID: "a", Label: "One"}, {ID: "b", Label: "Two"}}
	type askResult struct {
		approved bool
		option   string
		err      error
	}
	resultCh := make(chan askResult, 1)
	go func() {
		approved, option, err := broker.Ask(t.Context(), ApprovalRequest{RunID: "run-1", CallID: "plan_exit", Tool: "plan_exit", Options: options})
		resultCh <- askResult{approved: approved, option: option, err: err}
	}()

	deadline := time.Now().Add(4 * time.Second)
	for {
		if _, ok := broker.Pending("run-1"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Ask never registered the pending approval")
		}
		time.Sleep(10 * time.Millisecond)
	}
	pending, ok := broker.Pending("run-1")
	if !ok {
		t.Fatal("no pending approval")
	}
	if len(pending.Options) != 2 || pending.Options[1].ID != "b" {
		t.Fatalf("pending options = %#v", pending.Options)
	}
	if err := broker.ApproveWithOption("run-1", "b"); err != nil {
		t.Fatalf("ApproveWithOption: %v", err)
	}
	res := <-resultCh
	if res.err != nil {
		t.Fatalf("Ask error: %v", res.err)
	}
	if !res.approved || res.option != "b" {
		t.Fatalf("Ask returned approved=%v option=%q, want approved=true option=%q", res.approved, res.option, "b")
	}
}
