package deferred

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tools "go-agent-harness/internal/harness/tools"
)

// initTestRepo creates a temporary git repository with a small commit history
// suitable for testing all deep git tools.
//
// Repository layout after setup:
//
//	root/
//	  alpha.go     — created in commit 1, modified in commit 2
//	  beta.go      — created in commit 3
//	  sub/
//	    gamma.go   — created in commit 4
//
// Three commits by two authors:
//  1. "init: add alpha.go" — author: Alice
//  2. "feat: update alpha with token auth" — author: Alice
//  3. "feat: add beta.go" — author: Bob
//  4. "feat: add gamma in sub/" — author: Alice
func initTestRepo(t *testing.T) (repoDir string) {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}

	run("init")
	run("config", "user.email", "alice@example.com")
	run("config", "user.name", "Alice")

	// Commit 1: add alpha.go
	if err := os.WriteFile(filepath.Join(dir, "alpha.go"), []byte("package main\n\nfunc Alpha() {}\n"), 0o644); err != nil {
		t.Fatalf("write alpha.go: %v", err)
	}
	run("add", "alpha.go")
	run("commit", "-m", "init: add alpha.go")

	// Commit 2: update alpha.go with token auth content (for pickaxe search)
	if err := os.WriteFile(filepath.Join(dir, "alpha.go"), []byte("package main\n\n// tokenAuth handles authentication\nfunc Alpha() {\n\t// token auth logic here\n}\n"), 0o644); err != nil {
		t.Fatalf("write alpha.go v2: %v", err)
	}
	run("add", "alpha.go")
	run("commit", "-m", "feat: update alpha with token auth")

	// Commit 3: add beta.go (different author)
	run("config", "user.email", "bob@example.com")
	run("config", "user.name", "Bob")
	if err := os.WriteFile(filepath.Join(dir, "beta.go"), []byte("package main\n\nfunc Beta() {}\n"), 0o644); err != nil {
		t.Fatalf("write beta.go: %v", err)
	}
	run("add", "beta.go")
	run("commit", "-m", "feat: add beta.go")

	// Commit 4: add sub/gamma.go (Alice again)
	run("config", "user.email", "alice@example.com")
	run("config", "user.name", "Alice")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "gamma.go"), []byte("package sub\n\nfunc Gamma() {}\n"), 0o644); err != nil {
		t.Fatalf("write gamma.go: %v", err)
	}
	run("add", "sub/gamma.go")
	run("commit", "-m", "feat: add gamma in sub/")

	return dir
}

// --- git_log_search ---

func TestGitLogSearchTool_Definition(t *testing.T) {
	tool := GitLogSearchTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	assertToolDef(t, tool, "git_log_search", tools.TierDeferred)
	assertHasTags(t, tool, "git", "history", "search")
}

func TestGitLogSearchTool_RequiresQuery(t *testing.T) {
	tool := GitLogSearchTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when query is missing")
	}
}

func TestGitLogSearchTool_MessageMode(t *testing.T) {
	dir := initTestRepo(t)
	tool := GitLogSearchTool(tools.BuildOptions{WorkspaceRoot: dir})

	result, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"beta","mode":"message"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	commits, ok := out["commits"].([]any)
	if !ok {
		t.Fatalf("expected commits array, got %T: %v", out["commits"], out["commits"])
	}
	if len(commits) == 0 {
		t.Fatal("expected at least one commit matching 'beta'")
	}
	// Check first commit has required fields
	first := commits[0].(map[string]any)
	for _, field := range []string{"hash", "short_hash", "author_name", "author_email", "date", "subject", "match_type"} {
		if first[field] == nil {
			t.Errorf("expected field %q in commit result", field)
		}
	}
}

func TestGitLogSearchTool_PickaxeMode(t *testing.T) {
	dir := initTestRepo(t)
	tool := GitLogSearchTool(tools.BuildOptions{WorkspaceRoot: dir})

	result, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"tokenAuth","mode":"pickaxe"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	commits, ok := out["commits"].([]any)
	if !ok {
		t.Fatalf("expected commits array")
	}
	if len(commits) == 0 {
		t.Fatal("expected at least one commit for pickaxe search of 'tokenAuth'")
	}
	first := commits[0].(map[string]any)
	if first["match_type"] != "pickaxe" {
		t.Errorf("expected match_type=pickaxe, got %v", first["match_type"])
	}
}

