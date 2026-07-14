package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go-agent-harness/internal/harness/tools/descriptions"
)

func lsTool(workspaceRoot string, sandboxScope SandboxScope) Tool {
	def := Definition{
		Name:         "ls",
		Description:  descriptions.Load("ls"),
		Action:       ActionList,
		ParallelSafe: true,
		Tags:         []string{"list", "directory", "ls", "files", "tree"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":           map[string]any{"type": "string", "description": "relative path inside workspace"},
				"recursive":      map[string]any{"type": "boolean"},
				"max_entries":    map[string]any{"type": "integer", "minimum": 1, "maximum": 2000},
				"include_hidden": map[string]any{"type": "boolean"},
				"depth":          map[string]any{"type": "integer", "minimum": 0, "maximum": 50},
			},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		args := struct {
			Path          string `json:"path"`
			Recursive     bool   `json:"recursive"`
			MaxEntries    int    `json:"max_entries"`
			IncludeHidden bool   `json:"include_hidden"`
			Depth         int    `json:"depth"`
		}{Path: ".", MaxEntries: 200}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("parse ls args: %w", err)
			}
		}
		if args.MaxEntries <= 0 {
			args.MaxEntries = 200
		}
		if args.MaxEntries > 2000 {
			args.MaxEntries = 2000
		}
		if args.Depth < 0 {
			args.Depth = 0
		}

		absPath, err := ResolveWorkspacePathConfined(ctx, workspaceRoot, args.Path, sandboxScope)
		if err != nil {
			return "", err
		}

		entries, truncated, err := collectEntries(workspaceRoot, absPath, args.Recursive, args.MaxEntries, args.IncludeHidden, args.Depth)
		if err != nil {
			return "", err
		}
		sort.Strings(entries)

		result := map[string]any{
			"path":      NormalizeRelPath(workspaceRoot, absPath),
			"entries":   entries,
			"truncated": truncated,
		}
		return MarshalToolResult(result)
	}

	return Tool{Definition: def, Handler: handler}
}

func collectEntries(workspaceRoot, absPath string, recursive bool, maxEntries int, includeHidden bool, depth int) ([]string, bool, error) {
	entries := make([]string, 0, maxEntries)
	truncated := false

	appendEntry := func(path string) error {
		name := filepath.Base(path)
		if !includeHidden && strings.HasPrefix(name, ".") {
			return nil
		}
		entries = append(entries, NormalizeRelPath(workspaceRoot, path))
		if len(entries) >= maxEntries {
			truncated = true
			return io.EOF
		}
		return nil
	}

	if recursive {
		baseDepth := strings.Count(filepath.Clean(absPath), string(filepath.Separator))
		err := filepath.WalkDir(absPath, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == absPath {
				return nil
			}
			if depth > 0 {
				currentDepth := strings.Count(filepath.Clean(path), string(filepath.Separator)) - baseDepth
				if currentDepth > depth {
					if d.IsDir() {
						return fs.SkipDir
					}
					return nil
				}
			}
			if err := appendEntry(path); err != nil {
				return err
			}
			if !includeHidden && d.IsDir() && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		})
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, false, fmt.Errorf("walk entries: %w", err)
		}
		return entries, truncated, nil
	}

	dirEntries, err := os.ReadDir(absPath)
	if err != nil {
		return nil, false, fmt.Errorf("read directory: %w", err)
	}
	for _, entry := range dirEntries {
		if !includeHidden && strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if err := appendEntry(filepath.Join(absPath, entry.Name())); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, false, err
		}
	}
	return entries, truncated, nil
}
