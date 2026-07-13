package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// osSandboxAvailable reports whether this host has the OS-level confinement
// mechanism buildSandboxedCommand would actually apply for scope, so tests
// that prove real isolation (rather than the string heuristic) can skip
// gracefully on hosts/CI where it's missing.
func osSandboxAvailable(t *testing.T) bool {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
			return false
		}
		return true
	case "linux":
		_, err := exec.LookPath("bwrap")
		return err == nil
	default:
		return false
	}
}

func TestCheckSandboxCommandUnrestricted(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	// All commands should pass in unrestricted mode.
	commands := []string{
		"ls /tmp",
		"curl https://example.com",
		"wget http://example.com",
		"cd /etc && cat passwd",
	}
	for _, cmd := range commands {
		if err := CheckSandboxCommand(SandboxScopeUnrestricted, workspace, cmd); err != nil {
			t.Errorf("unrestricted scope: unexpected error for command %q: %v", cmd, err)
		}
	}
	// Empty scope is also unrestricted.
	for _, cmd := range commands {
		if err := CheckSandboxCommand("", workspace, cmd); err != nil {
			t.Errorf("empty scope: unexpected error for command %q: %v", cmd, err)
		}
	}
}

func TestCheckSandboxCommandLocalScope(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()

	blocked := []string{
		"curl https://example.com",
		"wget http://example.com",
		"nc -l 1234",
		"netcat example.com 80",
		"telnet example.com",
	}
	for _, cmd := range blocked {
		if err := CheckSandboxCommand(SandboxScopeLocal, workspace, cmd); err == nil {
			t.Errorf("local scope: expected error for command %q, got nil", cmd)
		}
	}

	// Local scope allows filesystem operations.
	allowed := []string{
		"ls /tmp",
		"cat /etc/hosts",
		"echo hello",
		"go test ./...",
	}
	for _, cmd := range allowed {
		if err := CheckSandboxCommand(SandboxScopeLocal, workspace, cmd); err != nil {
			t.Errorf("local scope: unexpected error for command %q: %v", cmd, err)
		}
	}
}

// TestCheckSandboxCommandWorkspaceScope verifies workspace-scope enforcement.
func TestCheckSandboxCommandWorkspaceScope(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	absWorkspace, _ := filepath.Abs(workspace)

	// Commands with absolute paths outside the workspace should be blocked.
	outsideAbsPaths := []string{
		"cat /etc/passwd",
		"ls /tmp",
		"rm /var/log/messages",
	}
	for _, cmd := range outsideAbsPaths {
		if err := CheckSandboxCommand(SandboxScopeWorkspace, absWorkspace, cmd); err == nil {
			t.Errorf("workspace scope: expected error for command %q with outside absolute path, got nil", cmd)
		}
	}

	// Commands entirely within the workspace should be allowed.
	insideCmd := "ls " + absWorkspace
	if err := CheckSandboxCommand(SandboxScopeWorkspace, absWorkspace, insideCmd); err != nil {
		t.Errorf("workspace scope: unexpected error for in-workspace command %q: %v", insideCmd, err)
	}

	// cd .. style escapes should be blocked.
	cdEscape := []string{
		"cd ..",
		"cd ../../etc",
		"cd ../  ",
	}
	for _, cmd := range cdEscape {
		if err := CheckSandboxCommand(SandboxScopeWorkspace, absWorkspace, cmd); err == nil {
			t.Errorf("workspace scope: expected error for cd-escape command %q, got nil", cmd)
		}
	}

	// Commands without absolute paths or cd escapes should be allowed.
	safeCommands := []string{
		"echo hello",
		"go test ./...",
		"ls",
		"cat notes.txt",
	}
	for _, cmd := range safeCommands {
		if err := CheckSandboxCommand(SandboxScopeWorkspace, absWorkspace, cmd); err != nil {
			t.Errorf("workspace scope: unexpected error for safe command %q: %v", cmd, err)
		}
	}
}

