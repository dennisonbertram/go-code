package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runHooksCLI runs the hooks subcommand with stdout/stderr captured and HOME
// pointed at a temp dir so the trust store is isolated.
func runHooksCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	origOut, origErr := stdout, stderr
	t.Cleanup(func() { stdout, stderr = origOut, origErr })
	var outBuf, errBuf bytes.Buffer
	stdout = &outBuf
	stderr = &errBuf
	code := dispatch(append([]string{"hooks"}, args...))
	return code, outBuf.String(), errBuf.String()
}

func writeProjectHook(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hook.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHooksCLI_TrustListRevoke(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hookPath := writeProjectHook(t, `{"name":"h","event":"pre_tool_use","kind":"command","command":["/bin/true"]}`)

	// trust
	code, out, _ := runHooksCLI(t, "trust", hookPath)
	if code != 0 {
		t.Fatalf("trust exit code: got %d", code)
	}
	if !strings.Contains(out, "trusted") {
		t.Errorf("trust output %q should confirm trust", out)
	}

	// list shows the record
	code, out, _ = runHooksCLI(t, "list")
	if code != 0 {
		t.Fatalf("list exit code: got %d", code)
	}
	if !strings.Contains(out, hookPath) {
		t.Errorf("list output %q should contain %q", out, hookPath)
	}

	// revoke
	code, out, _ = runHooksCLI(t, "revoke", hookPath)
	if code != 0 {
		t.Fatalf("revoke exit code: got %d", code)
	}
	if !strings.Contains(out, "revoked") {
		t.Errorf("revoke output %q should confirm revocation", out)
	}

	// list is empty again
	code, out, _ = runHooksCLI(t, "list")
	if code != 0 {
		t.Fatalf("list exit code: got %d", code)
	}
	if strings.Contains(out, hookPath) {
		t.Errorf("list output %q should not contain revoked path", out)
	}
}

func TestHooksCLI_TrustMissingFileFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	code, _, errOut := runHooksCLI(t, "trust", filepath.Join(t.TempDir(), "ghost.json"))
	if code == 0 {
		t.Fatal("trust of a nonexistent file must fail")
	}
	if !strings.Contains(errOut, "ghost.json") {
		t.Errorf("error output %q should name the file", errOut)
	}
}

func TestHooksCLI_NoSubcommandPrintsUsage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	code, _, errOut := runHooksCLI(t)
	if code == 0 {
		t.Fatal("bare 'hooks' must print usage and fail")
	}
	if !strings.Contains(errOut, "trust") || !strings.Contains(errOut, "revoke") {
		t.Errorf("usage %q should name subcommands", errOut)
	}
}

func TestHooksCLI_UnknownSubcommandFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	code, _, _ := runHooksCLI(t, "explode")
	if code == 0 {
		t.Fatal("unknown subcommand must fail")
	}
}
