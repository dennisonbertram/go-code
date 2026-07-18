//go:build unix

package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// readPidFile polls for a pid file written by a spawned grandchild and
// returns the parsed pid, failing the test if it does not appear in time.
func readPidFile(t *testing.T, path string, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if convErr == nil && pid > 0 {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid file %q not readable within %v", path, timeout)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// processGone reports whether pid is dead. ESRCH from kill(pid, 0) is the
// primary signal. On Linux a killed process can linger as a zombie when its
// reaper chain is broken (e.g. in a container whose PID 1 does not reap);
// kill(pid, 0) succeeds for zombies, so the /proc state is consulted too —
// a zombie is dead for the purposes of these tests: the group kill already
// did its job.
func processGone(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}
	if runtime.GOOS != "linux" {
		return false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true // no /proc entry: process is gone
	}
	if idx := strings.LastIndexByte(string(data), ')'); idx >= 0 && idx+2 < len(data) {
		return data[idx+2] == 'Z'
	}
	return false
}

// assertProcessEventuallyGone polls until the process is dead (ESRCH or, on
// Linux, zombie state), proving the process (and not just its parent shell)
// was really killed.
func assertProcessEventuallyGone(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if processGone(pid) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %d still alive after %v", pid, timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestRunForegroundTimeoutKillsProcessGroup guards #786: on timeout the
// entire process group (bash plus backgrounded grandchildren) must be killed.
// Without a group kill, the grandchild survives, holds the stdout/stderr
// pipes open, and cmd.Wait blocks until it exits (~10s here instead of ~1s).
func TestRunForegroundTimeoutKillsProcessGroup(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	pidFile := filepath.Join(workspace, "grandchild.pid")

	mgr := NewJobManager(workspace, nil)

	command := fmt.Sprintf("sleep 10 & echo $! > %q; wait", pidFile)
	start := time.Now()
	result, err := mgr.runForeground(context.Background(), command, 1, "")
	if err != nil {
		t.Fatalf("runForeground returned error: %v", err)
	}
	elapsed := time.Since(start)

	if timedOut, _ := result["timed_out"].(bool); !timedOut {
		t.Errorf("expected timed_out=true, got %v", result["timed_out"])
	}
	if elapsed >= 5*time.Second {
		t.Errorf("runForeground took %v; timeout should kill the process group and return promptly (<5s)", elapsed)
	}

	pid := readPidFile(t, pidFile, 2*time.Second)
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }() // no-op once the fix lands; cleans up pre-fix orphans
	assertProcessEventuallyGone(t, pid, 3*time.Second)
}

// TestJobKillKillsBackgroundJobGroup guards #786: job_kill must route to a
// process-group kill so backgrounded grandchildren of the job's shell die too.
func TestJobKillKillsBackgroundJobGroup(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	pidFile := filepath.Join(workspace, "grandchild.pid")

	mgr := NewJobManager(workspace, nil)
	defer func() { _ = mgr.Shutdown(context.Background()) }()

	command := fmt.Sprintf("sleep 30 & echo $! > %q; wait", pidFile)
	result, err := mgr.runBackground(context.Background(), command, 60, "")
	if err != nil {
		t.Fatalf("runBackground returned error: %v", err)
	}
	shellID, _ := result["shell_id"].(string)
	if shellID == "" {
		t.Fatal("expected shell_id in runBackground result")
	}

	pid := readPidFile(t, pidFile, 2*time.Second)
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }()

	if _, err := mgr.kill(shellID); err != nil {
		t.Fatalf("kill returned error: %v", err)
	}

	// cancel -> watchCtx -> Cancel is asynchronous, so poll for the grandchild
	// to disappear rather than asserting immediately.
	assertProcessEventuallyGone(t, pid, 3*time.Second)
}

// TestRunCommandOnceTimeoutKillsProcessGroup guards #786 for the shared
// single-shot exec path used by non-bash tools.
func TestRunCommandOnceTimeoutKillsProcessGroup(t *testing.T) {
	t.Parallel()
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")

	start := time.Now()
	_, _, timedOut, err := runCommandOnce(context.Background(), time.Second, "bash", "-c",
		fmt.Sprintf("sleep 10 & echo $! > %q; wait", pidFile))
	elapsed := time.Since(start)

	if !timedOut {
		t.Error("expected timedOut=true")
	}
	if err == nil {
		t.Error("expected non-nil error for a process killed on timeout")
	}
	if elapsed >= 5*time.Second {
		t.Errorf("runCommandOnce took %v; timeout should kill the process group and return promptly (<5s)", elapsed)
	}

	pid := readPidFile(t, pidFile, 2*time.Second)
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }()
	assertProcessEventuallyGone(t, pid, 3*time.Second)
}
