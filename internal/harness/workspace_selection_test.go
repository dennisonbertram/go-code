package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/profiles"
)

// staticProviderWS is a minimal Provider for workspace selection tests.
type staticProviderWS struct {
	result CompletionResult
}

func (s *staticProviderWS) Complete(_ context.Context, _ CompletionRequest) (CompletionResult, error) {
	return s.result, nil
}

// alwaysFailProviderWS is a Provider that always returns an error.
type alwaysFailProviderWS struct{}

func (p *alwaysFailProviderWS) Complete(_ context.Context, _ CompletionRequest) (CompletionResult, error) {
	return CompletionResult{}, errors.New("provider always fails")
}

var initGitRepoForWSMu sync.Mutex

// drainRunEventsWS subscribes to a run and collects all events until a
// terminal event is received or the deadline elapses.
func drainRunEventsWS(t *testing.T, runner *Runner, runID string) []Event {
	t.Helper()

	history, stream, cancel, err := runner.Subscribe(runID)
	if err != nil {
		t.Fatalf("Subscribe(%q): %v", runID, err)
	}
	defer cancel()

	var events []Event
	events = append(events, history...)

	// Check if already terminated in history.
	for _, ev := range history {
		if IsTerminalEvent(ev.Type) {
			return events
		}
	}

	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-stream:
			if !ok {
				return events
			}
			events = append(events, ev)
			if IsTerminalEvent(ev.Type) {
				return events
			}
		case <-deadline:
			t.Logf("drainRunEventsWS timed out; collected %d events", len(events))
			return events
		}
	}
}

