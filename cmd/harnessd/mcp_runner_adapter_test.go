package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
	htools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/mcpserver"
	runstore "go-agent-harness/internal/store"
)

func TestMCPRunnerAdapterRunStatusListAndConversation(t *testing.T) {
	t.Parallel()

	store := runstore.NewMemoryStore()
	runner := harness.NewRunner(&noopProvider{}, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     1,
		Store:        store,
	})
	adapter := &mcpRunnerAdapter{runner: runner, store: store}

	runID, err := adapter.StartRun("hello via mcp")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	status := waitForMCPRunStatus(t, adapter, runID, "completed")
	if status.Output != "ok" {
		t.Fatalf("status output = %q, want ok", status.Output)
	}

	runs, err := adapter.ListRuns()
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	found := false
	for _, run := range runs {
		if run.ID == runID && run.Status == "completed" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("stored run %q not found in ListRuns: %+v", runID, runs)
	}

	emptyList, err := (&mcpRunnerAdapter{runner: runner}).ListRuns()
	if err != nil {
		t.Fatalf("ListRuns without store: %v", err)
	}
	if len(emptyList) != 0 {
		t.Fatalf("ListRuns without store length = %d, want 0", len(emptyList))
	}

	conversationRun, err := runner.StartRun(harness.RunRequest{
		Prompt:         "remember this",
		ConversationID: "conv-mcp",
	})
	if err != nil {
		t.Fatalf("direct StartRun with conversation: %v", err)
	}
	waitForMCPRunStatus(t, adapter, conversationRun.ID, "completed")
	messages, ok := adapter.ConversationMessages("conv-mcp")
	if !ok {
		t.Fatal("ConversationMessages: expected conversation")
	}
	if len(messages) == 0 {
		t.Fatal("ConversationMessages returned no messages")
	}
	if messages[0].Role == "" || messages[0].Content == "" {
		t.Fatalf("unexpected conversation message: %+v", messages[0])
	}

	if _, err := adapter.GetRunStatus("missing-run"); err == nil {
		t.Fatal("expected missing run status error")
	}
	if _, ok := adapter.ConversationMessages("missing-conv"); ok {
		t.Fatal("expected missing conversation lookup to return false")
	}
}

func TestMCPRunnerAdapterSteerRun(t *testing.T) {
	t.Parallel()

	provider := &blockingMCPProvider{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	runner := harness.NewRunner(provider, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     1,
	})
	adapter := &mcpRunnerAdapter{runner: runner}

	runID, err := adapter.StartRun("long run")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not start")
	}
	if err := adapter.SteerRun(runID, "adjust course"); err != nil {
		t.Fatalf("SteerRun: %v", err)
	}
	close(provider.release)
	waitForMCPRunStatus(t, adapter, runID, "completed")
}

func TestMCPRunnerAdapterSubmitUserInput(t *testing.T) {
	t.Parallel()

	provider := &scriptedHarnessdProvider{turns: []harness.CompletionResult{
		{
			ToolCalls: []harness.ToolCall{{
				ID:        "call_ask_mcp",
				Name:      htools.AskUserQuestionToolName,
				Arguments: `{"questions":[{"question":"Where next?","header":"Route","options":[{"label":"Docs","description":"Read docs"},{"label":"Code","description":"Read code"}],"multiSelect":false}]}`,
			}},
		},
		{Content: "resumed"},
	}}
	broker := harness.NewInMemoryAskUserQuestionBroker(time.Now)
	runner := harness.NewRunner(provider, harness.NewDefaultRegistryWithOptions(t.TempDir(), harness.DefaultRegistryOptions{
		ApprovalMode:   harness.ToolApprovalModeFullAuto,
		AskUserBroker:  broker,
		AskUserTimeout: 2 * time.Second,
	}), harness.RunnerConfig{
		DefaultModel:   "test-model",
		MaxSteps:       4,
		AskUserBroker:  broker,
		AskUserTimeout: 2 * time.Second,
	})
	adapter := &mcpRunnerAdapter{runner: runner}

	runID, err := adapter.StartRun("needs operator input")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForMCPRunStatus(t, adapter, runID, string(harness.RunStatusWaitingForUser))

	if err := adapter.SubmitUserInput(runID, "Docs"); err != nil {
		t.Fatalf("SubmitUserInput: %v", err)
	}
	status := waitForMCPRunStatus(t, adapter, runID, "completed")
	if status.Output != "resumed" {
		t.Fatalf("output = %q, want resumed", status.Output)
	}

	if err := adapter.SubmitUserInput(runID, "Docs"); err == nil || !strings.Contains(err.Error(), "no pending input") {
		t.Fatalf("expected no pending input error after completion, got %v", err)
	}
}

type blockingMCPProvider struct {
	started chan struct{}
	release chan struct{}
}

func (p *blockingMCPProvider) Complete(ctx context.Context, _ harness.CompletionRequest) (harness.CompletionResult, error) {
	select {
	case <-p.started:
	default:
		close(p.started)
	}
	select {
	case <-p.release:
		return harness.CompletionResult{Content: "released"}, nil
	case <-ctx.Done():
		return harness.CompletionResult{}, ctx.Err()
	}
}

func waitForMCPRunStatus(t *testing.T, adapter *mcpRunnerAdapter, runID, want string) mcpserver.RunStatus {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last mcpserver.RunStatus
	for time.Now().Before(deadline) {
		status, err := adapter.GetRunStatus(runID)
		if err != nil {
			t.Fatalf("GetRunStatus(%q): %v", runID, err)
		}
		last = status
		if status.Status == want {
			return last
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for run %s status %q; last=%+v", runID, want, last)
	return last
}
