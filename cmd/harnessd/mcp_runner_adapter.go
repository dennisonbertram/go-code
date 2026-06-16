package main

import (
	"context"
	"fmt"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/mcpserver"
	istore "go-agent-harness/internal/store"
)

// mcpRunnerAdapter adapts harness.Runner to mcpserver.RunnerInterface.
// It uses an optional store.Store for ListRuns; when nil, ListRuns returns an
// empty result. SteerRun delegates directly. SubmitUserInput resolves the
// pending question keys from the runner's AskUserBroker and maps the single
// input string onto every question.
type mcpRunnerAdapter struct {
	runner *harness.Runner
	store  istore.Store // optional; enables ListRuns
}

// compile-time interface check
var _ mcpserver.RunnerInterface = (*mcpRunnerAdapter)(nil)

func (a *mcpRunnerAdapter) StartRun(prompt string) (string, error) {
	run, err := a.runner.StartRun(harness.RunRequest{Prompt: prompt})
	if err != nil {
		return "", err
	}
	return run.ID, nil
}

func (a *mcpRunnerAdapter) GetRunStatus(runID string) (mcpserver.RunStatus, error) {
	run, ok := a.runner.GetRun(runID)
	if !ok {
		return mcpserver.RunStatus{}, fmt.Errorf("run %q not found", runID)
	}
	return mcpserver.RunStatus{
		ID:     run.ID,
		Status: string(run.Status),
		Output: run.Output,
		Error:  run.Error,
	}, nil
}

func (a *mcpRunnerAdapter) ListRuns() ([]mcpserver.RunStatus, error) {
	if a.store == nil {
		return nil, nil
	}
	// Use store-backed list so completed runs that have been evicted from the
	// in-memory map are still visible to MCP clients.
	stored, err := a.store.ListRuns(context.Background(), istore.RunFilter{})
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	out := make([]mcpserver.RunStatus, 0, len(stored))
	for _, r := range stored {
		out = append(out, mcpserver.RunStatus{
			ID:     r.ID,
			Status: string(r.Status),
			Output: r.Output,
			Error:  r.Error,
		})
	}
	return out, nil
}

func (a *mcpRunnerAdapter) SteerRun(runID string, message string) error {
	return a.runner.SteerRun(runID, message)
}

func (a *mcpRunnerAdapter) SubmitUserInput(runID string, input string) error {
	pending, err := a.runner.PendingInput(runID)
	if err != nil {
		return err
	}
	answers := make(map[string]string, len(pending.Questions))
	for _, q := range pending.Questions {
		answers[q.Question] = input
	}
	return a.runner.SubmitInput(runID, answers)
}

func (a *mcpRunnerAdapter) ConversationMessages(conversationID string) ([]mcpserver.ConversationMessage, bool) {
	msgs, ok := a.runner.ConversationMessages(conversationID)
	if !ok {
		return nil, false
	}
	out := make([]mcpserver.ConversationMessage, len(msgs))
	for i, m := range msgs {
		out[i] = mcpserver.ConversationMessage{
			Role:    m.Role,
			Content: m.Content,
		}
	}
	return out, true
}
