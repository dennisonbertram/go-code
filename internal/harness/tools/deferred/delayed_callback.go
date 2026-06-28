package deferred

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	tools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/descriptions"
)

// SetDelayedCallbackTool returns a deferred tool for scheduling a one-shot delayed callback.
func SetDelayedCallbackTool(mgr *tools.CallbackManager) tools.Tool {
	def := tools.Definition{
		Name:        "set_delayed_callback",
		Description: descriptions.Load("set_delayed_callback"),
		Action:      tools.ActionExecute,
		Mutating:    true,
		Tier:        tools.TierDeferred,
		Tags:        []string{"callback", "delayed", "timer"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"delay": map[string]any{
					"type":        "string",
					"description": "How long to wait before firing the callback. Go duration format: '30s', '5m', '1h30m'. Minimum 5s, maximum 1h.",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "The prompt to use when starting the new run. Should describe what to check or do.",
				},
			},
			"required": []string{"delay", "prompt"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			Delay  string `json:"delay"`
			Prompt string `json:"prompt"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse set_delayed_callback args: %w", err)
		}

		delay, err := time.ParseDuration(args.Delay)
		if err != nil {
			return "", fmt.Errorf("invalid delay format %q: %w", args.Delay, err)
		}

		md, ok := tools.RunMetadataFromContext(ctx)
		if !ok {
			return "", fmt.Errorf("set_delayed_callback: no run metadata in context")
		}

		info, err := mgr.Set(tools.SetRequest{
			ConversationID: md.ConversationID,
			Delay:          delay,
			Prompt:         args.Prompt,
			TenantID:       md.TenantID,
			AgentID:        md.AgentID,
		})
		if err != nil {
			return "", fmt.Errorf("set_delayed_callback failed: %w", err)
		}

		return tools.MarshalToolResult(info)
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// CancelDelayedCallbackTool returns a deferred tool for canceling a pending delayed callback.
func CancelDelayedCallbackTool(mgr *tools.CallbackManager) tools.Tool {
	def := tools.Definition{
		Name:        "cancel_delayed_callback",
		Description: descriptions.Load("cancel_delayed_callback"),
		Action:      tools.ActionExecute,
		Mutating:    true,
		Tier:        tools.TierDeferred,
		Tags:        []string{"callback", "delayed", "timer"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"callback_id": map[string]any{
					"type":        "string",
					"description": "The ID of the callback to cancel.",
				},
			},
			"required": []string{"callback_id"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			CallbackID string `json:"callback_id"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse cancel_delayed_callback args: %w", err)
		}

		info, err := mgr.Cancel(args.CallbackID)
		if err != nil {
			return "", fmt.Errorf("cancel_delayed_callback failed: %w", err)
		}

		return tools.MarshalToolResult(info)
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// ListDelayedCallbacksTool returns a deferred tool for listing all delayed callbacks for the current conversation.
func ListDelayedCallbacksTool(mgr *tools.CallbackManager) tools.Tool {
	def := tools.Definition{
		Name:         "list_delayed_callbacks",
		Description:  descriptions.Load("list_delayed_callbacks"),
		Action:       tools.ActionList,
		ParallelSafe: true,
		Tier:         tools.TierDeferred,
		Tags:         []string{"callback", "delayed", "timer"},
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		md, ok := tools.RunMetadataFromContext(ctx)
		if !ok {
			return "", fmt.Errorf("list_delayed_callbacks: no run metadata in context")
		}

		callbacks := mgr.List(md.ConversationID)
		return tools.MarshalToolResult(callbacks)
	}

	return tools.Tool{Definition: def, Handler: handler}
}
