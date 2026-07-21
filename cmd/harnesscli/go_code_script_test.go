package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