// TestSandboxWorkspaceScopeEnforcesFilePaths checks the case required by the issue:
// workspace scope blocks ../outside paths.
func TestSandboxWorkspaceScopeEnforcesFilePaths(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	absWorkspace, _ := filepath.Abs(workspace)

	// Writing to a path outside the workspace via absolute path should be blocked.
	outsideFile := filepath.Join(filepath.Dir(absWorkspace), "outside.txt")
	cmd := "echo secret > " + outsideFile
	if err := CheckSandboxCommand(SandboxScopeWorkspace, absWorkspace, cmd); err == nil {
		t.Errorf("workspace scope: expected error for write to %q, got nil", outsideFile)
	}

	// Writing inside the workspace is fine.
	insideFile := filepath.Join(absWorkspace, "inside.txt")
	cmd2 := "echo hello > " + insideFile
	if err := CheckSandboxCommand(SandboxScopeWorkspace, absWorkspace, cmd2); err != nil {
		t.Errorf("workspace scope: unexpected error for write to %q: %v", insideFile, err)
	}
}

// TestCheckSandboxCommandUnknownScope checks that an unknown scope returns an error.
func TestCheckSandboxCommandUnknownScope(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	if err := CheckSandboxCommand("badscope", workspace, "echo hi"); err == nil {
		t.Error("expected error for unknown sandbox scope, got nil")
	}
}

// TestJobManagerSandboxScopeWorkspace verifies that commands blocked by the
// workspace sandbox scope are rejected by JobManager.runForeground.
func TestJobManagerSandboxScopeWorkspace(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	absWorkspace, _ := filepath.Abs(workspace)

	mgr := NewJobManager(absWorkspace, nil)
	mgr.SetSandboxScope(SandboxScopeWorkspace)

	ctx := context.Background()

	// A command that references /etc/passwd (outside workspace) should be rejected.
	_, err := mgr.RunForeground(ctx, "cat /etc/passwd", 5, "")
	if err == nil {
		t.Error("expected sandbox error for 'cat /etc/passwd', got nil")
	}

	// A safe command should pass.
	result, err := mgr.RunForeground(ctx, "echo hello", 5, "")
	if err != nil {
		t.Errorf("unexpected error for safe command: %v", err)
	}
	if result == nil {
		t.Error("expected non-nil result for safe command")
	}
}

// TestJobManagerSandboxScopeLocal verifies that network commands are blocked
// under SandboxScopeLocal.
func TestJobManagerSandboxScopeLocal(t *testing.T) {
	t.Parallel()

	// Skip if no workspace needed.
	workspace, _ := os.MkdirTemp("", "sandbox-test")
	defer os.RemoveAll(workspace)

	mgr := NewJobManager(workspace, nil)
	mgr.SetSandboxScope(SandboxScopeLocal)

	ctx := context.Background()

	// curl should be blocked.
	_, err := mgr.RunForeground(ctx, "curl https://example.com", 5, "")
	if err == nil {
		t.Error("expected sandbox error for curl, got nil")
	}

	// echo should be allowed.
	result, err := mgr.RunForeground(ctx, "echo hi", 5, "")
	if err != nil {
		t.Errorf("unexpected error for echo: %v", err)
	}
	if result == nil {
		t.Error("expected non-nil result for echo")
	}
}

