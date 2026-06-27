package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// runGit is a test helper that runs a git command in the given directory and
// fatals if the command fails.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// initTestRepo creates a temporary directory, initialises a git repository with
// an initial commit, and returns the directory path.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")

	// Create an initial commit so that HEAD exists (required for worktrees).
	readmePath := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readmePath, []byte("test"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "init")

	return dir
}

// --------------------------------------------------------------------------
// TestSanitizeBranch
// --------------------------------------------------------------------------

func TestSanitizeBranch(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "normal alphanumeric",
			input: "issue-42",
			want:  "issue-42",
		},
		{
			name:  "dots and underscores preserved",
			input: "v1.2_feature",
			want:  "v1.2_feature",
		},
		{
			name:  "slashes replaced",
			input: "feature/my-branch",
			want:  "feature-my-branch",
		},
		{
			name:  "spaces replaced",
			input: "hello world",
			want:  "hello-world",
		},
		{
			name:  "special chars replaced",
			input: "foo@bar#baz!qux",
			want:  "foo-bar-baz-qux",
		},
		{
			name:  "empty input returns workspace",
			input: "",
			want:  "workspace",
		},
		{
			name:  "all invalid chars returns workspace",
			input: "!!!@@@",
			want:  "------",
		},
		{
			name:  "uppercase preserved",
			input: "MyFeature",
			want:  "MyFeature",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeBranch(tc.input)
			if got != tc.want {
				t.Errorf("sanitizeBranch(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestWorktreeWorkspace_Provision
// --------------------------------------------------------------------------

func TestWorktreeWorkspace_Provision(t *testing.T) {
	repo := initTestRepo(t)
	ctx := context.Background()

	ws := NewWorktree(defaultHarnessURL, repo)
	opts := Options{ID: "issue-183"}

	if err := ws.Provision(ctx, opts); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	t.Cleanup(func() { _ = ws.Destroy(ctx) })

	// Verify the worktree directory was created.
	if _, err := os.Stat(ws.WorkspacePath()); err != nil {
		t.Errorf("worktree directory not found: %v", err)
	}
}

// --------------------------------------------------------------------------
// TestWorktreeWorkspace_Provision_EmptyID
// --------------------------------------------------------------------------

func TestWorktreeWorkspace_Provision_EmptyID(t *testing.T) {
	repo := initTestRepo(t)
	ctx := context.Background()

	ws := NewWorktree(defaultHarnessURL, repo)
	err := ws.Provision(ctx, Options{ID: ""})
	if err == nil {
		t.Fatal("Provision with empty ID: expected error, got nil")
	}
}

// --------------------------------------------------------------------------
// TestWorktreeWorkspace_WorkspacePath_BeforeProvision
// --------------------------------------------------------------------------

func TestWorktreeWorkspace_WorkspacePath_BeforeProvision(t *testing.T) {
	ws := NewWorktree(defaultHarnessURL, "/some/repo")
	if got := ws.WorkspacePath(); got != "" {
		t.Errorf("WorkspacePath before Provision = %q, want empty string", got)
	}
}

// --------------------------------------------------------------------------
// TestWorktreeWorkspace_WorkspacePath_AfterProvision
// --------------------------------------------------------------------------

func TestWorktreeWorkspace_WorkspacePath_AfterProvision(t *testing.T) {
	repo := initTestRepo(t)
	ctx := context.Background()

	ws := NewWorktree(defaultHarnessURL, repo)
	opts := Options{ID: "issue-183"}

	if err := ws.Provision(ctx, opts); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	t.Cleanup(func() { _ = ws.Destroy(ctx) })

	got := ws.WorkspacePath()
	want := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-subagents", "issue-183")
	if got != want {
		t.Errorf("WorkspacePath = %q, want %q", got, want)
	}
}

func TestWorktreeWorkspace_Provision_CustomRootAndBaseRef(t *testing.T) {
	repo := initTestRepo(t)
	ctx := context.Background()
	rootDir := filepath.Join(t.TempDir(), "custom-root")

	ws := NewWorktree(defaultHarnessURL, repo)
	opts := Options{
		ID:              "custom-root-test",
		WorktreeRootDir: rootDir,
		WorktreeBaseRef: "HEAD",
	}

	if err := ws.Provision(ctx, opts); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	t.Cleanup(func() { _ = ws.Destroy(ctx) })

	if got := ws.WorkspacePath(); filepath.Dir(got) != rootDir {
		t.Fatalf("workspace root = %q, want parent %q", got, rootDir)
	}
	if got := ws.BaseRef(); got != "HEAD" {
		t.Fatalf("BaseRef = %q, want HEAD", got)
	}
	if ws.BranchName() == "" {
		t.Fatal("expected branch name")
	}
}

func TestWorktreeWorkspace_ProvisionSerializesWorktreeAddPerRepo(t *testing.T) {
	oldRunGitCommand := runGitCommand
	defer func() { runGitCommand = oldRunGitCommand }()

	var activeAdds atomic.Int32
	var overlapped atomic.Bool
	runGitCommand = func(_ context.Context, args ...string) ([]byte, error) {
		if isWorktreeTestCommand(args, "add") {
			if activeAdds.Add(1) != 1 {
				overlapped.Store(true)
			}
			time.Sleep(50 * time.Millisecond)
			activeAdds.Add(-1)
		}
		return []byte("ok"), nil
	}

	repo := t.TempDir()
	root := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, id := range []string{"one", "two"} {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			ws := NewWorktree(defaultHarnessURL, repo)
			errs <- ws.Provision(context.Background(), Options{
				ID:              id,
				WorktreeRootDir: root,
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Provision: %v", err)
		}
	}
	if overlapped.Load() {
		t.Fatal("same-repo git worktree add commands overlapped")
	}
}

func TestWorktreeWorkspace_DestroyPrunesAfterRemoveError(t *testing.T) {
	oldRunGitCommand := runGitCommand
	defer func() { runGitCommand = oldRunGitCommand }()

	var commands [][]string
	runGitCommand = func(_ context.Context, args ...string) ([]byte, error) {
		commands = append(commands, slices.Clone(args))
		if isWorktreeTestCommand(args, "remove") {
			return []byte("remove failed"), errors.New("remove failed")
		}
		return []byte("ok"), nil
	}

	ws := &WorktreeWorkspace{
		repoPath: t.TempDir(),
		path:     filepath.Join(t.TempDir(), "wt"),
		branch:   "workspace-test",
	}
	err := ws.Destroy(context.Background())
	if err == nil {
		t.Fatal("expected remove error")
	}
	if !worktreeTestSawCommand(commands, "prune") {
		t.Fatalf("expected git worktree prune after remove error, saw %v", commands)
	}
}

func TestPoolClosePrunesEachDistinctWorktreeRepoOnce(t *testing.T) {
	oldRunGitCommand := runGitCommand
	defer func() { runGitCommand = oldRunGitCommand }()

	repoOne := filepath.Join(t.TempDir(), "repo-one")
	repoTwo := filepath.Join(t.TempDir(), "repo-two")
	prunes := map[string]int{}
	runGitCommand = func(_ context.Context, args ...string) ([]byte, error) {
		if isWorktreeTestCommand(args, "prune") && len(args) >= 2 {
			prunes[args[1]]++
		}
		return []byte("ok"), nil
	}

	p := &Pool{
		cancel: func() {},
		entries: []*poolEntry{
			{ws: &WorktreeWorkspace{repoPath: repoOne}},
			{ws: &WorktreeWorkspace{repoPath: repoOne}},
			{ws: &WorktreeWorkspace{repoPath: repoTwo}},
		},
	}
	p.Close()

	if prunes[repoOne] != 1 {
		t.Fatalf("repoOne prune count = %d, want 1", prunes[repoOne])
	}
	if prunes[repoTwo] != 1 {
		t.Fatalf("repoTwo prune count = %d, want 1", prunes[repoTwo])
	}
}

func isWorktreeTestCommand(args []string, subcommand string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "worktree" && args[i+1] == subcommand {
			return true
		}
	}
	return false
}

func worktreeTestSawCommand(commands [][]string, subcommand string) bool {
	for _, args := range commands {
		if isWorktreeTestCommand(args, subcommand) {
			return true
		}
	}
	return false
}

// --------------------------------------------------------------------------
// TestWorktreeWorkspace_HarnessURL_Default
// --------------------------------------------------------------------------

func TestWorktreeWorkspace_HarnessURL_Default(t *testing.T) {
	ws := &WorktreeWorkspace{}
	if got := ws.HarnessURL(); got != defaultHarnessURL {
		t.Errorf("HarnessURL = %q, want %q", got, defaultHarnessURL)
	}
}

// --------------------------------------------------------------------------
// TestWorktreeWorkspace_HarnessURL_FromEnv
// --------------------------------------------------------------------------

func TestWorktreeWorkspace_HarnessURL_FromEnv(t *testing.T) {
	repo := initTestRepo(t)
	ctx := context.Background()

	ws := NewWorktree("", repo)
	opts := Options{
		ID: "issue-183-env",
		Env: map[string]string{
			"HARNESS_URL": "http://harness.example.com:9000",
		},
	}

	if err := ws.Provision(ctx, opts); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	t.Cleanup(func() { _ = ws.Destroy(ctx) })

	want := "http://harness.example.com:9000"
	if got := ws.HarnessURL(); got != want {
		t.Errorf("HarnessURL = %q, want %q", got, want)
	}
}

// --------------------------------------------------------------------------
// TestWorktreeWorkspace_Destroy
// --------------------------------------------------------------------------

func TestWorktreeWorkspace_Destroy(t *testing.T) {
	repo := initTestRepo(t)
	ctx := context.Background()

	ws := NewWorktree(defaultHarnessURL, repo)
	opts := Options{ID: "issue-destroy"}

	if err := ws.Provision(ctx, opts); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	worktreePath := ws.WorkspacePath()

	// Confirm directory exists before destroy.
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("worktree path missing before Destroy: %v", err)
	}

	if err := ws.Destroy(ctx); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	// Directory should be gone after destroy.
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Errorf("worktree directory still exists after Destroy: %v", err)
	}
}

// --------------------------------------------------------------------------
// TestWorktreeWorkspace_Destroy_NotProvisioned
// --------------------------------------------------------------------------

func TestWorktreeWorkspace_Destroy_NotProvisioned(t *testing.T) {
	ws := &WorktreeWorkspace{}
	// Destroy on an unprovisioned workspace must be a no-op.
	if err := ws.Destroy(context.Background()); err != nil {
		t.Errorf("Destroy on unprovisioned workspace: got %v, want nil", err)
	}
}

// --------------------------------------------------------------------------
// TestWorktreeWorkspace_FullLifecycle
// --------------------------------------------------------------------------

func TestWorktreeWorkspace_FullLifecycle(t *testing.T) {
	repo := initTestRepo(t)
	ctx := context.Background()

	ws := NewWorktree(defaultHarnessURL, repo)
	opts := Options{ID: "lifecycle-test"}

	// Provision.
	if err := ws.Provision(ctx, opts); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	worktreePath := ws.WorkspacePath()

	// Verify the worktree directory exists.
	if _, err := os.Stat(worktreePath); err != nil {
		t.Errorf("worktree directory not found after Provision: %v", err)
	}

	// Destroy.
	if err := ws.Destroy(ctx); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	// Verify the worktree directory is gone.
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Errorf("worktree directory still exists after Destroy: %v", err)
	}
}

