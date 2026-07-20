package deferred

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tools "go-agent-harness/internal/harness/tools"
)

// TaskCompleteTool returns a deferred tool that signals the end of a subagent
// run with structured results. It is depth-gated: invisible to root agents
// (depth == 0) and available to all subagents (depth > 0).
//
// When a subagent calls task_complete, the runner terminates the run cleanly
// and the parent's spawn_agent call receives the structured result pointer.
//
// If a subagent exhausts its step budget without calling task_complete, the
// runner auto-calls task_complete synthetically with status="partial".
func TaskCompleteTool(runner tools.AgentRunner) tools.Tool {
	def := tools.Definition{
		Name:         "task_complete",
		Description:  taskCompleteDescription,
		Action:       tools.ActionExecute,
		Mutating:     false,
		ParallelSafe: false,
		Tier:         tools.TierDeferred,
		Tags:         []string{"agent", "subagent", "completion", "result"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"summary": map[string]any{
					"type":        "string",
					"description": "1-3 sentence summary of what was accomplished. Be concise and specific.",
				},
				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"completed", "partial", "failed"},
					"description": "completed = task fully done; partial = done what was possible but not everything; failed = could not complete the task.",
				},
				"findings": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"type": map[string]any{
								"type":        "string",
								"description": "Finding type: finding | file_changed | test_result | error | warning | conclusion",
							},
							"content": map[string]any{
								"type":        "string",
								"description": "Finding content or description.",
							},
						},
						"required": []string{"type", "content"},
					},
					"description": "Structured findings: notable discoveries, file changes, test results, errors, warnings, conclusions.",
				},
			},
			"required": []string{"summary"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		// Depth gate: task_complete is only available to subagents (depth > 0).
		// At depth 0 (root agent) this tool should not be callable, but if it
		// somehow is (e.g. via find_tool activation at depth 0), return an error.
		depth := tools.ForkDepthFromContext(ctx)
		if depth == 0 {
			return "", fmt.Errorf("task_complete is only available to subagents (depth > 0); root agents do not call task_complete")
		}

		var args struct {
			Summary  string                `json:"summary"`
			Status   string                `json:"status,omitempty"`
			Findings []TaskCompleteFinding `json:"findings,omitempty"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse task_complete args: %w", err)
		}
		if strings.TrimSpace(args.Summary) == "" {
			return "", fmt.Errorf("summary is required")
		}
		if args.Status == "" {
			args.Status = "completed"
		}
		// Validate status.
		switch args.Status {
		case "completed", "partial", "failed":
			// valid
		default:
			return "", fmt.Errorf("invalid status %q: must be 'completed', 'partial', or 'failed'", args.Status)
		}

		// Build the structured result that spawn_agent will parse.
		result := TaskCompleteResultPayload{
			Status:   args.Status,
			Summary:  args.Summary,
			Findings: args.Findings,
		}
		if result.Findings == nil {
			result.Findings = []TaskCompleteFinding{}
		}

		// The runner detects this special "task_complete_signal" marker in the
		// tool output and uses it to terminate the run cleanly. The entire JSON
		// payload is included so spawn_agent can parse the child result.
		//
		// We embed the result in the output JSON with a marker field so the
		// runner can identify it reliably.
		output := map[string]any{
			"_task_complete": true,
			"status":         result.Status,
			"summary":        result.Summary,
			"findings":       result.Findings,
		}

		return tools.MarshalToolResult(output)
	}

	return tools.Tool{Definition: def, Handler: handler}
}

const taskCompleteDescription = `Signal successful completion of your assigned task and return structured results to your parent agent.

You MUST call this tool when your task is done — it is the only valid return path for subagents. Do NOT write a final response instead of calling this tool.

Provide:
- summary: 1-3 sentences describing what was accomplished
- status: "completed" (done), "partial" (did what was possible), or "failed" (could not complete)
- findings: structured results array (optional but recommended)

Finding types:
- finding: notable discovery or observation
- file_changed: a file was created or modified (include path and brief description in content)
- test_result: test outcomes (include pass/fail counts in content)
- error: errors encountered
- warning: non-fatal issues
- conclusion: final answer or recommendation

Example:
{
  "summary": "Implemented JWT auth module with 14 tests passing.",
  "status": "completed",
  "findings": [
    {"type": "file_changed", "content": "internal/auth/jwt.go created (87 lines)"},
    {"type": "warning", "content": "JWT secret is hardcoded in config.go:42 — should be rotated"},
    {"type": "test_result", "content": "14 passed, 0 failed"},
    {"type": "conclusion", "content": "Auth module complete. Review config.go:42 before deploying."}
  ]
}`
