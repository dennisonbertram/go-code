package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go-agent-harness/internal/harness/tools/descriptions"
)

func gitDiffTool(workspaceRoot string, sandboxScope SandboxScope) Tool {
	def := Definition{
		Name:         "git_diff",
		Description:  descriptions.Load("git_diff"),
		Action:       ActionRead,
		ParallelSafe: true,
		Tags:         []string{"git", "diff", "changes", "delta", "patch"},
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

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		args := struct {
			Path     string `json:"path"`
			Staged   bool   `json:"staged"`
			Target   string `json:"target"`
			MaxBytes int    `json:"max_bytes"`
		}{MaxBytes: 256 * 1024}
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
			if err := ValidateGitRef(args.Target); err != nil {
				return "", err
			}
			cmdArgs = append(cmdArgs, args.Target)
		}
		if strings.TrimSpace(args.Path) != "" {
			absPath, err := ResolveWorkspacePathConfined(ctx, workspaceRoot, args.Path, sandboxScope)
			if err != nil {
				return "", err
			}
			rel := NormalizeRelPath(workspaceRoot, absPath)
			cmdArgs = append(cmdArgs, "--", filepath.FromSlash(rel))
		}

		output, exitCode, timedOut, err := runCommand(ctx, 30*time.Second, "git", cmdArgs...)
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
		return MarshalToolResult(result)
	}

	return Tool{Definition: def, Handler: handler}
}
