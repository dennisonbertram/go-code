package tools

import (
	"context"
	"testing"
	"time"
)

// TestRunCommand_TimeoutReturnsNilError guards the merge-critical contract: a
// command killed by our own context timeout reports timedOut=true with a NIL
// error (the timeout is signalled via the boolean, matching bash_manager). The
// #530 retry logic must not turn this into an error or retry it.
func TestRunCommand_TimeoutReturnsNilError(t *testing.T) {
	t.Parallel()
	start := time.Now()
	_, exitCode, timedOut, err := runCommand(context.Background(), 100*time.Millisecond, "sleep", "5")
	if !timedOut {
		t.Errorf("expected timedOut=true for a command that exceeds the timeout")
	}
	if err != nil {
		t.Errorf("expected nil error on timeout (contract), got %v", err)
	}
	if exitCode != -1 {
		t.Errorf("expected exitCode -1 (killed), got %d", exitCode)
	}
	// Must NOT have retried the timeout (single ~100ms attempt, no 300ms backoff).
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("timeout was retried (took %s); it must not be", elapsed)
	}
}

// TestRunCommand_NormalNonZeroExitNilError verifies a normal non-zero exit is
// not treated as a signal kill: nil error, real exit code, no retry.
func TestRunCommand_NormalNonZeroExitNilError(t *testing.T) {
	t.Parallel()
	_, exitCode, timedOut, err := runCommand(context.Background(), 5*time.Second, "bash", "-c", "exit 3")
	if err != nil {
		t.Errorf("normal non-zero exit should return nil error, got %v", err)
	}
	if timedOut {
		t.Error("normal exit should not be timedOut")
	}
	if exitCode != 3 {
		t.Errorf("expected exit code 3, got %d", exitCode)
	}
}

// TestRunCommand_ExternalSignalKillRetriesThenErrors verifies the #530 fix: a
// command killed by an external signal (not our timeout) is retried and, when
// it keeps failing, returns an error with exitCode -1 and timedOut=false. The
// retries introduce a small backoff (100ms + 200ms), so the call takes >=300ms.
func TestRunCommand_ExternalSignalKillRetriesThenErrors(t *testing.T) {
	t.Parallel()
	start := time.Now()
	_, exitCode, timedOut, err := runCommand(context.Background(), 5*time.Second, "bash", "-c", "kill -9 $$")
	if err == nil {
		t.Error("expected an error for a command persistently killed by an external signal")
	}
	if timedOut {
		t.Error("external signal kill must not be reported as timedOut")
	}
	if exitCode != -1 {
		t.Errorf("expected exitCode -1 for a signal kill, got %d", exitCode)
	}
	if elapsed := time.Since(start); elapsed < 300*time.Millisecond {
		t.Errorf("expected retry backoff (>=300ms) for signal kills, took only %s", elapsed)
	}
}
