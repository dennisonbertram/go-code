package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"go-agent-harness/internal/harness"
)

// The exit codes below are the public headless contract documented in
// website/docs/reference/exit-codes.md (epic #823). The literal values are
// the contract: changing them breaks scripting callers.

func TestExitCodeConstantsMatchContract(t *testing.T) {
	constants := []struct {
		name string
		got  int
		want int
	}{
		{"exitSuccess", exitSuccess, 0},
		{"exitClientError", exitClientError, 1},
		{"exitRunFailed", exitRunFailed, 2},
		{"exitBlocked", exitBlocked, 3},
		{"exitCancelled", exitCancelled, 6},
		{"exitInterrupted", exitInterrupted, 130},
	}
	for _, tc := range constants {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d (contract value)", tc.name, tc.got, tc.want)
		}
	}
}

func TestExitCodeForTerminalEvent(t *testing.T) {
	cases := []struct {
		name  string
		event harness.EventType
		want  int
	}{
		{"run.completed exits 0", harness.EventRunCompleted, 0},
		{"run.failed exits 2", harness.EventRunFailed, 2},
		{"run.cancelled exits 6", harness.EventRunCancelled, 6},
		// Defensive default: anything that is not a known terminal event maps
		// to the non-zero client-error code so a scripting caller never
		// mistakes an unrecognized outcome for success.
		{"non-terminal run.started exits 1", harness.EventRunStarted, 1},
		{"non-terminal max_turns.exhausted exits 1", harness.EventMaxTurnsExhausted, 1},
		{"non-terminal run.waiting_for_user exits 1", harness.EventRunWaitingForUser, 1},
		{"unknown event exits 1", harness.EventType("run.bogus"), 1},
		{"empty event exits 1", harness.EventType(""), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitCodeForTerminalEvent(tc.event); got != tc.want {
				t.Fatalf("exitCodeForTerminalEvent(%q) = %d, want %d", tc.event, got, tc.want)
			}
		})
	}
}

func TestIsBlockedEvent(t *testing.T) {
	cases := []struct {
		event harness.EventType
		want  bool
	}{
		{harness.EventRunWaitingForUser, true},
		{harness.EventToolApprovalRequired, true},
		{harness.EventPlanApprovalRequired, true},
		{harness.EventRunCompleted, false},
		{harness.EventRunFailed, false},
		{harness.EventRunCancelled, false},
		{harness.EventRunStarted, false},
		{harness.EventMaxTurnsExhausted, false},
		{harness.EventType(""), false},
	}
	for _, tc := range cases {
		if got := isBlockedEvent(tc.event); got != tc.want {
			t.Errorf("isBlockedEvent(%q) = %v, want %v", tc.event, got, tc.want)
		}
	}
}

func TestBlockedEventReason(t *testing.T) {
	cases := []struct {
		event harness.EventType
		want  string
	}{
		{harness.EventRunWaitingForUser, "waiting for user input"},
		{harness.EventToolApprovalRequired, "waiting for approval"},
		{harness.EventPlanApprovalRequired, "waiting for approval"},
	}
	for _, tc := range cases {
		if got := blockedEventReason(tc.event); got != tc.want {
			t.Errorf("blockedEventReason(%q) = %q, want %q", tc.event, got, tc.want)
		}
	}
}

// TestRunBlockedErrorMessage pins the sentinel's two contracts: Error() names
// the blocked event type (it surfaces verbatim if the error ever escapes the
// run()/runContinue() handlers), and errors.As detection — the mechanism
// run()/runContinue() use to map it to exitBlocked — keeps working through
// wrapping.
func TestRunBlockedErrorMessage(t *testing.T) {
	for _, tc := range blockedSignals {
		t.Run(tc.name, func(t *testing.T) {
			err := &runBlockedError{eventType: harness.EventType(tc.event)}
			msg := err.Error()
			for _, want := range []string{"run blocked", tc.event} {
				if !strings.Contains(msg, want) {
					t.Errorf("Error() = %q, want it to contain %q", msg, want)
				}
			}

			var detected *runBlockedError
			if !errors.As(fmt.Errorf("stream: %w", err), &detected) {
				t.Fatal("errors.As must detect runBlockedError through wrapping")
			}
			if detected.eventType != harness.EventType(tc.event) {
				t.Errorf("detected.eventType = %q, want %q", detected.eventType, tc.event)
			}
		})
	}
}

