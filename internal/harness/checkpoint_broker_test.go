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
		approved, err := broker.Ask(context.Background(), ApprovalRequest{
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

	done := make(chan error, 1)
	go func() {
		approved, err := broker.Ask(context.Background(), ApprovalRequest{
			RunID:   "run-deny",
			CallID:  "call-deny",
			Tool:    "write",
			Args:    `{"path":"README.md"}`,
			Timeout: time.Minute,
		})
		if err != nil {
			done <- err
			return
		}
		if approved {
			done <- context.Canceled
			return
		}
		done <- nil
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := broker.Pending("run-deny"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for pending approval")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := broker.Deny("run-deny"); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Ask completion: %v", err)
	}
	if err := broker.Deny("run-deny"); err != ErrNoPendingApproval {
		t.Fatalf("Deny without pending = %v, want %v", err, ErrNoPendingApproval)
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
