package deferred

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/descriptions"
)

// MessageSubagentTool returns a deferred tool that sends a follow-up message
// to a running subagent by resolving its subagent ID to a run ID (via
// manager.Get) and steering that run.
func MessageSubagentTool(manager tools.SubagentManager, steerer tools.RunSteerer) tools.Tool {
	def := tools.Definition{
		Name:         "message_subagent",
		Description:  descriptions.Load("message_subagent"),
		Action:       tools.ActionExecute,
		Mutating:     true,
		ParallelSafe: false,
		Tier:         tools.TierDeferred,
		Tags:         []string{"agent", "subagent", "delegation", "lifecycle"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Subagent identifier returned from start_subagent.",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "The message to send. Injected into the subagent's conversation before its next turn.",
				},
			},
			"required": []string{"id", "message"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			ID      string `json:"id"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse message_subagent args: %w", err)
		}
		id := strings.TrimSpace(args.ID)
		if id == "" {
			return "", fmt.Errorf("message_subagent: id is required")
		}
		message := strings.TrimSpace(args.Message)
		if message == "" {
			return "", fmt.Errorf("message_subagent: message is required")
		}
		if manager == nil {
			return "", fmt.Errorf("message_subagent: subagent manager is not configured")
		}
		if steerer == nil {
			return "", fmt.Errorf("message_subagent: run steerer is not configured")
		}

		item, err := manager.Get(ctx, id)
		if err != nil {
			return "", fmt.Errorf("message_subagent: failed to look up subagent %q: %w", id, err)
		}
		if err := steerer.SteerRun(item.RunID, message); err != nil {
			return "", fmt.Errorf("message_subagent: failed to send message to subagent %q: %w", id, err)
		}

		return tools.MarshalToolResult(map[string]any{
			"id":     id,
			"run_id": item.RunID,
			"status": "sent",
		})
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// NotifyParentTool returns a deferred tool that sends a message back to the
// run that spawned the current run as a subagent, looking up the parent run
// ID recorded via ParentContextHandoff (see BuildParentContextHandoffFromContext).
func NotifyParentTool(steerer tools.RunSteerer) tools.Tool {
	def := tools.Definition{
		Name:         "notify_parent",
		Description:  descriptions.Load("notify_parent"),
		Action:       tools.ActionExecute,
		Mutating:     true,
		ParallelSafe: false,
		Tier:         tools.TierDeferred,
		Tags:         []string{"agent", "subagent", "delegation", "lifecycle"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "The message to send back to the parent agent that spawned this run.",
				},
			},
			"required": []string{"message"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse notify_parent args: %w", err)
		}
		message := strings.TrimSpace(args.Message)
		if message == "" {
			return "", fmt.Errorf("notify_parent: message is required")
		}
		if steerer == nil {
			return "", fmt.Errorf("notify_parent: run steerer is not configured")
		}

		runID := tools.RunIDFromContext(ctx)
		if runID == "" {
			return "", fmt.Errorf("notify_parent: no run context available")
		}
		parentRunID, ok := steerer.ParentRunID(runID)
		if !ok || parentRunID == "" {
			return "", fmt.Errorf("notify_parent: this run has no recorded parent to notify (it was not spawned as a subagent)")
		}
		if err := steerer.SteerRun(parentRunID, message); err != nil {
			return "", fmt.Errorf("notify_parent: failed to send message to parent run %q: %w", parentRunID, err)
		}

		return tools.MarshalToolResult(map[string]any{
			"parent_run_id": parentRunID,
			"status":        "sent",
		})
	}

	return tools.Tool{Definition: def, Handler: handler}
}
