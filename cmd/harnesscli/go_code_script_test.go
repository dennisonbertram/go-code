package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestGoCodeScriptRoutesDailyCommands(t *testing.T) {
	scriptPath, err := filepath.Abs(filepath.Join("..", "..", "scripts", "go-code.sh"))
	if err != nil {
		t.Fatalf("resolve go-code script path: %v", err)
	}

	binDir := t.TempDir()
	recordFile := filepath.Join(t.TempDir(), "harnesscli.args")
	writeExecutable(t, filepath.Join(binDir, "curl"), "#!/usr/bin/env bash\nexit 0\n")
	writeExecutable(t, filepath.Join(binDir, "harnessd"), "#!/usr/bin/env bash\nexit 0\n")
	writeExecutable(t, filepath.Join(binDir, "harnesscli"), "#!/usr/bin/env bash\nprintf '%s\\n' \"$*\" >> \"$RECORD_FILE\"\n")

	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "runs", args: []string{"runs"}, want: "list -base-url http://127.0.0.1:19080"},
		{name: "show", args: []string{"show", "run_123"}, want: "status -base-url http://127.0.0.1:19080 run_123"},
		{name: "continue", args: []string{"continue", "run_123", "follow up"}, want: "continue -base-url http://127.0.0.1:19080 run_123 follow up"},
		{name: "search", args: []string{"search", "terminal"}, want: "search -base-url http://127.0.0.1:19080 terminal"},
		{name: "improve", args: []string{"improve", "--dry-run", "--target", "internal/server"}, want: "improve -base-url http://127.0.0.1:19080 --dry-run --target internal/server"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(recordFile, nil, 0o600); err != nil {
				t.Fatalf("reset record file: %v", err)
			}
			cmd := exec.Command("bash", append([]string{scriptPath}, tc.args...)...)
			cmd.Env = append(os.Environ(),
				"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"HARNESS_ADDR=:19080",
				"RECORD_FILE="+recordFile,
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go-code %v failed: %v\n%s", tc.args, err, out)
			}
			gotRaw, err := os.ReadFile(recordFile)
			if err != nil {
				t.Fatalf("read record file: %v", err)
			}
			got := strings.TrimSpace(string(gotRaw))
			if got != tc.want {
				t.Fatalf("harnesscli args = %q, want %q\nscript output:\n%s", got, tc.want, out)
			}
		})
	}
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}

