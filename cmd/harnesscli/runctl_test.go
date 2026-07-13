package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- helpers ---

func captureOutput(t *testing.T) (outBuf *bytes.Buffer, errBuf *bytes.Buffer, restore func()) {
	t.Helper()
	origStdout := stdout
	origStderr := stderr
	outBuf = &bytes.Buffer{}
	errBuf = &bytes.Buffer{}
	stdout = outBuf
	stderr = errBuf
	return outBuf, errBuf, func() {
		stdout = origStdout
		stderr = origStderr
	}
}

// sampleRunJSON returns a JSON-encoded run object for test servers to emit.
func sampleRunJSON(id, status, model, prompt string) map[string]any {
	return map[string]any{
		"id":         id,
		"status":     status,
		"model":      model,
		"prompt":     prompt,
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}
}

// --- BT-001: list shows all 3 run IDs ---

func TestRunList_ShowsAllRunIDs(t *testing.T) {
	runs := []map[string]any{
		sampleRunJSON("run_aaa", "completed", "gpt-4", "first prompt"),
		sampleRunJSON("run_bbb", "running", "gpt-4", "second prompt"),
		sampleRunJSON("run_ccc", "queued", "gpt-4", "third prompt"),
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs" || r.Method != http.MethodGet {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"runs": runs})
	}))
	defer ts.Close()

	outBuf, errBuf, restore := captureOutput(t)
	defer restore()

	origClient := requestHTTPClient
	requestHTTPClient = ts.Client()
	defer func() { requestHTTPClient = origClient }()

	code := runList([]string{"-base-url=" + ts.URL})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%s)", code, errBuf.String())
	}
	output := outBuf.String()
	for _, id := range []string{"run_aaa", "run_bbb", "run_ccc"} {
		if !strings.Contains(output, id) {
			t.Errorf("output missing run ID %q:\n%s", id, output)
		}
	}
}

// --- BT-002: list --status running only shows running runs ---

func TestRunList_StatusFilter(t *testing.T) {
	var capturedQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		runs := []map[string]any{
			sampleRunJSON("run_running1", "running", "gpt-4", "a prompt"),
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"runs": runs})
	}))
	defer ts.Close()

	outBuf, errBuf, restore := captureOutput(t)
	defer restore()

	origClient := requestHTTPClient
	requestHTTPClient = ts.Client()
	defer func() { requestHTTPClient = origClient }()

	code := runList([]string{"-base-url=" + ts.URL, "-status=running"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%s)", code, errBuf.String())
	}
	if !strings.Contains(capturedQuery, "status=running") {
		t.Errorf("expected query to contain status=running, got %q", capturedQuery)
	}
	if !strings.Contains(outBuf.String(), "run_running1") {
		t.Errorf("expected run_running1 in output:\n%s", outBuf.String())
	}
}

// --- BT-003: list 501 shows clear error ---

func TestRunList_501NoStore(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = io.WriteString(w, `{"error":{"code":"not_implemented","message":"run persistence is not configured"}}`)
	}))
	defer ts.Close()

	_, errBuf, restore := captureOutput(t)
	defer restore()

	origClient := requestHTTPClient
	requestHTTPClient = ts.Client()
	defer func() { requestHTTPClient = origClient }()

	code := runList([]string{"-base-url=" + ts.URL})
	if code != 1 {
		t.Fatalf("expected exit code 1 for 501, got %d", code)
	}
	errStr := errBuf.String()
	if !strings.Contains(errStr, "not configured") && !strings.Contains(errStr, "501") && !strings.Contains(errStr, "not_implemented") {
		t.Errorf("expected clear error message about run store not configured, got: %s", errStr)
	}
}

// --- BT-004: cancel succeeds, output says "cancelling" ---

