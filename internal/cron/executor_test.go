package cron

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

// TestShellExecutor_TimeoutKillsOrphanedChild_DoesNotLeaveItRunning
// (regression for BUG 5) covers a different angle than the red test: it
// doesn't just check that Execute returns promptly, it checks that the
// background grandchild is actually TERMINATED, not merely abandoned to
// keep running detached from the (now-exited) parent shell.
//
// The backgrounded child appends a line to a marker file every 200ms.
// If the process-group kill in Execute actually reaps it, writes stop
// shortly after Execute returns. If the child were only orphaned (the
// pre-fix behavior, modulo WaitDelay), it would keep writing for its
// full ~10s run, and this test would catch that by observing the marker
// file still growing well after Execute has returned.
func TestShellExecutor_TimeoutKillsOrphanedChild_DoesNotLeaveItRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell backgrounding construct")
	}

	executor := &ShellExecutor{}
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")

	shCmd := fmt.Sprintf(`(i=0; while [ $i -lt 50 ]; do echo x >> %q; sleep 0.2; i=$((i+1)); done &) ; sleep 60`, marker)
	job := Job{
		ExecConfig: fmt.Sprintf(`{"command":%q}`, shCmd),
		TimeoutSec: 1,
	}

	if _, err := executor.Execute(context.Background(), job); err == nil {
		t.Fatal("expected an error for a command killed by its timeout")
	}

	sizeAfterExecute := markerSize(t, marker)

	// Give a killed-but-still-writing process ample time to prove it's
	// still alive, while staying well under its ~10s natural run length.
	time.Sleep(3 * time.Second)

	sizeLater := markerSize(t, marker)
	if sizeLater > sizeAfterExecute {
		t.Fatalf("background child kept writing to the marker file after Execute returned (size %d -> %d after a 3s settle wait): it was orphaned rather than actually killed",
			sizeAfterExecute, sizeLater)
	}
}

func markerSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("stat marker file: %v", err)
	}
	return info.Size()
}
