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
		} else {
			exitCode = -1
		}
	}
	timedOut := errors.Is(ctxTimeout.Err(), context.DeadlineExceeded)

	output := mergeCommandStreams(strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()))
	return output, exitCode, timedOut, err
}

func runCommand(ctx context.Context, timeout time.Duration, command string, args ...string) (string, int, bool, error) {
	// When a subprocess is killed by an OS signal (not our timeout), it may
	// be a transient condition like CI OOM pressure.  Retry up to 2 extra
	// times with a brief backoff to deflake tests and production usage.
	const maxAttempts = 3

	var lastOutput string
	var lastExitCode int
	var lastTimedOut bool
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		output, exitCode, timedOut, err := runCommandOnce(ctx, timeout, command, args...)

		if err != nil && exitCode == -1 {
			lastOutput = output
			lastExitCode = exitCode
			lastTimedOut = timedOut
			lastErr = err

			// Timeout kills are expected; don't retry them.
			if timedOut {
				return output, exitCode, timedOut, fmt.Errorf("run command: %w", err)
			}

			// External signal kill: retry if we have attempts remaining.
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
		return output, exitCode, timedOut, nil
	}

	return lastOutput, lastExitCode, lastTimedOut, fmt.Errorf("run command: %w", lastErr)
}
