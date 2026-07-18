//go:build !unix

package tools

import "os/exec"

// configureGroupKill is a no-op on non-Unix platforms: there is no
// syscall-level process-group kill there, so the pre-#786 behavior
// (exec.CommandContext kills only the direct child) is kept.
func configureGroupKill(cmd *exec.Cmd) {}
