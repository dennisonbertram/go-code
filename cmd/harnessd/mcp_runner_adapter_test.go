package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/store"
)

func TestMCPRunnerAdapter_StartStatusAndConversationMessages(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(&noopProvider{}, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     1,
	})
	adapter := &mcpRunnerAdapter{runner: runner}

	runID, err := adapter.StartRun("write a status")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if runID == "" {
		t.Fatal("expected run ID")
	}
	waitHarnessdAdapterRun(t, runner, runID)

	status, err := adapter.GetRunStatus(runID)
	if err != nil {
		t.Fatalf("GetRunStatus: %v", err)
	}
	if status.ID != runID {
		t.Fatalf("status ID = %q, want %q", status.ID, runID)
	}
	if status.Status != string(harness.RunStatusCompleted) {
		t.Fatalf("status = %q, want completed", status.Status)
	}
	if status.Output != "ok" {
		t.Fatalf("output = %q, want ok", status.Output)
	}

	messages, ok := adapter.ConversationMessages(runID)
	if !ok {
		t.Fatal("expected conversation messages")
	}
	if len(messages) < 2 {
		t.Fatalf("expected user and assistant messages, got %d", len(messages))
	}
	if messages[0].Role != "user" || messages[0].Content != "write a status" {
		t.Fatalf("first message = %#v", messages[0])
	}
	if messages[len(messages)-1].Role != "assistant" || messages[len(messages)-1].Content != "ok" {
		t.Fatalf("last message = %#v", messages[len(messages)-1])
	}
}

func TestMCPRunnerAdapter_GetRunStatusMissing(t *testing.T) {
	t.Parallel()

	adapter := &mcpRunnerAdapter{runner: harness.NewRunner(&noopProvider{}, harness.NewRegistry(), harness.RunnerConfig{})}
	_, err := adapter.GetRunStatus("run_missing")
	if err == nil || !strings.Contains(err.Error(), "run_missing") {
		t.Fatalf("GetRunStatus missing error = %v", err)
	}
}

func TestMCPRunnerAdapter_ListRunsNilAndStoreBacked(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(&noopProvider{}, harness.NewRegistry(), harness.RunnerConfig{})
	adapter := &mcpRunnerAdapter{runner: runner}
	runs, err := adapter.ListRuns()
	if err != nil {
		t.Fatalf("ListRuns nil store: %v", err)
	}
	if runs != nil {
		t.Fatalf("nil store ListRuns = %#v, want nil", runs)
	}

	st := store.NewMemoryStore()
	now := time.Now().UTC()
	if err := st.CreateRun(context.Background(), &store.Run{
		ID:        "run_store",
		Model:     "gpt-4.1-mini",
		Prompt:    "stored",
		Status:    store.RunStatusFailed,
		Output:    "partial",
		Error:     "boom",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	adapter.store = st

	runs, err = adapter.ListRuns()
	if err != nil {
		t.Fatalf("ListRuns store-backed: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].ID != "run_store" || runs[0].Status != string(store.RunStatusFailed) || runs[0].Output != "partial" || runs[0].Error != "boom" {
		t.Fatalf("mapped run = %#v", runs[0])
	}
}

func TestMCPRunnerAdapter_DelegatedErrorPaths(t *testing.T) {
	t.Parallel()

	adapter := &mcpRunnerAdapter{runner: harness.NewRunner(&noopProvider{}, harness.NewRegistry(), harness.RunnerConfig{})}
	if err := adapter.SteerRun("run_missing", "focus"); err == nil {
		t.Fatal("expected SteerRun to return runner error for missing run")
	}
	if err := adapter.SubmitUserInput("run_missing", "answer"); err == nil {
		t.Fatal("expected SubmitUserInput to return runner error for missing run")
	}
	if messages, ok := adapter.ConversationMessages("missing-conversation"); ok || messages != nil {
		t.Fatalf("missing ConversationMessages = %#v, %v; want nil, false", messages, ok)
	}
}

type failingListStore struct {
	store.Store
}

func (f failingListStore) ListRuns(context.Context, store.RunFilter) ([]*store.Run, error) {
	return nil, errors.New("list exploded")
}

func TestMCPRunnerAdapter_ListRunsWrapsStoreError(t *testing.T) {
	t.Parallel()

	adapter := &mcpRunnerAdapter{
		runner: harness.NewRunner(&noopProvider{}, harness.NewRegistry(), harness.RunnerConfig{}),
		store:  failingListStore{Store: store.NewMemoryStore()},
	}
	_, err := adapter.ListRuns()
	if err == nil || !strings.Contains(err.Error(), "list runs: list exploded") {
		t.Fatalf("ListRuns error = %v", err)
	}
}

func waitHarnessdAdapterRun(t *testing.T, runner *harness.Runner, runID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run, ok := runner.GetRun(runID)
		if ok {
			switch run.Status {
			case harness.RunStatusCompleted, harness.RunStatusFailed, harness.RunStatusCancelled:
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	run, _ := runner.GetRun(runID)
	t.Fatalf("run %s did not finish, last state %#v", runID, run)
}