func TestRunCancel_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs/run_xyz/cancel" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"cancelling"}`)
	}))
	defer ts.Close()

	outBuf, errBuf, restore := captureOutput(t)
	defer restore()

	origClient := requestHTTPClient
	requestHTTPClient = ts.Client()
	defer func() { requestHTTPClient = origClient }()

	code := runCancel([]string{"-base-url=" + ts.URL, "run_xyz"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%s)", code, errBuf.String())
	}
	if !strings.Contains(outBuf.String(), "cancelling") {
		t.Errorf("expected 'cancelling' in output, got: %s", outBuf.String())
	}
}

// --- BT-005: cancel 404 says run not found ---

func TestRunCancel_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"run \"run_nope\" not found"}}`)
	}))
	defer ts.Close()

	_, errBuf, restore := captureOutput(t)
	defer restore()

	origClient := requestHTTPClient
	requestHTTPClient = ts.Client()
	defer func() { requestHTTPClient = origClient }()

	code := runCancel([]string{"-base-url=" + ts.URL, "run_nope"})
	if code != 1 {
		t.Fatalf("expected exit code 1 for 404, got %d", code)
	}
	errStr := errBuf.String()
	if !strings.Contains(errStr, "not found") && !strings.Contains(errStr, "404") {
		t.Errorf("expected 'not found' in error output, got: %s", errStr)
	}
}

// --- BT-006: cancel with no ID shows usage error ---

func TestRunCancel_NoID(t *testing.T) {
	_, errBuf, restore := captureOutput(t)
	defer restore()

	code := runCancel([]string{})
	if code != 1 {
		t.Fatalf("expected exit code 1 for missing run ID, got %d", code)
	}
	if !strings.Contains(errBuf.String(), "run ID") {
		t.Errorf("expected usage error about run ID, got: %s", errBuf.String())
	}
}

// --- BT-007: status succeeds, shows run details ---

func TestRunStatus_ShowsDetails(t *testing.T) {
	runData := sampleRunJSON("run_detail", "completed", "gpt-4o", "write a report")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs/run_detail" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(runData)
	}))
	defer ts.Close()

	outBuf, errBuf, restore := captureOutput(t)
	defer restore()

	origClient := requestHTTPClient
	requestHTTPClient = ts.Client()
	defer func() { requestHTTPClient = origClient }()

	code := runStatus([]string{"-base-url=" + ts.URL, "run_detail"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%s)", code, errBuf.String())
	}
	output := outBuf.String()
	for _, want := range []string{"run_detail", "completed", "gpt-4o"} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}

// TestRunStatus_ResponseBodyIsBounded verifies that runStatus reads response
// bodies through a bounded reader: with maxResponseBodyBytes shrunk below the
// size of a valid response, decoding must fail instead of silently reading
// the entire (in this test, still-small, but in principle attacker-huge) body.
func TestRunStatus_ResponseBodyIsBounded(t *testing.T) {
	orig := maxResponseBodyBytes
	maxResponseBodyBytes = 10
	defer func() { maxResponseBodyBytes = orig }()

	runData := sampleRunJSON("run_detail", "completed", "gpt-4o", "write a report")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(runData)
	}))
	defer ts.Close()

	_, errBuf, restore := captureOutput(t)
	defer restore()

	origClient := requestHTTPClient
	requestHTTPClient = ts.Client()
	defer func() { requestHTTPClient = origClient }()

	code := runStatus([]string{"-base-url=" + ts.URL, "run_detail"})
	if code != 1 {
		t.Fatalf("expected exit code 1 when the body is truncated below a decodable size, got %d (stderr=%s)", code, errBuf.String())
	}
}

// --- BT-008: status 404 says not found ---

func TestRunStatus_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"run not found"}}`)
	}))
	defer ts.Close()

	_, errBuf, restore := captureOutput(t)
	defer restore()

	origClient := requestHTTPClient
	requestHTTPClient = ts.Client()
	defer func() { requestHTTPClient = origClient }()

	code := runStatus([]string{"-base-url=" + ts.URL, "run_missing"})
	if code != 1 {
		t.Fatalf("expected exit code 1 for 404, got %d", code)
	}
	errStr := errBuf.String()
	if !strings.Contains(errStr, "not found") && !strings.Contains(errStr, "404") {
		t.Errorf("expected 'not found' in error output, got: %s", errStr)
	}
}

