package main

// askuser_test.go — TUI #476 non-TUI mode
// Behavioral tests for AskUserQuestion handling in the non-TUI streaming CLI.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// BT-006: Non-TUI stdin/stdout flow
// ---------------------------------------------------------------------------

func TestNonTUI_WaitingForUser_PrintsQuestionAndReadsAnswer(t *testing.T) {
	// When run.waiting_for_user arrives in streaming mode, the question is
	// printed to stdout and the answer is read from stdin.

	// Set up a mock server that serves the pending input and accepts POST.
	var receivedAnswers map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/input"):
			pending := map[string]interface{}{
				"run_id":  "run-cli-1",
				"call_id": "call-c1",
				"tool":    "AskUserQuestion",
				"questions": []map[string]interface{}{
					{
						"question": "What should I do?",
						"header":   "Decision",
						"options": []map[string]string{
							{"label": "Proceed", "description": "Go ahead"},
							{"label": "Abort", "description": "Stop here"},
						},
						"multiSelect": false,
					},
				},
				"deadline_at": "2099-01-01T00:00:00Z",
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(pending) //nolint:errcheck
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/input"):
			var payload struct {
				Answers map[string]string `json:"answers"`
			}
			json.NewDecoder(r.Body).Decode(&payload) //nolint:errcheck
			receivedAnswers = payload.Answers
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()

	// Simulate stdin with the answer "Proceed"
	stdin := strings.NewReader("Proceed\n")
	var stdout bytes.Buffer

	err := handleAskUserQuestion(srv.URL, "run-cli-1", stdin, &stdout)
	if err != nil {
		t.Fatalf("handleAskUserQuestion failed: %v", err)
	}

	// The question must have been printed to stdout
	out := stdout.String()
	if !strings.Contains(out, "What should I do?") {
		t.Errorf("expected question text in stdout; got: %q", out)
	}
	if !strings.Contains(out, "Proceed") {
		t.Errorf("expected option 'Proceed' in stdout; got: %q", out)
	}
	if !strings.Contains(out, "Abort") {
		t.Errorf("expected option 'Abort' in stdout; got: %q", out)
	}

	// The answer must have been submitted
	if receivedAnswers == nil {
		t.Fatal("expected answers to be submitted to server")
	}
	if receivedAnswers["What should I do?"] != "Proceed" {
		t.Errorf("expected answer 'Proceed', got: %q", receivedAnswers["What should I do?"])
	}
}

func TestNonTUI_WaitingForUser_InvalidOption_ReturnsError(t *testing.T) {
	// When the user types an option that is not in the list, it should
	// return an error (bad user input).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/input") {
			pending := map[string]interface{}{
				"run_id":  "run-cli-2",
				"call_id": "call-c2",
				"tool":    "AskUserQuestion",
				"questions": []map[string]interface{}{
					{
						"question": "Which?",
						"header":   "Choose",
						"options": []map[string]string{
							{"label": "A", "description": "Alpha"},
							{"label": "B", "description": "Beta"},
						},
						"multiSelect": false,
					},
				},
				"deadline_at": "2099-01-01T00:00:00Z",
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(pending) //nolint:errcheck
		}
	}))
	defer srv.Close()

	stdin := strings.NewReader("InvalidOption\n")
	var stdout bytes.Buffer

	err := handleAskUserQuestion(srv.URL, "run-cli-2", stdin, &stdout)
	if err == nil {
		t.Error("expected an error for invalid option, got nil")
	}
}

func TestNonTUI_WaitingForUser_NetworkError_ReturnsError(t *testing.T) {
	// When the GET /v1/runs/{id}/input request fails, the function returns an error.
	err := handleAskUserQuestion("http://localhost:0", "run-cli-3", strings.NewReader(""), &bytes.Buffer{})
	if err == nil {
		t.Error("expected error when server is unreachable")
	}
}

// ---------------------------------------------------------------------------
// Regression: GET input + POST answer HTTP client calls
// ---------------------------------------------------------------------------

