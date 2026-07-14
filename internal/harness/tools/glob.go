package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"go-agent-harness/internal/harness/tools/descriptions"
)

func globTool(workspaceRoot string, sandboxScope SandboxScope) Tool {
	def := Definition{
		Name:         "glob",
		Description:  descriptions.Load("glob"),
		Action:       ActionList,
		ParallelSafe: true,
		Tags:         []string{"glob", "find", "files", "pattern", "names", "wildcard"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":     map[string]any{"type": "string", "description": "glob pattern relative to workspace"},
				"max_matches": map[string]any{"type": "integer", "minimum": 1, "maximum": 2000},
			},
			"required": []string{"pattern"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		args := struct {
			Pattern    string `json:"pattern"`
			MaxMatches int    `json:"max_matches"`
		}{MaxMatches: 500}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse glob args: %w", err)
		}
		if strings.TrimSpace(args.Pattern) == "" {
			return "", fmt.Errorf("pattern is required")
		}
		if args.MaxMatches <= 0 {
			args.MaxMatches = 500
		}
		if args.MaxMatches > 2000 {
			args.MaxMatches = 2000
		}
		if err := validateWorkspaceRelativePattern(args.Pattern); err != nil {
			return "", err
		}

		absRoot, err := filepath.Abs(workspaceRoot)
		if err != nil {
			return "", fmt.Errorf("resolve workspace root: %w", err)
		}
		absPattern := filepath.Join(absRoot, filepath.FromSlash(args.Pattern))
		matches, err := filepath.Glob(absPattern)
		if err != nil {
			return "", fmt.Errorf("glob pattern: %w", err)
		}

		scope := EffectiveSandboxScope(ctx, sandboxScope)
		filtered := make([]string, 0, len(matches))
		for _, match := range matches {
			rel, err := filepath.Rel(absRoot, match)
			if err != nil {
				continue
			}
			if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
			// Under workspace scope, also drop matches that only appear inside the
			// workspace because a symlink component in the pattern resolves outside
			// it (e.g. a `workspace/escape-link -> /etc` symlink followed by the
			// glob library when matching `escape-link/*`).
			if _, err := ConfineWorkspacePath(scope, workspaceRoot, nil, match); err != nil {
				continue
			}
			filtered = append(filtered, filepath.ToSlash(rel))
			if len(filtered) >= args.MaxMatches {
				break
			}
		}
		sort.Strings(filtered)

		result := map[string]any{
			"pattern": args.Pattern,
			"matches": filtered,
		}
		return MarshalToolResult(result)
	}

	return Tool{Definition: def, Handler: handler}
}
