package harness

// This is the single acceptance test for the GAP-1 security effort: a
// completely DEFAULT-configured run (no explicit Permissions/Sandbox set on
// the RunRequest, exactly as an existing caller that never learned about the
// new Sandbox field would submit) must NOT be able to read an absolute path
// outside the workspace via the `read` tool.
//
// Before the fix, harness.DefaultPermissionConfig() returned
// Sandbox: SandboxScopeUnrestricted, so the workspace confinement added by
// ConfineWorkspacePath/ResolveWorkspacePathConfined (common_paths.go) never
// engaged unless a caller explicitly opted in to SandboxScopeWorkspace. This
// test proves that gap is closed: it reads a file standing in for
// ~/.ssh/id_rsa (an absolute path well outside the run's workspace) via a
// real Runner + real default tool registry + real step engine, with NO
// Permissions field set on the RunRequest at all.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfiguredRun_CannotReadAbsolutePathOutsideWorkspace(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()

	// Stand-in for a sensitive file outside the workspace, e.g. ~/.ssh/id_rsa.
	outsideDir := t.TempDir()
	secretPath := filepath.Join(outsideDir, "id_rsa")
	if err := os.WriteFile(secretPath, []byte("-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----"), 0o600); err != nil {
		t.Fatalf("write fixture secret file: %v", err)
	}

	// The production default tool registry, built with zero-value options —
	// exactly what an operator gets who does not pass a SandboxScope.
	registry := NewDefaultRegistryWithOptions(workspace, DefaultRegistryOptions{})

	provider := &continuationProvider{
		turns: []CompletionResult{
			{
				ToolCalls: []ToolCall{{
					ID:        "call_read_secret",
					Name:      "read",
					Arguments: fmt.Sprintf(`{"path":%q}`, secretPath),
				}},
			},
			{Content: "done"},
		},
	}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     4,
	})

	// The crux of the test: NO Permissions field is set on the request. This
	// is what "default-configured run" means for the acceptance question.
	run, err := runner.StartRun(RunRequest{
		Prompt: "please read my ssh private key at an absolute path",
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	if _, err := collectRunEvents(t, runner, run.ID); err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	payload := toolMessagePayload(t, runner, run.ID, "read")

	if content, ok := payload["content"].(string); ok && strings.Contains(content, "BEGIN PRIVATE KEY") {
		t.Fatalf("SECURITY REGRESSION: default-configured run read the absolute-path secret file outside the workspace: %+v", payload)
	}

	errMsg, _ := payload["error"].(string)
	if errMsg == "" {
		t.Fatalf("expected the default-configured run to be denied reading an absolute path outside the workspace, but got no error. payload=%+v", payload)
	}
	if !strings.Contains(errMsg, "sandbox") && !strings.Contains(errMsg, "escapes") {
		t.Fatalf("expected a sandbox/escape violation error, got: %q", errMsg)
	}
}
