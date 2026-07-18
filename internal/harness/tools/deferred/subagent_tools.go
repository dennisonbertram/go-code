package deferred

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/descriptions"
	"go-agent-harness/internal/profiles"
)

// StartSubagentTool returns a deferred tool that starts a profile-based subagent
// and returns immediately with the subagent identifier. tracker may be nil;
// when set, it is used to co-activate the rest of the subagent-lifecycle tool
// family (get_subagent, wait_subagent, cancel_subagent, message_subagent) for
// the calling run the moment a subagent is actually spawned — see the handler
// below for why.
func StartSubagentTool(manager tools.SubagentManager, profilesDir string, tracker tools.ActivationTrackerInterface) tools.Tool {
	def := tools.Definition{
		Name:         "start_subagent",
		Description:  descriptions.Load("start_subagent"),
		Action:       tools.ActionExecute,
		Mutating:     true,
		ParallelSafe: false,
		Tier:         tools.TierDeferred,
		Tags:         []string{"agent", "subagent", "delegation", "lifecycle"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{
					"type":        "string",
					"description": "The task for the subagent to execute. Be specific; the subagent has no parent conversation context.",
				},
				"profile": map[string]any{
					"type":        "string",
					"description": "Optional profile name (for example: full, fast, minimal, github). Defaults to full.",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Optional model override for this call. Defaults to the profile's model.",
				},
				"max_steps": map[string]any{
					"type":        "integer",
					"description": "Optional step override for this call. Defaults to the profile's max_steps.",
				},
				"allowed_tools": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional list of tool names to restrict the subagent to (e.g. [\"notify_parent\", \"bash\"]). Defaults to the profile's own tool set (the \"full\" profile grants every tool) — set this to give the subagent only what it needs for its task.",
				},
			},
			"required": []string{"task"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		req, err := parseSubagentProfileRequest(ctx, "start_subagent", raw, profilesDir)
		if err != nil {
			return "", err
		}
		if manager == nil {
			return "", fmt.Errorf("start_subagent: subagent manager is not configured")
		}

		item, err := manager.Start(ctx, req)
		if err != nil {
			return "", fmt.Errorf("start_subagent: subagent failed to start: %w", err)
		}

		// A caller that can spawn a subagent needs the rest of the lifecycle
		// family immediately, not whatever find_tool's keyword ranking
		// happened to surface — observed live: a parent that found only
		// start_subagent (missing wait_subagent/get_subagent) had no way to
		// check on what it spawned, and just kept spawning duplicates until
		// it ran out of steps.
		if tracker != nil {
			if runID := tools.RunIDFromContext(ctx); runID != "" {
				tracker.Activate(runID, "get_subagent", "wait_subagent", "cancel_subagent", "message_subagent")
			}
		}

		return tools.MarshalToolResult(map[string]any{
			"subagent_id": item.ID,
			"run_id":      item.RunID,
			"status":      item.Status,
		})
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// GetSubagentTool returns a deferred tool that returns the latest subagent status.
func GetSubagentTool(manager tools.SubagentManager) tools.Tool {
	def := tools.Definition{
		Name:         "get_subagent",
		Description:  descriptions.Load("get_subagent"),
		Action:       tools.ActionExecute,
		Mutating:     false,
		ParallelSafe: true,
		Tier:         tools.TierDeferred,
		Tags:         []string{"agent", "subagent", "delegation", "lifecycle"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Subagent identifier returned from start_subagent.",
				},
			},
			"required": []string{"id"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse get_subagent args: %w", err)
		}
		id := strings.TrimSpace(args.ID)
		if id == "" {
			return "", fmt.Errorf("get_subagent: id is required")
		}
		if manager == nil {
			return "", fmt.Errorf("get_subagent: subagent manager is not configured")
		}

		item, err := manager.Get(ctx, id)
		if err != nil {
			return "", fmt.Errorf("get_subagent: failed to get subagent %q: %w", id, err)
		}
		return tools.MarshalToolResult(map[string]any{
			"id":     item.ID,
			"run_id": item.RunID,
			"status": item.Status,
			"output": item.Output,
			"error":  item.Error,
		})
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// WaitSubagentTool returns a deferred tool that blocks until the subagent reaches a
// terminal state.
func WaitSubagentTool(manager tools.SubagentManager) tools.Tool {
	def := tools.Definition{
		Name:         "wait_subagent",
		Description:  descriptions.Load("wait_subagent"),
		Action:       tools.ActionExecute,
		Mutating:     false,
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
			},
			"required": []string{"id"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse wait_subagent args: %w", err)
		}
		id := strings.TrimSpace(args.ID)
		if id == "" {
			return "", fmt.Errorf("wait_subagent: id is required")
		}
		if manager == nil {
			return "", fmt.Errorf("wait_subagent: subagent manager is not configured")
		}

		item, err := manager.Wait(ctx, id)
		if err != nil {
			return "", fmt.Errorf("wait_subagent: failed to wait for subagent %q: %w", id, err)
		}
		return tools.MarshalToolResult(map[string]any{
			"id":     item.ID,
			"run_id": item.RunID,
			"status": item.Status,
			"output": item.Output,
			"error":  item.Error,
		})
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// CancelSubagentTool returns a deferred tool that requests cancellation for a
// running subagent.
func CancelSubagentTool(manager tools.SubagentManager) tools.Tool {
	def := tools.Definition{
		Name:         "cancel_subagent",
		Description:  descriptions.Load("cancel_subagent"),
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
			},
			"required": []string{"id"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse cancel_subagent args: %w", err)
		}
		id := strings.TrimSpace(args.ID)
		if id == "" {
			return "", fmt.Errorf("cancel_subagent: id is required")
		}
		if manager == nil {
			return "", fmt.Errorf("cancel_subagent: subagent manager is not configured")
		}

		if err := manager.Cancel(ctx, id); err != nil {
			return "", fmt.Errorf("cancel_subagent: failed to cancel subagent %q: %w", id, err)
		}
		return tools.MarshalToolResult(map[string]any{
			"id":     id,
			"status": "cancelling",
		})
	}

	return tools.Tool{Definition: def, Handler: handler}
}

type subagentProfileArgs struct {
	Task            string   `json:"task"`
	Profile         string   `json:"profile,omitempty"`
	Model           string   `json:"model,omitempty"`
	MaxSteps        int      `json:"max_steps,omitempty"`
	MaxCostUSD      float64  `json:"max_cost_usd,omitempty"`
	AllowedTools    []string `json:"allowed_tools,omitempty"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
	IsolationMode   string   `json:"isolation_mode,omitempty"`
	CleanupPolicy   string   `json:"cleanup_policy,omitempty"`
	BaseRef         string   `json:"base_ref,omitempty"`
}

func parseSubagentProfileRequest(ctx context.Context, toolName string, rawJSON json.RawMessage, profilesDir string) (tools.SubagentRequest, error) {
	var args subagentProfileArgs
	if err := json.Unmarshal(rawJSON, &args); err != nil {
		return tools.SubagentRequest{}, fmt.Errorf("parse %s args: %w", toolName, err)
	}
	if strings.TrimSpace(args.Task) == "" {
		return tools.SubagentRequest{}, fmt.Errorf("%s: task is required", toolName)
	}

	profileName := strings.TrimSpace(args.Profile)
	if profileName == "" {
		profileName = "full"
	}

	var p *profiles.Profile
	var loadErr error
	if profilesDir != "" {
		p, loadErr = profiles.LoadProfileFromUserDir(profileName, profilesDir)
	} else {
		p, loadErr = profiles.LoadProfile(profileName)
	}
	if loadErr != nil {
		return tools.SubagentRequest{}, fmt.Errorf("%s: profile %q could not be loaded: %w; check available profiles with list_profiles or use a built-in profile (\"full\", \"fast\", \"minimal\")", toolName, profileName, loadErr)
	}

	vals := p.ApplyValues()

	model := vals.Model
	if strings.TrimSpace(args.Model) != "" {
		model = strings.TrimSpace(args.Model)
	}

	maxSteps := vals.MaxSteps
	if args.MaxSteps > 0 {
		maxSteps = args.MaxSteps
	}
	maxCostUSD := vals.MaxCostUSD
	if args.MaxCostUSD > 0 {
		maxCostUSD = args.MaxCostUSD
	}
	allowedTools := append([]string(nil), vals.AllowedTools...)
	if len(args.AllowedTools) > 0 {
		allowedTools = append([]string(nil), args.AllowedTools...)
	}
	reasoningEffort := vals.ReasoningEffort
	if strings.TrimSpace(args.ReasoningEffort) != "" {
		reasoningEffort = strings.TrimSpace(args.ReasoningEffort)
	}
	isolationMode := vals.IsolationMode
	if strings.TrimSpace(args.IsolationMode) != "" {
		isolationMode = strings.TrimSpace(args.IsolationMode)
	}
	cleanupPolicy := vals.CleanupPolicy
	if strings.TrimSpace(args.CleanupPolicy) != "" {
		cleanupPolicy = strings.TrimSpace(args.CleanupPolicy)
	}
	baseRef := vals.BaseRef
	if strings.TrimSpace(args.BaseRef) != "" {
		baseRef = strings.TrimSpace(args.BaseRef)
	}
	childHandoff, hasHandoff := tools.BuildParentContextHandoffFromContext(ctx)
	var handoffRef *tools.ParentContextHandoff
	if hasHandoff {
		copyHandoff := childHandoff
		handoffRef = &copyHandoff
	}

	return tools.SubagentRequest{
		Prompt:               tools.RenderPromptWithParentContext(args.Task, childHandoff),
		Model:                model,
		SystemPrompt:         vals.SystemPrompt,
		MaxSteps:             maxSteps,
		MaxCostUSD:           maxCostUSD,
		AllowedTools:         allowedTools,
		ProfileName:          profileName,
		ReasoningEffort:      reasoningEffort,
		IsolationMode:        isolationMode,
		CleanupPolicy:        cleanupPolicy,
		BaseRef:              baseRef,
		ResultMode:           vals.ResultMode,
		ParentContextHandoff: handoffRef,
	}, nil
}
