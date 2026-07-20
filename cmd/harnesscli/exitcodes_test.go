package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
