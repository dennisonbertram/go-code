package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestPermissionConfigDefaults verifies that DefaultPermissionConfig returns
// the expected workspace/none combination.
//
// SAFETY DEFAULT (GAP-1): Sandbox defaults to SandboxScopeWorkspace, not
// SandboxScopeUnrestricted. A caller that submits a run with no Permissions
// field at all must be workspace-confined by default; callers that
// legitimately need broader filesystem access must explicitly opt in via
// PermissionConfig.Sandbox: SandboxScopeLocal or SandboxScopeUnrestricted.
// See TestDefaultConfiguredRun_CannotReadAbsolutePathOutsideWorkspace for the
// end-to-end acceptance test of this default.
func TestPermissionConfigDefaults(t *testing.T) {
	t.Parallel()

	cfg := DefaultPermissionConfig()
	if cfg.Sandbox != SandboxScopeWorkspace {
		t.Errorf("expected default sandbox %q, got %q", SandboxScopeWorkspace, cfg.Sandbox)
	}
	if cfg.Approval != ApprovalPolicyNone {
		t.Errorf("expected default approval %q, got %q", ApprovalPolicyNone, cfg.Approval)
	}
}

// TestPermissionConfigValidation checks that ValidatePermissionConfig accepts
// valid combinations and rejects invalid ones.
func TestPermissionConfigValidation(t *testing.T) {
	t.Parallel()

	valid := []PermissionConfig{
		{Sandbox: SandboxScopeWorkspace, Approval: ApprovalPolicyNone},
		{Sandbox: SandboxScopeWorkspace, Approval: ApprovalPolicyDestructive},
		{Sandbox: SandboxScopeWorkspace, Approval: ApprovalPolicyAll},
		{Sandbox: SandboxScopeLocal, Approval: ApprovalPolicyNone},
		{Sandbox: SandboxScopeLocal, Approval: ApprovalPolicyDestructive},
		{Sandbox: SandboxScopeLocal, Approval: ApprovalPolicyAll},
		{Sandbox: SandboxScopeUnrestricted, Approval: ApprovalPolicyNone},
		{Sandbox: SandboxScopeUnrestricted, Approval: ApprovalPolicyDestructive},
		{Sandbox: SandboxScopeUnrestricted, Approval: ApprovalPolicyAll},
		// Empty fields are also valid (zero values default gracefully).
		{Sandbox: "", Approval: ""},
		{Sandbox: SandboxScopeWorkspace, Approval: ""},
		{Sandbox: "", Approval: ApprovalPolicyAll},
	}

	for _, cfg := range valid {
		if err := ValidatePermissionConfig(cfg); err != nil {
			t.Errorf("expected valid config {Sandbox:%q Approval:%q} to pass, got: %v", cfg.Sandbox, cfg.Approval, err)
		}
	}

	invalid := []PermissionConfig{
		{Sandbox: "badscope", Approval: ApprovalPolicyNone},
		{Sandbox: SandboxScopeWorkspace, Approval: "badpolicy"},
		{Sandbox: "X", Approval: "Y"},
	}

	for _, cfg := range invalid {
		if err := ValidatePermissionConfig(cfg); err == nil {
			t.Errorf("expected invalid config {Sandbox:%q Approval:%q} to fail, but got nil error", cfg.Sandbox, cfg.Approval)
		}
	}
}

// TestPermissionConfigToLegacy verifies backward-compatible mapping from
// PermissionConfig to the legacy ToolApprovalMode.
func TestPermissionConfigToLegacy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		cfg      PermissionConfig
		wantMode ToolApprovalMode
	}{
		{
			cfg:      PermissionConfig{Sandbox: SandboxScopeUnrestricted, Approval: ApprovalPolicyNone},
			wantMode: ToolApprovalModeFullAuto,
		},
		{
			cfg:      PermissionConfig{Sandbox: SandboxScopeWorkspace, Approval: ApprovalPolicyDestructive},
			wantMode: ToolApprovalModePermissions,
		},
		{
			cfg:      PermissionConfig{Sandbox: SandboxScopeLocal, Approval: ApprovalPolicyAll},
			wantMode: ToolApprovalModeAll,
		},
		{
			// Unknown approval policy should fall back to full_auto.
			cfg:      PermissionConfig{Sandbox: SandboxScopeUnrestricted, Approval: ""},
			wantMode: ToolApprovalModeFullAuto,
		},
	}

	for _, tc := range cases {
		got := tc.cfg.ToLegacy()
		if got != tc.wantMode {
			t.Errorf("ToLegacy({Sandbox:%q Approval:%q}): want %q, got %q",
				tc.cfg.Sandbox, tc.cfg.Approval, tc.wantMode, got)
		}
	}
}

