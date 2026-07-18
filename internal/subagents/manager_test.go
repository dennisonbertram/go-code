package subagents

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
)

type staticProvider struct {
	result harness.CompletionResult
}

func (s *staticProvider) Complete(_ context.Context, _ harness.CompletionRequest) (harness.CompletionResult, error) {
	return s.result, nil
}

func newTestRunner(workspaceRoot string, content string) *harness.Runner {
	return harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: content}},
		harness.NewDefaultRegistryWithOptions(workspaceRoot, harness.DefaultRegistryOptions{}),
		harness.RunnerConfig{
			DefaultModel:        "gpt-4.1-mini",
			DefaultSystemPrompt: "You are helpful.",
			MaxSteps:            1,
		},
	)
}

func waitForTerminal(t *testing.T, mgr Manager, id string) Subagent {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		item, err := mgr.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("Get(%q): %v", id, err)
		}
		if item.Status == harness.RunStatusCompleted || item.Status == harness.RunStatusFailed {
			return item
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("subagent %q did not reach terminal state", id)
	return Subagent{}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("test"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "init")
	return dir
}

func TestManagerCreateInlineSubagent(t *testing.T) {
	t.Parallel()

	inlineRunner := newTestRunner(t.TempDir(), "inline done")
	mgr, err := NewManager(Options{InlineRunner: inlineRunner})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	item, err := mgr.Create(context.Background(), Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if item.Isolation != IsolationInline {
		t.Fatalf("Isolation = %q, want %q", item.Isolation, IsolationInline)
	}
	if item.WorkspacePath != "" {
		t.Fatalf("WorkspacePath = %q, want empty", item.WorkspacePath)
	}

	terminal := waitForTerminal(t, mgr, item.ID)
	if terminal.Output != "inline done" {
		t.Fatalf("Output = %q, want %q", terminal.Output, "inline done")
	}

	items, err := mgr.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("List len = %d, want 1", len(items))
	}

	if err := mgr.Delete(context.Background(), item.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := mgr.Get(context.Background(), item.ID); err != ErrNotFound {
		t.Fatalf("Get after Delete error = %v, want ErrNotFound", err)
	}
}

func TestManagerCreateWorktreeSubagentPreserve(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	worktreeRoot := filepath.Join(t.TempDir(), "subagents")
	inlineRunner := newTestRunner(t.TempDir(), "inline")
	configTOML := "model = \"gpt-4.1-mini\"\n"

	mgr, err := NewManager(Options{
		InlineRunner: inlineRunner,
		WorktreeRunnerFactory: func(workspaceRoot string) (RunEngine, error) {
			return newTestRunner(workspaceRoot, "worktree done"), nil
		},
		RepoPath:            repo,
		DefaultWorktreeRoot: worktreeRoot,
		DefaultBaseRef:      "HEAD",
		ConfigTOML:          configTOML,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	item, err := mgr.Create(context.Background(), Request{
		Prompt:        "fix code",
		Isolation:     IsolationWorktree,
		CleanupPolicy: CleanupPreserve,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if item.WorkspacePath == "" {
		t.Fatal("expected workspace path")
	}
	if item.BranchName == "" {
		t.Fatal("expected branch name")
	}
	if item.BaseRef != "HEAD" {
		t.Fatalf("BaseRef = %q, want HEAD", item.BaseRef)
	}
	if got := filepath.Dir(item.WorkspacePath); got != worktreeRoot {
		t.Fatalf("workspace parent = %q, want %q", got, worktreeRoot)
	}
	cfgPath := filepath.Join(item.WorkspacePath, "harness.toml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", cfgPath, err)
	}
	if string(data) != configTOML {
		t.Fatalf("harness.toml = %q, want %q", string(data), configTOML)
	}

	terminal := waitForTerminal(t, mgr, item.ID)
	if terminal.WorkspaceCleaned {
		t.Fatal("workspace should be preserved")
	}
	if _, err := os.Stat(item.WorkspacePath); err != nil {
		t.Fatalf("workspace missing after preserved run: %v", err)
	}

	if err := mgr.Delete(context.Background(), item.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(item.WorkspacePath); !os.IsNotExist(err) {
		t.Fatalf("workspace still exists after Delete: %v", err)
	}
}

func TestManagerCreateWorktreeSubagentDestroyOnSuccess(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	worktreeRoot := filepath.Join(t.TempDir(), "subagents")
	inlineRunner := newTestRunner(t.TempDir(), "inline")

	mgr, err := NewManager(Options{
		InlineRunner: inlineRunner,
		WorktreeRunnerFactory: func(workspaceRoot string) (RunEngine, error) {
			return newTestRunner(workspaceRoot, "worktree done"), nil
		},
		RepoPath:            repo,
		DefaultWorktreeRoot: worktreeRoot,
		DefaultBaseRef:      "HEAD",
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	item, err := mgr.Create(context.Background(), Request{
		Prompt:        "fix code",
		Isolation:     IsolationWorktree,
		CleanupPolicy: CleanupDestroyOnSuccess,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	terminal := waitForTerminal(t, mgr, item.ID)
	if !terminal.WorkspaceCleaned {
		t.Fatal("expected workspace cleanup on success")
	}
	if _, err := os.Stat(item.WorkspacePath); !os.IsNotExist(err) {
		t.Fatalf("workspace still exists after auto-cleanup: %v", err)
	}
}

func TestManagerCancelActiveSubagent(t *testing.T) {
	t.Parallel()

	engine := &statefulRunEngine{
		status: harness.RunStatusRunning,
	}
	mgr, err := NewManager(Options{InlineRunner: engine})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	item, err := mgr.Create(context.Background(), Request{Prompt: "cancel me"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := mgr.Cancel(context.Background(), item.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if engine.cancelCallCount != 1 {
		t.Fatalf("CancelRun call count = %d, want 1", engine.cancelCallCount)
	}
	if engine.cancelledRunID != item.RunID {
		t.Fatalf("CancelRun runID = %q, want %q", engine.cancelledRunID, item.RunID)
	}

	updated, err := mgr.Get(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("Get after Cancel: %v", err)
	}
	if updated.Status != harness.RunStatusCancelled {
		t.Fatalf("Status after Cancel = %q, want %q", updated.Status, harness.RunStatusCancelled)
	}
}

// TestManagerGetResolvesByRunIDToo covers a real, repeatedly-observed model
// confusion: start_subagent's response carries both `subagent_id` and
// `run_id` (two similar-looking UUIDs), and models frequently pass the run_id
// to get_subagent/wait_subagent/cancel_subagent/message_subagent instead of
// the subagent_id. Before this fix, that returned ErrNotFound outright — live
// testing showed a parent repeatedly re-spawning a new subagent instead of
// realizing it just used the wrong id, looping until it ran out of steps.
func TestManagerGetResolvesByRunIDToo(t *testing.T) {
	t.Parallel()

	inlineRunner := newTestRunner(t.TempDir(), "inline done")
	mgr, err := NewManager(Options{InlineRunner: inlineRunner})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	item, err := mgr.Create(context.Background(), Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if item.RunID == "" || item.RunID == item.ID {
		t.Fatalf("expected a distinct, non-empty RunID, got %q (ID=%q)", item.RunID, item.ID)
	}

	byRunID, err := mgr.Get(context.Background(), item.RunID)
	if err != nil {
		t.Fatalf("Get(RunID): %v", err)
	}
	if byRunID.ID != item.ID {
		t.Fatalf("Get(RunID).ID = %q, want %q", byRunID.ID, item.ID)
	}
}

func TestManagerCancelResolvesByRunIDToo(t *testing.T) {
	t.Parallel()

	engine := &statefulRunEngine{status: harness.RunStatusRunning}
	mgr, err := NewManager(Options{InlineRunner: engine})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	item, err := mgr.Create(context.Background(), Request{Prompt: "cancel me"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := mgr.Cancel(context.Background(), item.RunID); err != nil {
		t.Fatalf("Cancel(RunID): %v", err)
	}
	if engine.cancelCallCount != 1 {
		t.Fatalf("CancelRun call count = %d, want 1", engine.cancelCallCount)
	}
}

func TestManagerDeleteResolvesByRunIDTooAndFullyRemovesTheEntry(t *testing.T) {
	t.Parallel()

	inlineRunner := newTestRunner(t.TempDir(), "inline done")
	mgr, err := NewManager(Options{InlineRunner: inlineRunner})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	item, err := mgr.Create(context.Background(), Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	waitForTerminal(t, mgr, item.ID)

	if err := mgr.Delete(context.Background(), item.RunID); err != nil {
		t.Fatalf("Delete(RunID): %v", err)
	}
	// The entry must be gone under BOTH keys — a delete keyed on the raw
	// input (the run ID) rather than the resolved canonical subagent ID would
	// silently no-op and leave it reachable by ID.
	if _, err := mgr.Get(context.Background(), item.ID); err != ErrNotFound {
		t.Fatalf("Get(ID) after Delete(RunID) error = %v, want ErrNotFound", err)
	}
	if _, err := mgr.Get(context.Background(), item.RunID); err != ErrNotFound {
		t.Fatalf("Get(RunID) after Delete(RunID) error = %v, want ErrNotFound", err)
	}
}
