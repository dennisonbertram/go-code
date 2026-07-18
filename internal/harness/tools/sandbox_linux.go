//go:build linux

package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// bubblewrap ("bwrap") is used rather than hand-rolled landlock syscalls:
// landlock has no maintained wrapper in this module's existing dependency
// set (golang.org/x/sys is present only as an indirect transitive
// dependency of other packages, and driving LSM_GET_FS_CONFIGURATION /
// landlock_add_rule directly via raw syscalls is a substantial amount of
// unsafe, kernel-version-sensitive code for this change). bwrap is a
// widely available, single-binary, unprivileged (user-namespace based)
// sandboxing tool that gives us real mount-namespace filesystem
// confinement and network-namespace isolation without adding a Go
// dependency.
func buildSandboxedCommand(ctx context.Context, scope SandboxScope, workspaceRoot, command string) (*exec.Cmd, func(), SandboxExecResult, error) {
	noop := func() {}
	switch scope {
	case SandboxScopeUnrestricted, "":
		return exec.CommandContext(ctx, "/bin/bash", "-lc", command), noop, SandboxExecResult{Applied: false, Mechanism: "none"}, nil
	case SandboxScopeWorkspace, SandboxScopeLocal:
		bwrapPath, lookErr := exec.LookPath("bwrap")
		if lookErr != nil {
			res, err := resolveSandboxUnavailable(scope, "bubblewrap", "bwrap binary not found on PATH")
			if err != nil {
				return nil, nil, SandboxExecResult{}, err
			}
			return exec.CommandContext(ctx, "/bin/bash", "-lc", command), noop, res, nil
		}

		absRoot, absErr := filepath.Abs(workspaceRoot)
		if absErr != nil {
			absRoot = workspaceRoot
		}

		args := []string{
			"--die-with-parent",
			"--unshare-net",
			// Isolate PID and IPC namespaces so sandboxed processes can
			// neither signal same-UID host processes nor read host
			// /proc/<pid>/environ (API keys) — parity with darwin seatbelt's
			// (allow signal (target self)). --new-session detaches the
			// controlling terminal; bwrap's own minimal PID 1 reaps zombies,
			// so --as-pid-1 is intentionally not used (#785).
			"--unshare-pid",
			"--unshare-ipc",
			"--new-session",
			"--proc", "/proc",
			"--dev", "/dev",
		}
		if scope == SandboxScopeWorkspace {
			// Bind the whole root filesystem read-only, then punch a
			// read-write hole for the workspace only. Separate mounts
			// (e.g. /tmp is frequently its own tmpfs mount) are not
			// picked up by a "/" bind and must be bound explicitly so
			// writes there are also confined.
			args = append(args, "--ro-bind", "/", "/")
			for _, extra := range []string{"/tmp", "/var/tmp"} {
				if _, statErr := os.Stat(extra); statErr == nil {
					args = append(args, "--ro-bind", extra, extra)
				}
			}
			args = append(args, "--bind", absRoot, absRoot)
		} else {
			args = append(args, "--bind", "/", "/")
		}
		args = append(args, "--", "/bin/bash", "-lc", command)

		cmd := exec.CommandContext(ctx, bwrapPath, args...)
		return cmd, noop, SandboxExecResult{Applied: true, Mechanism: "bubblewrap"}, nil
	default:
		return nil, nil, SandboxExecResult{}, fmt.Errorf("unknown sandbox scope %q", scope)
	}
}
