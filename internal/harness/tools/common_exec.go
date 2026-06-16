package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func runCommand(ctx context.Context, timeout time.Duration, command string, args ...string) (string, int, bool, error) {
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
	if err != nil && exitCode == -1 {
		if timedOut {
			// Process was killed by the function's own context timeout.
			// The timeout is already communicated via the timedOut boolean,
			// so return nil error here. Callers inspect timedOut to detect
			// the timeout condition, matching the bash_manager.go pattern.
			return output, exitCode, timedOut, nil
		}
		// Process was killed by an external signal (e.g. OOM killer).
		return output, exitCode, timedOut, fmt.Errorf("run command: %w", err)
	}
	return output, exitCode, timedOut, nil
}
