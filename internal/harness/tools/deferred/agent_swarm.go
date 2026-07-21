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

// AgentSwarmTool returns the deferred agent_swarm tool: one call fans a
// prompt template out over an items array into up to 128 concurrent
// subagents and returns a single aggregated report. It runs on the same
// approval/policy path as other mutating tools (ActionExecute, Mutating).
//
// swarmRunner is the subagents-package swarm adapted to tools.SwarmRunner;
// profilesDir resolves the optional per-call profile override exactly like
// start_subagent.
func AgentSwarmTool(swarmRunner tools.SwarmRunner, profilesDir string) tools.Tool {
	def := tools.Definition{
		Name:         tools.AgentSwarmToolName,
		Description:  descriptions.Load(tools.AgentSwarmToolName),
		Action:       tools.ActionExecute,
		Mutating:     true,
		ParallelSafe: false,
		Tier:         tools.TierDeferred,
		Tags:         []string{"agent", "subagent", "delegation", "swarm"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt_template": map[string]any{
					"type":        "string",
					"description": "Prompt template for every member. Must contain the {{item}} placeholder, which is replaced with each item to produce the member prompts.",
				},
				"items": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Work items to fan out over (1-128). Expanded prompts must be distinct.",
				},
				"resume_agent_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional existing subagent IDs to resume instead of creating new members. Entry i is paired with items[i] and receives its expanded prompt; targets must be running or waiting for user input. Resumed members are scheduled before new items.",
				},
				"profile": map[string]any{
					"type":        "string",
					"description": "Optional profile name for member runs (for example: full, fast, minimal). Defaults to full.",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Optional model override for member runs. Defaults to the profile's model.",
				},
				"max_steps": map[string]any{
					"type":        "integer",
					"description": "Optional step override for member runs. Defaults to the profile's max_steps.",
				},
				"allowed_tools": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional list of tool names to restrict every member to. Defaults to the profile's own tool set. Members never receive agent_swarm.",
				},
			},
			"required": []string{"prompt_template", "items"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args agentSwarmArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse agent_swarm args: %w", err)
		}
		if strings.TrimSpace(args.PromptTemplate) == "" {
			return "", fmt.Errorf("agent_swarm: prompt_template is required")
		}
		if len(args.Items) == 0 {
			return "", fmt.Errorf("agent_swarm: items is required")
		}
		if swarmRunner == nil {
			return "", fmt.Errorf("agent_swarm: swarm runner is not configured")
		}

		req, err := resolveAgentSwarmRequest(ctx, args, profilesDir)
		if err != nil {
			return "", err
		}

		report, err := swarmRunner.RunSwarm(ctx, req)
		if err != nil {
			return "", fmt.Errorf("agent_swarm: %w", err)
		}
		return tools.MarshalToolResult(report)
	}

	return tools.Tool{Definition: def, Handler: handler}
}

type agentSwarmArgs struct {
	PromptTemplate  string   `json:"prompt_template"`
	Items           []string `json:"items"`
	ResumeAgentIDs  []string `json:"resume_agent_ids,omitempty"`
	Profile         string   `json:"profile,omitempty"`
	Model           string   `json:"model,omitempty"`
	MaxSteps        int      `json:"max_steps,omitempty"`
	MaxCostUSD      float64  `json:"max_cost_usd,omitempty"`
	AllowedTools    []string `json:"allowed_tools,omitempty"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
}

// resolveAgentSwarmRequest applies the same profile-resolution semantics as
// start_subagent: profile values are the base, per-call overrides win.
func resolveAgentSwarmRequest(ctx context.Context, args agentSwarmArgs, profilesDir string) (tools.SwarmRequest, error) {
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
		return tools.SwarmRequest{}, fmt.Errorf("agent_swarm: profile %q could not be loaded: %w; check available profiles with list_profiles or use a built-in profile (\"full\", \"fast\", \"minimal\")", profileName, loadErr)
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

	childHandoff, hasHandoff := tools.BuildParentContextHandoffFromContext(ctx)
	var handoffRef *tools.ParentContextHandoff
	if hasHandoff {
		copyHandoff := childHandoff
		handoffRef = &copyHandoff
	}

	return tools.SwarmRequest{
		PromptTemplate:       strings.TrimSpace(args.PromptTemplate),
		Items:                append([]string(nil), args.Items...),
		ResumeAgentIDs:       append([]string(nil), args.ResumeAgentIDs...),
		Model:                model,
		SystemPrompt:         vals.SystemPrompt,
		MaxSteps:             maxSteps,
		MaxCostUSD:           maxCostUSD,
		ReasoningEffort:      reasoningEffort,
		AllowedTools:         allowedTools,
		ProfileName:          profileName,
		IsolationMode:        vals.IsolationMode,
		CleanupPolicy:        vals.CleanupPolicy,
		BaseRef:              vals.BaseRef,
		ResultMode:           vals.ResultMode,
		ParentContextHandoff: handoffRef,
	}, nil
}
