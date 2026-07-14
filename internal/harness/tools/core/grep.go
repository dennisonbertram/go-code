package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	tools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/descriptions"
)

// GrepTool returns a core tool that searches file contents in the workspace.
func GrepTool(opts tools.BuildOptions) tools.Tool {
	def := tools.Definition{
		Name:         "grep",
		Description:  descriptions.Load("grep"),
		Action:       tools.ActionRead,
		ParallelSafe: true,
		Tier:         tools.TierCore,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":          map[string]any{"type": "string"},
				"path":           map[string]any{"type": "string", "description": "relative path/file in workspace"},
				"regex":          map[string]any{"type": "boolean"},
				"case_sensitive": map[string]any{"type": "boolean"},
				"max_matches":    map[string]any{"type": "integer", "minimum": 1, "maximum": 2000},
				"literal_text":   map[string]any{"type": "boolean"},
			},
			"required": []string{"query"},
		},
	}

	workspaceRoot := opts.WorkspaceRoot

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			Query         string `json:"query"`
			Path          string `json:"path"`
			Regex         bool   `json:"regex"`
			LiteralText   bool   `json:"literal_text"`
			CaseSensitive bool   `json:"case_sensitive"`
			MaxMatches    int    `json:"max_matches"`
		}
		args.Path = "."
		args.MaxMatches = 200
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse grep args: %w", err)
		}
		if strings.TrimSpace(args.Query) == "" {
			return "", fmt.Errorf("query is required")
		}
		if args.MaxMatches <= 0 {
			args.MaxMatches = 200
		}
		if args.MaxMatches > 2000 {
			args.MaxMatches = 2000
		}
		if args.LiteralText {
			args.Regex = false
		}

		absPath, err := tools.ResolveWorkspacePathConfined(ctx, workspaceRoot, args.Path, opts.SandboxScope)
		if err != nil {
			return "", err
		}

		matcher, err := tools.BuildLineMatcher(args.Query, args.Regex, args.CaseSensitive)
		if err != nil {
			return "", err
		}

		matches := make([]map[string]any, 0, args.MaxMatches)
		truncated := false
		addMatch := func(path string, lineNumber int, line string) bool {
			matches = append(matches, map[string]any{
				"path":        tools.NormalizeRelPath(workspaceRoot, path),
				"line_number": lineNumber,
				"line":        line,
			})
			if len(matches) >= args.MaxMatches {
				truncated = true
				return true
			}
			return false
		}

		searchFile := func(path string) error {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			if bytes.IndexByte(data, 0) >= 0 {
				return nil
			}
			scanner := bufio.NewScanner(bytes.NewReader(data))
			lineNumber := 0
			for scanner.Scan() {
				lineNumber++
				line := scanner.Text()
				if matcher(line) {
					if stop := addMatch(path, lineNumber, line); stop {
						return io.EOF
					}
				}
			}
			return scanner.Err()
		}

		info, err := os.Stat(absPath)
		if err != nil {
			return "", fmt.Errorf("stat grep path: %w", err)
		}
		if info.IsDir() {
			err := filepath.WalkDir(absPath, func(path string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return nil
				}
				if d.IsDir() {
					if strings.HasPrefix(d.Name(), ".git") {
						return fs.SkipDir
					}
					return nil
				}
				if err := searchFile(path); err != nil {
					if errors.Is(err, io.EOF) {
						return io.EOF
					}
					return nil
				}
				return nil
			})
			if err != nil && !errors.Is(err, io.EOF) {
				return "", fmt.Errorf("walk grep path: %w", err)
			}
		} else {
			if err := searchFile(absPath); err != nil && !errors.Is(err, io.EOF) {
				return "", fmt.Errorf("search file: %w", err)
			}
		}

		result := map[string]any{
			"query":     args.Query,
			"matches":   matches,
			"truncated": truncated,
		}
		return tools.MarshalToolResult(result)
	}

	return tools.Tool{Definition: def, Handler: handler}
}
