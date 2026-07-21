package e2e

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/fakeprovider"
)

// This file proves the headless exit-code contract
// (website/docs/reference/exit-codes.md, epic #823) end to end: the real
// harnesscli binary, built from source, streaming real HTTP+SSE from a real
// in-process harnessd, must exit 0 on run.completed, 2 on run.failed, and 6
// on run.cancelled. The assertions are on the process exit code itself — the
// same $? a shell script or CI job would branch on.

var (
	cliBuildOnce sync.Once
	cliPath      string
	cliBuildErr  error
)

// harnesscliPath builds the real harnesscli binary once per test binary run
// and returns its path. Building from source (rather than relying on an
// installed binary) keeps the suite hermetic and pinned to the current tree.
func harnesscliPath(t *testing.T) string {
	t.Helper()
	cliBuildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "harnesscli-e2e-*")
		if err != nil {
			cliBuildErr = err
			return
		}
		cliPath = filepath.Join(dir, "harnesscli")
		out, err := exec.Command("go", "build", "-o", cliPath, "go-agent-harness/cmd/harnesscli").CombinedOutput()
		if err != nil {
			cliBuildErr = fmt.Errorf("build harnesscli: %v\n%s", err, out)
		}
	})
	if cliBuildErr != nil {
		t.Fatalf("%v", cliBuildErr)
	}
	return cliPath
}

// cliResult captures the outcome of one harnesscli invocation.
type cliResult struct {
	exitCode int
	stdout   string
	stderr   string
}

// runHarnessCLI runs the real binary to completion and returns its process
// exit code. A non-zero exit is a normal outcome here, not a test failure.
func runHarnessCLI(t *testing.T, args ...string) cliResult {
	t.Helper()
	cmd := exec.Command(harnesscliPath(t), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := cliResult{stdout: stdout.String(), stderr: stderr.String()}
	if err == nil {
		return res
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("run harnesscli %v: %v (not an exit-code error)", args, err)
	}
	res.exitCode = exitErr.ExitCode()
	return res
}

// TestE2E_ExitCodeCompleted drives a one-shot run whose provider answers
// immediately and asserts the CLI process exits 0 (run.completed).
func TestE2E_ExitCodeCompleted(t *testing.T) {
	t.Parallel()

	provider := fakeprovider.New([]fakeprovider.Turn{{Content: "hello from the fake model"}})
	ts := newTestServer(t, provider, nil, nil)

	res := runHarnessCLI(t, "-base-url", ts.URL, "-prompt", "say hi")
	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, want 0 for run.completed (stderr=%s)", res.exitCode, res.stderr)
	}
}

// TestE2E_ExitCodeFailed drives a one-shot run whose provider errors on every
// turn and asserts the CLI process exits 2 (run.failed).
func TestE2E_ExitCodeFailed(t *testing.T) {
	t.Parallel()

	provider := fakeprovider.New(nil, fakeprovider.WithExhaustedBehavior(fakeprovider.ExhaustError))
	ts := newTestServer(t, provider, nil, nil)

	res := runHarnessCLI(t, "-base-url", ts.URL, "-prompt", "this will fail")
	if res.exitCode != 2 {
		t.Fatalf("exit code = %d, want 2 for run.failed (stderr=%s)", res.exitCode, res.stderr)
	}
}

// TestE2E_ExitCodeCancelled cancels an in-flight run exactly as an operator
// would (POST /v1/runs/{id}/cancel) while the real CLI streams it, and
// asserts the CLI process exits 6 (run.cancelled). The run ID is read from
// the CLI's own run_id= stdout contract line — the in-process test server has
// no run store, so the list route (GET /v1/runs) is not available here.
func TestE2E_ExitCodeCancelled(t *testing.T) {
	t.Parallel()

	provider := fakeprovider.New([]fakeprovider.Turn{{Hang: true}})
	ts := newTestServer(t, provider, nil, nil)

	cmd := exec.Command(harnesscliPath(t), "-base-url", ts.URL, "-prompt", "do something slow")
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start harnesscli: %v", err)
	}
	// The child must never outlive the test: while it runs it holds an SSE
	// connection that blocks httptest.Server.Close during cleanup.
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-waitDone:
		case <-time.After(5 * time.Second):
		}
	})

	// Read the run ID from the CLI's run_id= stdout line as it streams.
	runIDCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			if line := scanner.Text(); strings.HasPrefix(line, "run_id=") {
				runIDCh <- strings.TrimPrefix(line, "run_id=")
				return
			}
		}
	}()
	var runID string
	select {
	case runID = <-runIDCh:
	case <-time.After(10 * time.Second):
		t.Fatal("CLI never printed the run_id= line")
	}

	// Wait until the provider is actually inside its Hang turn so the cancel
	// lands mid-run rather than before the run starts.
	deadline := time.Now().Add(5 * time.Second)
	for provider.Calls() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("provider never entered the hanging turn")
		}
		time.Sleep(10 * time.Millisecond)
	}

	res, err := ts.Client().Post(ts.URL+"/v1/runs/"+runID+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatalf("POST cancel: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST cancel: expected 200, got %d", res.StatusCode)
	}

	select {
	case waitErr := <-waitDone:
		if waitErr == nil {
			t.Fatalf("exit code = 0, want 6 for run.cancelled")
		}
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			t.Fatalf("wait harnesscli: %v (not an exit-code error)", waitErr)
		}
		if exitErr.ExitCode() != 6 {
			t.Fatalf("exit code = %d, want 6 for run.cancelled (stderr=%s)", exitErr.ExitCode(), stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("CLI did not exit after the run was cancelled")
	}
}

// TestE2E_ExitCodeClientError drives the CLI against an unreachable server
// and asserts the conventional client-error exit 1 (unchanged behavior,
// pinned here so the transport-error path cannot drift into the run-outcome
// codes).
func TestE2E_ExitCodeClientError(t *testing.T) {
	t.Parallel()

	// Bind then immediately close a listener to obtain an address that is
	// guaranteed to refuse connections.
	ts := newTestServer(t, fakeprovider.New(nil), nil, nil)
	url := ts.URL
	ts.Close()

	res := runHarnessCLI(t, "-base-url", url, "-prompt", "no server listening")
	if res.exitCode != 1 {
		t.Fatalf("exit code = %d, want 1 for client/transport error (stderr=%s)", res.exitCode, res.stderr)
	}
	if !strings.Contains(res.stderr, "harnesscli") {
		t.Errorf("expected a client error message on stderr, got %q", res.stderr)
	}
}