func TestGitLogSearchTool_BothMode(t *testing.T) {
	dir := initTestRepo(t)
	tool := GitLogSearchTool(tools.BuildOptions{WorkspaceRoot: dir})

	result, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"alpha","mode":"both"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	commits, ok := out["commits"].([]any)
	if !ok {
		t.Fatalf("expected commits array")
	}
	if len(commits) == 0 {
		t.Fatal("expected commits for 'alpha' search")
	}
}

func TestGitLogSearchTool_NoResults(t *testing.T) {
	dir := initTestRepo(t)
	tool := GitLogSearchTool(tools.BuildOptions{WorkspaceRoot: dir})

	result, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"xyzzy_nonexistent_string_12345"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	commits, ok := out["commits"].([]any)
	if !ok {
		// nil commits is acceptable for empty results
		commits = nil
	}
	_ = commits
	if out["total_found"].(float64) != 0 {
		t.Errorf("expected total_found=0, got %v", out["total_found"])
	}
}

func TestGitLogSearchTool_MaxResults(t *testing.T) {
	dir := initTestRepo(t)
	tool := GitLogSearchTool(tools.BuildOptions{WorkspaceRoot: dir})

	// Search for "feat" which matches 3 commits; limit to 1
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"feat","mode":"message","max_results":1}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	commits := out["commits"].([]any)
	if len(commits) > 1 {
		t.Errorf("expected at most 1 commit, got %d", len(commits))
	}
}

func TestGitLogSearchTool_InvalidJSON(t *testing.T) {
	tool := GitLogSearchTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	_, err := tool.Handler(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// --- git_file_history ---

func TestGitFileHistoryTool_Definition(t *testing.T) {
	tool := GitFileHistoryTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	assertToolDef(t, tool, "git_file_history", tools.TierDeferred)
	assertHasTags(t, tool, "git", "history")
}

func TestGitFileHistoryTool_RequiresPath(t *testing.T) {
	tool := GitFileHistoryTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when path is missing")
	}
}

func TestGitFileHistoryTool_Success(t *testing.T) {
	dir := initTestRepo(t)
	tool := GitFileHistoryTool(tools.BuildOptions{WorkspaceRoot: dir})

	result, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"alpha.go"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["file"] != "alpha.go" {
		t.Errorf("expected file=alpha.go, got %v", out["file"])
	}
	commits, ok := out["commits"].([]any)
	if !ok || len(commits) == 0 {
		t.Fatalf("expected commits array with entries, got %v", out["commits"])
	}
	// alpha.go was created in commit 1 and modified in commit 2
	if len(commits) < 2 {
		t.Errorf("expected at least 2 commits for alpha.go, got %d", len(commits))
	}
	first := commits[0].(map[string]any)
	for _, field := range []string{"hash", "short_hash", "author_name", "date", "subject"} {
		if first[field] == nil {
			t.Errorf("expected field %q in commit result", field)
		}
	}
}

func TestGitFileHistoryTool_ShowDiffs(t *testing.T) {
	dir := initTestRepo(t)
	tool := GitFileHistoryTool(tools.BuildOptions{WorkspaceRoot: dir})

	result, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"alpha.go","show_diffs":true}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	commits := out["commits"].([]any)
	if len(commits) == 0 {
		t.Fatal("expected commits")
	}
	// At least one commit should have a diff field
	hasDiff := false
	for _, c := range commits {
		cm := c.(map[string]any)
		if d, ok := cm["diff"].(string); ok && d != "" {
			hasDiff = true
			break
		}
	}
	if !hasDiff {
		t.Error("expected at least one commit to have a non-empty diff when show_diffs=true")
	}
}

func TestGitFileHistoryTool_MaxCommits(t *testing.T) {
	dir := initTestRepo(t)
	tool := GitFileHistoryTool(tools.BuildOptions{WorkspaceRoot: dir})

	result, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"alpha.go","max_commits":1}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	commits := out["commits"].([]any)
	if len(commits) != 1 {
		t.Errorf("expected 1 commit with max_commits=1, got %d", len(commits))
	}
}