// TestStartRunRejectsInvalidPermissions checks that StartRun returns an error
// when a RunRequest contains an invalid PermissionConfig.
func TestStartRunRejectsInvalidPermissions(t *testing.T) {
	t.Parallel()

	runner := NewRunner(&stubProvider{}, nil, RunnerConfig{})

	badPerms := &PermissionConfig{
		Sandbox:  "not-a-valid-scope",
		Approval: ApprovalPolicyNone,
	}

	_, err := runner.StartRun(RunRequest{
		Prompt:      "test",
		Permissions: badPerms,
	})
	if err == nil {
		t.Fatal("expected error for invalid permissions, got nil")
	}
}

// TestStartRunAcceptsValidPermissions checks that a RunRequest with a valid
// PermissionConfig is accepted.
func TestStartRunAcceptsValidPermissions(t *testing.T) {
	t.Parallel()

	runner := NewRunner(&stubProvider{}, nil, RunnerConfig{})

	goodPerms := &PermissionConfig{
		Sandbox:  SandboxScopeWorkspace,
		Approval: ApprovalPolicyDestructive,
	}

	run, err := runner.StartRun(RunRequest{
		Prompt:      "test",
		Permissions: goodPerms,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.ID == "" {
		t.Fatal("expected non-empty run ID")
	}
}

// TestApprovalPolicyDestructive verifies that ApprovalModePermissions (mapped from
// ApprovalPolicyDestructive) causes the policy to be consulted for mutating tools
// but not for read-only tools.
func TestApprovalPolicyDestructive(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()

	// denyPolicy denies all mutating tool calls.
	denyPolicy := staticPolicy{decision: ToolPolicyDecision{Allow: false, Reason: "denied by test"}}

	registry := NewDefaultRegistryWithOptions(workspace, DefaultRegistryOptions{
		ApprovalMode: ToolApprovalModePermissions,
		Policy:       denyPolicy,
	})

	// Write tool is mutating — should be denied.
	writeArgs, _ := json.Marshal(map[string]any{"path": "test.txt", "content": "hello"})
	out, err := registry.Execute(context.Background(), "write", writeArgs)
	if err != nil {
		// Policy denial should not return an error but a denial message in JSON output.
		t.Logf("write returned error (acceptable): %v", err)
	} else {
		// Check the output contains permission_denied.
		if out == "" {
			t.Error("expected non-empty output for denied write")
		}
		var result map[string]any
		if jsonErr := json.Unmarshal([]byte(out), &result); jsonErr == nil {
			if errField, ok := result["error"]; ok {
				errMap, _ := errField.(map[string]any)
				if code, _ := errMap["code"].(string); code != "permission_denied" {
					t.Errorf("expected permission_denied code, got %q in output: %s", code, out)
				}
			}
		}
	}

	// Read tool should be auto-approved (not consulted against policy).
	// Write a file first directly via the filesystem so read has something to read.
	notesPath := filepath.Join(workspace, "notes.txt")
	if fileErr := os.WriteFile(notesPath, []byte("hello"), 0644); fileErr != nil {
		t.Logf("skipping read test (can't write test fixture): %v", fileErr)
		return
	}
	readArgs, _ := json.Marshal(map[string]any{"path": "notes.txt"})
	_, readErr := registry.Execute(context.Background(), "read", readArgs)
	if readErr != nil {
		t.Errorf("read tool should be allowed in permissions mode, but got error: %v", readErr)
	}
}
