package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go-agent-harness/internal/harness/tools/descriptions"
)

type gitStatusArgs struct {
	// Porcelain is a pointer so an absent key keeps the porcelain default
	// while an explicit false switches to human-readable output.
	Porcelain *bool `json:"porcelain,omitempty"`
}

func gitStatusTool(workspaceRoot string) Tool {
	return MustTyped(TypedSpec{
		Name:         "git_status",
		Description:  descriptions.Load("git_status"),
		Action:       ActionRead,
		ParallelSafe: true,
		Tags:         []string{"git", "status", "repository", "staged", "modified"},
	}, func(ctx context.Context, args gitStatusArgs) (any, error) {
		porcelain := true
		if args.Porcelain != nil {
			porcelain = *args.Porcelain
		}

		absRoot, err := filepath.Abs(workspaceRoot)
		if err != nil {
			return nil, fmt.Errorf("resolve workspace root: %w", err)
		}

		cmdArgs := []string{"-C", absRoot, "status"}
		if porcelain {
			cmdArgs = append(cmdArgs, "--porcelain=v1")
		}
		output, exitCode, timedOut, err := runCommand(ctx, 30*time.Second, "git", cmdArgs...)
		if err != nil {
			return nil, fmt.Errorf("git status failed: %w", err)
		}

		trimmed := strings.TrimSpace(output)
		return map[string]any{
			"clean":     trimmed == "",
			"output":    trimmed,
			"exit_code": exitCode,
			"timed_out": timedOut,
		}, nil
	})
}
