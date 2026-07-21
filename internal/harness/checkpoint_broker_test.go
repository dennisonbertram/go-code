package harness

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go-agent-harness/internal/checkpoints"
	htools "go-agent-harness/internal/harness/tools"
)

func TestCheckpointApprovalBrokerPersistsPendingApproval(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	checkpointSvc := checkpoints.NewService(checkpoints.NewMemoryStore(), func() time.Time { return now })
	broker := NewCheckpointApprovalBroker(checkpointSvc)

	done := make(chan error, 1)
	go func() {
		approved, _, err := broker.Ask(context.Background(), ApprovalRequest{
			RunID:   "run-1",
			CallID:  "call-1",
			Tool:    "write",
			Args:    `{"path":"README.md"}`,
			Timeout: time.Minute,
		})
		if err != nil {
			done <- err
			return
		}
		if !approved {
			done <- context.Canceled
			return
		}
		done <- nil
	}()

	var pending ApprovalPendingView
	deadline := time.Now().Add(2 * time.Second)
	for {
		current, ok := broker.Pending("run-1")
		if ok {
			pending = ApprovalPendingView(current)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for pending approval")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if pending.Tool != "write" {
		t.Fatalf("pending tool = %q, want write", pending.Tool)
	}
	record, ok, err := checkpointSvc.PendingByRun(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("PendingByRun: %v", err)
	}
	if !ok {
		t.Fatal("expected persisted checkpoint")
	}
	if record.Kind != checkpoints.KindApproval {
		t.Fatalf("kind = %q, want %q", record.Kind, checkpoints.KindApproval)
	}

	if err := broker.Approve("run-1"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Ask completion: %v", err)
	}
}

func TestCheckpointApprovalBrokerDenyRejectsPendingApproval(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	checkpointSvc := checkpoints.NewService(checkpoints.NewMemoryStore(), func() time.Time { return now })
	broker := NewCheckpointApprovalBroker(checkpointSvc)

	resultCh := make(chan bool, 1)
	errCh := make(chan error, 1)
	go func() {
		approved, _, err := broker.Ask(context.Background(), ApprovalRequest{
			RunID:   "run-denied",
			CallID:  "call-denied",
			Tool:    "write",
			Args:    `{"path":"blocked.txt"}`,
			Timeout: time.Minute,
		})
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- approved
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := broker.Pending("run-denied"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for pending approval")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := broker.Deny("run-denied"); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	select {
	case err := <-errCh:
		t.Fatalf("Ask returned error: %v", err)
	case approved := <-resultCh:
		if approved {
			t.Fatal("denied approval returned approved=true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for denied approval result")
	}
	if err := broker.Deny("run-denied"); err != ErrNoPendingApproval {
		t.Fatalf("Deny after resolution = %v, want ErrNoPendingApproval", err)
	}
}

func TestCheckpointAskUserBrokerPersistsQuestionsAndAnswers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	checkpointSvc := checkpoints.NewService(checkpoints.NewMemoryStore(), func() time.Time { return now })
	broker := NewCheckpointAskUserQuestionBroker(checkpointSvc, func() time.Time { return now })

	done := make(chan error, 1)
	go func() {
		answers, answeredAt, err := broker.Ask(context.Background(), htools.AskUserQuestionRequest{
			RunID:  "run-ask",
			CallID: "call-ask",
			Questions: []htools.AskUserQuestion{{
				Question: "Where next?",
				Header:   "Route",
				Options: []htools.AskUserQuestionOption{
					{Label: "Docs", Description: "Read docs"},
					{Label: "Code", Description: "Read code"},
				},
			}},
			Timeout: time.Minute,
		})
		if err != nil {
			done <- err
			return
		}
		if answeredAt != now {
			done <- context.Canceled
			return
		}
		if answers["Where next?"] != "Docs" {
			done <- context.Canceled
			return
		}
		done <- nil
	}()

	var pending htools.AskUserQuestionPending
	deadline := time.Now().Add(2 * time.Second)
	for {
		current, ok := broker.Pending("run-ask")
		if ok {
			pending = current
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for pending question")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if pending.CallID != "call-ask" {
		t.Fatalf("pending call id = %q, want call-ask", pending.CallID)
	}
	record, ok, err := checkpointSvc.PendingByRun(context.Background(), "run-ask")
	if err != nil {
		t.Fatalf("PendingByRun: %v", err)
	}
	if !ok {
		t.Fatal("expected persisted checkpoint")
	}
	if record.Kind != checkpoints.KindUserInput {
		t.Fatalf("kind = %q, want %q", record.Kind, checkpoints.KindUserInput)
	}
	var questions []htools.AskUserQuestion
	if err := json.Unmarshal([]byte(record.Questions), &questions); err != nil {
		t.Fatalf("unmarshal questions: %v", err)
	}
	if len(questions) != 1 || questions[0].Question != "Where next?" {
		t.Fatalf("unexpected persisted questions: %+v", questions)
	}

	if err := broker.Submit("run-ask", map[string]string{"Where next?": "Docs"}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Ask completion: %v", err)
	}
}

type ApprovalPendingView PendingApproval

// TestCheckpointApprovalBrokerOptionsRoundTrip proves plan approach options
// survive the checkpoint-backed broker: they are persisted on the pending
// record, and the operator's selected option comes back to the blocked Ask.
func TestCheckpointApprovalBrokerOptionsRoundTrip(t *testing.T) {
	t.Parallel()

	checkpointSvc := checkpoints.NewService(checkpoints.NewMemoryStore(), time.Now)
	broker := NewCheckpointApprovalBroker(checkpointSvc)
	options := []PlanApproachOption{{ID: "a", Label: "One"}, {ID: "b", Label: "Two"}}

	type askResult struct {
		approved bool
		option   string
		err      error
	}
	resultCh := make(chan askResult, 1)
	go func() {
		approved, option, err := broker.Ask(context.Background(), ApprovalRequest{
			RunID:   "run-opts",
			CallID:  "plan_exit",
			Tool:    "plan_exit",
			Options: options,
			Timeout: time.Minute,
		})
		resultCh <- askResult{approved: approved, option: option, err: err}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := broker.Pending("run-opts"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for pending approval")
		}
		time.Sleep(10 * time.Millisecond)
	}
	pending, ok := broker.Pending("run-opts")
	if !ok {
		t.Fatal("no pending approval")
	}
	if len(pending.Options) != 2 || pending.Options[0].ID != "a" || pending.Options[1].Label != "Two" {
		t.Fatalf("pending options = %#v", pending.Options)
	}

	if err := broker.ApproveWithOption("run-opts", "b"); err != nil {
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
