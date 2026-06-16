package deferred

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/profiles"
)

// SpawnAgentTool returns a deferred tool that spawns a child agent and waits
// for it to complete. The child agent receives task_complete in its tool list
// (via the depth counter in context) and must call it to return results.
//
// The tool is TierDeferred and visible at all depths. It uses the
// ForkedAgentRunner interface (same as skills with context:fork) so it works
// with the existing RunForkedSkill synchronous wait path. In Phase 1 the
// parent goroutine blocks until the child completes; Phase 2 will add true
// suspension with DB-backed resume.
//
// profilesDir is the directory to search for user-global profile TOML files
// (same semantics as RunAgentTool). Pass "" to use built-in profiles only.
func SpawnAgentTool(runner tools.AgentRunner, profilesDir string) tools.Tool {
	def := tools.Definition{
		Name:         "spawn_agent",
		Description:  spawnAgentDescription,
		Action:       tools.ActionExecute,
		Mutating:     true,
		ParallelSafe: false,
		Tier:         tools.TierDeferred,
		Tags:         []string{"agent", "spawn", "recursive", "subagent", "delegation"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{
					"type":        "string",
					"description": "The task for the child agent to complete. Be specific and complete — the child has no context from the parent conversation.",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Optional model override for the child agent (e.g. 'gpt-4.1-mini', 'o3'). Defaults to the runner's configured model.",
				},
				"max_steps": map[string]any{
					"type":        "integer",
					"description": "Maximum steps for the child agent (default 30). The child receives step-budget pressure warnings near the limit and must call task_complete before hitting it.",
				},
				"max_turns": map[string]any{
					"type":        "integer",
					"description": "Maximum assistant turns for the child agent (default 0 = unlimited). Overrides max_steps when set. The child receives turn-budget pressure warnings near the limit.",
				},
				"allowed_tools": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional list of tool names the child may use. When omitted, the child inherits the parent's tool set.",
				},
				"profile": map[string]any{
					"type":        "string",
					"description": "Optional profile name to load for the child agent (MCP servers, custom tools, etc.).",
				},
			},
			"required": []string{"task"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			Task         string   `json:"task"`
			Model        string   `json:"model,omitempty"`
			MaxSteps     int      `json:"max_steps,omitempty"`
			MaxTurns     int      `json:"max_turns,omitempty"`
			AllowedTools []string `json:"allowed_tools,omitempty"`
			Profile      string   `json:"profile,omitempty"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse spawn_agent args: %w", err)
		}
		if strings.TrimSpace(args.Task) == "" {
			return "", fmt.Errorf("task is required")
		}

		if runner == nil {
			return "", fmt.Errorf("spawn_agent: no AgentRunner configured")
		}

		// --- Profile loading (mirrors run_agent.go pattern) ---
		// Default profile to "full" when not specified.
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
			// Non-fatal: if the profile is not found, use empty defaults.
			// This allows spawn_agent to work even without a profile system.
			p = &profiles.Profile{}
			p.Meta.Name = profileName
		}

		// Apply profile values, then apply per-call overrides on top.
		vals := p.ApplyValues()

		model := vals.Model
		if strings.TrimSpace(args.Model) != "" {
			model = strings.TrimSpace(args.Model)
		}

		maxSteps := vals.MaxSteps
		if args.MaxSteps > 0 {
			maxSteps = args.MaxSteps
		}
		// Fall back to spawn_agent default of 30 if neither profile nor arg sets it.
		if maxSteps <= 0 {
			maxSteps = 30
		}

		// Resolve max_turns from profile, then args override.
		maxTurns := vals.MaxTurns
		if args.MaxTurns > 0 {
			maxTurns = args.MaxTurns
		}
		// 0 means unlimited — no default applied.

		// Check depth limit before spawning.
		currentDepth := tools.ForkDepthFromContext(ctx)
		if currentDepth >= tools.DefaultMaxForkDepth {
			return "", fmt.Errorf("spawn_agent: max recursion depth %d reached", tools.DefaultMaxForkDepth)
		}

		// Build the child context with incremented fork depth so the child's
		// runner can enforce task_complete visibility (depth > 0) and the next
		// level's depth limit.
		childCtx := tools.WithForkDepth(ctx, currentDepth+1)
		childHandoff, hasHandoff := tools.BuildParentContextHandoffFromContext(childCtx)
		var handoffRef *tools.ParentContextHandoff
		if hasHandoff {
			copyHandoff := childHandoff
			handoffRef = &copyHandoff
		}

		// Build a system prompt that instructs the child to call task_complete.
		childSystemPrompt := buildSubagentSystemPrompt(args.Task, maxSteps)
		childPrompt := tools.RenderPromptWithParentContext(
			childSystemPrompt+"\n\n# Task\n\n"+args.Task,
			childHandoff,
		)

		// Check whether the runner supports ForkedAgentRunner (RunForkedSkill).
		forkedRunner, ok := runner.(tools.ForkedAgentRunner)
		if !ok {
			// Fallback: use RunPrompt directly (no structured result).
			output, err := runner.RunPrompt(childCtx, childPrompt)
			if err != nil {
				return "", fmt.Errorf("spawn_agent: child run failed: %w", err)
			}
			return tools.MarshalToolResult(map[string]any{
				"status":  "completed",
				"summary": output,
				"jsonl":   []any{},
			})
		}

		config := tools.ForkConfig{
			Prompt:               childPrompt,
			ParentContextHandoff: handoffRef,
			SkillName:            "spawn_agent",
			AllowedTools:         args.AllowedTools,
			Model:                model,
			MaxSteps:             maxSteps,
			MaxTurns:             maxTurns,
		}

		result, err := forkedRunner.RunForkedSkill(childCtx, config)
		if err != nil {
			return "", fmt.Errorf("spawn_agent: child run failed: %w", err)
		}

		// Parse the child's task_complete result if available.
		// The child either called task_complete (structured) or just returned text.
		childResult := parseChildResult(result)

		return tools.MarshalToolResult(childResult)
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// buildSubagentSystemPrompt returns the system prompt injected into all
// spawn_agent child runs. It mandates calling task_complete as the exit path.
func buildSubagentSystemPrompt(task string, maxSteps int) string {
	return fmt.Sprintf(`You are a subagent. You have been spawned to complete a specific task.

IMPORTANT: When your task is complete (or when you have done as much as you can), you MUST call the task_complete tool. This is the only way to return results to your parent agent. Do NOT write a final response — call task_complete instead.

You have at most %d steps. At step %d you will receive a warning to wrap up. Call task_complete with what you have completed, even if partial.

Your task:
%s`, maxSteps, maxSteps-3, task)
}

// parseChildResult attempts to parse the child run's ForkResult as a
// TaskCompleteResult (produced by the task_complete tool). Falls back to
// a generic result pointer if the output is plain text.
//
// The returned map always includes both "jsonl" (backward compat) and
// "findings" (unified ChildResult schema) so parent agents can parse either.
func parseChildResult(result tools.ForkResult) map[string]any {
	if result.Error != "" {
		return map[string]any{
			"status":   "failed",
			"summary":  result.Error,
			"jsonl":    []any{},
			"findings": []any{},
		}
	}

	// Prefer summary over raw output.
	output := result.Summary
	if output == "" {
		output = result.Output
	}

	// Try to parse as a TaskCompleteResult (JSON from task_complete tool).
	var tcResult TaskCompleteResultPayload
	if err := json.Unmarshal([]byte(output), &tcResult); err == nil && tcResult.Summary != "" {
		// Successfully parsed structured task_complete output.
		// Build both jsonl (backward compat) and findings (unified schema).
		jsonl := make([]any, 0, len(tcResult.Findings))
		findings := make([]TaskCompleteFinding, 0, len(tcResult.Findings))
		for _, f := range tcResult.Findings {
			jsonl = append(jsonl, map[string]any{
				"type":    f.Type,
				"content": f.Content,
			})
			findings = append(findings, TaskCompleteFinding{
				Type:    f.Type,
				Content: f.Content,
			})
		}
		return map[string]any{
			"status":   tcResult.Status,
			"summary":  tcResult.Summary,
			"jsonl":    jsonl,
			"findings": findings,
		}
	}

	// Plain text output — wrap in a generic result.
	return map[string]any{
		"status":  "completed",
		"summary": output,
		"jsonl": []any{
			map[string]any{
				"type":    "conclusion",
				"content": output,
			},
		},
		"findings": []TaskCompleteFinding{
			{Type: "conclusion", Content: output},
		},
	}
}

const spawnAgentDescription = `Spawn a child agent to complete a specific task and wait for the result.

The child agent runs with its own step budget and receives a structured return path (task_complete tool). When the child finishes, you receive a result pointer with:
- status: "completed" | "partial" | "failed"
- summary: 1-3 sentence description of what was done
- jsonl: structured findings array (type + content pairs)

Use spawn_agent to delegate well-scoped subtasks: research, implementation, analysis. The child cannot see your conversation — provide all necessary context in the task parameter.

Step budget: the child defaults to 30 steps. Provide max_steps to override.`

// TaskCompleteResultPayload is the structured output from a task_complete call.
// It is parsed by spawn_agent to extract the child's result. Defined here to
// avoid a circular import with the deferred package that owns TaskCompleteTool.
type TaskCompleteResultPayload struct {
	Status   string                  `json:"status"`
	Summary  string                  `json:"summary"`
	Findings []TaskCompleteFinding   `json:"findings,omitempty"`
}

// TaskCompleteFinding is a single structured finding from a child agent.
type TaskCompleteFinding struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}