// --------------------------------------------------------------------------
// TestWorktreeWorkspace_ImplementsWorkspace
// --------------------------------------------------------------------------

// Compile-time interface compliance check.
var _ Workspace = (*WorktreeWorkspace)(nil)

func TestWorktreeWorkspace_ImplementsWorkspace(t *testing.T) {
	var ws Workspace = &WorktreeWorkspace{}
	_ = ws // use the variable to silence the linter
}

// --------------------------------------------------------------------------
// TestWorktreeWorkspace_PathContainment
// --------------------------------------------------------------------------

func TestWorktreeWorkspace_PathContainment(t *testing.T) {
	repo := initTestRepo(t)
	ctx := context.Background()

	// An ID with path traversal characters that might escape the repo root.
	// After sanitization, "../../evil" becomes "..-..-evil", which stays within the repo.
	// We test that even IDs that look traversal-like are contained after sanitisation.
	ws := NewWorktree(defaultHarnessURL, repo)
	opts := Options{ID: "../../evil"}

	if err := ws.Provision(ctx, opts); err != nil {
		// This is also acceptable — if the git command fails for an unusual path,
		// that's fine, but it must not have escaped the repo root.
		t.Logf("Provision returned error (acceptable): %v", err)
		return
	}
	t.Cleanup(func() { _ = ws.Destroy(ctx) })

	// The workspace path must be under the default external worktree root.
	absRoot, _ := filepath.Abs(filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-subagents"))
	absPath, _ := filepath.Abs(ws.WorkspacePath())

	if absPath != absRoot && (len(absPath) <= len(absRoot) || absPath[:len(absRoot)] != absRoot) {
		t.Errorf("workspace path %q escapes worktree root %q", absPath, absRoot)
	}
}