// --- BT-009: status with no run ID shows usage error ---

func TestRunStatus_NoID(t *testing.T) {
	_, errBuf, restore := captureOutput(t)
	defer restore()

	code := runStatus([]string{})
	if code != 1 {
		t.Fatalf("expected exit code 1 for missing run ID, got %d", code)
	}
	if !strings.Contains(errBuf.String(), "run ID") {
		t.Errorf("expected usage error about run ID, got: %s", errBuf.String())
	}
}

// --- Regression: dispatch routes list/cancel/status correctly ---

func TestDispatch_ListRouted(t *testing.T) {
	// Verify that dispatch("list", ...) calls runList, not run().
	// If run() is called without -prompt it returns 1 with "prompt is required".
	// If runList is called against a valid server returning empty list, returns 0.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"runs": []any{}})
	}))
	defer ts.Close()

	outBuf, errBuf, restore := captureOutput(t)
	defer restore()

	origClient := requestHTTPClient
	requestHTTPClient = ts.Client()
	defer func() { requestHTTPClient = origClient }()

	code := dispatch([]string{"list", "-base-url=" + ts.URL})
	if code != 0 {
		t.Fatalf("dispatch list should route to runList and return 0; got %d (stderr=%s)", code, errBuf.String())
	}
	// Should not print "prompt is required" (which run() would emit)
	if strings.Contains(errBuf.String(), "prompt is required") {
		t.Errorf("dispatch('list') should not call run(); got: %s", errBuf.String())
	}
	_ = outBuf
}

func TestDispatch_CancelRoutedNoID(t *testing.T) {
	_, errBuf, restore := captureOutput(t)
	defer restore()

	// dispatch("cancel") with no ID should route to runCancel and return 1 with usage.
	code := dispatch([]string{"cancel"})
	if code != 1 {
		t.Fatalf("dispatch cancel with no ID should return 1; got %d", code)
	}
	if strings.Contains(errBuf.String(), "prompt is required") {
		t.Errorf("dispatch('cancel') should not call run(); got: %s", errBuf.String())
	}
}

func TestDispatch_StatusRoutedNoID(t *testing.T) {
	_, errBuf, restore := captureOutput(t)
	defer restore()

	// dispatch("status") with no ID should route to runStatus and return 1 with usage.
	code := dispatch([]string{"status"})
	if code != 1 {
		t.Fatalf("dispatch status with no ID should return 1; got %d", code)
	}
	if strings.Contains(errBuf.String(), "prompt is required") {
		t.Errorf("dispatch('status') should not call run(); got: %s", errBuf.String())
	}
}

// --- Regression: list with conversation-id filter sends query param ---

func TestRunList_ConversationIDFilter(t *testing.T) {
	var capturedQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"runs": []any{}})
	}))
	defer ts.Close()

	_, _, restore := captureOutput(t)
	defer restore()

	origClient := requestHTTPClient
	requestHTTPClient = ts.Client()
	defer func() { requestHTTPClient = origClient }()

	code := runList([]string{"-base-url=" + ts.URL, "-conversation-id=conv_abc"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !strings.Contains(capturedQuery, "conversation_id=conv_abc") {
		t.Errorf("expected conversation_id=conv_abc in query, got %q", capturedQuery)
	}
}

// --- Regression: list empty result says no runs ---

func TestRunList_EmptyResult(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"runs": []any{}})
	}))
	defer ts.Close()

	outBuf, errBuf, restore := captureOutput(t)
	defer restore()

	origClient := requestHTTPClient
	requestHTTPClient = ts.Client()
	defer func() { requestHTTPClient = origClient }()

	code := runList([]string{"-base-url=" + ts.URL})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%s)", code, errBuf.String())
	}
	if !strings.Contains(outBuf.String(), "No runs") {
		t.Errorf("expected 'No runs' in output for empty list, got: %s", outBuf.String())
	}
}