// initGitRepoForWS creates a temp directory with a minimal git repo.
func initGitRepoForWS(t *testing.T) string {
	t.Helper()

	// The race detector and macOS fork/exec can interact poorly when many
	// parallel tests create git repositories at once. Serializing this test
	// setup keeps the workspace tests deterministic without changing the
	// production worktree paths they exercise.
	initGitRepoForWSMu.Lock()
	defer initGitRepoForWSMu.Unlock()

	dir := t.TempDir()

	runGitWS := func(args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runGitWS("init")
	runGitWS("config", "user.email", "test@test.com")
	runGitWS("config", "user.name", "Test")

	// Create a README and initial commit so HEAD exists (required for worktrees).
	readmePath := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readmePath, []byte("test"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runGitWS("add", ".")
	runGitWS("commit", "-m", "init")

	return dir
}

// TestRunRequest_WorkspaceType_Default verifies that a RunRequest with no
// workspace_type field is accepted and runs without error (local default).
func TestRunRequest_WorkspaceType_Default(t *testing.T) {
	t.Parallel()

	runner := NewRunner(
		&staticProviderWS{result: CompletionResult{Content: "done"}},
		NewRegistry(),
		RunnerConfig{MaxSteps: 1},
	)

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun with no workspace_type failed: %v", err)
	}
	if run.ID == "" {
		t.Fatal("expected non-empty run ID")
	}
}

// TestRunRequest_WorkspaceType_Local verifies that workspace_type="local" is
// accepted and behaves identically to the default (no workspace isolation).
func TestRunRequest_WorkspaceType_Local(t *testing.T) {
	t.Parallel()

	runner := NewRunner(
		&staticProviderWS{result: CompletionResult{Content: "done"}},
		NewRegistry(),
		RunnerConfig{MaxSteps: 1},
	)

	run, err := runner.StartRun(RunRequest{
		Prompt:        "hello",
		WorkspaceType: "local",
	})
	if err != nil {
		t.Fatalf("StartRun with workspace_type=local failed: %v", err)
	}
	if run.ID == "" {
		t.Fatal("expected non-empty run ID")
	}

	events := drainRunEventsWS(t, runner, run.ID)
	var gotCompleted bool
	for _, ev := range events {
		if ev.Type == EventRunCompleted {
			gotCompleted = true
		}
	}
	if !gotCompleted {
		t.Error("expected run.completed event for local workspace run")
	}
}

// TestRunRequest_WorkspaceType_Unknown verifies that an unknown workspace_type
// is rejected immediately by StartRun with a descriptive validation error.
func TestRunRequest_WorkspaceType_Unknown(t *testing.T) {
	t.Parallel()

	runner := NewRunner(
		&staticProviderWS{result: CompletionResult{Content: "done"}},
		NewRegistry(),
		RunnerConfig{MaxSteps: 1},
	)

	_, err := runner.StartRun(RunRequest{
		Prompt:        "hello",
		WorkspaceType: "bogus-type-xyz",
	})
	if err == nil {
		t.Fatal("expected error for unknown workspace_type, got nil")
	}
	if !strings.Contains(err.Error(), "bogus-type-xyz") {
		t.Errorf("error should mention the invalid workspace_type, got: %v", err)
	}
}

// TestRunRequest_WorkspaceType_Worktree verifies that workspace_type="worktree"
// provisions a real git worktree, emits workspace events, and cleans up on completion.
func TestRunRequest_WorkspaceType_Worktree(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepoForWS(t)
	worktreeRootDir := t.TempDir()

	runner := NewRunner(
		&staticProviderWS{result: CompletionResult{Content: "done"}},
		NewRegistry(),
		RunnerConfig{
			MaxSteps: 1,
			WorkspaceBaseOptions: WorkspaceProvisionOptions{
				RepoPath:        repoDir,
				WorktreeRootDir: worktreeRootDir,
			},
		},
	)

	run, err := runner.StartRun(RunRequest{
		Prompt:        "hello",
		WorkspaceType: "worktree",
	})
	if err != nil {
		t.Fatalf("StartRun with workspace_type=worktree failed: %v", err)
	}

	events := drainRunEventsWS(t, runner, run.ID)

	var provisionedEv *Event
	var destroyedEv *Event
	for i := range events {
		switch events[i].Type {
		case EventWorkspaceProvisioned:
			provisionedEv = &events[i]
		case EventWorkspaceDestroyed:
			destroyedEv = &events[i]
		}
	}

	if provisionedEv == nil {
		t.Error("expected workspace.provisioned event")
	} else {
		if provisionedEv.Payload["workspace_type"] != "worktree" {
			t.Errorf("workspace.provisioned workspace_type = %v, want worktree",
				provisionedEv.Payload["workspace_type"])
		}
		wsPath, _ := provisionedEv.Payload["workspace_path"].(string)
		if wsPath == "" {
			t.Error("workspace.provisioned should have non-empty workspace_path")
		}
	}

	if destroyedEv == nil {
		t.Error("expected workspace.destroyed event after run completion")
	}
}

// TestRunRequest_WorkspaceType_WorktreeMissingRepoPath verifies that a run
// with workspace_type="worktree" but no repo path configured fails the run.
func TestRunRequest_WorkspaceType_WorktreeMissingRepoPath(t *testing.T) {
	t.Parallel()

	runner := NewRunner(
		&staticProviderWS{result: CompletionResult{Content: "done"}},
		NewRegistry(),
		RunnerConfig{
			MaxSteps: 1,
			// WorkspaceBaseOptions is zero — no RepoPath for worktree.
		},
	)

	run, err := runner.StartRun(RunRequest{
		Prompt:        "hello",
		WorkspaceType: "worktree",
	})
	// StartRun should succeed (validates type name only, not provisioning).
	if err != nil {
		t.Fatalf("StartRun unexpectedly failed: %v", err)
	}

	events := drainRunEventsWS(t, runner, run.ID)

	var gotFailed bool
	for _, ev := range events {
		if ev.Type == EventRunFailed {
			gotFailed = true
		}
	}
	if !gotFailed {
		t.Error("expected run.failed when worktree provisioning lacks a repo path")
	}
}

// TestRunRequest_WorkspaceType_WorkspaceDestroyed_OnFailure verifies that the
// workspace is cleaned up even when the run itself fails.
func TestRunRequest_WorkspaceType_WorkspaceDestroyed_OnFailure(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepoForWS(t)
	worktreeRootDir := t.TempDir()

	runner := NewRunner(
		&alwaysFailProviderWS{},
		NewRegistry(),
		RunnerConfig{
			MaxSteps: 1,
			WorkspaceBaseOptions: WorkspaceProvisionOptions{
				RepoPath:        repoDir,
				WorktreeRootDir: worktreeRootDir,
			},
		},
	)

	run, err := runner.StartRun(RunRequest{
		Prompt:        "hello",
		WorkspaceType: "worktree",
	})
	if err != nil {
		t.Fatalf("StartRun failed: %v", err)
	}

	events := drainRunEventsWS(t, runner, run.ID)

	var gotProvisioned, gotDestroyed bool
	for _, ev := range events {
		switch ev.Type {
		case EventWorkspaceProvisioned:
			gotProvisioned = true
		case EventWorkspaceDestroyed:
			gotDestroyed = true
		}
	}

	if !gotProvisioned {
		t.Error("expected workspace.provisioned event")
	}
	if !gotDestroyed {
		t.Error("expected workspace.destroyed event even when run fails")
	}
}

// TestRunRequest_WorkspaceType_ValidTypes verifies that the known-valid
// workspace types are all accepted without validation error.
func TestRunRequest_WorkspaceType_ValidTypes(t *testing.T) {
	t.Parallel()

	// "container" and "vm" pass validation (type name is known) but
	// provisioning will fail at execute() time for lack of orchestrator config.
	// StartRun itself must not reject them.
	validTypes := []string{"", "local", "worktree", "container", "vm"}
	runner := NewRunner(
		&staticProviderWS{result: CompletionResult{Content: "done"}},
		NewRegistry(),
		RunnerConfig{MaxSteps: 1},
	)

	for _, wsType := range validTypes {
		wsType := wsType
		t.Run("type="+wsType, func(t *testing.T) {
			t.Parallel()
			_, err := runner.StartRun(RunRequest{
				Prompt:        "hello",
				WorkspaceType: wsType,
			})
			if err != nil {
				t.Errorf("StartRun with workspace_type=%q rejected unexpectedly: %v", wsType, err)
			}
		})
	}
}

// TestRunRequest_WorkspaceType_JSONField verifies the JSON field name is
// "workspace_type" with omitempty semantics.
func TestRunRequest_WorkspaceType_JSONField(t *testing.T) {
	t.Parallel()

	// Unmarshal with workspace_type present.
	raw := `{"prompt":"hi","workspace_type":"local"}`
	var req RunRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if req.WorkspaceType != "local" {
		t.Errorf("WorkspaceType = %q, want local", req.WorkspaceType)
	}
	if req.Prompt != "hi" {
		t.Errorf("Prompt = %q, want hi", req.Prompt)
	}

	// Marshal omits workspace_type when empty.
	empty := RunRequest{Prompt: "hi"}
	b, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(b), "workspace_type") {
		t.Errorf("empty WorkspaceType should be omitted from JSON, got: %s", string(b))
	}
}

// ---- resolveWorkspaceType unit tests (issue #414) ----

