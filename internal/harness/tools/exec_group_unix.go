//go:build unix

package tools

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// groupKillWaitDelay bounds how long cmd.Wait waits for inherited I/O pipes
// to close once the command's process (and its group) is gone, after which
// Wait returns an error wrapping exec.ErrWaitDelay instead of hanging.
const groupKillWaitDelay = 2 * time.Second

// configureGroupKill places cmd in its own process group and overrides
// exec.CommandContext's default Cancel (which SIGKILLs only the direct
// child) with a kill of the whole group, so grandchildren spawned via
// `bash -lc 'sleep 300 &'` die on timeout/job_kill too. Without this, an
// orphaned grandchild keeps the stdout/stderr pipes open and cmd.Wait
// blocks until it exits (#786). WaitDelay is a second belt for the same
// hang: if anything still holds the pipes after the kill, Wait returns
// exec.ErrWaitDelay instead of blocking forever. The same pattern is used
// for script tools in tools/script/loader.go.
func configureGroupKill(cmd *exec.Cmd) {
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
	cmd.WaitDelay = groupKillWaitDelay
}