// TestGoCodeScriptPropagatesHarnessCLIExitCode pins the wrapper's exit-code
// contract (website/docs/reference/exit-codes.md): the harnesscli invocation
// is the last command of main for both prompt and cli modes, so its exit
// status must surface unchanged through `go-code` — including when the
// wrapper started the server itself and the stop_server EXIT trap runs.
func TestGoCodeScriptPropagatesHarnessCLIExitCode(t *testing.T) {
	scriptPath, err := filepath.Abs(filepath.Join("..", "..", "scripts", "go-code.sh"))
	if err != nil {
		t.Fatalf("resolve go-code script path: %v", err)
	}

	binDir := t.TempDir()
	// curl: fail the first health check when CURL_FAIL_FIRST=1 (forces the
	// wrapper down the start-server path so the EXIT trap is armed), then
	// succeed. A per-subtest counter file tracks call count.
	writeExecutable(t, filepath.Join(binDir, "curl"), "#!/usr/bin/env bash\nf=\"$CURL_COUNT_FILE\"\nn=0\nif [ -f \"$f\" ]; then n=$(cat \"$f\"); fi\nn=$((n+1))\necho \"$n\" > \"$f\"\nif [ \"${CURL_FAIL_FIRST:-0}\" = \"1\" ] && [ \"$n\" -eq 1 ]; then exit 1; fi\nexit 0\n")
	// harnessd: never actually contacted beyond being started in the
	// background (it exits immediately; the health check is stubbed).
	writeExecutable(t, filepath.Join(binDir, "harnessd"), "#!/usr/bin/env bash\nexit 0\n")
	// harnesscli: record that it ran and exit with the injected code.
	writeExecutable(t, filepath.Join(binDir, "harnesscli"), "#!/usr/bin/env bash\nprintf 'called\\n' >> \"$RECORD_FILE\"\nexit \"${STUB_EXIT_CODE:-0}\"\n")

	cases := []struct {
		name         string
		args         []string
		stubExitCode int
		startServer  bool // CURL_FAIL_FIRST=1 → wrapper starts harnessd and arms the EXIT trap
	}{
		{name: "prompt mode with pre-existing server", args: []string{"hello world"}, stubExitCode: 2},
		{name: "prompt mode with wrapper-started server (trap armed)", args: []string{"hello world"}, stubExitCode: 2, startServer: true},
		{name: "prompt mode cancelled run with wrapper-started server", args: []string{"hello world"}, stubExitCode: 6, startServer: true},
		{name: "prompt mode blocked run with wrapper-started server", args: []string{"hello world"}, stubExitCode: 3, startServer: true},
		{name: "cli mode with pre-existing server", args: []string{"runs"}, stubExitCode: 2},
		{name: "cli mode with wrapper-started server (trap armed)", args: []string{"runs"}, stubExitCode: 6, startServer: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			recordFile := filepath.Join(tmp, "harnesscli.called")
			countFile := filepath.Join(tmp, "curl.count")
			failFirst := "0"
			if tc.startServer {
				failFirst = "1"
			}
			cmd := exec.Command("bash", append([]string{scriptPath}, tc.args...)...)
			cmd.Env = append(os.Environ(),
				"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"HARNESS_ADDR=:19181",
				"RECORD_FILE="+recordFile,
				"CURL_COUNT_FILE="+countFile,
				"CURL_FAIL_FIRST="+failFirst,
				fmt.Sprintf("STUB_EXIT_CODE=%d", tc.stubExitCode),
			)
			out, runErr := cmd.CombinedOutput()

			if tc.stubExitCode == 0 && runErr != nil {
				t.Fatalf("go-code %v: unexpected failure: %v\n%s", tc.args, runErr, out)
			}
			gotExit := 0
			if runErr != nil {
				var exitErr *exec.ExitError
				if !errors.As(runErr, &exitErr) {
					t.Fatalf("go-code %v: %v (not an exit-code error)\n%s", tc.args, runErr, out)
				}
				gotExit = exitErr.ExitCode()
			}
			if gotExit != tc.stubExitCode {
				t.Fatalf("go-code %v exit code = %d, want %d (harnesscli exit code must propagate unchanged)\n%s", tc.args, gotExit, tc.stubExitCode, out)
			}
			raw, err := os.ReadFile(recordFile)
			if err != nil || !strings.Contains(string(raw), "called") {
				t.Fatalf("harnesscli stub was not invoked (record=%q, err=%v) — the test proves nothing\n%s", raw, err, out)
			}
		})
	}
}