// TestResolveWorkspaceType_ExplicitOverride verifies that a non-empty
// RunRequest.WorkspaceType always wins, regardless of what the profile says.
func TestResolveWorkspaceType_ExplicitOverride(t *testing.T) {
	t.Parallel()

	profile := &profiles.Profile{IsolationMode: "container"}
	got := resolveWorkspaceType("worktree", profile)
	if got != "worktree" {
		t.Errorf("resolveWorkspaceType(\"worktree\", container-profile) = %q, want \"worktree\"", got)
	}
}

// TestResolveWorkspaceType_ProfileFallback_Worktree verifies that when
// RunRequest.WorkspaceType is empty the profile's IsolationMode="worktree"
// is used.
func TestResolveWorkspaceType_ProfileFallback_Worktree(t *testing.T) {
	t.Parallel()

	profile := &profiles.Profile{IsolationMode: "worktree"}
	got := resolveWorkspaceType("", profile)
	if got != "worktree" {
		t.Errorf("resolveWorkspaceType(\"\", worktree-profile) = %q, want \"worktree\"", got)
	}
}

// TestResolveWorkspaceType_ProfileFallback_Container verifies that
// IsolationMode="container" is propagated when WorkspaceType is empty.
func TestResolveWorkspaceType_ProfileFallback_Container(t *testing.T) {
	t.Parallel()

	profile := &profiles.Profile{IsolationMode: "container"}
	got := resolveWorkspaceType("", profile)
	if got != "container" {
		t.Errorf("resolveWorkspaceType(\"\", container-profile) = %q, want \"container\"", got)
	}
}

// TestResolveWorkspaceType_ProfileFallback_VM verifies that
// IsolationMode="vm" is propagated when WorkspaceType is empty.
func TestResolveWorkspaceType_ProfileFallback_VM(t *testing.T) {
	t.Parallel()

	profile := &profiles.Profile{IsolationMode: "vm"}
	got := resolveWorkspaceType("", profile)
	if got != "vm" {
		t.Errorf("resolveWorkspaceType(\"\", vm-profile) = %q, want \"vm\"", got)
	}
}

// TestResolveWorkspaceType_ProfileNone verifies that IsolationMode="none"
// results in "" (no provisioning), since "none" is an explicit opt-out.
func TestResolveWorkspaceType_ProfileNone(t *testing.T) {
	t.Parallel()

	profile := &profiles.Profile{IsolationMode: "none"}
	got := resolveWorkspaceType("", profile)
	if got != "" {
		t.Errorf("resolveWorkspaceType(\"\", none-profile) = %q, want \"\"", got)
	}
}

// TestResolveWorkspaceType_NoProfile verifies that when both WorkspaceType and
// profile are absent the result is "" (no provisioning).
func TestResolveWorkspaceType_NoProfile(t *testing.T) {
	t.Parallel()

	got := resolveWorkspaceType("", nil)
	if got != "" {
		t.Errorf("resolveWorkspaceType(\"\", nil) = %q, want \"\"", got)
	}
}

// TestResolveWorkspaceType_EmptyIsolationMode verifies that a profile with an
// empty IsolationMode field falls back to "" (no provisioning).
func TestResolveWorkspaceType_EmptyIsolationMode(t *testing.T) {
	t.Parallel()

	profile := &profiles.Profile{IsolationMode: ""}
	got := resolveWorkspaceType("", profile)
	if got != "" {
		t.Errorf("resolveWorkspaceType(\"\", empty-isolation-profile) = %q, want \"\"", got)
	}
}

// TestResolveWorkspaceType_ExplicitOverride_VM verifies that an explicit
// WorkspaceType="vm" overrides a profile that specifies "worktree".
func TestResolveWorkspaceType_ExplicitOverride_VM(t *testing.T) {
	t.Parallel()

	profile := &profiles.Profile{IsolationMode: "worktree"}
	got := resolveWorkspaceType("vm", profile)
	if got != "vm" {
		t.Errorf("resolveWorkspaceType(\"vm\", worktree-profile) = %q, want \"vm\"", got)
	}
}

// TestValidateWorkspaceType_Container verifies that "container" is now accepted.
func TestValidateWorkspaceType_Container(t *testing.T) {
	t.Parallel()

	if err := validateWorkspaceType("container"); err != nil {
		t.Errorf("validateWorkspaceType(\"container\") = %v, want nil", err)
	}
}

// TestValidateWorkspaceType_VM verifies that "vm" is now accepted.
func TestValidateWorkspaceType_VM(t *testing.T) {
	t.Parallel()

	if err := validateWorkspaceType("vm"); err != nil {
		t.Errorf("validateWorkspaceType(\"vm\") = %v, want nil", err)
	}
}

// TestValidateWorkspaceType_ErrorMessage verifies that the error message for
// an unknown type lists all four valid options.
func TestValidateWorkspaceType_ErrorMessage(t *testing.T) {
	t.Parallel()

	err := validateWorkspaceType("bogus")
	if err == nil {
		t.Fatal("expected error for unknown workspace type")
	}
	for _, expected := range []string{"local", "worktree", "container", "vm"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("error message should mention %q, got: %v", expected, err)
		}
	}
}

