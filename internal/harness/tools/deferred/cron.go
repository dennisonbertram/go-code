package deferred

import (
	"context"
	"encoding/json"
	"fmt"

	tools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/descriptions"
)

func strPtr(s string) *string { return &s }

// CronCreateTool returns a deferred tool for creating cron jobs.
func CronCreateTool(client tools.CronClient) tools.Tool {
	def := tools.Definition{
		Name:        "cron_create",
		Description: descriptions.Load("cron_create"),
		Action:      tools.ActionExecute,
		Mutating:    true,
		Tier:        tools.TierDeferred,
		Tags:        []string{"cron", "schedule", "automation"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":            map[string]any{"type": "string", "description": "Unique name for the cron job"},
				"schedule":        map[string]any{"type": "string", "description": "Standard 5-field cron expression: <minute> <hour> <day-of-month> <month> <day-of-week>. All times are UTC. Must be a literal string — no shell substitutions or variables. Examples: \"*/5 * * * *\" = every 5 minutes, \"0 * * * *\" = every hour on the hour, \"30 2 * * *\" = daily at 02:30 UTC, \"0 9 * * 1-5\" = weekdays at 09:00 UTC, \"0 0 1 * *\" = first of every month at midnight UTC. To schedule relative to 'now', first run the bash tool to get the current UTC time, then compute the desired cron fields yourself."},
				"command":         map[string]any{"type": "string", "description": "Shell command to execute on each trigger"},
				"timeout_seconds": map[string]any{"type": "integer", "description": "Max execution time in seconds (default 30). The job is killed if it exceeds this."},
			},
			"required": []string{"name", "schedule", "command"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			Name           string `json:"name"`
			Schedule       string `json:"schedule"`
			Command        string `json:"command"`
			TimeoutSeconds int    `json:"timeout_seconds"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse cron_create args: %w", err)
		}
		if args.TimeoutSeconds == 0 {
			args.TimeoutSeconds = 30
		}

		execCfg, err := json.Marshal(map[string]string{"command": args.Command})
		if err != nil {
			return "", fmt.Errorf("marshal exec config: %w", err)
		}

		job, err := client.CreateJob(ctx, tools.CronCreateJobRequest{
			Name:       args.Name,
			Schedule:   args.Schedule,
			ExecType:   "shell",
			ExecConfig: string(execCfg),
			TimeoutSec: args.TimeoutSeconds,
		})
		if err != nil {
			return "", fmt.Errorf("cron_create failed: %w", err)
		}
		return tools.MarshalToolResult(job)
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// CronListTool returns a deferred tool for listing cron jobs.
func CronListTool(client tools.CronClient) tools.Tool {
	def := tools.Definition{
		Name:         "cron_list",
		Description:  descriptions.Load("cron_list"),
		Action:       tools.ActionList,
		ParallelSafe: true,
		Tier:         tools.TierDeferred,
		Tags:         []string{"cron", "schedule", "automation"},
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		jobs, err := client.ListJobs(ctx)
		if err != nil {
			return "", fmt.Errorf("cron_list failed: %w", err)
		}
		return tools.MarshalToolResult(jobs)
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// CronGetTool returns a deferred tool for getting a cron job's details.
func CronGetTool(client tools.CronClient) tools.Tool {
	def := tools.Definition{
		Name:         "cron_get",
		Description:  descriptions.Load("cron_get"),
		Action:       tools.ActionRead,
		ParallelSafe: true,
		Tier:         tools.TierDeferred,
		Tags:         []string{"cron", "schedule", "automation"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "string", "description": "Job ID"},
			},
			"required": []string{"id"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse cron_get args: %w", err)
		}

		job, err := client.GetJob(ctx, args.ID)
		if err != nil {
			return "", fmt.Errorf("cron_get failed: %w", err)
		}

		execs, execErr := client.ListExecutions(ctx, args.ID, 5, 0)
		if execErr != nil {
			execs = []tools.CronExecution{}
		}

		result := map[string]any{
			"job":               job,
			"recent_executions": execs,
		}
		return tools.MarshalToolResult(result)
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// CronDeleteTool returns a deferred tool for deleting a cron job.
func CronDeleteTool(client tools.CronClient) tools.Tool {
	def := tools.Definition{
		Name:        "cron_delete",
		Description: descriptions.Load("cron_delete"),
		Action:      tools.ActionExecute,
		Mutating:    true,
		Tier:        tools.TierDeferred,
		Tags:        []string{"cron", "schedule", "automation"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "string", "description": "Job ID"},
			},
			"required": []string{"id"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse cron_delete args: %w", err)
		}

		if err := client.DeleteJob(ctx, args.ID); err != nil {
			return "", fmt.Errorf("cron_delete failed: %w", err)
		}

		return tools.MarshalToolResult(map[string]any{
			"deleted": true,
			"id":      args.ID,
		})
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// CronPauseTool returns a deferred tool for pausing a cron job.
func CronPauseTool(client tools.CronClient) tools.Tool {
	def := tools.Definition{
		Name:        "cron_pause",
		Description: descriptions.Load("cron_pause"),
		Action:      tools.ActionExecute,
		Mutating:    true,
		Tier:        tools.TierDeferred,
		Tags:        []string{"cron", "schedule", "automation"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "string", "description": "Job ID"},
			},
			"required": []string{"id"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse cron_pause args: %w", err)
		}

		job, err := client.UpdateJob(ctx, args.ID, tools.CronUpdateJobRequest{
			Status: strPtr("paused"),
		})
		if err != nil {
			return "", fmt.Errorf("cron_pause failed: %w", err)
		}
		return tools.MarshalToolResult(job)
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// CronResumeTool returns a deferred tool for resuming a paused cron job.
func CronResumeTool(client tools.CronClient) tools.Tool {
	def := tools.Definition{
		Name:        "cron_resume",
		Description: descriptions.Load("cron_resume"),
		Action:      tools.ActionExecute,
		Mutating:    true,
		Tier:        tools.TierDeferred,
		Tags:        []string{"cron", "schedule", "automation"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "string", "description": "Job ID"},
			},
			"required": []string{"id"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse cron_resume args: %w", err)
		}

		job, err := client.UpdateJob(ctx, args.ID, tools.CronUpdateJobRequest{
			Status: strPtr("active"),
		})
		if err != nil {
			return "", fmt.Errorf("cron_resume failed: %w", err)
		}
		return tools.MarshalToolResult(job)
	}

	return tools.Tool{Definition: def, Handler: handler}
}