// stubStdinTerminal swaps the injectable terminal-detection double and
// restores it after the test, so blocked-path tests never need a real TTY.
func stubStdinTerminal(t *testing.T, isTerminal bool) {
	t.Helper()
	orig := stdinIsTerminal
	stdinIsTerminal = func() bool { return isTerminal }
	t.Cleanup(func() { stdinIsTerminal = orig })
}

// blockedSignals are the three events the contract treats as "the run cannot
// proceed without input a headless caller will never provide".
var blockedSignals = []struct {
	name       string
	event      string
	wantReason string
}{
	{"question-blocked", "run.waiting_for_user", "waiting for user input"},
	{"tool-approval-blocked", "tool.approval_required", "waiting for approval"},
	{"plan-approval-blocked", "plan.approval_required", "waiting for approval"},
}

// TestRunBlockedSignalExits3WhenStdinNonInteractive drives run() against a
// scripted SSE server that emits a blocked signal and then holds the stream
// open forever. With non-interactive stdin the CLI must stop streaming, exit
// 3, print a stderr message naming the run ID and the continue command, and
// leave the server-side run intact (no cancel POST).
func TestRunBlockedSignalExits3WhenStdinNonInteractive(t *testing.T) {
	for _, tc := range blockedSignals {
		t.Run(tc.name, func(t *testing.T) {
			stubStdinTerminal(t, false)

			var cancelRequested atomic.Bool
			mux := http.NewServeMux()
			mux.HandleFunc("/v1/runs", func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("unexpected method: %s", r.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = io.WriteString(w, `{"run_id":"run_abc","status":"queued"}`)
			})
			mux.HandleFunc("/v1/runs/run_abc/events", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
				_, _ = io.WriteString(w, "event: "+tc.event+"\n")
				_, _ = io.WriteString(w, "data: {\"id\":\"e1\",\"run_id\":\"run_abc\",\"type\":\""+tc.event+"\"}\n\n")
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				// Hold the stream open: no terminal event ever arrives. The
				// client must abort on its own; the handler returns once the
				// client closes the connection.
				<-r.Context().Done()
			})
			mux.HandleFunc("/v1/runs/run_abc/cancel", func(w http.ResponseWriter, _ *http.Request) {
				cancelRequested.Store(true)
				w.WriteHeader(http.StatusOK)
			})
			ts := httptest.NewServer(mux)
			defer ts.Close()

			outBuf, errBuf, restore := captureOutput(t)
			defer restore()

			origRequestClient := requestHTTPClient
			origStreamClient := streamHTTPClient
			requestHTTPClient = ts.Client()
			streamHTTPClient = ts.Client()
			defer func() {
				requestHTTPClient = origRequestClient
				streamHTTPClient = origStreamClient
			}()

			code := run([]string{"-base-url=" + ts.URL, "-prompt=test prompt"})
			if code != 3 {
				t.Fatalf("run() exit code = %d, want 3 (stderr=%s)", code, errBuf.String())
			}

			errOutput := errBuf.String()
			for _, want := range []string{"run_abc", tc.event, tc.wantReason, "harnesscli continue run_abc"} {
				if !strings.Contains(errOutput, want) {
					t.Errorf("stderr missing %q:\n%s", want, errOutput)
				}
			}

			output := outBuf.String()
			for _, want := range []string{"run_id=run_abc", tc.event} {
				if !strings.Contains(output, want) {
					t.Errorf("stdout missing %q (blocked event line must still be printed):\n%s", want, output)
				}
			}

			if cancelRequested.Load() {
				t.Error("server-side run must be left intact, but a cancel POST was received")
			}
		})
	}
}

