package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"go-agent-harness/internal/cron"
)

func TestMainDelegatesToRunAndExit(t *testing.T) {
	origRun := runCommand
	origExit := exitFunc
	origArgs := osArgs
	defer func() {
		runCommand = origRun
		exitFunc = origExit
		osArgs = origArgs
	}()

	runCommand = func(args []string) int {
		if len(args) != 1 || args[0] != "health" {
			t.Fatalf("unexpected args: %v", args)
		}
		return 0
	}
	exitCode := -1
	exitFunc = func(code int) { exitCode = code }
	osArgs = []string{"cronctl", "health"}

	main()

	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
}

func TestRunNoArgs(t *testing.T) {
	origStderr := stderr
	defer func() { stderr = origStderr }()
	stderr = &bytes.Buffer{}

	code := run(nil)
	if code != 1 {
		t.Fatalf("expected 1, got %d", code)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	origStderr := stderr
	defer func() { stderr = origStderr }()
	stderr = &bytes.Buffer{}

	code := run([]string{"badcmd"})
	if code != 1 {
		t.Fatalf("expected 1, got %d", code)
	}
}

func newMockServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/healthz":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case r.URL.Path == "/v1/jobs" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jobs": []cron.Job{{
					ID: "j1", Name: "test-job", Schedule: "* * * * *",
					Status: cron.StatusActive, ExecType: cron.ExecTypeShell,
				}},
			})

		case r.URL.Path == "/v1/jobs" && r.Method == http.MethodPost:
			var req cron.CreateJobRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(cron.Job{
				ID: "new-id", Name: req.Name, Schedule: req.Schedule,
				ExecType: req.ExecType, Status: cron.StatusActive, TimeoutSec: 30,
			})

		case r.Method == http.MethodGet && len(r.URL.Path) > len("/v1/jobs/"):
			path := r.URL.Path[len("/v1/jobs/"):]
			if idx := len(path) - len("/history"); idx > 0 && path[idx:] == "/history" {
				_ = json.NewEncoder(w).Encode(map[string]any{"executions": []cron.Execution{}})
				return
			}
			_ = json.NewEncoder(w).Encode(cron.Job{
				ID: "j1", Name: "test-job", Schedule: "* * * * *",
				Status: cron.StatusActive, ExecType: cron.ExecTypeShell,
			})

		case r.Method == http.MethodPatch:
			_ = json.NewEncoder(w).Encode(cron.Job{
				ID: "j1", Name: "test-job", Status: cron.StatusPaused,
			})

		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestCmdHealth(t *testing.T) {
	srv := newMockServer(t)
	defer srv.Close()
	t.Setenv("CRONSD_URL", srv.URL)

	origStdout := stdout
	origStderr := stderr
	defer func() {
		stdout = origStdout
		stderr = origStderr
	}()
	var out bytes.Buffer
	stdout = &out
	stderr = &bytes.Buffer{}

	code := run([]string{"health"})
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
	if out.Len() == 0 {
		t.Fatalf("expected output")
	}
}

func TestCmdList(t *testing.T) {
	srv := newMockServer(t)
	defer srv.Close()
	t.Setenv("CRONSD_URL", srv.URL)

	origStdout := stdout
	origStderr := stderr
	defer func() {
		stdout = origStdout
		stderr = origStderr
	}()
	var out bytes.Buffer
	stdout = &out
	stderr = &bytes.Buffer{}

	code := run([]string{"list"})
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
	if out.Len() == 0 {
		t.Fatalf("expected output")
	}
}

func TestCmdCreate(t *testing.T) {
	srv := newMockServer(t)
	defer srv.Close()
	t.Setenv("CRONSD_URL", srv.URL)

	origStdout := stdout
	origStderr := stderr
	defer func() {
		stdout = origStdout
		stderr = origStderr
	}()
	var out bytes.Buffer
	stdout = &out
	stderr = &bytes.Buffer{}

	code := run([]string{"create", "--name", "my-job", "--schedule", "* * * * *", "--command", "echo hi"})
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
	if out.Len() == 0 {
		t.Fatalf("expected output")
	}
}

func TestCmdCreateMissingFlags(t *testing.T) {
	srv := newMockServer(t)
	defer srv.Close()
	t.Setenv("CRONSD_URL", srv.URL)

	origStdout := stdout
	origStderr := stderr
	defer func() {
		stdout = origStdout
		stderr = origStderr
	}()
	stdout = &bytes.Buffer{}
	stderr = &bytes.Buffer{}

	code := run([]string{"create"})
	if code != 1 {
		t.Fatalf("expected 1, got %d", code)
	}
}

func TestCmdGet(t *testing.T) {
	srv := newMockServer(t)
	defer srv.Close()
	t.Setenv("CRONSD_URL", srv.URL)

	origStdout := stdout
	origStderr := stderr
	defer func() {
		stdout = origStdout
		stderr = origStderr
	}()
	var out bytes.Buffer
	stdout = &out
	stderr = &bytes.Buffer{}

	code := run([]string{"get", "j1"})
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
}

func TestCmdGetNoArgs(t *testing.T) {
	origStderr := stderr
	defer func() { stderr = origStderr }()
	stderr = &bytes.Buffer{}

	code := run([]string{"get"})
	if code != 1 {
		t.Fatalf("expected 1, got %d", code)
	}
}

