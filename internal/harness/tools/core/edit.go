package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	tools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/descriptions"
)

// EditTool returns a core tool that edits a workspace file by replacing text.
func EditTool(opts tools.BuildOptions) tools.Tool {
	def := tools.Definition{
		Name:         "edit",
		Description:  descriptions.Load("edit"),
		Action:       tools.ActionWrite,
		Mutating:     true,
		ParallelSafe: false,
		Tier:         tools.TierCore,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":             map[string]any{"type": "string", "description": "relative file path inside workspace"},
				"file_path":        map[string]any{"type": "string", "description": "alias of path"},
				"old_text":         map[string]any{"type": "string"},
				"new_text":         map[string]any{"type": "string"},
				"replace_all":      map[string]any{"type": "boolean"},
				"expected_version": map[string]any{"type": "string"},
			},
			"required": []string{"path", "old_text", "new_text"},
		},
	}

	workspaceRoot := opts.WorkspaceRoot

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			Path            string `json:"path"`
			FilePath        string `json:"file_path"`
			OldText         string `json:"old_text"`
			NewText         string `json:"new_text"`
			ReplaceAll      bool   `json:"replace_all"`
			ExpectedVersion string `json:"expected_version"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse edit args: %w", err)
		}
		if args.Path == "" {
			args.Path = args.FilePath
		}
		if args.Path == "" {
			return "", fmt.Errorf("path is required")
		}
		if args.OldText == "" {
			return "", fmt.Errorf("old_text is required")
		}

		absPath, err := tools.ResolveWorkspacePathConfined(ctx, workspaceRoot, args.Path, opts.SandboxScope)
		if err != nil {
			return "", err
		}

		content, err := os.ReadFile(absPath)
		if err != nil {
			return "", fmt.Errorf("read file for edit: %w", err)
		}
		original := string(content)

		if args.ExpectedVersion != "" {
			actual := tools.FileVersionFromBytes(content)
			if actual != args.ExpectedVersion {
				return tools.MarshalToolResult(map[string]any{
					"error": map[string]any{
						"code":             "stale_write",
						"path":             args.Path,
						"expected_version": args.ExpectedVersion,
						"actual_version":   actual,
					},
				})
			}
		}

		replacements := 0
		updated := original
		if args.ReplaceAll {
			replacements = strings.Count(original, args.OldText)
			updated = strings.ReplaceAll(original, args.OldText, args.NewText)
		} else {
			if strings.Contains(original, args.OldText) {
				replacements = 1
				updated = strings.Replace(original, args.OldText, args.NewText, 1)
			}
		}
		if replacements == 0 {
			return "", fmt.Errorf("old_text not found in %s", args.Path)
		}

		if err := os.WriteFile(absPath, []byte(updated), 0o644); err != nil {
			return "", fmt.Errorf("write edited file: %w", err)
		}
		version := tools.FileVersionFromBytes([]byte(updated))

		result := map[string]any{
			"path":         tools.NormalizeRelPath(workspaceRoot, absPath),
			"replacements": replacements,
			"version":      version,
			"diff": map[string]any{
				"before_bytes": len(original),
				"after_bytes":  len(updated),
				"changed":      original != updated,
			},
		}
		return tools.MarshalToolResult(result)
	}

	return tools.Tool{Definition: def, Handler: handler}
}
