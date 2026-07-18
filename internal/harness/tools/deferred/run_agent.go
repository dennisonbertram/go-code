package deferred

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	tools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/descriptions"
)

// RunAgentTool returns a deferred tool that spawns a subagent using a named profile.
func RunAgentTool(manager tools.SubagentManager, profilesDir string) tools.Tool {
	def := tools.Definition{
		Name:         "run_agent",
		Description:  descriptions.Load("run_agent"),
		Action:       tools.ActionExecute,
		Mutating:     true,
		ParallelSafe: false,
		Tier:         tools.TierDeferred,
		Tags:         []string{"agent", "subagent", "profile", "delegation", "run"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{
					"type":        "string",
					"description": "The task for the subagent to complete. Be specific — it has no context from the parent conversation.",
				},
				"profile": map[string]any{
					"type":        "string",
					"description": "Optional profile name (e.g. 'github', 'researcher'). Defaults to 'full' (all tools).",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Optional model override for this call. Overrides the profile's configured model.",
				},
				"max_steps": map[string]any{
					"type":        "integer",
					"description": "Optional step override for this call. Overrides the profile's max_steps.",
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
		if manager == nil {
			return "", fmt.Errorf("run_agent: subagent manager is not configured")
		}
		req, err := parseSubagentProfileRequest(ctx, "run_agent", raw, profilesDir)
		if err != nil {
			return "", err
		}

		result, err := manager.CreateAndWait(ctx, req)
		if err != nil {
			return "", fmt.Errorf("run_agent: subagent failed: %w", err)
		}

		response := map[string]any{
			"run_id":  result.RunID,
			"status":  result.Status,
			"profile": req.ProfileName,
			"output":  result.Output,
			// Unified ChildResult fields: summary derived from output.
			"summary": deriveSummary(result.Output, result.Status),
		}
		if result.Error != "" {
			response["error"] = result.Error
		}

		return tools.MarshalToolResult(response)
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// deriveSummary returns a concise summary derived from the subagent output.
// It uses the first line of the output, truncated to 200 runes. When the
// output is empty, it falls back to a status-based default message.
func deriveSummary(output, status string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		switch status {
		case "failed":
			return "Subagent run failed."
		case "partial":
			return "Subagent completed partially with no output."
		default:
			return "Subagent completed with no output."
		}
	}

	// Use the first line as the summary, capped at 200 runes.
	line := output
	if idx := strings.IndexByte(output, '\n'); idx >= 0 {
		line = output[:idx]
	}
	line = strings.TrimSpace(line)

	const maxRunes = 200
	if utf8.RuneCountInString(line) > maxRunes {
		runes := []rune(line)
		line = string(runes[:maxRunes]) + "…"
	}
	if line == "" {
		return "Subagent completed."
	}
	return line
}
