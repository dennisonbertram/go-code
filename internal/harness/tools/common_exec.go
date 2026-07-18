package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// runCommandOnce executes a single attempt.  exitCode == -1 signals the
// process was killed (signal) rather than exiting normally.
func runCommandOnce(ctx context.Context, timeout time.Duration, command string, args ...string) (string, int, bool, error) {
	ctxTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctxTimeout, command, args...)
	configureGroupKill(cmd)
	stdout := newHeadTailBuffer(defaultMaxCommandOutputBytes)
	stderr := newHeadTailBuffer(defaultMaxCommandOutputBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()

	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			// The process exited normally but a descendant kept the pipes
			// open past WaitDelay; preserve the real exit code (#786).
			exitCode = cmd.ProcessState.ExitCode()
		} else {
			exitCode = -1
		}
	}
	timedOut := errors.Is(ctxTimeout.Err(), context.DeadlineExceeded)

	output := mergeCommandStreams(strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()))
	return output, exitCode, timedOut, err
}

func runCommand(ctx context.Context, timeout time.Duration, command string, args ...string) (string, int, bool, error) {
	// When a subprocess is killed by an external OS signal (not our own
	// timeout), it may be a transient condition such as CI OOM pressure.
	// Retry up to 2 extra times with a brief backoff to deflake tests and
	// production usage. Timeouts and normal exits are never retried.
	const maxAttempts = 3

	var lastOutput string
	var lastExitCode int
	var lastTimedOut bool
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		output, exitCode, timedOut, err := runCommandOnce(ctx, timeout, command, args...)

		if err != nil && exitCode == -1 {
			if timedOut {
				// Killed by our own context timeout. Preserve the existing
				// contract: the timeout is communicated via the timedOut
				// boolean, so callers see a nil error (matching bash_manager).
				return output, exitCode, timedOut, nil
			}

			// External signal kill: retry if we have attempts remaining.
			lastOutput, lastExitCode, lastTimedOut, lastErr = output, exitCode, timedOut, err
			if attempt < maxAttempts-1 {
				select {
				case <-ctx.Done():
					return output, exitCode, timedOut, fmt.Errorf("run command: %w", err)
				case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
				}
				continue
			}
			return output, exitCode, timedOut, fmt.Errorf("run command: %w", err)
		}

		// Normal exit (including non-zero) or success: preserve the existing
		// nil-error contract.
		return output, exitCode, timedOut, nil
	}

	return lastOutput, lastExitCode, lastTimedOut, fmt.Errorf("run command: %w", lastErr)
}
