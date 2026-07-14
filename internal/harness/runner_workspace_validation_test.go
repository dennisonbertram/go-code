package harness

// runner_workspace_validation_test.go — issue #561: StartRun must reject
// workspace types that can never provision synchronously, before any run
// state is created, so HTTP callers get a 400 instead of a queued run that
// dies during provisioning.

import (
	"strings"
	"testing"
)

func TestValidateWorkspaceProvisionPreconditions(t *testing.T) {
	t.Parallel()

	repoOpts := WorkspaceProvisionOptions{RepoPath: "/some/repo"}
	cases := []struct {
		name    string
		wsType  string
		opts    WorkspaceProvisionOptions
		wantErr string // empty means no error
	}{
		{name: "local always ok", wsType: "local", opts: WorkspaceProvisionOptions{}},
		{name: "worktree with repo ok", wsType: "worktree", opts: repoOpts},
		{name: "worktree without repo rejected", wsType: "worktree", opts: WorkspaceProvisionOptions{}, wantErr: "RepoPath"},
		{name: "unregistered type rejected", wsType: "does-not-exist", opts: repoOpts, wantErr: "registered"},
		// container and vm are registered; their environment-dependent
		// requirements (Docker, HETZNER_API_KEY) are checked at provisioning
		// time, not here.
		{name: "container passes preconditions", wsType: "container", opts: WorkspaceProvisionOptions{}},
		{name: "vm passes preconditions", wsType: "vm", opts: WorkspaceProvisionOptions{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateWorkspaceProvisionPreconditions(tc.wsType, tc.opts)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateWorkspaceProvisionPreconditions(%q) = %v, want nil", tc.wsType, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateWorkspaceProvisionPreconditions(%q) = nil, want error containing %q", tc.wsType, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestStartRunRejectsWorktreeWithoutRepoPathSynchronously(t *testing.T) {
	t.Parallel()

	runner := NewRunner(
		&staticProviderWS{result: CompletionResult{Content: "done"}},
		NewRegistry(),
		RunnerConfig{DefaultModel: "test-model", MaxSteps: 1},
	)

	_, err := runner.StartRun(RunRequest{
		Prompt:        "hello",
		WorkspaceType: "worktree",
	})
	if err == nil {
		t.Fatal("StartRun with workspace_type=worktree and no RepoPath should fail synchronously")
	}
	for _, want := range []string{"workspace_type=worktree", "RepoPath"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err, want)
		}
	}
	runner.mu.RLock()
	runCount := len(runner.runs)
	runner.mu.RUnlock()
	if runCount != 0 {
		t.Fatalf("no run state should be created on synchronous rejection, got %d runs", runCount)
	}
}