// --- Review finding 1: URL-escape run IDs in cancel path ---

// TestRunCancel_PathEscapesRunID verifies that a run ID containing path-traversal
// characters ("../admin") does not alter the wire-level URL path beyond the runs
// prefix. url.PathEscape encodes "/" to "%2F" so the RawPath on the server side
// must contain the encoded form, not a literal slash that would allow traversal.
func TestRunCancel_PathEscapesRunID(t *testing.T) {
	var capturedRawPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// RawPath holds the percent-encoded wire path; Path is always cleaned.
		capturedRawPath = r.URL.RawPath
		if capturedRawPath == "" {
			capturedRawPath = r.URL.Path // fallback when no encoding was needed
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"cancelling"}`)
	}))
	defer ts.Close()

	_, _, restore := captureOutput(t)
	defer restore()

	origClient := requestHTTPClient
	requestHTTPClient = ts.Client()
	defer func() { requestHTTPClient = origClient }()

	runCancel([]string{"-base-url=" + ts.URL, "../admin"})

	// The RawPath must not contain a literal "/admin" segment — the slash in the
	// run ID must be percent-encoded (%2F), preventing path traversal.
	if strings.Contains(capturedRawPath, "/admin") {
		t.Errorf("path traversal not escaped: raw path %q contains literal /admin; run ID slash must be %%2F-encoded", capturedRawPath)
	}
}

// TestRunStatus_PathEscapesRunID verifies that a run ID containing path-traversal
// characters ("../admin") does not alter the wire-level URL path beyond the runs
// prefix.
func TestRunStatus_PathEscapesRunID(t *testing.T) {
	var capturedRawPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRawPath = r.URL.RawPath
		if capturedRawPath == "" {
			capturedRawPath = r.URL.Path
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(sampleRunJSON("x", "running", "gpt-4", "p"))
	}))
	defer ts.Close()

	_, _, restore := captureOutput(t)
	defer restore()

	origClient := requestHTTPClient
	requestHTTPClient = ts.Client()
	defer func() { requestHTTPClient = origClient }()

	runStatus([]string{"-base-url=" + ts.URL, "../admin"})

	if strings.Contains(capturedRawPath, "/admin") {
		t.Errorf("path traversal not escaped: raw path %q contains literal /admin; run ID slash must be %%2F-encoded", capturedRawPath)
	}
}

// --- Review finding 2: url.Values encoding for query parameters ---

// TestRunList_QueryParamInjection verifies that a status filter value containing
// "&admin=true" is properly encoded and does not inject extra query parameters.
func TestRunList_QueryParamInjection(t *testing.T) {
	var capturedQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"runs": []any{}})
	}))
	defer ts.Close()

	_, _, restore := captureOutput(t)
	defer restore()

	origClient := requestHTTPClient
	requestHTTPClient = ts.Client()
	defer func() { requestHTTPClient = origClient }()

	runList([]string{"-base-url=" + ts.URL, "-status=running&admin=true"})

	// The injected "&admin=true" must not appear as a separate query parameter.
	// url.Values.Encode would percent-encode the ampersand.
	if strings.Contains(capturedQuery, "admin=true") {
		t.Errorf("query parameter injection not escaped: got query %q, 'admin=true' must not appear as a separate param", capturedQuery)
	}
}

// --- Review finding 3: reject extra positional arguments ---

// TestRunCancel_RejectsExtraArgs verifies that passing multiple run IDs returns an error.
func TestRunCancel_RejectsExtraArgs(t *testing.T) {
	_, errBuf, restore := captureOutput(t)
	defer restore()

	code := runCancel([]string{"run1", "run2"})
	if code != 1 {
		t.Fatalf("expected exit code 1 for extra args, got %d", code)
	}
	if !strings.Contains(errBuf.String(), "too many") && !strings.Contains(errBuf.String(), "extra") && !strings.Contains(errBuf.String(), "accepts") {
		t.Errorf("expected error about too many arguments, got: %s", errBuf.String())
	}
}

// TestRunStatus_RejectsExtraArgs verifies that passing multiple run IDs returns an error.
func TestRunStatus_RejectsExtraArgs(t *testing.T) {
	_, errBuf, restore := captureOutput(t)
	defer restore()

	code := runStatus([]string{"run1", "run2"})
	if code != 1 {
		t.Fatalf("expected exit code 1 for extra args, got %d", code)
	}
	if !strings.Contains(errBuf.String(), "too many") && !strings.Contains(errBuf.String(), "extra") && !strings.Contains(errBuf.String(), "accepts") {
		t.Errorf("expected error about too many arguments, got: %s", errBuf.String())
	}
}

// --- Regression: cancel sends POST to correct path ---

func TestRunCancel_SendsPostToCorrectPath(t *testing.T) {
	var capturedMethod, capturedPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"cancelling"}`)
	}))
	defer ts.Close()

	_, _, restore := captureOutput(t)
	defer restore()

	origClient := requestHTTPClient
	requestHTTPClient = ts.Client()
	defer func() { requestHTTPClient = origClient }()

	runCancel([]string{"-base-url=" + ts.URL, "run_test123"})

	if capturedMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", capturedMethod)
	}
	if capturedPath != "/v1/runs/run_test123/cancel" {
		t.Errorf("expected /v1/runs/run_test123/cancel, got %s", capturedPath)
	}
}