// TestGoCodeScriptStartsHarnessdOnLoopback pins the wrapper's bind contract
// (issue #1411): a harnessd the wrapper starts for its own use must listen on
// loopback only.
//
// The wrapper's client base URL is always http://127.0.0.1:${port}, so a wider
// bind has no consumer — it only exposes an unauthenticated agent-execution
// service to the local network. cmd/harnessd/bind_guard.go refuses exactly that
// address when no auth is configured, which killed `go-code` at startup on any
// machine without an API key store.
func TestGoCodeScriptStartsHarnessdOnLoopback(t *testing.T) {
	scriptPath, err := filepath.Abs(filepath.Join("..", "..", "scripts", "go-code.sh"))
	if err != nil {
		t.Fatalf("resolve go-code script path: %v", err)
	}

	tmp := t.TempDir()
	binDir := t.TempDir()
	addrFile := filepath.Join(tmp, "harnessd.addr")
	recordFile := filepath.Join(tmp, "harnesscli.called")
	countFile := filepath.Join(tmp, "curl.count")

	// curl: fail the first health check so the wrapper takes the start_server
	// path, then succeed so it proceeds to harnesscli.
	writeExecutable(t, filepath.Join(binDir, "curl"), "#!/usr/bin/env bash\nf=\"$CURL_COUNT_FILE\"\nn=0\nif [ -f \"$f\" ]; then n=$(cat \"$f\"); fi\nn=$((n+1))\necho \"$n\" > \"$f\"\nif [ \"$n\" -eq 1 ]; then exit 1; fi\nexit 0\n")
	// harnessd: record the bind address it was handed, then exit.
	writeExecutable(t, filepath.Join(binDir, "harnessd"), "#!/usr/bin/env bash\nprintf '%s' \"$HARNESS_ADDR\" > \"$ADDR_FILE\"\nexit 0\n")
	writeExecutable(t, filepath.Join(binDir, "harnesscli"), "#!/usr/bin/env bash\nprintf 'called\\n' >> \"$RECORD_FILE\"\nexit 0\n")

	cmd := exec.Command("bash", scriptPath, "runs")
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HARNESS_ADDR=:19282",
		"ADDR_FILE="+addrFile,
		"RECORD_FILE="+recordFile,
		"CURL_COUNT_FILE="+countFile,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go-code runs failed: %v\n%s", err, out)
	}

	gotRaw, err := os.ReadFile(addrFile)
	if err != nil {
		t.Fatalf("read recorded harnessd address: %v\nscript output:\n%s", err, out)
	}
	got := strings.TrimSpace(string(gotRaw))

	// The port from HARNESS_ADDR must still be honored, so a fix that hardcodes
	// 127.0.0.1:8080 fails here too.
	const want = "127.0.0.1:19282"
	if got != want {
		t.Fatalf("harnessd bind address = %q, want %q\n"+
			"a wildcard bind is refused by cmd/harnessd/bind_guard.go when no auth is configured\nscript output:\n%s",
			got, want, out)
	}
}

// TestGoCodeScriptEmitsNoAnsiWhenNotATty guards the color contract of issue
// #1413: styling is an enhancement for interactive terminals only.
//
// exec.Command gives the script no controlling terminal, so this is the same
// condition as `go-code runs | cat`. It cannot be red before the feature exists
// — it is a regression guard against a later change that colors unconditionally
// and corrupts piped or captured output.
func TestGoCodeScriptEmitsNoAnsiWhenNotATty(t *testing.T) {
	scriptPath, err := filepath.Abs(filepath.Join("..", "..", "scripts", "go-code.sh"))
	if err != nil {
		t.Fatalf("resolve go-code script path: %v", err)
	}

	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "curl"), "#!/usr/bin/env bash\nexit 0\n")
	writeExecutable(t, filepath.Join(binDir, "harnessd"), "#!/usr/bin/env bash\nexit 0\n")
	writeExecutable(t, filepath.Join(binDir, "harnesscli"), "#!/usr/bin/env bash\nexit 0\n")

	for _, tc := range []struct {
		name string
		env  []string
	}{
		{name: "no tty", env: nil},
		{name: "NO_COLOR set", env: []string{"NO_COLOR=1"}},
		{name: "dumb terminal", env: []string{"TERM=dumb"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("bash", scriptPath, "runs")
			cmd.Env = append(os.Environ(),
				"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"HARNESS_ADDR=:19383",
			)
			cmd.Env = append(cmd.Env, tc.env...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go-code runs failed: %v\n%s", err, out)
			}
			if bytes.Contains(out, []byte("\x1b[")) {
				t.Fatalf("output contains ANSI escape sequences with no terminal attached:\n%q", out)
			}
		})
	}
}

