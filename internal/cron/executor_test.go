package cron

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestShellExecutor_Success(t *testing.T) {
	executor := &ShellExecutor{}
	job := Job{
		ExecConfig: `{"command":"echo hello world"}`,
		TimeoutSec: 10,
	}

	output, err := executor.Execute(context.Background(), job)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.TrimSpace(output) != "hello world" {
		t.Fatalf("expected 'hello world', got %q", output)
	}
}

func TestShellExecutor_NonZeroExit(t *testing.T) {
	executor := &ShellExecutor{}
	job := Job{
		ExecConfig: `{"command":"exit 1"}`,
		TimeoutSec: 10,
	}

	_, err := executor.Execute(context.Background(), job)
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if !strings.Contains(err.Error(), "command failed") {
		t.Fatalf("expected 'command failed' error, got: %v", err)
	}
}

func TestShellExecutor_InvalidJSON(t *testing.T) {
	executor := &ShellExecutor{}
	job := Job{
		ExecConfig: `not json`,
		TimeoutSec: 10,
	}

	_, err := executor.Execute(context.Background(), job)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "parse execution config") {
		t.Fatalf("expected parse error, got: %v", err)
	}
}

func TestShellExecutor_EmptyCommand(t *testing.T) {
	executor := &ShellExecutor{}
	job := Job{
		ExecConfig: `{"command":""}`,
		TimeoutSec: 10,
	}

	_, err := executor.Execute(context.Background(), job)
	if err == nil {
		t.Fatal("expected error for empty command")
	}
	if !strings.Contains(err.Error(), "missing 'command' field") {
		t.Fatalf("expected missing command error, got: %v", err)
	}
}

func TestShellExecutor_Timeout(t *testing.T) {
	executor := &ShellExecutor{}
	job := Job{
		ExecConfig: `{"command":"sleep 10"}`,
		TimeoutSec: 1,
	}

	_, err := executor.Execute(context.Background(), job)
	if err == nil {
		t.Fatal("expected error for timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
}

func TestShellExecutor_CapturesStderr(t *testing.T) {
	executor := &ShellExecutor{}
	job := Job{
		ExecConfig: `{"command":"echo stderr_output >&2"}`,
		TimeoutSec: 10,
	}

	output, err := executor.Execute(context.Background(), job)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(output, "stderr_output") {
		t.Fatalf("expected stderr output, got %q", output)
	}
}

func TestShellExecutor_TruncatesOutput(t *testing.T) {
	executor := &ShellExecutor{}
	// Generate output larger than 4096 bytes.
	job := Job{
		ExecConfig: `{"command":"awk 'BEGIN { for (i = 0; i < 8192; i++) printf \"A\" }'"}`,
		TimeoutSec: 10,
	}

	output, err := executor.Execute(context.Background(), job)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(output) > maxOutputBytes {
		t.Fatalf("expected output truncated to %d bytes, got %d", maxOutputBytes, len(output))
	}
}

func TestShellExecutor_TruncationBoundary(t *testing.T) {
	executor := &ShellExecutor{}

	// Test 1: Output larger than 4096 bytes should be truncated to exactly 4096.
	job := Job{
		ExecConfig: `{"command":"awk 'BEGIN { for (i = 0; i < 8192; i++) printf \"B\" }'"}`,
		TimeoutSec: 10,
	}
	output, err := executor.Execute(context.Background(), job)
	if err != nil {
		t.Fatalf("Execute large output: %v", err)
	}
	if len(output) != maxOutputBytes {
		t.Fatalf("expected output truncated to exactly %d bytes, got %d", maxOutputBytes, len(output))
	}

	// Test 2: Output of exactly 4096 bytes should NOT be truncated.
	job2 := Job{
		ExecConfig: `{"command":"awk 'BEGIN { for (i = 0; i < 4096; i++) printf \"C\" }'"}`,
		TimeoutSec: 10,
	}
	output2, err := executor.Execute(context.Background(), job2)
	if err != nil {
		t.Fatalf("Execute exact boundary output: %v", err)
	}
	if len(output2) != maxOutputBytes {
		t.Fatalf("expected output of exactly %d bytes to not be truncated, got %d", maxOutputBytes, len(output2))
	}
}

func TestShellExecutor_DefaultTimeout(t *testing.T) {
	executor := &ShellExecutor{}
	job := Job{
		ExecConfig: `{"command":"echo fast"}`,
		TimeoutSec: 0, // should default to 30s
	}

	output, err := executor.Execute(context.Background(), job)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.TrimSpace(output) != "fast" {
		t.Fatalf("expected 'fast', got %q", output)
	}
}

// TestShellExecutor_TimeoutWithOrphanedChildHoldingPipes (BT-007, P2)
// reproduces BUG 5: ShellExecutor.Execute calls cmd.CombinedOutput() with
// no WaitDelay set. When the context deadline kills the direct child (the
// "sh" process) but a background grandchild it spawned still holds the
// inherited stdout/stderr pipes open, Wait() blocks until that grandchild
// exits and closes the pipes on its own — potentially forever (or, here,
// bounded only by the grandchild's own sleep), even though the job's
// configured timeout was 1 second.
//
// The shell command backgrounds a 30s sleep (`(sleep 30 &)`) that
// inherits the parent's stdout/stderr, then the parent itself sleeps far
// longer than the 1s job timeout. Before the fix, Execute does not return
// until the orphaned "sleep 30" exits and releases the pipe — this test
// bounds its wait at 10s (well under 30s) so a regression fails fast
// instead of hanging the test suite for 30+ seconds.
func TestShellExecutor_TimeoutWithOrphanedChildHoldingPipes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell backgrounding construct")
	}

	executor := &ShellExecutor{}
	job := Job{
		ExecConfig: `{"command":"(sleep 30 &) ; sleep 60"}`,
		TimeoutSec: 1,
	}

	type result struct {
		elapsed time.Duration
		err     error
	}
	done := make(chan result, 1)
	go func() {
		start := time.Now()
		_, execErr := executor.Execute(context.Background(), job)
		done <- result{elapsed: time.Since(start), err: execErr}
	}()

	select {
	case r := <-done:
		// cmd.WaitDelay (5s, once set) starts counting from the 1s
		// context deadline, so total elapsed should land around 6s; allow
		// generous headroom for CI/test-machine scheduling jitter while
		// staying far below the orphaned child's 30s sleep.
		if r.elapsed > 10*time.Second {
			t.Fatalf("Execute took %v after a 1s job timeout; expected it to return in roughly job-timeout+WaitDelay, not block on an orphaned child holding the output pipes", r.elapsed)
		}
		if r.err == nil {
			t.Fatal("expected an error for a command killed by its timeout")
		}
	case <-time.After(12 * time.Second):
		t.Fatal("Execute did not return within 12s of a 1s timeout: a background child holding the output pipes open is blocking Wait() indefinitely")
	}
}
