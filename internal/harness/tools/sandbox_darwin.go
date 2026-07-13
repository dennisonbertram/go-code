//go:build darwin

package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// sandboxExecBinary is Apple's seatbelt CLI. It is deprecated but remains
// functional on all currently shipping macOS releases, and is the mechanism
// used by other coding-agent sandboxes for the same purpose.
const sandboxExecBinary = "/usr/bin/sandbox-exec"

// buildSandboxedCommand wraps `/bin/bash -lc command` in a seatbelt profile
// appropriate for scope. The returned cleanup func must be called once the
// command has finished running (Run/Wait returned) to remove the temporary
// profile file.
func buildSandboxedCommand(ctx context.Context, scope SandboxScope, workspaceRoot, command string) (*exec.Cmd, func(), SandboxExecResult, error) {
	noop := func() {}
	switch scope {
	case SandboxScopeUnrestricted, "":
		return exec.CommandContext(ctx, "/bin/bash", "-lc", command), noop, SandboxExecResult{Applied: false, Mechanism: "none"}, nil
	case SandboxScopeWorkspace, SandboxScopeLocal:
		if _, statErr := os.Stat(sandboxExecBinary); statErr != nil {
			res, err := resolveSandboxUnavailable(scope, "seatbelt", fmt.Sprintf("%s not found: %v", sandboxExecBinary, statErr))
			if err != nil {
				return nil, nil, SandboxExecResult{}, err
			}
			return exec.CommandContext(ctx, "/bin/bash", "-lc", command), noop, res, nil
		}

		absRoot, absErr := filepath.Abs(workspaceRoot)
		if absErr != nil {
			absRoot = workspaceRoot
		}

		profile := seatbeltProfile(scope, absRoot)
		f, err := os.CreateTemp("", "harness-sandbox-*.sb")
		if err != nil {
			return nil, nil, SandboxExecResult{}, fmt.Errorf("sandbox: create seatbelt profile: %w", err)
		}
		if _, err := f.WriteString(profile); err != nil {
			f.Close()
			os.Remove(f.Name())
			return nil, nil, SandboxExecResult{}, fmt.Errorf("sandbox: write seatbelt profile: %w", err)
		}
		if err := f.Close(); err != nil {
			os.Remove(f.Name())
			return nil, nil, SandboxExecResult{}, fmt.Errorf("sandbox: close seatbelt profile: %w", err)
		}
		cleanup := func() { os.Remove(f.Name()) }

		cmd := exec.CommandContext(ctx, sandboxExecBinary, "-f", f.Name(), "/bin/bash", "-lc", command)
		return cmd, cleanup, SandboxExecResult{Applied: true, Mechanism: "seatbelt"}, nil
	default:
		return nil, nil, SandboxExecResult{}, fmt.Errorf("unknown sandbox scope %q", scope)
	}
}

// seatbeltProfile generates an Apple sandbox (seatbelt) profile for scope.
//
// SandboxScopeWorkspace: reads are allowed broadly (needed for coreutils,
// dynamic linking, terminfo, locale data, etc. without hand-maintaining an
// allowlist of every system path a shell invocation might touch); writes are
// confined to workspaceRoot plus the handful of device nodes a non-interactive
// bash needs (/dev/null, /dev/tty, /dev/zero, /dev/dtracehelper); all network
// operations are denied.
//
// SandboxScopeLocal: filesystem access (read and write) is unconfined;
// network operations are denied.
func seatbeltProfile(scope SandboxScope, workspaceRoot string) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(deny default)\n(allow process-fork)\n(allow process-exec)\n(allow signal (target self))\n(allow sysctl-read)\n(allow mach-lookup)\n(allow iokit-open)\n(allow file-read*)\n")
	switch scope {
	case SandboxScopeWorkspace:
		b.WriteString(fmt.Sprintf("(allow file-write* (subpath %s))\n", seatbeltQuote(workspaceRoot)))
		b.WriteString(`(allow file-write-data (literal "/dev/null") (literal "/dev/tty") (literal "/dev/dtracehelper") (literal "/dev/zero"))` + "\n")
		b.WriteString(`(allow file-ioctl (literal "/dev/null") (literal "/dev/tty"))` + "\n")
	case SandboxScopeLocal:
		b.WriteString("(allow file-write*)\n")
	}
	b.WriteString("(deny network*)\n")
	return b.String()
}

// seatbeltQuote renders p as a double-quoted seatbelt string literal.
func seatbeltQuote(p string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range p {
		if r == '"' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}
