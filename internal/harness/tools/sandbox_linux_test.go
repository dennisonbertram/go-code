//go:build linux

package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestBuildSandboxedCommandLinuxIsolatesPIDAndIPC guards #785 at the
// argument level: the bwrap invocation must unshare the PID and IPC
// namespaces (and start a new session) in addition to the pre-existing
// network unshare, for both confinement scopes. Without --unshare-pid a
// sandboxed process can signal every same-UID host process and read host
// /proc/<pid>/environ (API keys); darwin's seatbelt already restricts
// signals to self.
func TestBuildSandboxedCommandLinuxIsolatesPIDAndIPC(t *testing.T) {
	// Not parallel: this test rewrites the process-global PATH via
	// t.Setenv, which the testing package forbids in parallel tests.
	//
	// A fake bwrap on PATH is sufficient: the test only inspects the
	// assembled argv, it never executes it.
	dir := t.TempDir()
	fakeBwrap := filepath.Join(dir, "bwrap")
	if err := os.WriteFile(fakeBwrap, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	for _, scope := range []SandboxScope{SandboxScopeWorkspace, SandboxScopeLocal} {
		scope := scope
		t.Run(string(scope), func(t *testing.T) {
			t.Parallel()
			cmd, cleanup, res, err := buildSandboxedCommand(context.Background(), scope, t.TempDir(), "echo hi")
			if err != nil {
				t.Fatalf("buildSandboxedCommand: %v", err)
			}
			defer cleanup()
			if !res.Applied {
				t.Fatalf("expected sandbox to be applied, got %+v", res)
			}

			var bwrapArgs []string
			for _, a := range cmd.Args {
				if a == "--" {
					break
				}
				bwrapArgs = append(bwrapArgs, a)
			}
			for _, want := range []string{"--unshare-pid", "--unshare-ipc", "--new-session", "--unshare-net", "--die-with-parent"} {
				found := false
				for _, a := range bwrapArgs {
					if a == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected %q in bwrap args before \"--\", got: %s", want, strings.Join(bwrapArgs, " "))
				}
			}
		})
	}
}

// TestSandboxLinuxPIDNamespaceHidesHostProcesses guards #785 at the OS level:
// a process inside the sandbox must not be able to signal a host canary
// process nor read its /proc environ. Skipped on hosts without a usable
// bubblewrap install (same gating pattern as sandbox_test.go).
func TestSandboxLinuxPIDNamespaceHidesHostProcesses(t *testing.T) {
	if !osSandboxAvailable(t) {
		t.Skip("no OS-level sandbox mechanism (bubblewrap) available on this host")
	}
	// bwrap can be present while user namespaces are unusable (e.g.
	// kernel.unprivileged_userns_clone=0 in locked-down containers); probe
	// once with the same isolation flags and skip rather than fail there.
	probe := exec.Command("bwrap", "--die-with-parent", "--unshare-net",
		"--unshare-pid", "--unshare-ipc", "--new-session",
		"--proc", "/proc", "--dev", "/dev", "--bind", "/", "/",
		"--", "/bin/true")
	if err := probe.Run(); err != nil {
		t.Skipf("bwrap present but namespace sandbox not usable on this host: %v", err)
	}

	// Host canary the sandboxed command will try to signal and inspect.
	canary := exec.Command("sleep", "30")
	if err := canary.Start(); err != nil {
		t.Fatalf("start canary: %v", err)
	}
	defer func() {
		_ = canary.Process.Kill()
		_ = canary.Wait()
	}()
	canaryPid := canary.Process.Pid

	workspace := t.TempDir()
	mgr := NewJobManager(workspace, nil)
	mgr.SetSandboxScope(SandboxScopeLocal)

	command := fmt.Sprintf(
		`if kill -0 %d 2>/dev/null; then echo CAN_SIGNAL_HOST; else echo HOST_HIDDEN; fi; `+
			`if [ -r /proc/%d/environ ]; then echo ENVIRON_READABLE; else echo ENVIRON_HIDDEN; fi`,
		canaryPid, canaryPid)

	result, err := mgr.RunForeground(context.Background(), command, 10, "")
	if err != nil {
		t.Fatalf("run foreground: %v", err)
	}
	output, _ := result["output"].(string)

	if strings.Contains(output, "CAN_SIGNAL_HOST") {
		t.Errorf("sandboxed process could signal host process %d; output=%q", canaryPid, output)
	}
	if !strings.Contains(output, "HOST_HIDDEN") {
		t.Errorf("expected HOST_HIDDEN (sandboxed process must not see host pids); output=%q result=%v", output, result)
	}
	if strings.Contains(output, "ENVIRON_READABLE") {
		t.Errorf("sandboxed process could read host /proc/%d/environ; output=%q", canaryPid, output)
	}
	if !strings.Contains(output, "ENVIRON_HIDDEN") {
		t.Errorf("expected ENVIRON_HIDDEN (host /proc environ must be unreadable); output=%q result=%v", output, result)
	}

	// The canary must still be alive from the host side.
	if err := canary.Process.Signal(syscall.Signal(0)); err != nil {
		t.Errorf("canary process %d died unexpectedly: %v", canaryPid, err)
	}
}