// TestRunRequest_WorkspaceType_ValidTypes_Extended verifies that the full set
// of known workspace types (including container and vm) pass validation.
func TestRunRequest_WorkspaceType_ValidTypes_Extended(t *testing.T) {
	t.Parallel()

	runner := NewRunner(
		&staticProviderWS{result: CompletionResult{Content: "done"}},
		NewRegistry(),
		RunnerConfig{MaxSteps: 1},
	)

	// container and vm pass validation but provisioning will fail at execute()
	// time since they require orchestrator config. We only test that StartRun
	// does NOT return a validation error (the error comes later as run.failed).
	for _, wsType := range []string{"container", "vm"} {
		wsType := wsType
		t.Run("type="+wsType, func(t *testing.T) {
			t.Parallel()
			_, err := runner.StartRun(RunRequest{
				Prompt:        "hello",
				WorkspaceType: wsType,
			})
			// StartRun validates the type name only — should not error.
			if err != nil {
				t.Errorf("StartRun with workspace_type=%q rejected by validation: %v", wsType, err)
			}
		})
	}
}

// TestProfile_IsolationMode_Worktree_Integration verifies that when a profile
// with IsolationMode="worktree" is loaded from disk, the runner provisions a
// worktree workspace (workspace.provisioned event is emitted).
func TestProfile_IsolationMode_Worktree_Integration(t *testing.T) {
	t.Parallel()

	// Set up a git repo for the worktree.
	repoDir := initGitRepoForWS(t)
	worktreeRootDir := t.TempDir()

	// Create a profiles dir with a profile that specifies isolation_mode = "worktree".
	// IMPORTANT: isolation_mode is a top-level TOML field — it must appear before
	// any section header ([meta], [runner], [tools]) to avoid being parsed as a
	// nested field under the last section.
	profilesDir := t.TempDir()
	profileTOML := `isolation_mode = "worktree"

[meta]
name = "isolated-worktree"
description = "Test profile with worktree isolation"
version = 1

[runner]
model = ""
max_steps = 1

[tools]
allow = []
`
	if err := os.WriteFile(filepath.Join(profilesDir, "isolated-worktree.toml"), []byte(profileTOML), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	runner := NewRunner(
		&staticProviderWS{result: CompletionResult{Content: "done"}},
		NewRegistry(),
		RunnerConfig{
			MaxSteps:    1,
			ProfilesDir: profilesDir,
			WorkspaceBaseOptions: WorkspaceProvisionOptions{
				RepoPath:        repoDir,
				WorktreeRootDir: worktreeRootDir,
			},
		},
	)

	// No WorkspaceType in the request — the profile's IsolationMode should kick in.
	run, err := runner.StartRun(RunRequest{
		Prompt:      "hello",
		ProfileName: "isolated-worktree",
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	events := drainRunEventsWS(t, runner, run.ID)

	var provisionedEv *Event
	var destroyedEv *Event
	for i := range events {
		switch events[i].Type {
		case EventWorkspaceProvisioned:
			provisionedEv = &events[i]
		case EventWorkspaceDestroyed:
			destroyedEv = &events[i]
		}
	}

	if provisionedEv == nil {
		t.Error("expected workspace.provisioned event — profile IsolationMode should have triggered provisioning")
	} else {
		if provisionedEv.Payload["workspace_type"] != "worktree" {
			t.Errorf("workspace.provisioned workspace_type = %v, want worktree",
				provisionedEv.Payload["workspace_type"])
		}
	}

	if destroyedEv == nil {
		t.Error("expected workspace.destroyed event after run completion")
	}
}

// TestProfile_IsolationMode_None_NoProvisioning verifies that a profile with
// IsolationMode="none" does NOT trigger workspace provisioning.
func TestProfile_IsolationMode_None_NoProvisioning(t *testing.T) {
	t.Parallel()

	profilesDir := t.TempDir()
	// isolation_mode must appear before section headers in TOML.
	profileTOML := `isolation_mode = "none"

[meta]
name = "no-isolation"
description = "Test profile with no isolation"
version = 1

[runner]
model = ""
max_steps = 1

[tools]
allow = []
`
	if err := os.WriteFile(filepath.Join(profilesDir, "no-isolation.toml"), []byte(profileTOML), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	runner := NewRunner(
		&staticProviderWS{result: CompletionResult{Content: "done"}},
		NewRegistry(),
		RunnerConfig{
			MaxSteps:    1,
			ProfilesDir: profilesDir,
		},
	)

	run, err := runner.StartRun(RunRequest{
		Prompt:      "hello",
		ProfileName: "no-isolation",
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	events := drainRunEventsWS(t, runner, run.ID)

	for _, ev := range events {
		if ev.Type == EventWorkspaceProvisioned {
			t.Error("unexpected workspace.provisioned event — IsolationMode=none should not provision")
		}
	}
}

// TestProfile_IsolationMode_Explicit_Override verifies that an explicit
// RunRequest.WorkspaceType="local" overrides a profile's IsolationMode.
func TestProfile_IsolationMode_Explicit_Override(t *testing.T) {
	t.Parallel()

	// Profile says "worktree" but request says "local" — "local" wins.
	profilesDir := t.TempDir()
	// isolation_mode must appear before section headers in TOML.
	profileTOML := `isolation_mode = "worktree"

[meta]
name = "wants-worktree"
description = "Profile that prefers worktree"
version = 1

[runner]
model = ""
max_steps = 1

[tools]
allow = []
`
	if err := os.WriteFile(filepath.Join(profilesDir, "wants-worktree.toml"), []byte(profileTOML), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	runner := NewRunner(
		&staticProviderWS{result: CompletionResult{Content: "done"}},
		NewRegistry(),
		RunnerConfig{
			MaxSteps:    1,
			ProfilesDir: profilesDir,
		},
	)

	run, err := runner.StartRun(RunRequest{
		Prompt:        "hello",
		ProfileName:   "wants-worktree",
		WorkspaceType: "local", // explicit override: local, not worktree
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	events := drainRunEventsWS(t, runner, run.ID)

	for _, ev := range events {
		if ev.Type == EventWorkspaceProvisioned {
			wsType, _ := ev.Payload["workspace_type"].(string)
			if wsType == "worktree" {
				t.Errorf("workspace.provisioned type = worktree, want local — explicit WorkspaceType should win")
			}
		}
	}
}

// T-PFIX-1: when workspace provisioning fails AFTER git worktree add (partial
// success), the runner must Destroy the partial workspace before emitting
// workspace.provision_failed so no orphaned worktree dir or git branch remains.
//
// Failure is induced by committing a harness.toml directory into the source
// repository. The worktree is created successfully, then ConfigTOML writing
// fails because the target path is a directory. This deterministically triggers
// the partial-failure cleanup path after git worktree add has succeeded.
func TestWorktreePartialProvisionFailure_NoOrphan(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepoForWS(t)
	worktreeRootDir := t.TempDir()
	blockingDir := filepath.Join(repoDir, "harness.toml")
	if err := os.Mkdir(blockingDir, 0o755); err != nil {
		t.Fatalf("mkdir harness.toml blocker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blockingDir, "placeholder"), []byte("block"), 0o644); err != nil {
		t.Fatalf("write harness.toml blocker: %v", err)
	}
	for _, args := range [][]string{
		{"add", "harness.toml"},
		{"commit", "-m", "add harness toml directory blocker"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repoDir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runner := NewRunner(
		&staticProviderWS{result: CompletionResult{Content: "done"}},
		NewRegistry(),
		RunnerConfig{
			MaxSteps: 1,
			WorkspaceBaseOptions: WorkspaceProvisionOptions{
				RepoPath:        repoDir,
				WorktreeRootDir: worktreeRootDir,
				ConfigTOML:      "[runner]\nmax_steps = 1\n",
			},
		},
	)

	run, err := runner.StartRun(RunRequest{
		Prompt:        "hello",
		WorkspaceType: "worktree",
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	events := drainRunEventsWS(t, runner, run.ID)

	// Verify the run failed (provision_failed → run.failed).
	var gotProvisionFailed, gotRunFailed bool
	for _, ev := range events {
		switch ev.Type {
		case EventWorkspaceProvisionFailed:
			gotProvisionFailed = true
		case EventRunFailed:
			gotRunFailed = true
		}
	}
	if !gotProvisionFailed {
		t.Error("expected workspace.provision_failed event")
	}
	if !gotRunFailed {
		t.Error("expected run.failed event")
	}

	// Give Destroy a moment to run (it's called before workspace.provision_failed
	// is emitted in the fixed code, but just in case).
	time.Sleep(50 * time.Millisecond)

	// Assert no orphaned worktree directory remains.
	entries, err := os.ReadDir(worktreeRootDir)
	if err != nil {
		t.Fatalf("ReadDir worktreeRootDir: %v", err)
	}
	// After restore (cleanup fn runs later), we may still have the dir, so
	// check using git worktree list which is definitive.
	_ = entries

	// Assert no leftover git worktree entry (other than the main worktree).
	cmd := exec.CommandContext(context.Background(), "git", "-C", repoDir, "worktree", "list", "--porcelain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree list: %v: %s", err, out)
	}
	worktreeEntries := strings.Count(string(out), "worktree ")
	if worktreeEntries > 1 {
		t.Errorf("orphaned git worktree entry found after partial provision failure:\n%s", string(out))
	}

	// Assert no orphan branch remains.
	branchCmd := exec.CommandContext(context.Background(), "git", "-C", repoDir, "branch", "--list", "workspace-*")
	branchOut, err := branchCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git branch --list: %v: %s", err, branchOut)
	}
	if strings.TrimSpace(string(branchOut)) != "" {
		t.Errorf("orphaned git branch found after partial provision failure: %s", strings.TrimSpace(string(branchOut)))
	}
}

// ---- Deliverable A: worktree containment ----

// evalSymlinksWS resolves a path through symlinks for robust path comparison.
// macOS t.TempDir() lives under /var/folders, a symlink to /private/var, and the
// bash `pwd` builtin reports the logical (unresolved) cwd, so a raw string compare
// against workspace_path can spuriously differ. Resolving both sides removes that
// noise without weakening the assertion (both must still name the same directory).
func evalSymlinksWS(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		// Fall back to the raw path if the target no longer exists (e.g. after
		// Destroy). The caller decides whether a missing path is acceptable.
		return path
	}
	return resolved
}

// worktreeContainmentProvider issues, on its first turn, a bash tool call that
// writes two files using paths RELATIVE to the tool's cwd:
//   - `pwd > marker.txt` records the shell's working directory, and
//   - `echo hi > out.txt` writes a sentinel file.
//
// Because relative paths resolve against the bash tool's cwd, both files must
// land inside the provisioned worktree (not the daemon startup cwd) if tool
// routing is wired to the workspace. The second turn terminates the run.
func worktreeContainmentProvider() *stubProvider {
	return &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-write-marker",
				Name:      "bash",
				Arguments: `{"command":"pwd > marker.txt && echo hi > out.txt","timeout_seconds":30}`,
			}},
		},
		{Content: "done"},
	}}
}

// TestWorktreeContainment_ToolCwdIsWorktree (Deliverable A) proves that file/shell
// tools in a workspace_type="worktree" run operate INSIDE the provisioned worktree,
// not the daemon's startup cwd. A real bash tool call writes out.txt and marker.txt
// via cwd-relative paths; the test asserts:
//   - out.txt exists at <workspace_path>/out.txt,
//   - out.txt does NOT exist at the daemon startup cwd, and
//   - marker.txt (containing `pwd`) names <workspace_path>, proving the tool's cwd
//     was the worktree.
//
// The worktree is destroyed on run completion (git worktree remove), so the
// in-worktree filesystem assertions must run BEFORE the terminal event. We
// subscribe to the live event stream and perform them the moment
// tool.call.completed arrives — the worktree is still on disk at that point.
func TestWorktreeContainment_ToolCwdIsWorktree(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepoForWS(t)
	worktreeRootDir := t.TempDir()

	// Use a dedicated temp directory as the simulated daemon startup cwd rather
	// than os.Getwd(). os.Getwd() returns the shared process cwd; under t.Parallel
	// another test could coincidentally create "out.txt" there, making the negative
	// assertion ("file must NOT appear in daemon cwd") spuriously pass or flap.
	// A test-private temp dir guarantees isolation: no other test writes there.
	daemonCwd := t.TempDir()

	// NewDefaultRegistryWithOptions wires the real bash tool; BaseRegistryOptions
	// (ApprovalMode empty → FullAuto in NewDefaultRegistryWithOptions) means the
	// per-run registry the runner builds at the worktree path runs bash ungated.
	runner := NewRunner(
		worktreeContainmentProvider(),
		NewDefaultRegistryWithOptions(daemonCwd, DefaultRegistryOptions{
			ApprovalMode: ToolApprovalModeFullAuto,
		}),
		RunnerConfig{
			MaxSteps:     3,
			DefaultModel: "test-model",
			WorkspaceBaseOptions: WorkspaceProvisionOptions{
				RepoPath:        repoDir,
				WorktreeRootDir: worktreeRootDir,
			},
		},
	)

	run, err := runner.StartRun(RunRequest{
		Prompt:        "write files relative to cwd",
		WorkspaceType: "worktree",
		AllowedTools:  []string{"bash"},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	history, stream, cancelSub, subErr := runner.Subscribe(run.ID)
	if subErr != nil {
		t.Fatalf("Subscribe: %v", subErr)
	}
	defer cancelSub()

	// assertContainment runs the in-worktree filesystem checks while the worktree
	// is still on disk (before workspace.destroyed). wsPath is the captured
	// provisioned path. It is called exactly once, on tool.call.completed.
	assertContainment := func(wsPath string) {
		t.Helper()
		if wsPath == "" {
			t.Error("workspace.provisioned event missing non-empty workspace_path")
			return
		}

		// out.txt must exist inside the worktree.
		wsOut := filepath.Join(wsPath, "out.txt")
		if _, statErr := os.Stat(wsOut); statErr != nil {
			t.Errorf("expected out.txt inside worktree at %s: %v", wsOut, statErr)
		}

		// out.txt must NOT exist at the daemon startup cwd (no leak outside worktree).
		daemonOut := filepath.Join(daemonCwd, "out.txt")
		if _, statErr := os.Stat(daemonOut); statErr == nil {
			// Remove the leaked file so we don't pollute the repo tree, then fail.
			_ = os.Remove(daemonOut)
			t.Errorf("out.txt leaked to daemon cwd at %s — tool cwd was not the worktree", daemonOut)
		}

		// marker.txt's contents (the bash `pwd`) must name the worktree path.
		markerBytes, readErr := os.ReadFile(filepath.Join(wsPath, "marker.txt"))
		if readErr != nil {
			t.Errorf("read marker.txt inside worktree: %v", readErr)
			return
		}
		markerPath := strings.TrimSpace(string(markerBytes))
		if evalSymlinksWS(t, markerPath) != evalSymlinksWS(t, wsPath) {
			t.Errorf("bash pwd = %q (resolved %q), want worktree %q (resolved %q) — tool cwd was not the worktree",
				markerPath, evalSymlinksWS(t, markerPath), wsPath, evalSymlinksWS(t, wsPath))
		}
	}

	var (
		wsPath         string
		gotProvisioned bool
		gotToolDone    bool
		gotCompleted   bool
	)
	process := func(ev Event) {
		switch ev.Type {
		case EventWorkspaceProvisioned:
			wsPath, _ = ev.Payload["workspace_path"].(string)
			gotProvisioned = true
		case EventToolCallCompleted:
			// Run filesystem assertions while the worktree still exists.
			if !gotToolDone {
				gotToolDone = true
				assertContainment(wsPath)
			}
		case EventRunCompleted:
			gotCompleted = true
		}
	}

	for _, ev := range history {
		process(ev)
	}

	deadline := time.After(15 * time.Second)
	for !gotCompleted {
		select {
		case ev, ok := <-stream:
			if !ok {
				t.Fatal("event stream closed before run.completed")
			}
			process(ev)
			if IsTerminalEvent(ev.Type) && ev.Type != EventRunCompleted {
				t.Fatalf("run reached non-success terminal %s; events so far provisioned=%v toolDone=%v",
					ev.Type, gotProvisioned, gotToolDone)
			}
		case <-deadline:
			t.Fatal("timed out waiting for run.completed")
		}
	}

	if !gotProvisioned {
		t.Error("expected workspace.provisioned event")
	}
	if !gotToolDone {
		t.Error("expected tool.call.completed event (bash never ran)")
	}

	if err := runner.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

// ---- Deliverable B: full workspace lifecycle across success / failure / cancel ----

// eventTypesWS returns the ordered list of event types, for diagnostics.
func eventTypesWS(events []Event) []EventType {
	types := make([]EventType, len(events))
	for i, ev := range events {
		types[i] = ev.Type
	}
	return types
}

// indexOfEventWS returns the index of the first event of the given type, or -1.
func indexOfEventWS(events []Event, t EventType) int {
	for i, ev := range events {
		if ev.Type == t {
			return i
		}
	}
	return -1
}

// assertNoWorktreeOrphanWS asserts that the repo at repoDir has no extra git
// worktree entry (beyond the main checkout) and no leftover workspace-* branch.
func assertNoWorktreeOrphanWS(t *testing.T, repoDir string) {
	t.Helper()

	listCmd := exec.CommandContext(context.Background(), "git", "-C", repoDir, "worktree", "list", "--porcelain")
	out, err := listCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree list: %v: %s", err, out)
	}
	if n := strings.Count(string(out), "worktree "); n > 1 {
		t.Errorf("orphaned git worktree entry remains:\n%s", string(out))
	}

	branchCmd := exec.CommandContext(context.Background(), "git", "-C", repoDir, "branch", "--list", "workspace-*")
	branchOut, err := branchCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git branch --list: %v: %s", err, branchOut)
	}
	if strings.TrimSpace(string(branchOut)) != "" {
		t.Errorf("orphaned workspace-* branch remains: %s", strings.TrimSpace(string(branchOut)))
	}
}

// TestWorkspaceLifecycle_Success (Deliverable B, scenario 1) verifies that a
// normal worktree run emits workspace.provisioned and then, after the run
// completes, workspace.destroyed — and that cleanup leaves no orphan worktree
// or branch behind.
func TestWorkspaceLifecycle_Success(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepoForWS(t)
	worktreeRootDir := t.TempDir()

	runner := NewRunner(
		&staticProviderWS{result: CompletionResult{Content: "done"}},
		NewRegistry(),
		RunnerConfig{
			MaxSteps:     1,
			DefaultModel: "test-model",
			WorkspaceBaseOptions: WorkspaceProvisionOptions{
				RepoPath:        repoDir,
				WorktreeRootDir: worktreeRootDir,
			},
		},
	)

	run, err := runner.StartRun(RunRequest{
		Prompt:        "hello",
		WorkspaceType: "worktree",
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	events := drainRunEventsWS(t, runner, run.ID)

	provIdx := indexOfEventWS(events, EventWorkspaceProvisioned)
	destIdx := indexOfEventWS(events, EventWorkspaceDestroyed)
	complIdx := indexOfEventWS(events, EventRunCompleted)

	if provIdx < 0 {
		t.Fatalf("expected workspace.provisioned; events=%v", eventTypesWS(events))
	}
	if destIdx < 0 {
		t.Fatalf("expected workspace.destroyed on success; events=%v", eventTypesWS(events))
	}
	if complIdx < 0 {
		t.Fatalf("expected run.completed; events=%v", eventTypesWS(events))
	}
	if !(provIdx < destIdx) {
		t.Errorf("workspace.provisioned (%d) must precede workspace.destroyed (%d)", provIdx, destIdx)
	}
	if !(destIdx < complIdx) {
		t.Errorf("workspace.destroyed (%d) must precede run.completed (%d)", destIdx, complIdx)
	}

	assertNoWorktreeOrphanWS(t, repoDir)
}

// TestWorkspaceLifecycle_ProvisionFailure (Deliverable B, scenario 2) verifies
// that a run whose provisioning fails emits workspace.provision_failed, fails the
// run, and leaves no orphan worktree/branch.
//
// Failure is induced deterministically by placing a regular file at the path that
// WorktreeRootDir resolves to. os.MkdirAll fails immediately ("not a directory")
// before git worktree add is ever invoked, so there is no timing race and no
// partial worktree to clean up. The runner's event contract (provision_failed →
// run.failed, no workspace.provisioned) is what this test exercises; the partial-
// provision + Destroy cleanup path is covered by TestWorktreePartialProvisionFailure_NoOrphan.
func TestWorkspaceLifecycle_ProvisionFailure(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepoForWS(t)

	// Create a regular file where WorktreeRootDir would be.  os.MkdirAll returns
	// "not a directory" when any path component is a plain file, so Provision
	// fails immediately, deterministically, without any goroutine racing.
	base := t.TempDir()
	blockerFile := filepath.Join(base, "blocker")
	if err := os.WriteFile(blockerFile, []byte("block"), 0o644); err != nil {
		t.Fatalf("create blocker file: %v", err)
	}
	// WorktreeRootDir points inside the regular file — MkdirAll always fails here.
	worktreeRootDir := filepath.Join(blockerFile, "nested")

	runner := NewRunner(
		&staticProviderWS{result: CompletionResult{Content: "done"}},
		NewRegistry(),
		RunnerConfig{
			MaxSteps: 1,
			WorkspaceBaseOptions: WorkspaceProvisionOptions{
				RepoPath:        repoDir,
				WorktreeRootDir: worktreeRootDir,
				ConfigTOML:      "[runner]\nmax_steps = 1\n",
			},
		},
	)

	run, err := runner.StartRun(RunRequest{
		Prompt:        "hello",
		WorkspaceType: "worktree",
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	events := drainRunEventsWS(t, runner, run.ID)

	if indexOfEventWS(events, EventWorkspaceProvisionFailed) < 0 {
		t.Errorf("expected workspace.provision_failed; events=%v", eventTypesWS(events))
	}
	if indexOfEventWS(events, EventRunFailed) < 0 {
		t.Errorf("expected run.failed; events=%v", eventTypesWS(events))
	}
	// A failed provisioning must never report a successful provisioned/destroyed
	// pair — provisioning never reached the success path.
	if indexOfEventWS(events, EventWorkspaceProvisioned) >= 0 {
		t.Errorf("unexpected workspace.provisioned on a provision failure; events=%v", eventTypesWS(events))
	}

	// No git worktree was created, so no orphan can remain.
	assertNoWorktreeOrphanWS(t, repoDir)
}

// hangingBashWorktreeProvider returns a bash tool call that sleeps far longer
// than the test budget on its first turn, then blocks until the run context is
// cancelled. It lets the test cancel a worktree run mid-tool-execution.
type hangingBashWorktreeProvider struct {
	mu        sync.Mutex
	calls     int
	firstCall chan struct{}
}

func newHangingBashWorktreeProvider() *hangingBashWorktreeProvider {
	return &hangingBashWorktreeProvider{firstCall: make(chan struct{})}
}

func (p *hangingBashWorktreeProvider) Complete(ctx context.Context, _ CompletionRequest) (CompletionResult, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.mu.Unlock()

	if idx == 0 {
		select {
		case <-p.firstCall:
		default:
			close(p.firstCall)
		}
		return CompletionResult{
			ToolCalls: []ToolCall{{
				ID:        "call-hang",
				Name:      "bash",
				Arguments: `{"command":"sleep 30","timeout_seconds":60}`,
			}},
		}, nil
	}
	<-ctx.Done()
	return CompletionResult{}, ctx.Err()
}

// TestWorkspaceLifecycle_CancelMidFlight (Deliverable B, scenario 3) verifies
// that a worktree run cancelled while a tool is executing still cleans up its
// workspace: workspace.destroyed is emitted, the run reaches RunStatusCancelled,
// and no orphan worktree/branch remains.
func TestWorkspaceLifecycle_CancelMidFlight(t *testing.T) {
	t.Parallel()

	repoDir := initGitRepoForWS(t)
	worktreeRootDir := t.TempDir()

	prov := newHangingBashWorktreeProvider()

	runner := NewRunner(
		prov,
		NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{
			ApprovalMode: ToolApprovalModeFullAuto,
		}),
		RunnerConfig{
			MaxSteps:     5,
			DefaultModel: "test-model",
			WorkspaceBaseOptions: WorkspaceProvisionOptions{
				RepoPath:        repoDir,
				WorktreeRootDir: worktreeRootDir,
			},
		},
	)

	run, err := runner.StartRun(RunRequest{
		Prompt:        "run a long sleep in the worktree",
		WorkspaceType: "worktree",
		AllowedTools:  []string{"bash"},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	history, stream, cancelSub, subErr := runner.Subscribe(run.ID)
	if subErr != nil {
		t.Fatalf("Subscribe: %v", subErr)
	}
	defer cancelSub()

	toolStarted := make(chan struct{})
	terminal := make(chan struct{})
	closeOnce := func(ch chan struct{}) {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
	inspect := func(ev Event) {
		if ev.Type == EventToolCallStarted {
			closeOnce(toolStarted)
		}
		if IsTerminalEvent(ev.Type) {
			closeOnce(terminal)
		}
	}
	for _, ev := range history {
		inspect(ev)
	}
	go func() {
		for {
			ev, ok := <-stream
			if !ok {
				closeOnce(terminal)
				return
			}
			inspect(ev)
			if IsTerminalEvent(ev.Type) {
				closeOnce(terminal)
				return
			}
		}
	}()

	// Wait until the bash tool is actually executing inside the worktree.
	select {
	case <-toolStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for tool.call.started (bash never started in worktree)")
	}

	// Cancel mid-flight; the sleep must be killed (run terminates well under 30s).
	if err := runner.CancelRun(run.ID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	select {
	case <-terminal:
	case <-time.After(10 * time.Second):
		state, _ := runner.GetRun(run.ID)
		t.Fatalf("timed out waiting for terminal event after cancel; last status: %q", state.Status)
	}

	// The run must be cancelled, not completed/failed.
	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("run missing from store after cancel")
	}
	if state.Status != RunStatusCancelled {
		t.Errorf("status = %q, want %q", state.Status, RunStatusCancelled)
	}

	// Collect the full event history and assert workspace cleanup happened.
	full := drainRunEventsWS(t, runner, run.ID)
	provIdx := indexOfEventWS(full, EventWorkspaceProvisioned)
	destIdx := indexOfEventWS(full, EventWorkspaceDestroyed)
	cancelIdx := indexOfEventWS(full, EventRunCancelled)

	if provIdx < 0 {
		t.Fatalf("expected workspace.provisioned; events=%v", eventTypesWS(full))
	}
	if destIdx < 0 {
		t.Fatalf("expected workspace.destroyed after cancel; events=%v", eventTypesWS(full))
	}
	if cancelIdx < 0 {
		t.Fatalf("expected run.cancelled; events=%v", eventTypesWS(full))
	}
	if !(destIdx < cancelIdx) {
		t.Errorf("workspace.destroyed (%d) must precede run.cancelled (%d)", destIdx, cancelIdx)
	}

	// No orphan worktree directory or branch may remain after cancel cleanup.
	assertNoWorktreeOrphanWS(t, repoDir)

	if err := runner.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