func TestGitFileHistoryTool_InvalidJSON(t *testing.T) {
	tool := GitFileHistoryTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	_, err := tool.Handler(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// --- git_blame_context ---

func TestGitBlameContextTool_Definition(t *testing.T) {
	tool := GitBlameContextTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	assertToolDef(t, tool, "git_blame_context", tools.TierDeferred)
	assertHasTags(t, tool, "git", "blame", "authorship")
}

func TestGitBlameContextTool_RequiresPath(t *testing.T) {
	tool := GitBlameContextTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when path is missing")
	}
}

func TestGitBlameContextTool_Success(t *testing.T) {
	dir := initTestRepo(t)
	tool := GitBlameContextTool(tools.BuildOptions{WorkspaceRoot: dir})

	result, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"alpha.go"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["file"] != "alpha.go" {
		t.Errorf("expected file=alpha.go, got %v", out["file"])
	}
	lines, ok := out["lines"].([]any)
	if !ok || len(lines) == 0 {
		t.Fatalf("expected lines array with entries, got %v", out["lines"])
	}
	first := lines[0].(map[string]any)
	for _, field := range []string{"line_number", "content", "commit_hash", "short_hash", "author_name", "date", "commit_subject"} {
		if first[field] == nil {
			t.Errorf("expected field %q in blame line result", field)
		}
	}
	if out["unique_commits"] == nil {
		t.Error("expected unique_commits field")
	}
}

func TestGitBlameContextTool_LineRange(t *testing.T) {
	dir := initTestRepo(t)
	tool := GitBlameContextTool(tools.BuildOptions{WorkspaceRoot: dir})

	result, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"alpha.go","start_line":1,"end_line":3}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	lines := out["lines"].([]any)
	// Should have at most 3 lines (lines 1-3)
	if len(lines) > 3 {
		t.Errorf("expected at most 3 lines for range 1-3, got %d", len(lines))
	}
	if len(lines) == 0 {
		t.Error("expected at least one line")
	}
}

