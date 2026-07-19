package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The migrated tools must expose exactly the schema their hand-written
// predecessors declared: the expected literals below are copied verbatim
// from the pre-migration Parameters maps.

func TestGlobToolSchemaParity(t *testing.T) {
	tool := globTool(t.TempDir(), SandboxScopeWorkspace)
	assertSchemaEqual(t, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern":     map[string]any{"type": "string", "description": "glob pattern relative to workspace"},
			"max_matches": map[string]any{"type": "integer", "minimum": 1, "maximum": 2000},
		},
		"required": []string{"pattern"},
	}, tool.Definition.Parameters)

	def := tool.Definition
	if def.Name != "glob" || def.Action != ActionList || !def.ParallelSafe || def.Mutating {
		t.Fatalf("glob metadata changed: %+v", def)
	}
	if !reflect.DeepEqual(def.Tags, []string{"glob", "find", "files", "pattern", "names", "wildcard"}) {
		t.Fatalf("glob tags changed: %v", def.Tags)
	}
}

func TestGitStatusToolSchemaParity(t *testing.T) {
	tool := gitStatusTool(t.TempDir())
	assertSchemaEqual(t, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"porcelain": map[string]any{"type": "boolean"},
		},
	}, tool.Definition.Parameters)

	def := tool.Definition
	if def.Name != "git_status" || def.Action != ActionRead || !def.ParallelSafe || def.Mutating {
		t.Fatalf("git_status metadata changed: %+v", def)
	}
	if !reflect.DeepEqual(def.Tags, []string{"git", "status", "repository", "staged", "modified"}) {
		t.Fatalf("git_status tags changed: %v", def.Tags)
	}
}

func TestGlobToolBehavior(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.go", "b.go", "c.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tool := globTool(root, SandboxScopeWorkspace)

	out, err := tool.Handler(context.Background(), json.RawMessage(`{"pattern":"*.go"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var result struct {
		Pattern string   `json:"pattern"`
		Matches []string `json:"matches"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Pattern != "*.go" || !reflect.DeepEqual(result.Matches, []string{"a.go", "b.go"}) {
		t.Fatalf("unexpected result: %+v", result)
	}

	if _, err := tool.Handler(context.Background(), json.RawMessage(`{}`)); err == nil ||
		!strings.Contains(err.Error(), "pattern is required") {
		t.Fatalf("expected pattern-required error, got %v", err)
	}

	if _, err := tool.Handler(context.Background(), json.RawMessage(`{"max_matches":"many"}`)); err == nil ||
		!strings.Contains(err.Error(), "parse glob args") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestGitStatusToolBehavior(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := gitStatusTool(root)

	// Absent args default to porcelain output, matching the pre-migration handler.
	out, err := tool.Handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var result struct {
		Clean  bool   `json:"clean"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Clean || !strings.Contains(result.Output, "?? f.txt") {
		t.Fatalf("expected porcelain untracked entry, got %+v", result)
	}

	// Explicit porcelain=false switches to the human-readable format.
	out, err = tool.Handler(context.Background(), json.RawMessage(`{"porcelain":false}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !strings.Contains(result.Output, "Untracked files") {
		t.Fatalf("expected human-readable status, got %+v", result)
	}
}