func TestRunContinue_PostsPromptAndStreamsNewRun(t *testing.T) {
	var capturedPrompt string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs/run_prev/continue":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode continue body: %v", err)
			}
			capturedPrompt = body["prompt"]
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"run_id":"run_next","status":"queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/run_next/events":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: run.completed\n")
			_, _ = io.WriteString(w, `data: {"id":"run_next:0","run_id":"run_next","type":"run.completed","payload":{"output":"ok"}}`+"\n\n")
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
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
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%s)", code, errBuf.String())
	}
	if capturedPrompt != "follow up" {
		t.Fatalf("prompt = %q, want %q", capturedPrompt, "follow up")
	}
	output := outBuf.String()
	for _, want := range []string{"run_id=run_next", "run.completed", "terminal_event=run.completed"} {
		if !strings.Contains(output, want) {
			t.Errorf("continue output missing %q:\n%s", want, output)
		}
	}
}

func TestRunReplay_PostsRolloutSpecifier(t *testing.T) {
	var captured map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/runs/replay" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode replay body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"mode":"simulate","events_replayed":3,"matched":true}`)
	}))
	defer ts.Close()

	outBuf, errBuf, restore := captureOutput(t)
	defer restore()

	origClient := requestHTTPClient
	requestHTTPClient = ts.Client()
	defer func() { requestHTTPClient = origClient }()

	code := runReplay([]string{"-base-url=" + ts.URL, "-detect-drift", "run_abc"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%s)", code, errBuf.String())
	}
	if captured["rollout_path"] != "run_abc" {
		t.Fatalf("rollout_path = %v, want run_abc", captured["rollout_path"])
	}
	if captured["mode"] != "simulate" {
		t.Fatalf("mode = %v, want simulate", captured["mode"])
	}
	if captured["detect_drift"] != true {
		t.Fatalf("detect_drift = %v, want true", captured["detect_drift"])
	}
	if !strings.Contains(outBuf.String(), `"events_replayed": 3`) {
		t.Errorf("expected formatted replay JSON, got:\n%s", outBuf.String())
	}
}

func TestRunSearch_FiltersRunMetadata(t *testing.T) {
	runs := []map[string]any{
		sampleRunJSON("run_match", "completed", "gpt-4o", "fix flaky terminal search"),
		sampleRunJSON("run_other", "completed", "gpt-4o", "write release notes"),
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/runs" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"runs": runs})
	}))
	defer ts.Close()

	outBuf, errBuf, restore := captureOutput(t)
	defer restore()

	origClient := requestHTTPClient
	requestHTTPClient = ts.Client()
	defer func() { requestHTTPClient = origClient }()

	code := runSearch([]string{"-base-url=" + ts.URL, "terminal"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%s)", code, errBuf.String())
	}
	output := outBuf.String()
	if !strings.Contains(output, "run_match") {
		t.Errorf("search output missing matching run:\n%s", output)
	}
	if strings.Contains(output, "run_other") {
		t.Errorf("search output included non-matching run:\n%s", output)
	}
}