// TestGoCodeScriptSurfacesHarnessdLogOnStartupFailure pins the other half of
// issue #1413: the wrapper captures harnessd's output to a log file so a clean
// start is not buried in boot noise (and so no daemon line can scribble into the
// TUI after handoff) — but a failed start must still show the operator why the
// daemon died. Capturing without surfacing would trade noise for silence.
func TestGoCodeScriptSurfacesHarnessdLogOnStartupFailure(t *testing.T) {
	scriptPath, err := filepath.Abs(filepath.Join("..", "..", "scripts", "go-code.sh"))
	if err != nil {
		t.Fatalf("resolve go-code script path: %v", err)
	}

	binDir := t.TempDir()
	// curl: the health check never succeeds, so the wrapper must give up.
	writeExecutable(t, filepath.Join(binDir, "curl"), "#!/usr/bin/env bash\nexit 1\n")
	// harnessd: emit a boot line and a fatal line, then die — the shape of the
	// real bind-guard and workspace-lock failures.
	writeExecutable(t, filepath.Join(binDir, "harnessd"), "#!/usr/bin/env bash\necho 'loaded model catalog with 15 providers'\necho 'fatal: refusing to start: sentinel failure reason'\nexit 1\n")
	writeExecutable(t, filepath.Join(binDir, "harnesscli"), "#!/usr/bin/env bash\nexit 0\n")

	cmd := exec.Command("bash", scriptPath, "runs")
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HARNESS_ADDR=:19384",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected go-code to fail when harnessd never becomes healthy\n%s", out)
	}

	if !bytes.Contains(out, []byte("fatal: refusing to start: sentinel failure reason")) {
		t.Fatalf("startup failure did not surface harnessd's own reason for dying;\n"+
			"the daemon log was captured but never shown, which hides the cause:\n%s", out)
	}
	if !bytes.Contains(out, []byte("harnessd.")) || !bytes.Contains(out, []byte(".log")) {
		t.Fatalf("startup failure did not report the harnessd log path:\n%s", out)
	}
}

