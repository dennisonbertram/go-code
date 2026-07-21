package subagents

import (
	"context"
	"fmt"
	"time"

	"go-agent-harness/internal/harness"
	tools "go-agent-harness/internal/harness/tools"
)

// InlineManager wraps a Manager and implements the tools.SubagentManager interface.
// It creates subagents with inline isolation and offers both blocking and
// non-blocking waits through shared helper logic.
type InlineManager struct {
	m Manager
}

// NewInlineManager wraps a Manager to implement tools.SubagentManager.
func NewInlineManager(m Manager) *InlineManager {
	return &InlineManager{m: m}
}

// CreateAndWait creates an inline subagent and blocks until it completes.
// It polls the manager's Get method until the subagent reaches a terminal status.
func (im *InlineManager) CreateAndWait(ctx context.Context, req tools.SubagentRequest) (tools.SubagentResult, error) {
	sa, err := im.Start(ctx, req)
	if err != nil {
		return tools.SubagentResult{}, err
	}
	return im.Wait(ctx, sa.ID)
}

// Start creates an inline subagent and returns immediately.
func (im *InlineManager) Start(ctx context.Context, req tools.SubagentRequest) (tools.SubagentResult, error) {
	// Map isolation mode from profile string to typed constant.
	isolation := IsolationInline
	if req.IsolationMode == string(IsolationWorktree) {
		isolation = IsolationWorktree
	}

	// Map cleanup policy from profile string to typed constant.
	// Default to DestroyOnCompletion for resource hygiene; profiles may override.
	cleanupPolicy := CleanupDestroyOnCompletion
	switch req.CleanupPolicy {
	case "keep":
		cleanupPolicy = CleanupPreserve
	case "delete":
		cleanupPolicy = CleanupDestroyOnCompletion
	case "delete_on_success":
		cleanupPolicy = CleanupDestroyOnSuccess
	}

	saReq := Request{
		Prompt:               req.Prompt,
		Model:                req.Model,
		SystemPrompt:         req.SystemPrompt,
		MaxSteps:             req.MaxSteps,
		MaxCostUSD:           req.MaxCostUSD,
		ReasoningEffort:      req.ReasoningEffort,
		AllowedTools:         append([]string(nil), req.AllowedTools...),
		DeniedTools:          append([]string(nil), req.DeniedTools...),
		ProfileName:          req.ProfileName,
		ParentContextHandoff: req.ParentContextHandoff,
		Isolation:            isolation,
		CleanupPolicy:        cleanupPolicy,
		BaseRef:              req.BaseRef,
	}

	sa, err := im.m.Create(ctx, saReq)
	if err != nil {
		return tools.SubagentResult{}, fmt.Errorf("create subagent: %w", err)
	}
	return im.toToolResult(sa), nil
}

// GetSubagent fetches the latest subagent state.
func (im *InlineManager) Get(ctx context.Context, id string) (tools.SubagentResult, error) {
	sa, err := im.m.Get(ctx, id)
	if err != nil {
		return tools.SubagentResult{}, fmt.Errorf("get subagent: %w", err)
	}
	return im.toToolResult(sa), nil
}

// Wait blocks until the subagent reaches a terminal status and returns the
// terminal result.
func (im *InlineManager) Wait(ctx context.Context, id string) (tools.SubagentResult, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return tools.SubagentResult{}, ctx.Err()
		case <-ticker.C:
			sa, err := im.m.Get(ctx, id)
			if err != nil {
				return tools.SubagentResult{}, fmt.Errorf("poll subagent: %w", err)
			}
			switch string(sa.Status) {
			case string(harness.RunStatusCompleted), string(harness.RunStatusFailed), string(harness.RunStatusCancelled):
				return im.toToolResult(sa), nil
			}
		}
	}
}

// Cancel requests cancellation for a subagent.
func (im *InlineManager) Cancel(ctx context.Context, id string) error {
	if err := im.m.Cancel(ctx, id); err != nil {
		return fmt.Errorf("cancel subagent: %w", err)
	}
	return nil
}

func (im *InlineManager) toToolResult(sa Subagent) tools.SubagentResult {
	return tools.SubagentResult{
		ID:     sa.ID,
		RunID:  sa.RunID,
		Status: string(sa.Status),
		Output: sa.Output,
		Error:  sa.Error,
	}
}