// TestRunBlockedSignalKeepsStreamingWhenStdinIsTerminal proves interactive
// behavior is unchanged: with a TTY stdin the blocked signal does not abort
// the stream, and the run exits with the subsequent terminal event's code.
func TestRunBlockedSignalKeepsStreamingWhenStdinIsTerminal(t *testing.T) {
	for _, tc := range blockedSignals {
		t.Run(tc.name, func(t *testing.T) {
			stubStdinTerminal(t, true)

			mux := http.NewServeMux()
			mux.HandleFunc("/v1/runs", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = io.WriteString(w, `{"run_id":"run_abc","status":"queued"}`)
			})
			mux.HandleFunc("/v1/runs/run_abc/events", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
				_, _ = io.WriteString(w, "event: "+tc.event+"\n")
				_, _ = io.WriteString(w, "data: {\"id\":\"e1\",\"run_id\":\"run_abc\",\"type\":\""+tc.event+"\"}\n\n")
				_, _ = io.WriteString(w, "event: run.completed\n")
				_, _ = io.WriteString(w, "data: {\"id\":\"e2\",\"run_id\":\"run_abc\",\"type\":\"run.completed\"}\n\n")
			})
			ts := httptest.NewServer(mux)
			defer ts.Close()

			outBuf, errBuf, restore := captureOutput(t)
			defer restore()

			origRequestClient := requestHTTPClient
			origStreamClient := streamHTTPClient
			requestHTTPClient = ts.Client()
			streamHTTPClient = ts.Client()
			defer func() {
				requestHTTPClient = origRequestClient
				streamHTTPClient = origStreamClient
			}()

			code := run([]string{"-base-url=" + ts.URL, "-prompt=test prompt"})
			if code != 0 {
				t.Fatalf("run() exit code = %d, want 0 — blocked signal must not abort an interactive stream (stderr=%s)", code, errBuf.String())
			}
			output := outBuf.String()
			for _, want := range []string{tc.event, "terminal_event=run.completed"} {
				if !strings.Contains(output, want) {
					t.Errorf("stdout missing %q — stream must continue past the blocked signal:\n%s", want, output)
				}
			}
			if errBuf.Len() != 0 {
				t.Errorf("expected empty stderr for interactive stream, got %q", errBuf.String())
			}
		})
	}
}

// TestRunContinueBlockedSignalExits3WhenStdinNonInteractive applies the same
// blocked contract to the streaming continue path.
func TestRunContinueBlockedSignalExits3WhenStdinNonInteractive(t *testing.T) {
	stubStdinTerminal(t, false)

	var cancelRequested atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs/run_prev/continue":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode continue body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"run_id":"run_next","status":"queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_next/events":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: run.waiting_for_user\n")
			_, _ = io.WriteString(w, "data: {\"id\":\"run_next:0\",\"run_id\":\"run_next\",\"type\":\"run.waiting_for_user\"}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs/run_next/cancel":
			cancelRequested.Store(true)
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer ts.Close()

	outBuf, errBuf, restore := captureOutput(t)
	defer restore()

	origRequestClient := requestHTTPClient
	origStreamClient := streamHTTPClient
	requestHTTPClient = ts.Client()
	streamHTTPClient = ts.Client()
	defer func() {
		requestHTTPClient = origRequestClient
		streamHTTPClient = origStreamClient
	}()

	code := runContinue([]string{"-base-url=" + ts.URL, "run_prev", "follow up"})
	if code != 3 {
		t.Fatalf("runContinue() exit code = %d, want 3 (stderr=%s)", code, errBuf.String())
	}
	errOutput := errBuf.String()
	for _, want := range []string{"run_next", "run.waiting_for_user", "harnesscli continue run_next"} {
		if !strings.Contains(errOutput, want) {
			t.Errorf("stderr missing %q:\n%s", want, errOutput)
		}
	}
	if !strings.Contains(outBuf.String(), "run_id=run_next") {
		t.Errorf("stdout missing run_id line:\n%s", outBuf.String())
	}
	if cancelRequested.Load() {
		t.Error("server-side run must be left intact, but a cancel POST was received")
	}
}

