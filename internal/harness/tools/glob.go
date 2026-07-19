package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"go-agent-harness/internal/harness/tools/descriptions"
)

type globArgs struct {
	Pattern    string `json:"pattern" desc:"glob pattern relative to workspace"`
	MaxMatches int    `json:"max_matches,omitempty" min:"1" max:"2000"`
}

func globTool(workspaceRoot string, sandboxScope SandboxScope) Tool {
	return MustTyped(TypedSpec{
		Name:         "glob",
		Description:  descriptions.Load("glob"),
		Action:       ActionList,
		ParallelSafe: true,
		Tags:         []string{"glob", "find", "files", "pattern", "names", "wildcard"},
	}, func(ctx context.Context, args globArgs) (any, error) {
		if strings.TrimSpace(args.Pattern) == "" {
			return nil, fmt.Errorf("pattern is required")
		}
		if args.MaxMatches <= 0 {
			args.MaxMatches = 500
		}
		if args.MaxMatches > 2000 {
			args.MaxMatches = 2000
		}
		if err := validateWorkspaceRelativePattern(args.Pattern); err != nil {
			return nil, err
		}

		absRoot, err := filepath.Abs(workspaceRoot)
		if err != nil {
			return nil, fmt.Errorf("resolve workspace root: %w", err)
		}
		absPattern := filepath.Join(absRoot, filepath.FromSlash(args.Pattern))
		matches, err := filepath.Glob(absPattern)
		if err != nil {
			return nil, fmt.Errorf("glob pattern: %w", err)
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

		return map[string]any{
			"pattern": args.Pattern,
			"matches": filtered,
		}, nil
	})
}
