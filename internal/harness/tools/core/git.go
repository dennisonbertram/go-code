package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/descriptions"
)

// GitStatusTool returns a core tool that gets git status for the workspace repository.
func GitStatusTool(opts tools.BuildOptions) tools.Tool {
	def := tools.Definition{
		Name:         "git_status",
		Description:  descriptions.Load("git_status"),
		Action:       tools.ActionRead,
		ParallelSafe: true,
		Tier:         tools.TierCore,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"porcelain": map[string]any{"type": "boolean"},
			},
		},
	}

	workspaceRoot := opts.WorkspaceRoot

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			Porcelain bool `json:"porcelain"`
		}
		args.Porcelain = true
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("parse git_status args: %w", err)
			}
		}

		absRoot, err := filepath.Abs(workspaceRoot)
		if err != nil {
			return "", fmt.Errorf("resolve workspace root: %w", err)
		}

		cmdArgs := []string{"-C", absRoot, "status"}
		if args.Porcelain {
			cmdArgs = append(cmdArgs, "--porcelain=v1")
		}
		output, exitCode, timedOut, err := tools.RunCommand(ctx, 30*time.Second, "git", cmdArgs...)
		if err != nil {
			return "", fmt.Errorf("git status failed: %w", err)
		}

		trimmed := strings.TrimSpace(output)
		result := map[string]any{
			"clean":     trimmed == "",
			"output":    trimmed,
			"exit_code": exitCode,
			"timed_out": timedOut,
		}
		return tools.MarshalToolResult(result)
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// GitDiffTool returns a core tool that gets git diff for the workspace repository.
func GitDiffTool(opts tools.BuildOptions) tools.Tool {
	def := tools.Definition{
		Name:         "git_diff",
		Description:  descriptions.Load("git_diff"),
		Action:       tools.ActionRead,
		ParallelSafe: true,
		Tier:         tools.TierCore,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":      map[string]any{"type": "string", "description": "optional relative file path"},
				"staged":    map[string]any{"type": "boolean"},
				"target":    map[string]any{"type": "string", "description": "optional revision/range"},
				"max_bytes": map[string]any{"type": "integer", "minimum": 1, "maximum": 1048576},
			},
		},
	}

	workspaceRoot := opts.WorkspaceRoot

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			Path     string `json:"path"`
			Staged   bool   `json:"staged"`
			Target   string `json:"target"`
			MaxBytes int    `json:"max_bytes"`
		}
		args.MaxBytes = 256 * 1024
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("parse git_diff args: %w", err)
			}
		}
		if args.MaxBytes <= 0 {
			args.MaxBytes = 256 * 1024
		}
		if args.MaxBytes > 1024*1024 {
			args.MaxBytes = 1024 * 1024
		}

		absRoot, err := filepath.Abs(workspaceRoot)
		if err != nil {
			return "", fmt.Errorf("resolve workspace root: %w", err)
		}

		cmdArgs := []string{"-C", absRoot, "diff"}
		if args.Staged {
			cmdArgs = append(cmdArgs, "--staged")
		}
		if strings.TrimSpace(args.Target) != "" {
			if err := tools.ValidateGitRef(args.Target); err != nil {
				return "", err
			}
			cmdArgs = append(cmdArgs, args.Target)
		}
		if strings.TrimSpace(args.Path) != "" {
			absPath, err := tools.ResolveWorkspacePathConfined(ctx, workspaceRoot, args.Path, opts.SandboxScope)
			if err != nil {
				return "", err
			}
			rel := tools.NormalizeRelPath(workspaceRoot, absPath)
			cmdArgs = append(cmdArgs, "--", filepath.FromSlash(rel))
		}

		output, exitCode, timedOut, err := tools.RunCommand(ctx, 30*time.Second, "git", cmdArgs...)
		if err != nil {
			return "", fmt.Errorf("git diff failed: %w", err)
		}
		truncated := false
		if len(output) > args.MaxBytes {
			output = output[:args.MaxBytes]
			truncated = true
		}

		result := map[string]any{
			"diff":      output,
			"truncated": truncated,
			"exit_code": exitCode,
			"timed_out": timedOut,
		}
		return tools.MarshalToolResult(result)
	}

	return tools.Tool{Definition: def, Handler: handler}
}