// TestRunExitCodeMatchesTerminalEvent drives the one-shot streaming path
// against a scripted SSE server and asserts both the process exit code and
// that the stdout contract (run_id= / terminal_event= lines) is preserved.
func TestRunExitCodeMatchesTerminalEvent(t *testing.T) {
	cases := []struct {
		name          string
		terminalEvent string
		wantCode      int
	}{
		{"run.completed exits 0", "run.completed", 0},
		{"run.failed exits 2", "run.failed", 2},
		{"run.cancelled exits 6", "run.cancelled", 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/v1/runs", func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("unexpected method: %s", r.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = io.WriteString(w, `{"run_id":"run_abc","status":"queued"}`)
			})
			mux.HandleFunc("/v1/runs/run_abc/events", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
				_, _ = io.WriteString(w, "event: run.started\n")
				_, _ = io.WriteString(w, "data: {\"id\":\"e1\",\"run_id\":\"run_abc\",\"type\":\"run.started\"}\n\n")
				_, _ = io.WriteString(w, "event: "+tc.terminalEvent+"\n")
				_, _ = io.WriteString(w, "data: {\"id\":\"e2\",\"run_id\":\"run_abc\",\"type\":\""+tc.terminalEvent+"\"}\n\n")
			})
			ts := httptest.NewServer(mux)
			defer ts.Close()

			outBuf, errBuf, restore := captureOutput(t)
			defer restore()

			origRequestClient := requestHTTPClient
			origStreamClient := streamHTTPClient
			requestHTTPClient = ts.Client()
			streamHTTPClient = ts.Client()
			defer func() {
				requestHTTPClient = origRequestClient
				streamHTTPClient = origStreamClient
			}()

			code := run([]string{"-base-url=" + ts.URL, "-prompt=test prompt"})
			if code != tc.wantCode {
				t.Fatalf("run() exit code = %d, want %d (stderr=%s)", code, tc.wantCode, errBuf.String())
			}
			output := outBuf.String()
			for _, want := range []string{"run_id=run_abc", "terminal_event=" + tc.terminalEvent} {
				if !strings.Contains(output, want) {
					t.Errorf("stdout missing %q (stdout contract must be preserved):\n%s", want, output)
				}
			}
		})
	}
}

// TestRunContinueExitCodeMatchesTerminalEvent applies the same terminal-event
// mapping to the streaming continue path.
func TestRunContinueExitCodeMatchesTerminalEvent(t *testing.T) {
	cases := []struct {
		name          string
		terminalEvent string
		wantCode      int
	}{
		{"run.completed exits 0", "run.completed", 0},
		{"run.failed exits 2", "run.failed", 2},
		{"run.cancelled exits 6", "run.cancelled", 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/v1/runs/run_prev/continue":
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("decode continue body: %v", err)
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusAccepted)
					_, _ = io.WriteString(w, `{"run_id":"run_next","status":"queued"}`)
				case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_next/events":
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "event: "+tc.terminalEvent+"\n")
					_, _ = io.WriteString(w, "data: {\"id\":\"run_next:0\",\"run_id\":\"run_next\",\"type\":\""+tc.terminalEvent+"\"}\n\n")
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected", http.StatusNotFound)
				}
			}))
			defer ts.Close()

			outBuf, errBuf, restore := captureOutput(t)
			defer restore()

			origRequestClient := requestHTTPClient
			origStreamClient := streamHTTPClient
			requestHTTPClient = ts.Client()
			streamHTTPClient = ts.Client()
			defer func() {
				requestHTTPClient = origRequestClient
				streamHTTPClient = origStreamClient
			}()

			code := runContinue([]string{"-base-url=" + ts.URL, "run_prev", "follow up"})
			if code != tc.wantCode {
				t.Fatalf("runContinue() exit code = %d, want %d (stderr=%s)", code, tc.wantCode, errBuf.String())
			}
			output := outBuf.String()
			for _, want := range []string{"run_id=run_next", "terminal_event=" + tc.terminalEvent} {
				if !strings.Contains(output, want) {
					t.Errorf("stdout missing %q (stdout contract must be preserved):\n%s", want, output)
				}
			}
		})
	}
}

// TestRunContinueNoStreamDoesNotObserveEvents pins the contract that
// -no-stream exits 0 on creation without ever opening the event stream, so
// no terminal-event mapping applies.
func TestRunContinueNoStreamDoesNotObserveEvents(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs/run_prev/continue":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"run_id":"run_next","status":"queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_next/events":
			t.Errorf("event stream must not be requested with -no-stream")
			http.Error(w, "unexpected", http.StatusNotFound)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer ts.Close()

	outBuf, errBuf, restore := captureOutput(t)
	defer restore()

	origRequestClient := requestHTTPClient
	origStreamClient := streamHTTPClient
	requestHTTPClient = ts.Client()
	streamHTTPClient = ts.Client()
	defer func() {
		requestHTTPClient = origRequestClient
		streamHTTPClient = origStreamClient
	}()

	code := runContinue([]string{"-base-url=" + ts.URL, "-no-stream", "run_prev", "follow up"})
	if code != 0 {
		t.Fatalf("runContinue(-no-stream) exit code = %d, want 0 (stderr=%s)", code, errBuf.String())
	}
	if !strings.Contains(outBuf.String(), "run_id=run_next") {
		t.Errorf("stdout missing run_id line:\n%s", outBuf.String())
	}
}
