package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestSteerRunCmd_PostsPromptToSteerEndpoint pins the wire contract of
// POST /v1/runs/{id}/steer (internal/server/http_runs.go handleRunSteer):
// POST method, path-escaped run ID, JSON body {"prompt": ...},
// Content-Type: application/json, the harnessd API key on the Authorization
// header, and HTTP 202 mapping to SteerAcceptedMsg carrying the run ID.
func TestSteerRunCmd_PostsPromptToSteerEndpoint(t *testing.T) {
	var gotMethod, gotPath, gotPrompt, gotContentType, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode steer body: %v", err)
		}
		gotPrompt = body.Prompt
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer srv.Close()

	msg := steerRunCmd(srv.URL, "run-x", "focus on X", "test-key")()

	accepted, ok := msg.(SteerAcceptedMsg)
	if !ok {
		t.Fatalf("expected SteerAcceptedMsg, got %T: %+v", msg, msg)
	}
	if accepted.RunID != "run-x" {
		t.Errorf("SteerAcceptedMsg.RunID = %q, want %q", accepted.RunID, "run-x")
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/runs/run-x/steer" {
		t.Errorf("path = %q, want /v1/runs/run-x/steer", gotPath)
	}
	if gotPrompt != "focus on X" {
		t.Errorf("prompt body = %q, want %q", gotPrompt, "focus on X")
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-key")
	}
}

// TestSteerRunCmd_PathEscapesRunID mirrors the cancel regression: a run ID
// containing "/" must be percent-encoded so it cannot traverse the URL path.
func TestSteerRunCmd_PathEscapesRunID(t *testing.T) {
	var gotRawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRawPath = r.URL.RawPath
		if gotRawPath == "" {
			gotRawPath = r.URL.Path
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer srv.Close()

	steerRunCmd(srv.URL, "../admin", "focus", "")()

	if gotRawPath != "/v1/runs/..%2Fadmin/steer" {
		t.Errorf("raw path = %q, want /v1/runs/..%%2Fadmin/steer (run ID slash percent-encoded)", gotRawPath)
	}
}

// TestSteerRunCmd_MapsErrorStatuses verifies each documented steer failure
// status maps to a SteerErrorMsg with a stable Kind the model can translate
// into status-bar text (slice 3 consumes these kinds).
func TestSteerRunCmd_MapsErrorStatuses(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantKind string
	}{
		{"not found", http.StatusNotFound, `{"error":{"code":"not_found","message":"run \"run-x\" not found"}}`, "not_found"},
		{"run not active", http.StatusConflict, `{"error":{"code":"run_not_active","message":"run \"run-x\" is not active"}}`, "run_not_active"},
		{"buffer full", http.StatusTooManyRequests, `{"error":{"code":"steering_buffer_full","message":"steering buffer full"}}`, "steering_buffer_full"},
		{"unexpected status", http.StatusInternalServerError, `boom`, "http"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			msg := steerRunCmd(srv.URL, "run-x", "focus on X", "")()

			steerErr, ok := msg.(SteerErrorMsg)
			if !ok {
				t.Fatalf("expected SteerErrorMsg, got %T: %+v", msg, msg)
			}
			if steerErr.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", steerErr.Kind, tc.wantKind)
			}
			if steerErr.RunID != "run-x" {
				t.Errorf("RunID = %q, want %q", steerErr.RunID, "run-x")
			}
			if steerErr.Err == "" {
				t.Error("expected non-empty error detail")
			}
		})
	}
}

// TestSteerRunCmd_RejectsEmptyPromptClientSide verifies that an empty or
// whitespace-only prompt is rejected before any HTTP request is issued — the
// server would answer 400, but the client must not send it at all.
func TestSteerRunCmd_RejectsEmptyPromptClientSide(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	for _, prompt := range []string{"", "   ", "\n\t "} {
		msg := steerRunCmd(srv.URL, "run-x", prompt, "")()
		steerErr, ok := msg.(SteerErrorMsg)
		if !ok {
			t.Fatalf("prompt %q: expected SteerErrorMsg, got %T: %+v", prompt, msg, msg)
		}
		if steerErr.Kind != "invalid_prompt" {
			t.Errorf("prompt %q: Kind = %q, want %q", prompt, steerErr.Kind, "invalid_prompt")
		}
		if steerErr.RunID != "run-x" {
			t.Errorf("prompt %q: RunID = %q, want %q", prompt, steerErr.RunID, "run-x")
		}
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("server received %d request(s) for empty prompts; want 0 (client-side rejection)", got)
	}
}

// TestSteerRunCmd_TransportError verifies a connection failure surfaces as a
// SteerErrorMsg (Kind "transport") rather than panicking or returning nil.
func TestSteerRunCmd_TransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // refuse connections

	msg := steerRunCmd(srv.URL, "run-x", "focus on X", "")()

	steerErr, ok := msg.(SteerErrorMsg)
	if !ok {
		t.Fatalf("expected SteerErrorMsg, got %T: %+v", msg, msg)
	}
	if steerErr.Kind != "transport" {
		t.Errorf("Kind = %q, want %q", steerErr.Kind, "transport")
	}
	if steerErr.RunID != "run-x" {
		t.Errorf("RunID = %q, want %q", steerErr.RunID, "run-x")
	}
	if steerErr.Err == "" {
		t.Error("expected non-empty error detail")
	}
}
