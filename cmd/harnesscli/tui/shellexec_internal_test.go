package tui

// Unit tests for the shell-mode local executor (epic #811, slice 2):
// stdout/stderr capture, non-zero exit, timeout kill, bounded output,
// streaming deltas, and kill() interruption.

import (
	"strings"
	"testing"
	"time"
)

// collectShellMsgs drains ex.ch until the done message, returning all streamed
// chunks and the final done message.
func collectShellMsgs(t *testing.T, ex *shellExec) (chunks []string, done shellExecDoneMsg) {
	t.Helper()
	timeout := time.After(15 * time.Second)
	for {
		select {
		case msg := <-ex.ch:
			switch m := msg.(type) {
			case shellExecOutputMsg:
				chunks = append(chunks, m.Chunk)
			case shellExecDoneMsg:
				return chunks, m
			}
		case <-timeout:
			t.Fatal("timed out waiting for shell exec to finish")
		}
	}
}

func TestShellExec_CapturesStdout(t *testing.T) {
	ex, err := startShellExec("shell-1", "echo hello", 5*time.Second)
	if err != nil {
		t.Fatalf("startShellExec: %v", err)
	}
	_, done := collectShellMsgs(t, ex)
	if done.Err != nil {
		t.Fatalf("unexpected error: %v", done.Err)
	}
	if done.ExitCode != 0 {
		t.Errorf("exit code: got %d, want 0", done.ExitCode)
	}
	if !strings.Contains(done.Output, "hello") {
		t.Errorf("output must contain stdout, got %q", done.Output)
	}
}

func TestShellExec_CapturesStderr(t *testing.T) {
	ex, err := startShellExec("shell-1", "echo oops >&2", 5*time.Second)
	if err != nil {
		t.Fatalf("startShellExec: %v", err)
	}
	_, done := collectShellMsgs(t, ex)
	if !strings.Contains(done.Output, "oops") {
		t.Errorf("output must contain stderr, got %q", done.Output)
	}
}

func TestShellExec_NonZeroExit(t *testing.T) {
	ex, err := startShellExec("shell-1", "exit 3", 5*time.Second)
	if err != nil {
		t.Fatalf("startShellExec: %v", err)
	}
	_, done := collectShellMsgs(t, ex)
	if done.ExitCode != 3 {
		t.Errorf("exit code: got %d, want 3", done.ExitCode)
	}
}

func TestShellExec_TimeoutKillsProcess(t *testing.T) {
	start := time.Now()
	ex, err := startShellExec("shell-1", "sleep 30", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("startShellExec: %v", err)
	}
	_, done := collectShellMsgs(t, ex)
	elapsed := time.Since(start)
	if !done.TimedOut {
		t.Error("done must report TimedOut when the timeout kills the command")
	}
	if elapsed > 10*time.Second {
		t.Errorf("timeout kill took too long: %v (command must not run to completion)", elapsed)
	}
}

func TestShellExec_OutputBounded(t *testing.T) {
	// ~100KB of output — far above the 30KB cap.
	ex, err := startShellExec("shell-1", "yes a | head -c 100000", 15*time.Second)
	if err != nil {
		t.Fatalf("startShellExec: %v", err)
	}
	_, done := collectShellMsgs(t, ex)
	// The bounded buffer keeps head+tail within the cap plus a truncation marker.
	if len(done.Output) > shellExecMaxOutputBytes+256 {
		t.Errorf("output must be bounded near %d bytes, got %d", shellExecMaxOutputBytes, len(done.Output))
	}
	if !strings.Contains(done.Output, "truncated") {
		t.Errorf("over-cap output must carry a truncation marker, got %q", done.Output[:200])
	}
}

func TestShellExec_StreamsDeltasBeforeDone(t *testing.T) {
	ex, err := startShellExec("shell-1", "printf a; sleep 0.2; printf b", 5*time.Second)
	if err != nil {
		t.Fatalf("startShellExec: %v", err)
	}
	chunks, done := collectShellMsgs(t, ex)
	if len(chunks) == 0 {
		t.Fatal("expected at least one streamed delta before the done message")
	}
	joined := strings.Join(chunks, "")
	if joined != "ab" {
		t.Errorf("streamed deltas must reconstruct the output, got %q", joined)
	}
	if done.ExitCode != 0 {
		t.Errorf("exit code: got %d, want 0", done.ExitCode)
	}
}

func TestShellExec_KillInterrupts(t *testing.T) {
	ex, err := startShellExec("shell-1", "sleep 30", 30*time.Second)
	if err != nil {
		t.Fatalf("startShellExec: %v", err)
	}
	start := time.Now()
	ex.kill()
	_, done := collectShellMsgs(t, ex)
	elapsed := time.Since(start)
	if !done.Interrupted {
		t.Error("done must report Interrupted after kill()")
	}
	if elapsed > 10*time.Second {
		t.Errorf("kill took too long: %v", elapsed)
	}
}

func TestShellExec_DetachStopsDeltasButBuffers(t *testing.T) {
	ex, err := startShellExec("shell-1", "printf a; sleep 0.3; printf b", 5*time.Second)
	if err != nil {
		t.Fatalf("startShellExec: %v", err)
	}
	// Wait for the first delta ("a") before detaching.
	select {
	case msg := <-ex.ch:
		out, ok := msg.(shellExecOutputMsg)
		if !ok {
			t.Fatalf("expected a streamed delta first, got %T", msg)
		}
		if !strings.Contains(out.Chunk, "a") {
			t.Fatalf("first delta must carry the initial output, got %q", out.Chunk)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no streamed delta before detach")
	}

	ex.detach()

	chunks, done := collectShellMsgs(t, ex)
	for _, c := range chunks {
		if strings.Contains(c, "b") {
			t.Errorf("no deltas must be emitted after detach, got %q", c)
		}
	}
	// The done message still carries the complete buffered output.
	if done.Output != "ab" {
		t.Errorf("detached command must buffer output to completion, got %q", done.Output)
	}
	if done.ExitCode != 0 {
		t.Errorf("exit code: got %d, want 0", done.ExitCode)
	}
}