func TestRunSearch_MatchesWorkflowRecap(t *testing.T) {
	match := sampleRunJSON("run_recap", "completed", "gpt-4o", "ordinary prompt")
	match["recap"] = map[string]any{
		"goal":                     "ordinary prompt",
		"changed_files":            []string{"internal/harness/workflow_recap.go"},
		"tests_run":                []string{"go test ./internal/harness"},
		"fix_pattern":              "rare coverage needle",
		"next_continuation_prompt": "Continue run_recap",
	}
	runs := []map[string]any{
		match,
		sampleRunJSON("run_other", "completed", "gpt-4o", "write release notes"),
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"runs": runs})
	}))
	defer ts.Close()

	outBuf, errBuf, restore := captureOutput(t)
	defer restore()

	origClient := requestHTTPClient
	requestHTTPClient = ts.Client()
	defer func() { requestHTTPClient = origClient }()

	code := runSearch([]string{"-base-url=" + ts.URL, "rare coverage needle"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%s)", code, errBuf.String())
	}
	output := outBuf.String()
	if !strings.Contains(output, "run_recap") {
		t.Errorf("search output missing recap match:\n%s", output)
	}
	if strings.Contains(output, "run_other") {
		t.Errorf("search output included non-matching run:\n%s", output)
	}
}

func TestPrintWorkflowRecapShowsSearchableTaskSummary(t *testing.T) {
	outBuf, _, restore := captureOutput(t)
	defer restore()

	printWorkflowRecap(&workflowRecap{
		Goal:                   "stabilize TUI run controls",
		ChangedFiles:           []string{"cmd/harnesscli/tui/model.go", "cmd/harnesscli/tui/api.go"},
		TestsRun:               []string{"go test ./cmd/harnesscli/tui"},
		FailureCause:           "coveragegate found an uncovered run-control seam",
		FixPattern:             "added regression coverage before implementation",
		UsefulCommands:         []string{"go test ./cmd/harnesscli/tui", "./scripts/test-regression.sh"},
		NextContinuationPrompt: "Continue run run_123",
	})

	output := outBuf.String()
	for _, want := range []string{
		"Recap:",
		"Goal: stabilize TUI run controls",
		"Changed files: cmd/harnesscli/tui/model.go, cmd/harnesscli/tui/api.go",
		"Tests run: go test ./cmd/harnesscli/tui",
		"Failure cause: coveragegate found an uncovered run-control seam",
		"Fix pattern: added regression coverage before implementation",
		"Useful commands: go test ./cmd/harnesscli/tui, ./scripts/test-regression.sh",
		"Next: Continue run run_123",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("recap output missing %q:\n%s", want, output)
		}
	}
}

func TestDispatch_DailyAliasesRouted(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/runs":
			_ = json.NewEncoder(w).Encode(map[string]any{"runs": []any{}})
		case "/v1/runs/run_show":
			_ = json.NewEncoder(w).Encode(sampleRunJSON("run_show", "completed", "gpt-4o", "done"))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()

	origClient := requestHTTPClient
	requestHTTPClient = ts.Client()
	defer func() { requestHTTPClient = origClient }()

	_, _, restore := captureOutput(t)
	defer restore()

	if code := dispatch([]string{"runs", "-base-url=" + ts.URL}); code != 0 {
		t.Fatalf("dispatch runs returned %d", code)
	}
	if code := dispatch([]string{"show", "-base-url=" + ts.URL, "run_show"}); code != 0 {
		t.Fatalf("dispatch show returned %d", code)
	}
}