func TestGitBlameContextTool_InvalidJSON(t *testing.T) {
	tool := GitBlameContextTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	_, err := tool.Handler(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// --- git_diff_range ---

func TestGitDiffRangeTool_Definition(t *testing.T) {
	tool := GitDiffRangeTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	assertToolDef(t, tool, "git_diff_range", tools.TierDeferred)
	assertHasTags(t, tool, "git", "diff", "history")
}

func TestGitDiffRangeTool_RequiresFrom(t *testing.T) {
	tool := GitDiffRangeTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when from is missing")
	}
}

func TestGitDiffRangeTool_Success(t *testing.T) {
	dir := initTestRepo(t)
	tool := GitDiffRangeTool(tools.BuildOptions{WorkspaceRoot: dir})

	// Diff first commit to HEAD
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"from":"HEAD~3","to":"HEAD"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, field := range []string{"from", "to", "diff", "stat", "files_changed", "insertions", "deletions", "truncated"} {
		if out[field] == nil {
			t.Errorf("expected field %q in diff_range result", field)
		}
	}
	diff, ok := out["diff"].(string)
	if !ok || diff == "" {
		t.Error("expected non-empty diff")
	}
}

func TestGitDiffRangeTool_StatOnly(t *testing.T) {
	dir := initTestRepo(t)
	tool := GitDiffRangeTool(tools.BuildOptions{WorkspaceRoot: dir})

	result, err := tool.Handler(context.Background(), json.RawMessage(`{"from":"HEAD~1","to":"HEAD","stat_only":true}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// When stat_only=true, diff should be empty
	diff, _ := out["diff"].(string)
	if diff != "" {
		t.Errorf("expected empty diff with stat_only=true, got %q", diff)
	}
	// stat should be non-empty
	stat, ok := out["stat"].(string)
	if !ok || stat == "" {
		t.Error("expected non-empty stat with stat_only=true")
	}
}

func TestGitDiffRangeTool_WithPath(t *testing.T) {
	dir := initTestRepo(t)
	tool := GitDiffRangeTool(tools.BuildOptions{WorkspaceRoot: dir})

	result, err := tool.Handler(context.Background(), json.RawMessage(`{"from":"HEAD~3","to":"HEAD","path":"alpha.go"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	diff, ok := out["diff"].(string)
	if !ok {
		t.Fatal("expected diff field")
	}
	// diff should mention alpha.go
	if diff != "" && !strings.Contains(diff, "alpha.go") {
		t.Errorf("expected alpha.go in diff output, got: %q", diff)
	}
}

func TestGitDiffRangeTool_DefaultToHEAD(t *testing.T) {
	dir := initTestRepo(t)
	tool := GitDiffRangeTool(tools.BuildOptions{WorkspaceRoot: dir})

	// Omit "to" — should default to HEAD
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"from":"HEAD~1"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["to"] != "HEAD" {
		t.Errorf("expected to=HEAD, got %v", out["to"])
	}
}

func TestGitDiffRangeTool_InvalidJSON(t *testing.T) {
	tool := GitDiffRangeTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	_, err := tool.Handler(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// --- git ref option-injection regression tests (issue #789) ---
//
// git parses a leading "-..." argv element as an option even where a revision
// is expected — e.g. --output=/abs/path yields an arbitrary file write from
// these read-classified tools. The refs must be rejected before git runs.

func TestGitBlameContextTool_RejectsOptionLikeRev(t *testing.T) {
	dir := initTestRepo(t)
	pwn := filepath.Join(dir, "pwn")
	tool := GitBlameContextTool(tools.BuildOptions{WorkspaceRoot: dir})

	_, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"alpha.go","rev":"--output=`+pwn+`"}`))
	if err == nil {
		t.Fatal("expected error for option-like rev, got nil")
	}
	if !strings.Contains(err.Error(), "must not begin with '-'") {
		t.Errorf("expected error to contain %q, got %q", "must not begin with '-'", err.Error())
	}
	if _, statErr := os.Stat(pwn); !os.IsNotExist(statErr) {
		t.Errorf("expected %s to not exist (git must not run with an injected option)", pwn)
	}
}

func TestGitDiffRangeTool_RejectsOptionLikeFrom(t *testing.T) {
	dir := initTestRepo(t)
	pwn := filepath.Join(dir, "pwn")
	tool := GitDiffRangeTool(tools.BuildOptions{WorkspaceRoot: dir})

	_, err := tool.Handler(context.Background(), json.RawMessage(`{"from":"--output=`+pwn+`"}`))
	if err == nil {
		t.Fatal("expected error for option-like from, got nil")
	}
	if !strings.Contains(err.Error(), "must not begin with '-'") {
		t.Errorf("expected error to contain %q, got %q", "must not begin with '-'", err.Error())
	}
	// Pre-fix, git parses the range spec "--output=<pwn>..HEAD" as the
	// --output option and creates the file "<pwn>..HEAD".
	if _, statErr := os.Stat(pwn + "..HEAD"); !os.IsNotExist(statErr) {
		t.Errorf("expected %s to not exist (git must not run with an injected option)", pwn+"..HEAD")
	}
}

func TestGitDiffRangeTool_RejectsOptionLikeTo(t *testing.T) {
	dir := initTestRepo(t)
	pwn := filepath.Join(dir, "pwn")
	tool := GitDiffRangeTool(tools.BuildOptions{WorkspaceRoot: dir})

	_, err := tool.Handler(context.Background(), json.RawMessage(`{"from":"HEAD~1","to":"--output=`+pwn+`"}`))
	if err == nil {
		t.Fatal("expected error for option-like to, got nil")
	}
	if !strings.Contains(err.Error(), "must not begin with '-'") {
		t.Errorf("expected error to contain %q, got %q", "must not begin with '-'", err.Error())
	}
	if _, statErr := os.Stat(pwn); !os.IsNotExist(statErr) {
		t.Errorf("expected %s to not exist (git must not run with an injected option)", pwn)
	}
}

func TestGitBlameContextTool_AcceptsLegitRev(t *testing.T) {
	dir := initTestRepo(t)
	tool := GitBlameContextTool(tools.BuildOptions{WorkspaceRoot: dir})

	result, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"alpha.go","rev":"HEAD~1"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["rev"] != "HEAD~1" {
		t.Errorf("expected rev=HEAD~1, got %v", out["rev"])
	}
	lines, ok := out["lines"].([]any)
	if !ok || len(lines) == 0 {
		t.Fatalf("expected lines array with entries, got %v", out["lines"])
	}
}

func TestGitDiffRangeTool_AcceptsSHARange(t *testing.T) {
	dir := initTestRepo(t)
	tool := GitDiffRangeTool(tools.BuildOptions{WorkspaceRoot: dir})

	sha1 := gitRevParse(t, dir, "HEAD~2")
	sha2 := gitRevParse(t, dir, "HEAD")

	result, err := tool.Handler(context.Background(), json.RawMessage(`{"from":"`+sha1+`","to":"`+sha2+`"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["from"] != sha1 || out["to"] != sha2 {
		t.Errorf("expected from=%s to=%s, got from=%v to=%v", sha1, sha2, out["from"], out["to"])
	}
	diff, ok := out["diff"].(string)
	if !ok || diff == "" {
		t.Error("expected non-empty diff for SHA range")
	}
}

func gitRevParse(t *testing.T, dir, rev string) string {
	t.Helper()

	cmd := exec.Command("git", "rev-parse", rev)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse %s: %v\n%s", rev, err, out)
	}
	return strings.TrimSpace(string(out))
}

// --- git_contributor_context ---

func TestGitContributorContextTool_Definition(t *testing.T) {
	tool := GitContributorContextTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	assertToolDef(t, tool, "git_contributor_context", tools.TierDeferred)
	assertHasTags(t, tool, "git", "contributors", "authorship")
}

func TestGitContributorContextTool_WholeRepo(t *testing.T) {
	dir := initTestRepo(t)
	tool := GitContributorContextTool(tools.BuildOptions{WorkspaceRoot: dir})

	result, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	authors, ok := out["authors"].([]any)
	if !ok || len(authors) == 0 {
		t.Fatalf("expected authors array with entries, got %v", out["authors"])
	}
	// Should have both Alice and Bob
	if len(authors) < 2 {
		t.Errorf("expected at least 2 authors, got %d", len(authors))
	}
	first := authors[0].(map[string]any)
	for _, field := range []string{"name", "email", "commit_count"} {
		if first[field] == nil {
			t.Errorf("expected field %q in author result", field)
		}
	}
	// Authors should be sorted by commit_count descending (Alice has 3 commits, Bob has 1)
	firstCount := first["commit_count"].(float64)
	if len(authors) > 1 {
		second := authors[1].(map[string]any)
		secondCount := second["commit_count"].(float64)
		if firstCount < secondCount {
			t.Errorf("expected authors sorted by commit_count descending: first=%v, second=%v", firstCount, secondCount)
		}
	}
}

func TestGitContributorContextTool_WithPath(t *testing.T) {
	dir := initTestRepo(t)
	tool := GitContributorContextTool(tools.BuildOptions{WorkspaceRoot: dir})

	// sub/ only has Alice commits
	result, err := tool.Handler(context.Background(), json.RawMessage(`{"path":"sub/"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	authors, ok := out["authors"].([]any)
	if !ok || len(authors) == 0 {
		t.Fatalf("expected authors array with entries for sub/, got %v", out["authors"])
	}
	// Only Alice should appear for sub/
	if len(authors) != 1 {
		t.Errorf("expected 1 author for sub/, got %d", len(authors))
	}
	first := authors[0].(map[string]any)
	if first["email"] != "alice@example.com" {
		t.Errorf("expected alice@example.com, got %v", first["email"])
	}
}

func TestGitContributorContextTool_MaxAuthors(t *testing.T) {
	dir := initTestRepo(t)
	tool := GitContributorContextTool(tools.BuildOptions{WorkspaceRoot: dir})

	result, err := tool.Handler(context.Background(), json.RawMessage(`{"max_authors":1}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	authors := out["authors"].([]any)
	if len(authors) != 1 {
		t.Errorf("expected 1 author with max_authors=1, got %d", len(authors))
	}
}

func TestGitContributorContextTool_InvalidJSON(t *testing.T) {
	tool := GitContributorContextTool(tools.BuildOptions{WorkspaceRoot: t.TempDir()})
	_, err := tool.Handler(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
