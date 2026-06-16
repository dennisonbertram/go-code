package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go-agent-harness/internal/harness/tools/descriptions"
	"gopkg.in/yaml.v3"
)

func writeTool(workspaceRoot string) Tool {
	def := Definition{
		Name:         "write",
		Description:  descriptions.Load("write"),
		Action:       ActionWrite,
		Mutating:     true,
		ParallelSafe: false,
		Tags:         []string{"write", "create", "file", "replace", "new"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":             map[string]any{"type": "string", "description": "relative file path inside workspace"},
				"file_path":        map[string]any{"type": "string", "description": "alias of path"},
				"content":          map[string]any{"type": "string"},
				"new_text":         map[string]any{"type": "string", "description": "alias of content"},
				"new_string":       map[string]any{"type": "string", "description": "alias of content"},
				"text":             map[string]any{"type": "string", "description": "alias of content"},
				"append":           map[string]any{"type": "boolean"},
				"expected_version": map[string]any{"type": "string"},
			},
			"required": []string{"path"},
		},
	}

	handler := func(_ context.Context, raw json.RawMessage) (string, error) {
		args := struct {
			Path            string  `json:"path"`
			FilePath        string  `json:"file_path"`
			Content         *string `json:"content"`
			NewText         *string `json:"new_text"`
			NewString       *string `json:"new_string"`
			Text            *string `json:"text"`
			Append          bool    `json:"append"`
			ExpectedVersion string  `json:"expected_version"`
		}{}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse write args: %w", err)
		}
		if args.Path == "" {
			args.Path = args.FilePath
		}
		if args.Path == "" {
			return "", fmt.Errorf("path is required")
		}

		contentPtr := args.Content
		if contentPtr == nil {
			contentPtr = args.NewText
		}
		if contentPtr == nil {
			contentPtr = args.NewString
		}
		if contentPtr == nil {
			contentPtr = args.Text
		}
		if contentPtr == nil {
			return "", fmt.Errorf("content is required")
		}
		content := *contentPtr

		if err := EnsureWorkspaceRootUsable(workspaceRoot); err != nil {
			return "", err
		}

		absPath, err := ResolveWorkspacePath(workspaceRoot, args.Path)
		if err != nil {
			return "", err
		}

		before := ""
		if existing, err := os.ReadFile(absPath); err == nil {
			before = string(existing)
			if args.ExpectedVersion != "" {
				version := FileVersionFromBytes(existing)
				if version != args.ExpectedVersion {
					return MarshalToolResult(map[string]any{
						"error": map[string]any{
							"code":             "stale_write",
							"path":             args.Path,
							"expected_version": args.ExpectedVersion,
							"actual_version":   version,
						},
					})
				}
			}
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("read file before write: %w", err)
		} else if args.ExpectedVersion != "" {
			return MarshalToolResult(map[string]any{
				"error": map[string]any{
					"code":             "stale_write",
					"path":             args.Path,
					"expected_version": args.ExpectedVersion,
					"actual_version":   "",
				},
			})
		}

		// Validate JSON content before writing to .json files.
		// This guards against the model emitting malformed JSON that would corrupt
		// machine-readable files (e.g. unclosed braces, literal \n sequences outside
		// quoted values, etc.).
		if strings.EqualFold(filepath.Ext(args.Path), ".json") && !args.Append {
			if !json.Valid([]byte(content)) {
				return MarshalToolResult(map[string]any{
					"error": map[string]any{
						"code":    "invalid_json",
						"path":    args.Path,
						"message": "content is not valid JSON; the file was not written. Fix the JSON and retry.",
					},
				})
			}
		}

		// Validate YAML content before writing to .yaml/.yml files.
		ext := strings.ToLower(filepath.Ext(args.Path))
		if (ext == ".yaml" || ext == ".yml") && !args.Append {
			var v any
			if err := yaml.Unmarshal([]byte(content), &v); err != nil {
				return MarshalToolResult(map[string]any{
					"error": map[string]any{
						"code":    "invalid_yaml",
						"path":    args.Path,
						"message": fmt.Sprintf("content is not valid YAML; the file was not written. Fix the YAML and retry. Parse error: %s", err.Error()),
					},
				})
			}
		}

		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return "", fmt.Errorf("create parent directory: %w", err)
		}

		flags := os.O_CREATE | os.O_WRONLY
		if args.Append {
			flags |= os.O_APPEND
		} else {
			flags |= os.O_TRUNC
		}
		file, err := os.OpenFile(absPath, flags, 0o644)
		if err != nil {
			return "", fmt.Errorf("open file for write: %w", err)
		}
		defer file.Close()

		n, err := file.WriteString(content)
		if err != nil {
			return "", fmt.Errorf("write file: %w", err)
		}

		afterBytes, err := os.ReadFile(absPath)
		if err != nil {
			return "", fmt.Errorf("read file after write: %w", err)
		}
		after := string(afterBytes)

		result := map[string]any{
			"path":          NormalizeRelPath(workspaceRoot, absPath),
			"bytes_written": n,
			"appended":      args.Append,
			"version":       FileVersionFromBytes(afterBytes),
			"diff": map[string]any{
				"before_bytes": len(before),
				"after_bytes":  len(after),
				"changed":      before != after,
			},
		}
		return MarshalToolResult(result)
	}

	return Tool{Definition: def, Handler: handler}
}
