package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// shellCommandWaitDelay bounds how long ShellExecutor.Execute waits for
// cmd.Wait() to return after the job's timeout fires. Without it, Wait
// blocks until the process's stdout/stderr pipes reach EOF, which can
// never happen if a background grandchild process inherited those pipes
// and is still running — even after the direct child has been killed.
const shellCommandWaitDelay = 5 * time.Second

const maxOutputBytes = 4096

// Executor runs a job and returns the result.
type Executor interface {
	Execute(ctx context.Context, job Job) (output string, err error)
}

// ShellExecutor runs shell commands.
type ShellExecutor struct{}

// shellConfig is the JSON structure for shell execution config.
type shellConfig struct {
	Command string `json:"command"`
}

// Execute runs the shell command specified in the job's ExecConfig.
func (e *ShellExecutor) Execute(ctx context.Context, job Job) (string, error) {
	var cfg shellConfig
	if err := json.Unmarshal([]byte(job.ExecConfig), &cfg); err != nil {
		return "", fmt.Errorf("parse execution config: %w", err)
	}
	if cfg.Command == "" {
		return "", fmt.Errorf("execution config missing 'command' field")
	}

	timeout := time.Duration(job.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", cfg.Command)

	// Bound how long Wait can block after the timeout kills the command:
	// force-close the I/O pipes after shellCommandWaitDelay even if a
	// grandchild process is still holding them open. Without this, a
	// worker can leak forever (never releasing its scheduler semaphore
	// slot / WaitGroup count), which also prevents Scheduler.Stop() from
	// ever completing.
	cmd.WaitDelay = shellCommandWaitDelay

	// Run the command in its own process group and kill the WHOLE group
	// on cancellation, so background/orphaned children spawned by the
	// shell (e.g. `cmd &`) are actually terminated on timeout rather than
	// left running to hold the output pipes open. Mirrors the pattern in
	// internal/harness/tools/script/loader.go and internal/workflow/source.go.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	out, err := cmd.CombinedOutput()

	// Truncate output to maxOutputBytes.
	if len(out) > maxOutputBytes {
		out = out[:maxOutputBytes]
	}
	output := string(out)

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return output, fmt.Errorf("command timed out after %d seconds", job.TimeoutSec)
		}
		return output, fmt.Errorf("command failed: %w", err)
	}
	return output, nil
}