// TestGoCodeScriptStopsHarnessdOnInterrupt pins the cleanup contract of issue
// #1416: a harnessd the wrapper started must not outlive the wrapper, however
// the wrapper exits — including Ctrl+C, which is the way users actually abort.
//
// An orphan holds the workspace lock (internal/harness/tools/delayed_callback_store.go),
// so the next go-code in that project dies with "callback workspace is already
// owned" — a message that names neither the cause nor the remedy. This test
// reproduces the orphan itself rather than that downstream symptom.
func TestGoCodeScriptStopsHarnessdOnInterrupt(t *testing.T) {
	scriptPath, err := filepath.Abs(filepath.Join("..", "..", "scripts", "go-code.sh"))
	if err != nil {
		t.Fatalf("resolve go-code script path: %v", err)
	}

	for _, tc := range []struct {
		name string
		// startedByWrapper false simulates a daemon the user already had
		// running: the health check succeeds immediately, so the wrapper
		// never starts one and must never kill it.
		startedByWrapper bool
	}{
		{name: "wrapper-started daemon is stopped", startedByWrapper: true},
		{name: "pre-existing daemon is left alone", startedByWrapper: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			binDir := t.TempDir()
			pidFile := filepath.Join(tmp, "harnessd.pid")
			countFile := filepath.Join(tmp, "curl.count")

			failFirst := "0"
			if tc.startedByWrapper {
				failFirst = "1"
			}
			writeExecutable(t, filepath.Join(binDir, "curl"), "#!/usr/bin/env bash\nf=\"$CURL_COUNT_FILE\"\nn=0\nif [ -f \"$f\" ]; then n=$(cat \"$f\"); fi\nn=$((n+1))\necho \"$n\" > \"$f\"\nif [ \"${CURL_FAIL_FIRST:-0}\" = \"1\" ] && [ \"$n\" -eq 1 ]; then exit 1; fi\nexit 0\n")
			// harnessd: record own PID, then outlive the wrapper unless stopped.
			// Ignores SIGINT, so it survives the process-group signal a real
			// Ctrl+C delivers. Only an explicit stop from the wrapper ends it —
			// which is exactly the orphan this test is about.
			// Ignores SIGINT so it survives the process-group signal a real
			// Ctrl+C delivers — POSIX inherits an ignored disposition across
			// exec, so the sleep ignores it too, and only an explicit stop from
			// the wrapper ends this. On SIGTERM it takes its child with it
			// rather than orphaning a stray sleep. Issue #1422.
			writeExecutable(t, filepath.Join(binDir, "harnessd"), "#!/usr/bin/env bash\ntrap '' INT\ntrap 'kill \"$child\" 2>/dev/null; exit 0' TERM\necho $$ > \"$DAEMON_PID_FILE\"\nsleep 300 &\nchild=$!\nwait \"$child\"\n")
			// harnesscli: keep the wrapper alive so it is still running when signalled.
			writeExecutable(t, filepath.Join(binDir, "harnesscli"), "#!/usr/bin/env bash\nsleep 300\n")

			cmd := exec.Command("bash", scriptPath, "runs")
			cmd.Env = append(os.Environ(),
				"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"HARNESS_ADDR=:19620",
				"DAEMON_PID_FILE="+pidFile,
				"CURL_COUNT_FILE="+countFile,
				"CURL_FAIL_FIRST="+failFirst,
			)
			// Own process group so the signal goes to the wrapper alone, not to
			// the whole test process group.
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := cmd.Start(); err != nil {
				t.Fatalf("start go-code: %v", err)
			}
			// Kill only. Wait has exactly one owner (the goroutine below);
			// os.Process.Wait is not safe to call twice on the same process,
			// and the two racing was a real defect regardless of whether it is
			// what fails on Linux CI. Issue #1422.
			defer func() { _ = cmd.Process.Kill() }()

			var daemonPID int
			if tc.startedByWrapper {
				daemonPID = waitForPIDFile(t, pidFile)
				// Control: the daemon must be alive before the signal, or a
				// passing test would prove nothing.
				if !processAlive(daemonPID) {
					t.Fatalf("stub harnessd (pid %d) was not running before the interrupt", daemonPID)
				}
			} else {
				// Give the wrapper time to reach harnesscli; it must not have
				// started a daemon at all.
				time.Sleep(2 * time.Second)
				if _, err := os.Stat(pidFile); err == nil {
					t.Fatal("wrapper started a daemon even though one was already healthy")
				}
				// Stand up an unrelated daemon-like process to prove it survives.
				sleeper := exec.Command("sleep", "300")
				if err := sleeper.Start(); err != nil {
					t.Fatalf("start stand-in daemon: %v", err)
				}
				defer func() { _ = sleeper.Process.Kill(); _, _ = sleeper.Process.Wait() }()
				daemonPID = sleeper.Process.Pid
			}

			// A real Ctrl+C goes to the foreground process group, not to bash
			// alone. Signalling only the wrapper would deadlock: bash defers a
			// trap until the foreground child exits, and that child sleeps.
			if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGINT); err != nil {
				t.Fatalf("signal wrapper process group: %v", err)
			}
			waitDone := make(chan struct{})
			go func() { _, _ = cmd.Process.Wait(); close(waitDone) }()
			select {
			case <-waitDone:
			case <-time.After(30 * time.Second):
				// This is a hang, not a slow exit: locally the wrapper exits in
				// ~0.2s, and it has never reproduced outside CI — not on macOS
				// idle or loaded, not on Linux, not on Linux under -race with
				// constrained CPU, and not with SIGINT ignored by the parent.
				//
				// Rather than time out opaquely again, capture what the process
				// tree actually looks like while it is stuck, so the next CI
				// failure carries evidence instead of only a duration.
				// Issue #1422.
				t.Fatalf("wrapper did not exit within 30s of SIGINT\n%s",
					describeStuckProcesses(cmd.Process.Pid))
			}

			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				if tc.startedByWrapper && !processAlive(daemonPID) {
					return // cleaned up as required
				}
				time.Sleep(100 * time.Millisecond)
			}

			if tc.startedByWrapper {
				t.Fatalf("harnessd (pid %d) still running after the wrapper was interrupted; "+
					"it will hold the workspace lock and break the next go-code", daemonPID)
			}
			if !processAlive(daemonPID) {
				t.Fatal("wrapper killed a daemon it did not start")
			}
		})
	}
}