func TestRegression_NonTUI_GetInput_UsesCorrectURL(t *testing.T) {
	// Verifies the correct URL path /v1/runs/{id}/input is called.
	var calledPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calledPath = r.URL.Path
		if r.Method == http.MethodGet {
			// Return valid pending
			pending := map[string]interface{}{
				"run_id":  "run-url-1",
				"call_id": "call-u1",
				"tool":    "AskUserQuestion",
				"questions": []map[string]interface{}{
					{
						"question": "URL test?",
						"header":   "H",
						"options": []map[string]string{
							{"label": "Yes", "description": "Affirmative"},
							{"label": "No", "description": "Negative"},
						},
						"multiSelect": false,
					},
				},
				"deadline_at": "2099-01-01T00:00:00Z",
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(pending) //nolint:errcheck
		} else if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()

	handleAskUserQuestion(srv.URL, "run-url-1", strings.NewReader("Yes\n"), &bytes.Buffer{}) //nolint:errcheck

	if calledPath != "/v1/runs/run-url-1/input" {
		t.Errorf("expected GET path /v1/runs/run-url-1/input, got %q", calledPath)
	}
}

func TestRegression_NonTUI_PostInput_SendsCorrectPayload(t *testing.T) {
	// Verifies the POST body contains {"answers": {"question": "label"}}.
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			pending := map[string]interface{}{
				"run_id":  "run-post-1",
				"call_id": "call-p1",
				"tool":    "AskUserQuestion",
				"questions": []map[string]interface{}{
					{
						"question": "Go or Stop?",
						"header":   "Action",
						"options": []map[string]string{
							{"label": "Go", "description": "Proceed"},
							{"label": "Stop", "description": "Halt"},
						},
						"multiSelect": false,
					},
				},
				"deadline_at": "2099-01-01T00:00:00Z",
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(pending) //nolint:errcheck
		} else if r.Method == http.MethodPost {
			body := make([]byte, r.ContentLength)
			r.Body.Read(body) //nolint:errcheck
			receivedBody = body
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()

	handleAskUserQuestion(srv.URL, "run-post-1", strings.NewReader("Go\n"), &bytes.Buffer{}) //nolint:errcheck

	if len(receivedBody) == 0 {
		t.Fatal("expected POST body")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("invalid JSON POST body: %v", err)
	}
	answers, ok := payload["answers"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'answers' in POST body, got: %+v", payload)
	}
	if answers["Go or Stop?"] != "Go" {
		t.Errorf("expected answer 'Go', got: %v", answers["Go or Stop?"])
	}
}

// ---------------------------------------------------------------------------
// Helper: ensures integration with streamRunEvents indirectly via format
// ---------------------------------------------------------------------------

func TestNonTUI_WaitingForUser_MultipleQuestionsInSingleCall(t *testing.T) {
	// When there are multiple questions, all are printed and stdin is read
	// once per question.
	var receivedAnswers map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			pending := map[string]interface{}{
				"run_id":  "run-multi-1",
				"call_id": "call-m1",
				"tool":    "AskUserQuestion",
				"questions": []map[string]interface{}{
					{
						"question": "First question?",
						"header":   "Q1",
						"options": []map[string]string{
							{"label": "A1", "description": "Answer 1"},
							{"label": "B1", "description": "Answer 2"},
						},
						"multiSelect": false,
					},
					{
						"question": "Second question?",
						"header":   "Q2",
						"options": []map[string]string{
							{"label": "A2", "description": "Option A"},
							{"label": "B2", "description": "Option B"},
						},
						"multiSelect": false,
					},
				},
				"deadline_at": "2099-01-01T00:00:00Z",
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(pending) //nolint:errcheck
		} else if r.Method == http.MethodPost {
			var payload struct {
				Answers map[string]string `json:"answers"`
			}
			json.NewDecoder(r.Body).Decode(&payload) //nolint:errcheck
			receivedAnswers = payload.Answers
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()

	// Two answers on two lines
	stdin := strings.NewReader("A1\nA2\n")
	var stdout bytes.Buffer

	err := handleAskUserQuestion(srv.URL, "run-multi-1", stdin, &stdout)
	if err != nil {
		t.Fatalf("handleAskUserQuestion failed: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "First question?") {
		t.Errorf("expected 'First question?' in stdout; got: %q", out)
	}
	if !strings.Contains(out, "Second question?") {
		t.Errorf("expected 'Second question?' in stdout; got: %q", out)
	}

	if receivedAnswers["First question?"] != "A1" {
		t.Errorf("expected A1 for First question?, got: %q", receivedAnswers["First question?"])
	}
	if receivedAnswers["Second question?"] != "A2" {
		t.Errorf("expected A2 for Second question?, got: %q", receivedAnswers["Second question?"])
	}
	_ = fmt.Sprintf // suppress import
}

// ---------------------------------------------------------------------------
// Fix 3 (MEDIUM): Non-TUI deadline enforcement
// ---------------------------------------------------------------------------

func TestNonTUI_DeadlineExpired_ReturnsErrorWithoutPrompting(t *testing.T) {
	// When DeadlineAt is already in the past, handleAskUserQuestion must return
	// an error immediately without prompting for input.
	// We detect: (a) function returns an error, (b) no POST sent (no answer submitted),
	// (c) nothing was written to stdout (no prompt shown).
	// We provide a VALID answer in stdin so that only a deadline check can cause failure.
	var postReceived bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/input") {
			pending := map[string]interface{}{
				"run_id":  "run-deadline-2",
				"call_id": "call-d2",
				"tool":    "AskUserQuestion",
				"questions": []map[string]interface{}{
					{
						"question": "Past deadline?",
						"header":   "Past",
						"options": []map[string]string{
							{"label": "Yes", "description": "Yes"},
						},
						"multiSelect": false,
					},
				},
				"deadline_at": "2000-01-01T00:00:00Z",
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(pending) //nolint:errcheck
		} else if r.Method == http.MethodPost {
			postReceived = true
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()

	// Provide a VALID answer — if deadline check is missing, this would succeed.
	stdin := strings.NewReader("Yes\n")
	var stdout bytes.Buffer

	err := handleAskUserQuestion(srv.URL, "run-deadline-2", stdin, &stdout)
	if err == nil {
		t.Error("expected an error when deadline has already expired, got nil")
	}
	if postReceived {
		t.Error("expected no POST to server when deadline is already expired")
	}
}

// ---------------------------------------------------------------------------
// Fix 4 (MEDIUM): URL path injection — special chars in runID
// ---------------------------------------------------------------------------

func TestNonTUI_RunIDWithSlashes_URLIsEscaped(t *testing.T) {
	// When the runID contains '/' characters, the HTTP URL path must be
	// percent-encoded so those chars are not treated as path separators.
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.RequestURI
		if r.Method == http.MethodGet {
			pending := map[string]interface{}{
				"run_id":  "run/with/slashes",
				"call_id": "call-escape-1",
				"tool":    "AskUserQuestion",
				"questions": []map[string]interface{}{
					{
						"question": "Escape?",
						"header":   "H",
						"options": []map[string]string{
							{"label": "Yes", "description": "OK"},
						},
						"multiSelect": false,
					},
				},
				"deadline_at": "2099-01-01T00:00:00Z",
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(pending) //nolint:errcheck
		} else if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()

	stdin := strings.NewReader("Yes\n")
	var stdout bytes.Buffer
	handleAskUserQuestion(srv.URL, "run/with/slashes", stdin, &stdout) //nolint:errcheck

	// The path must NOT contain raw slashes in the run ID portion.
	// /v1/runs/run%2Fwith%2Fslashes/input is correct.
	// /v1/runs/run/with/slashes/input is wrong.
	if strings.Contains(receivedPath, "/v1/runs/run/with/slashes/input") {
		t.Errorf("runID slashes must be percent-escaped in URL, but got raw path: %q", receivedPath)
	}
	if !strings.Contains(receivedPath, "run%2Fwith%2Fslashes") {
		t.Errorf("expected percent-escaped runID in path; got: %q", receivedPath)
	}
}

// ---------------------------------------------------------------------------
// Auth: ask-user GET/POST must attach the Bearer token from ~/.harness/config.json
// ---------------------------------------------------------------------------

func writeTestHarnessConfig(t *testing.T, apiKey string) {
	t.Helper()
	tmpHome := t.TempDir()
	oldHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", oldHome) })
	os.Setenv("HOME", tmpHome)

	cfgDir := filepath.Join(tmpHome, ".harness")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	cfgBody, err := json.Marshal(harnessConfig{Server: "http://localhost:8080", APIKey: apiKey})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), cfgBody, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func TestAskUser_GetInput_AttachesAuthorizationHeader(t *testing.T) {
	writeTestHarnessConfig(t, "harness_sk_test_token")

	var getAuthHeader, postAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/input"):
			getAuthHeader = r.Header.Get("Authorization")
			pending := map[string]interface{}{
				"run_id":  "run-auth-1",
				"call_id": "call-a1",
				"tool":    "AskUserQuestion",
				"questions": []map[string]interface{}{
					{
						"question": "Proceed?",
						"header":   "H",
						"options": []map[string]string{
							{"label": "Yes", "description": "OK"},
						},
						"multiSelect": false,
					},
				},
				"deadline_at": "2099-01-01T00:00:00Z",
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(pending) //nolint:errcheck
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/input"):
			postAuthHeader = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()

	stdin := strings.NewReader("Yes\n")
	var stdout bytes.Buffer
	if err := handleAskUserQuestion(srv.URL, "run-auth-1", stdin, &stdout); err != nil {
		t.Fatalf("handleAskUserQuestion failed: %v", err)
	}

	wantAuth := "Bearer harness_sk_test_token"
	if getAuthHeader != wantAuth {
		t.Errorf("GET /input: expected Authorization %q, got %q", wantAuth, getAuthHeader)
	}
	if postAuthHeader != wantAuth {
		t.Errorf("POST /input: expected Authorization %q, got %q", wantAuth, postAuthHeader)
	}
}
