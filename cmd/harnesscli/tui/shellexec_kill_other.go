//go:build !unix

package tui

import "os/exec"

// configureShellGroupKill is a no-op on non-Unix platforms: there is no
// syscall-level process-group kill there, so exec.CommandContext's default
// behavior (kill only the direct child) is kept.
func configureShellGroupKill(cmd *exec.Cmd) {}
