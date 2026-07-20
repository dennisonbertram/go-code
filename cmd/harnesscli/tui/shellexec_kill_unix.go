//go:build unix

package tui

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// shellGroupKillWaitDelay bounds how long cmd.Wait waits for inherited I/O
// pipes to close once the command's process group is gone, after which Wait
// returns an error wrapping exec.ErrWaitDelay instead of hanging.
const shellGroupKillWaitDelay = 2 * time.Second

// configureShellGroupKill places cmd in its own process group and overrides
// exec.CommandContext's default Cancel (which SIGKILLs only the direct child)
// with a kill of the whole group, so children spawned by `sh -c` die on
// timeout and on Esc/Ctrl-C too. Mirrors configureGroupKill in
// internal/harness/tools/exec_group_unix.go (#786), which is unexported.
func configureShellGroupKill(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	cmd.WaitDelay = shellGroupKillWaitDelay
}