// waitForPIDFile blocks until the stub daemon has recorded its PID.
func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw))); convErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("stub harnessd never recorded a pid at %s", path)
	return 0
}

// processAlive reports whether pid is still running. Signal 0 performs the
// permission and existence check without delivering anything.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// TestGoCodeScriptStopsHarnessdWhenOutputPipeCloses pins the other half of the
// cleanup contract in issue #1416, and the half that actually orphans daemons
// in practice: `go-code runs | head -5`, or piping into a pager the user quits.
//
// When the reader closes early the wrapper dies of SIGPIPE, and bash does not
// run an EXIT trap for a shell killed by a signal it has no handler for. The
// daemon is left holding the workspace lock, so the next go-code in that
// project fails with "callback workspace is already owned".
//
// Ctrl+C, by contrast, is already handled correctly — see
// TestGoCodeScriptStopsHarnessdOnInterrupt.
func TestGoCodeScriptStopsHarnessdWhenOutputPipeCloses(t *testing.T) {
	scriptPath, err := filepath.Abs(filepath.Join("..", "..", "scripts", "go-code.sh"))
	if err != nil {
		t.Fatalf("resolve go-code script path: %v", err)
	}

	tmp := t.TempDir()
	binDir := t.TempDir()
	pidFile := filepath.Join(tmp, "harnessd.pid")
	countFile := filepath.Join(tmp, "curl.count")

	writeExecutable(t, filepath.Join(binDir, "curl"), "#!/usr/bin/env bash\nf=\"$CURL_COUNT_FILE\"\nn=0\nif [ -f \"$f\" ]; then n=$(cat \"$f\"); fi\nn=$((n+1))\necho \"$n\" > \"$f\"\nif [ \"$n\" -eq 1 ]; then exit 1; fi\nexit 0\n")
	writeExecutable(t, filepath.Join(binDir, "harnessd"), "#!/usr/bin/env bash\necho $$ > \"$DAEMON_PID_FILE\"\nsleep 300\n")
	// Emit far more than the reader will consume, so the write lands on a
	// closed pipe — exactly what a real `runs` listing into `head` does.
	writeExecutable(t, filepath.Join(binDir, "harnesscli"), "#!/usr/bin/env bash\nfor i in $(seq 1 500); do echo \"run_$i completed\"; done\n")

	cmd := exec.Command("bash", "-c", scriptPath+" runs 2>&1 | head -5 >/dev/null")
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HARNESS_ADDR=:19640",
		"DAEMON_PID_FILE="+pidFile,
		"CURL_COUNT_FILE="+countFile,
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("pipeline failed: %v", err)
	}

	daemonPID := waitForPIDFile(t, pidFile)

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(daemonPID) {
			return // cleaned up as required
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("harnessd (pid %d) still running after the wrapper's output pipe closed; "+
		"it will hold the workspace lock and break the next go-code in this project", daemonPID)
}

// describeStuckProcesses renders the state of the wrapper and its descendants,
// for the failure message when the wrapper does not exit. Best-effort: it runs
// only on a path that is already failing, so any error here is reported as text
// rather than allowed to mask the original failure.
func describeStuckProcesses(wrapperPID int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "wrapper pid: %d\n", wrapperPID)

	// Full listing with parent and state, so the tree and each process's wait
	// state (S, D, Z, T) are both visible.
	out, err := exec.Command("ps", "-eo", "pid,ppid,stat,wchan:20,args").CombinedOutput()
	if err != nil {
		fmt.Fprintf(&b, "ps failed: %v\n", err)
		return b.String()
	}
	b.WriteString("relevant processes:\n")
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "harnessd") || strings.Contains(line, "harnesscli") ||
			strings.Contains(line, "go-code.sh") || strings.Contains(line, "sleep") ||
			strings.Contains(line, fmt.Sprintf(" %d ", wrapperPID)) {
			b.WriteString("  " + line + "\n")
		}
	}
	return b.String()
}
