package core

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go-agent-harness/internal/harness/tools"
)

func TestGitDiffTool_Basic(t *testing.T) {
	opts := tools.BuildOptions{WorkspaceRoot: "."}
	gitDiff := GitDiffTool(opts)

	// Test default call with no arguments
	resultStr, err := gitDiff.Handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("GitDiffTool handler failed: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(resultStr), &result); err != nil {
		t.Fatalf("Failed to parse result JSON: %v", err)
	}

	// Check keys and types
	if _, ok := result["diff"]; !ok {
		t.Error("Result missing 'diff' key")
	}
	if _, ok := result["exit_code"]; !ok {
		t.Error("Result missing 'exit_code' key")
	}
	if _, ok := result["timed_out"]; !ok {
		t.Error("Result missing 'timed_out' key")
	}
}

func TestGitDiffTool_MaxBytes(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")
	writeFile(t, filepath.Join(repo, "example.txt"), "short\n")
	runGit(t, repo, "add", "example.txt")
	runGit(t, repo, "commit", "-m", "initial")
	writeFile(t, filepath.Join(repo, "example.txt"), "short\nthis line makes the diff long enough to truncate\n")

	opts := tools.BuildOptions{WorkspaceRoot: repo}
	gitDiff := GitDiffTool(opts)

	args := []byte(`{"max_bytes":10}`)
	resultStr, err := gitDiff.Handler(context.Background(), args)
	if err != nil {
		t.Fatalf("GitDiffTool handler failed with max_bytes: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(resultStr), &result); err != nil {
		t.Fatalf("Failed to parse result JSON: %v", err)
	}

	diff, ok := result["diff"].(string)
	if !ok {
		t.Fatal("Diff field is not a string")
	}
	if len(diff) > 10 {
		t.Errorf("Diff output longer than max_bytes: got %d", len(diff))
	}
	if truncated, ok := result["truncated"].(bool); !ok || !truncated {
		t.Error("Truncated flag not set when output truncated")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestGitDiffTool_BadJSON(t *testing.T) {
	opts := tools.BuildOptions{WorkspaceRoot: "."}
	gitDiff := GitDiffTool(opts)

	// Passing malformed JSON should cause an error
	_, err := gitDiff.Handler(context.Background(), []byte(`{"max_bytes": "notanint"}`))
	if err == nil {
		t.Error("Expected error for malformed JSON input, got nil")
	}
}

// Regression test for issue #789: an option-like target must be rejected
// before git runs — git parses e.g. --output=/abs/path as an option, giving
// an arbitrary file write from a read-classified tool. No repo is needed:
// validation precedes exec.
func TestGitDiffTool_RejectsOptionLikeTarget(t *testing.T) {
	dir := t.TempDir()
	pwn := filepath.Join(dir, "pwn")
	gitDiff := GitDiffTool(tools.BuildOptions{WorkspaceRoot: dir})

	_, err := gitDiff.Handler(context.Background(), []byte(`{"target":"--output=`+pwn+`"}`))
	if err == nil {
		t.Fatal("expected error for option-like target, got nil")
	}
	if !strings.Contains(err.Error(), "must not begin with '-'") {
		t.Errorf("expected error to contain %q, got %q", "must not begin with '-'", err.Error())
	}
	if _, statErr := os.Stat(pwn); !os.IsNotExist(statErr) {
		t.Errorf("expected %s to not exist (git must not run with an injected option)", pwn)
	}
}

// TestGitDiffTool_AcceptsLegitTargets guards the fix for issue #789 against
// over-rejection: ordinary revisions, branch names, and SHA ranges must
// still work.
func TestGitDiffTool_AcceptsLegitTargets(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")
	writeFile(t, filepath.Join(repo, "example.txt"), "one\n")
	runGit(t, repo, "add", "example.txt")
	runGit(t, repo, "commit", "-m", "initial")
	writeFile(t, filepath.Join(repo, "example.txt"), "one\ntwo\n")
	runGit(t, repo, "add", "example.txt")
	runGit(t, repo, "commit", "-m", "second")
	runGit(t, repo, "branch", "feature")

	sha1 := gitOutput(t, repo, "rev-parse", "HEAD~1")
	sha2 := gitOutput(t, repo, "rev-parse", "HEAD")

	gitDiff := GitDiffTool(tools.BuildOptions{WorkspaceRoot: repo})
	for _, target := range []string{"HEAD~1", "feature", sha1 + ".." + sha2} {
		resultStr, err := gitDiff.Handler(context.Background(), []byte(`{"target":"`+target+`"}`))
		if err != nil {
			t.Errorf("target %q: unexpected error: %v", target, err)
			continue
		}
		var result map[string]any
		if err := json.Unmarshal([]byte(resultStr), &result); err != nil {
			t.Fatalf("target %q: parse result: %v", target, err)
		}
		if code, ok := result["exit_code"].(float64); !ok || code != 0 {
			t.Errorf("target %q: expected exit_code 0, got %v", target, result["exit_code"])
		}
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
