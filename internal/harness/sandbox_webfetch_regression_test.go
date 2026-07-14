package harness

// End-to-end regression coverage for the sandbox-defaults-webfetch security
// pass (GAP-1, GAP-2, GAP-3). Each test here targets an angle NOT already
// covered by the gap-specific behavioral tests, so a future change that
// silently reverts or narrows any of the three fixes gets caught here even
// if it doesn't touch the original acceptance tests directly.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRegression_DefaultConfiguredRun_WriteTool_CannotEscapeWorkspace proves
// GAP-1's fix generalizes beyond the `read` tool: `write` (a different
// code path, core/write.go) is also confined by default. If a future change
// re-narrows the DefaultPermissionConfig fix to only cover reads, this
// catches it.
func TestRegression_DefaultConfiguredRun_WriteTool_CannotEscapeWorkspace(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outsideDir := t.TempDir()
	targetPath := filepath.Join(outsideDir, "authorized_keys")

	registry := NewDefaultRegistryWithOptions(workspace, DefaultRegistryOptions{})
	provider := &continuationProvider{
		turns: []CompletionResult{
			{ToolCalls: []ToolCall{{
				ID:        "call_write_outside",
				Name:      "write",
				Arguments: fmt.Sprintf(`{"path":%q,"content":"attacker-controlled-key"}`, targetPath),
			}}},
			{Content: "done"},
		},
	}
	runner := NewRunner(provider, registry, RunnerConfig{DefaultModel: "test-model", MaxSteps: 4})

	run, err := runner.StartRun(RunRequest{Prompt: "write outside the workspace"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if _, err := collectRunEvents(t, runner, run.ID); err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	if _, statErr := os.Stat(targetPath); statErr == nil {
		t.Fatalf("SECURITY REGRESSION: default-configured run wrote outside the workspace at %s", targetPath)
	}

	payload := toolMessagePayload(t, runner, run.ID, "write")
	errMsg, _ := payload["error"].(string)
	if errMsg == "" {
		t.Fatalf("expected the default-configured run's write to be denied, got payload=%+v", payload)
	}
}

// TestRegression_ExplicitUnrestrictedOptOut_StillWorks proves the GAP-1 fix
// left the documented escape hatch intact: a run that EXPLICITLY requests
// SandboxScope: unrestricted can still read outside the workspace. Without
// this, an operator with a legitimate need (e.g. a purely local, trusted,
// single-tenant CLI session) would have no way to opt out, which was
// explicitly required by the task ("a user/operator can still opt out
// cleanly").
func TestRegression_ExplicitUnrestrictedOptOut_StillWorks(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outsideDir := t.TempDir()
	referencePath := filepath.Join(outsideDir, "reference.txt")
	if err := os.WriteFile(referencePath, []byte("shared reference data"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	registry := NewDefaultRegistryWithOptions(workspace, DefaultRegistryOptions{})
	provider := &continuationProvider{
		turns: []CompletionResult{
			{ToolCalls: []ToolCall{{
				ID:        "call_read_ref",
				Name:      "read",
				Arguments: fmt.Sprintf(`{"path":%q}`, referencePath),
			}}},
			{Content: "done"},
		},
	}
	runner := NewRunner(provider, registry, RunnerConfig{DefaultModel: "test-model", MaxSteps: 4})

	run, err := runner.StartRun(RunRequest{
		Prompt: "read the shared reference file",
		Permissions: &PermissionConfig{
			Sandbox:  SandboxScopeUnrestricted,
			Approval: ApprovalPolicyNone,
		},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if _, err := collectRunEvents(t, runner, run.ID); err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	payload := toolMessagePayload(t, runner, run.ID, "read")
	content, _ := payload["content"].(string)
	if content != "shared reference data" {
		t.Fatalf("expected the explicit unrestricted opt-out to still permit reading outside the workspace, payload=%+v", payload)
	}
}

// TestRegression_NewDefaultRegistryWithOptions_ZeroValueAllowlist_StaysDenyByDefault
// proves GAP-3's default (an operator who does not set NetworkAllowlist at
// all — the zero value) remains deny-by-default: a private destination is
// still refused via the download tool. This guards against a future change
// that accidentally makes the zero value permissive (e.g. treating a nil
// slice as "allow everything" instead of "allow nothing").
func TestRegression_NewDefaultRegistryWithOptions_ZeroValueAllowlist_StaysDenyByDefault(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	registry := NewDefaultRegistryWithOptions(workspace, DefaultRegistryOptions{
		ApprovalMode: ToolApprovalModeFullAuto,
		// NetworkAllowlist intentionally left as the zero value (nil).
	})

	args, _ := json.Marshal(map[string]any{"url": "http://10.255.255.2:1/", "file_path": "out.bin"})
	_, err := registry.Execute(context.Background(), "download", args)
	if err == nil {
		t.Fatal("expected a private destination to be refused when NetworkAllowlist is left at its zero value")
	}
	if !strings.Contains(err.Error(), "ssrf-guard") {
		t.Fatalf("expected the ssrf guard to be what rejected the request, got: %v", err)
	}
}