func TestCmdDelete(t *testing.T) {
	srv := newMockServer(t)
	defer srv.Close()
	t.Setenv("CRONSD_URL", srv.URL)

	origStdout := stdout
	origStderr := stderr
	defer func() {
		stdout = origStdout
		stderr = origStderr
	}()
	var out bytes.Buffer
	stdout = &out
	stderr = &bytes.Buffer{}

	code := run([]string{"delete", "j1"})
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
}

func TestCmdDeleteNoArgs(t *testing.T) {
	origStderr := stderr
	defer func() { stderr = origStderr }()
	stderr = &bytes.Buffer{}

	code := run([]string{"delete"})
	if code != 1 {
		t.Fatalf("expected 1, got %d", code)
	}
}

func TestCmdPause(t *testing.T) {
	srv := newMockServer(t)
	defer srv.Close()
	t.Setenv("CRONSD_URL", srv.URL)

	origStdout := stdout
	origStderr := stderr
	defer func() {
		stdout = origStdout
		stderr = origStderr
	}()
	var out bytes.Buffer
	stdout = &out
	stderr = &bytes.Buffer{}

	code := run([]string{"pause", "j1"})
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
}

func TestCmdPauseNoArgs(t *testing.T) {
	origStderr := stderr
	defer func() { stderr = origStderr }()
	stderr = &bytes.Buffer{}

	code := run([]string{"pause"})
	if code != 1 {
		t.Fatalf("expected 1, got %d", code)
	}
}

func TestCmdResume(t *testing.T) {
	srv := newMockServer(t)
	defer srv.Close()
	t.Setenv("CRONSD_URL", srv.URL)

	origStdout := stdout
	origStderr := stderr
	defer func() {
		stdout = origStdout
		stderr = origStderr
	}()
	var out bytes.Buffer
	stdout = &out
	stderr = &bytes.Buffer{}

	code := run([]string{"resume", "j1"})
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
}

func TestCmdResumeNoArgs(t *testing.T) {
	origStderr := stderr
	defer func() { stderr = origStderr }()
	stderr = &bytes.Buffer{}

	code := run([]string{"resume"})
	if code != 1 {
		t.Fatalf("expected 1, got %d", code)
	}
}

func TestCmdHistory(t *testing.T) {
	srv := newMockServer(t)
	defer srv.Close()
	t.Setenv("CRONSD_URL", srv.URL)

	origStdout := stdout
	origStderr := stderr
	defer func() {
		stdout = origStdout
		stderr = origStderr
	}()
	var out bytes.Buffer
	stdout = &out
	stderr = &bytes.Buffer{}

	code := run([]string{"history", "j1"})
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
}

func TestCmdHistoryNoArgs(t *testing.T) {
	origStderr := stderr
	defer func() { stderr = origStderr }()
	stderr = &bytes.Buffer{}

	code := run([]string{"history"})
	if code != 1 {
		t.Fatalf("expected 1, got %d", code)
	}
}

func TestCLI_CreateConnectionRefused(t *testing.T) {
	// Point to an unreachable address.
	t.Setenv("CRONSD_URL", "http://127.0.0.1:1")

	origStdout := stdout
	origStderr := stderr
	defer func() {
		stdout = origStdout
		stderr = origStderr
	}()
	stdout = &bytes.Buffer{}
	var errBuf bytes.Buffer
	stderr = &errBuf

	code := run([]string{"create", "--name", "test", "--schedule", "* * * * *", "--command", "echo hi"})
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if errBuf.Len() == 0 {
		t.Fatal("expected error message on stderr")
	}
}

func TestCLI_ListConnectionRefused(t *testing.T) {
	// Point to an unreachable address.
	t.Setenv("CRONSD_URL", "http://127.0.0.1:1")

	origStdout := stdout
	origStderr := stderr
	defer func() {
		stdout = origStdout
		stderr = origStderr
	}()
	stdout = &bytes.Buffer{}
	var errBuf bytes.Buffer
	stderr = &errBuf

	code := run([]string{"list"})
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if errBuf.Len() == 0 {
		t.Fatal("expected error message on stderr")
	}
}

func TestGetBaseURL(t *testing.T) {
	origVal := os.Getenv("CRONSD_URL")
	defer func() {
		if origVal != "" {
			os.Setenv("CRONSD_URL", origVal)
		} else {
			os.Unsetenv("CRONSD_URL")
		}
	}()

	os.Unsetenv("CRONSD_URL")
	if got := getBaseURL(); got != "http://localhost:9090" {
		t.Fatalf("expected default, got %q", got)
	}

	os.Setenv("CRONSD_URL", "http://custom:1234")
	if got := getBaseURL(); got != "http://custom:1234" {
		t.Fatalf("expected custom, got %q", got)
	}
}

func TestFormatTime(t *testing.T) {
	if got := formatTime(time.Time{}); got != "-" {
		t.Fatalf("expected -, got %q", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("expected short, got %q", got)
	}
	if got := truncate("very-long-string", 10); got != "very-lo..." {
		t.Fatalf("expected truncation, got %q", got)
	}
}
