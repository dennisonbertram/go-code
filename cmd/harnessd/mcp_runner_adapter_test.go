package main

import (
	"context"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
)

func TestMCPRunnerAdapterDelegatesRunnerOperations(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(&noopProvider{}, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel: "gpt-test",
		MaxSteps:     1,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = runner.Shutdown(ctx)
	})

	adapter := &mcpRunnerAdapter{runner: runner}
	runID, err := adapter.StartRun("hello from mcp")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if runID == "" {
		t.Fatal("expected non-empty run id")
	}

	status, err := adapter.GetRunStatus(runID)
	if err != nil {
		t.Fatalf("GetRunStatus: %v", err)
	}
	if status.ID != runID {
		t.Fatalf("status id = %q, want %q", status.ID, runID)
	}

	runs, err := adapter.ListRuns()
	if err != nil {
		t.Fatalf("ListRuns without store: %v", err)
	}
	if runs != nil {
		t.Fatalf("expected nil runs without store, got %#v", runs)
	}

	if err := adapter.SteerRun("missing-run", "continue"); err == nil {
		t.Fatal("expected SteerRun to reject unknown run")
	}
	if err := adapter.SubmitUserInput("missing-run", "answer"); err == nil {
		t.Fatal("expected SubmitUserInput to reject unknown run")
	}
	if _, ok := adapter.ConversationMessages("missing-conversation"); ok {
		t.Fatal("expected missing conversation to be absent")
	}
}
