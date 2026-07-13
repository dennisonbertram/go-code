package main

// main_robustness_test.go covers the CLI-client robustness cluster:
//   - (B) best-effort run cancellation when the streaming context is cancelled
//     (SIGINT/SIGTERM), factored into cancelRunOnInterrupt/handleStreamError
//     so it is testable without simulating real OS signals.
//   - (C) the SSE line reader tolerating an oversized event line without
//     breaking the rest of the stream.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// (B) SIGINT/SIGTERM: best-effort cancel of the active run
// ---------------------------------------------------------------------------

func TestCancelRunOnInterrupt_PostsCancelEndpoint(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cancelRunOnInterrupt(srv.Client(), srv.URL, "run-int-1")

	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %q", gotMethod)
	}
	if gotPath != "/v1/runs/run-int-1/cancel" {
		t.Errorf("expected /v1/runs/run-int-1/cancel, got %q", gotPath)
	}
}

func TestCancelRunOnInterrupt_ServerUnreachable_DoesNotPanic(t *testing.T) {
	// Best-effort: an unreachable server must not panic or hang the caller.
	cancelRunOnInterrupt(requestHTTPClient, "http://127.0.0.1:0", "run-int-x")
}

func TestHandleStreamError_ContextCancelled_IssuesCancelRequest(t *testing.T) {
	var cancelCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cancel") {
			cancelCalled = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	origStderr, origStdout := stderr, stdout
	var errBuf, outBuf bytes.Buffer
	stderr, stdout = &errBuf, &outBuf
	defer func() { stderr, stdout = origStderr, origStdout }()

	code := handleStreamError(ctx, srv.Client(), srv.URL, "run-int-2", errors.New("stream broke"))

	if code != 130 {
		t.Errorf("expected exit code 130 for interrupted run, got %d", code)
	}
	if !cancelCalled {
		t.Error("expected a POST /v1/runs/{id}/cancel request to be issued when the context is cancelled with an active run id")
	}
	if !strings.Contains(errBuf.String(), "interrupted") {
		t.Errorf("expected an interrupted message on stderr, got: %q", errBuf.String())
	}
}

func TestHandleStreamError_NotCancelled_ReportsErrorWithoutCancelling(t *testing.T) {
	var cancelCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/cancel") {
			cancelCalled = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	origStderr := stderr
	var errBuf bytes.Buffer
	stderr = &errBuf
	defer func() { stderr = origStderr }()

	code := handleStreamError(context.Background(), srv.Client(), srv.URL, "run-int-3", errors.New("network blip"))

	if code != 1 {
		t.Errorf("expected exit code 1 for a non-interrupt streaming error, got %d", code)
	}
	if cancelCalled {
		t.Error("must not cancel the run when no interrupt occurred (no active run should be touched)")
	}
	if !strings.Contains(errBuf.String(), "network blip") {
		t.Errorf("expected the original stream error to be reported, got: %q", errBuf.String())
	}
}

// ---------------------------------------------------------------------------
// (D) response bodies must be read through a bounded reader
// ---------------------------------------------------------------------------

func TestStartRun_ResponseBodyIsBounded(t *testing.T) {
	orig := maxResponseBodyBytes
	maxResponseBodyBytes = 10
	defer func() { maxResponseBodyBytes = orig }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"run_id":"run_123","status":"queued"}`)
	}))
	defer ts.Close()

	_, err := startRun(context.Background(), ts.Client(), ts.URL, runCreateRequest{Prompt: "x"})
	if err == nil {
		t.Fatal("expected a decode error: the response body must be truncated at maxResponseBodyBytes instead of read in full")
	}
}

// ---------------------------------------------------------------------------
// (C) SSE oversized line handling
// ---------------------------------------------------------------------------

func TestReadSSELine_NormalLine(t *testing.T) {
	r := bufio.NewReaderSize(strings.NewReader("hello\nworld\n"), 64)
	line, err := readSSELine(r, 1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if line != "hello" {
		t.Fatalf("expected %q, got %q", "hello", line)
	}
}

func TestReadSSELine_OversizedLine_TruncatesAndRealigns(t *testing.T) {
	long := strings.Repeat("x", 500)
	input := "short\n" + long + "\nafter\n"
	r := bufio.NewReaderSize(strings.NewReader(input), 64)

	line, err := readSSELine(r, 100)
	if err != nil || line != "short" {
		t.Fatalf("first line: got %q, %v", line, err)
	}

	line, err = readSSELine(r, 100)
	if !errors.Is(err, errSSELineTruncated) {
		t.Fatalf("expected errSSELineTruncated for the oversized line, got %v", err)
	}
	if len(line) != 100 {
		t.Fatalf("expected the returned line to be capped at 100 bytes, got %d", len(line))
	}

	line, err = readSSELine(r, 100)
	if err != nil || line != "after" {
		t.Fatalf("stream must realign to the next real line after an oversized one: got %q, %v", line, err)
	}
}

func TestStreamRunEvents_OversizedEventLine_SkipsEventButKeepsStreaming(t *testing.T) {
	origMax := maxSSELineSize
	maxSSELineSize = 200
	defer func() { maxSSELineSize = origMax }()

	oversized := strings.Repeat("z", 1000)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: assistant.message\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"e1\",\"run_id\":\"run_big\",\"type\":\"assistant.message\",\"payload\":{\"content\":\"before\"}}\n\n")
		_, _ = io.WriteString(w, "event: assistant.message\n")
		_, _ = io.WriteString(w, "data: "+oversized+"\n\n")
		_, _ = io.WriteString(w, "event: run.completed\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"e3\",\"run_id\":\"run_big\",\"type\":\"run.completed\"}\n\n")
	}))
	defer ts.Close()

	origStderr := stderr
	var errBuf bytes.Buffer
	stderr = &errBuf
	defer func() { stderr = origStderr }()

	var out bytes.Buffer
	terminal, err := streamRunEvents(context.Background(), ts.Client(), ts.URL, "run_big", &out)
	if err != nil {
		t.Fatalf("streamRunEvents returned error instead of skipping the oversized event: %v", err)
	}
	if terminal != "run.completed" {
		t.Fatalf("expected terminal event run.completed, got %q", terminal)
	}
	if !strings.Contains(out.String(), "before") {
		t.Errorf("expected the event before the oversized one to be surfaced; got output: %q", out.String())
	}
	if strings.Contains(out.String(), oversized) {
		t.Errorf("oversized event payload must not appear in surfaced output")
	}
	if !strings.Contains(errBuf.String(), "warning") {
		t.Errorf("expected a warning about the skipped oversized SSE line, got stderr: %q", errBuf.String())
	}
}