func TestJobManagerContextSandboxScopeOverridesDefault(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	absWorkspace, _ := filepath.Abs(workspace)

	mgr := NewJobManager(absWorkspace, nil)
	mgr.SetSandboxScope(SandboxScopeWorkspace)

	ctx := WithSandboxScope(context.Background(), SandboxScopeUnrestricted)

	result, err := mgr.RunForeground(ctx, "cat /etc/hosts", 5, "")
	if err != nil {
		t.Fatalf("expected context sandbox override to allow command, got error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if exitCode, _ := result["exit_code"].(int); exitCode != 0 {
		t.Fatalf("expected exit_code 0, got %v", result["exit_code"])
	}
}

func TestJobManagerContextSandboxScopeBlocksBackgroundCommand(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()

	mgr := NewJobManager(workspace, nil)
	mgr.SetSandboxScope(SandboxScopeUnrestricted)

	ctx := WithSandboxScope(context.Background(), SandboxScopeLocal)

	if _, err := mgr.RunBackgroundWithContext(ctx, "curl https://example.com", 5, ""); err == nil {
		t.Fatal("expected local sandbox override to block background network command")
	}
}

// TestSandboxWorkspaceScopeBlocksWriteOutsideWorkspaceAtOSLevel proves that
// workspace-scope confinement is enforced by the OS, not by string matching:
// the destination path is built via shell variable indirection so it never
// appears as a literal absolute-path token in the command, which the
// existing regex/token heuristic in checkWorkspaceScopeCommand would have
// caught. The write must still fail at the OS level.
func TestSandboxWorkspaceScopeBlocksWriteOutsideWorkspaceAtOSLevel(t *testing.T) {
	if !osSandboxAvailable(t) {
		t.Skip("no OS-level sandbox mechanism (seatbelt/bubblewrap) available on this host")
	}

	workspace := t.TempDir()
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		t.Fatal(err)
	}

	mgr := NewJobManager(absWorkspace, nil)
	mgr.SetSandboxScope(SandboxScopeWorkspace)

	target := filepath.Join(os.TempDir(), fmt.Sprintf("harness-sandbox-proof-%d", time.Now().UnixNano()))
	_ = os.Remove(target)
	defer os.Remove(target)

	dir := filepath.Dir(target)
	base := filepath.Base(target)
	command := fmt.Sprintf(`D=%s; echo pwned > "$D/%s"`, dir, base)

	// The heuristic layer must NOT catch this obfuscated escape — that is
	// what makes this a proof of OS-level enforcement rather than a
	// duplicate of the existing string-matching tests above.
	if err := CheckSandboxCommand(SandboxScopeWorkspace, absWorkspace, command); err != nil {
		t.Fatalf("expected heuristic to miss the obfuscated escape (so the OS layer is what's under test), got error: %v", err)
	}

	result, _ := mgr.RunForeground(context.Background(), command, 5, "")
	if result != nil {
		if exitCode, _ := result["exit_code"].(int); exitCode == 0 {
			t.Errorf("expected non-zero exit code for OS-blocked write outside workspace, got 0; result=%v", result)
		}
	}
	if _, statErr := os.Stat(target); statErr == nil {
		t.Fatalf("sandbox violation: file %q was created outside the workspace despite workspace sandbox scope", target)
	}
}

// TestSandboxLocalScopeBlocksObfuscatedNetworkAtOSLevel proves that
// local-scope network denial is enforced by the OS, not by regex matching
// against the command string: "curl" is assembled from two shell variables
// so the literal substring "curl" never appears in the command, defeating
// the \bcurl\b pattern in checkLocalScopeCommand. The request must still
// fail because the OS layer denies network operations outright.
func TestSandboxLocalScopeBlocksObfuscatedNetworkAtOSLevel(t *testing.T) {
	if !osSandboxAvailable(t) {
		t.Skip("no OS-level sandbox mechanism (seatbelt/bubblewrap) available on this host")
	}

	workspace := t.TempDir()
	mgr := NewJobManager(workspace, nil)
	mgr.SetSandboxScope(SandboxScopeLocal)

	command := `A=cur; B=l; "$A$B" -s -m 5 https://example.com -o /dev/null -w '%{http_code}'`

	if err := CheckSandboxCommand(SandboxScopeLocal, workspace, command); err != nil {
		t.Fatalf("expected heuristic to miss the obfuscated network command (so the OS layer is what's under test), got error: %v", err)
	}

	result, err := mgr.RunForeground(context.Background(), command, 10, "")
	if err != nil {
		// A hard exec failure also demonstrates the network call never
		// succeeded; only a clean success (exit 0) would be a problem.
		return
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	exitCode, _ := result["exit_code"].(int)
	if exitCode == 0 {
		t.Fatalf("expected obfuscated curl to fail under OS-level network denial, got exit_code 0; result=%v", result)
	}
}

func TestResolveSandboxUnavailableDegradesByDefault(t *testing.T) {
	res, err := resolveSandboxUnavailable(SandboxScopeWorkspace, "seatbelt", "binary not found")
	if err != nil {
		t.Fatalf("expected no error in default (non-strict) mode, got: %v", err)
	}
	if res.Applied {
		t.Error("expected Applied=false for unavailable mechanism")
	}
	if res.Mechanism != "unavailable" {
		t.Errorf("expected Mechanism=\"unavailable\", got %q", res.Mechanism)
	}
	if res.Warning == "" {
		t.Error("expected a non-empty warning explaining the degradation")
	}
}

func TestResolveSandboxUnavailableFailsClosedWhenStrict(t *testing.T) {
	t.Setenv(SandboxEnforcementEnv, "1")

	if _, err := resolveSandboxUnavailable(SandboxScopeWorkspace, "seatbelt", "binary not found"); err == nil {
		t.Fatal("expected an error when strict mode is enabled and the mechanism is unavailable")
	}
}
