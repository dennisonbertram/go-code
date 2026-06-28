package deferred

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/descriptions"
	"go-agent-harness/internal/workflow"
)

func CreateWorkflowTool(manager workflow.SourceService) tools.Tool {
	def := tools.Definition{
		Name:         "create_workflow",
		Description:  descriptions.Load("create_workflow"),
		Action:       tools.ActionWrite,
		Mutating:     true,
		ParallelSafe: false,
		Tier:         tools.TierDeferred,
		Tags:         []string{"workflow", "go", "automation", "create"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":            map[string]any{"type": "string", "description": "Kebab-case workflow name."},
				"description":     map[string]any{"type": "string", "description": "What the workflow does."},
				"when_to_use":     map[string]any{"type": "string", "description": "When an agent should use this workflow."},
				"source":          map[string]any{"type": "string", "description": "Full Go source for package main using go-agent-harness/pkg/workflowsdk."},
				"scope":           map[string]any{"type": "string", "enum": []string{"workspace", "global", "skill"}, "description": "Where to save the workflow. Defaults to workspace."},
				"skill":           map[string]any{"type": "string", "description": "Skill name when scope=skill."},
				"timeout_seconds": map[string]any{"type": "integer", "description": "Optional per-run timeout."},
				"overwrite":       map[string]any{"type": "boolean", "description": "Replace an existing workflow with the same name."},
				"args_schema":     map[string]any{"type": "object", "description": "Optional JSON-schema-like argument schema."},
			},
			"required":             []string{"name", "description", "source"},
			"additionalProperties": false,
		},
	}
	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		if manager == nil {
			return "", fmt.Errorf("create_workflow: workflow service is not configured")
		}
		var args workflow.CreateWorkflowRequest
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse create_workflow args: %w", err)
		}
		bundle, err := manager.CreateWorkflow(ctx, args)
		if err != nil {
			return "", err
		}
		return tools.MarshalToolResult(map[string]any{
			"status": "created",
			"name":   bundle.Manifest.Name,
			"path":   bundle.Dir,
			"hash":   bundle.Hash,
			"scope":  bundle.Scope,
		})
	}
	return tools.Tool{Definition: def, Handler: handler}
}

func RunWorkflowTool(manager workflow.SourceService) tools.Tool {
	def := tools.Definition{
		Name:         "run_workflow",
		Description:  descriptions.Load("run_workflow"),
		Action:       tools.ActionExecute,
		Mutating:     true,
		ParallelSafe: false,
		Tier:         tools.TierDeferred,
		Tags:         []string{"workflow", "go", "automation", "run"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":            map[string]any{"type": "string", "description": "Workflow name to run."},
				"args":            map[string]any{"type": "object", "description": "Workflow arguments."},
				"wait":            map[string]any{"type": "boolean", "description": "Wait for terminal result. Defaults to true."},
				"timeout_seconds": map[string]any{"type": "integer", "description": "Tool wait timeout."},
				"resume_run_id":   map[string]any{"type": "string", "description": "Failed workflow run id to resume."},
			},
			"required":             []string{"name"},
			"additionalProperties": false,
		},
	}
	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		if manager == nil {
			return "", fmt.Errorf("run_workflow: workflow service is not configured")
		}
		var args struct {
			Name           string         `json:"name"`
			Args           map[string]any `json:"args"`
			Wait           *bool          `json:"wait"`
			TimeoutSeconds int            `json:"timeout_seconds"`
			ResumeRunID    string         `json:"resume_run_id"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse run_workflow args: %w", err)
		}
		name := strings.TrimSpace(args.Name)
		if name == "" && strings.TrimSpace(args.ResumeRunID) == "" {
			return "", fmt.Errorf("run_workflow: name is required")
		}
		wait := true
		if args.Wait != nil {
			wait = *args.Wait
		}
		runArgs := map[string]any{}
		if args.Args != nil {
			runArgs = args.Args
		}
		var run *workflow.Run
		var err error
		if strings.TrimSpace(args.ResumeRunID) != "" {
			run, err = manager.Resume(ctx, strings.TrimSpace(args.ResumeRunID), runArgs)
		} else {
			run, err = manager.Start(ctx, name, runArgs)
		}
		if err != nil {
			return "", err
		}
		events := []workflow.Event{}
		if wait {
			waitCtx := ctx
			cancel := func() {}
			if args.TimeoutSeconds > 0 {
				waitCtx, cancel = context.WithTimeout(ctx, time.Duration(args.TimeoutSeconds)*time.Second)
			}
			defer cancel()
			run, events, err = manager.Wait(waitCtx, run.ID)
			if err != nil {
				return "", err
			}
		}
		return tools.MarshalToolResult(map[string]any{
			"run_id":        run.ID,
			"workflow_name": run.WorkflowName,
			"status":        run.Status,
			"result_json":   run.ResultJSON,
			"error":         run.Error,
			"feedback":      workflowFeedback(events),
		})
	}
	return tools.Tool{Definition: def, Handler: handler}
}

func workflowFeedback(events []workflow.Event) []map[string]any {
	out := []map[string]any{}
	for _, ev := range events {
		switch ev.Type {
		case workflow.EventWorkflowFeedback, workflow.EventWorkflowFinding, workflow.EventWorkflowWarning, workflow.EventWorkflowQuestion, workflow.EventWorkflowLog, workflow.EventWorkflowPhaseStarted:
			item := map[string]any{
				"seq":       ev.Seq,
				"type":      ev.Type,
				"payload":   ev.Payload,
				"timestamp": ev.Timestamp,
			}
			out = append(out, item)
		}
	}
	return out
}
