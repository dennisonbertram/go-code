package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCsvListFlagStringEmpty(t *testing.T) {
	t.Parallel()
	var f csvListFlag
	if f.String() != "" {
		t.Fatalf("expected empty string, got %q", f.String())
	}
}

func TestCsvListFlagSet(t *testing.T) {
	t.Parallel()
	var f csvListFlag
	if err := f.Set("a, b, c"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if len(f.values) != 3 {
		t.Fatalf("expected 3 values, got %d: %v", len(f.values), f.values)
	}
	if f.values[0] != "a" || f.values[1] != "b" || f.values[2] != "c" {
		t.Fatalf("unexpected values: %v", f.values)
	}
}

func TestCsvListFlagSetSkipsEmpty(t *testing.T) {
	t.Parallel()
	var f csvListFlag
	if err := f.Set("a,,b, ,c"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if len(f.values) != 3 {
		t.Fatalf("expected 3 values (skipping blanks), got %d: %v", len(f.values), f.values)
	}
}

func TestCsvListFlagStringJoins(t *testing.T) {
	t.Parallel()
	f := csvListFlag{values: []string{"x", "y"}}
	if got := f.String(); got != "x,y" {
		t.Fatalf("expected 'x,y', got %q", got)
	}
}

func TestFormatAPIErrorWithCodeAndMessage(t *testing.T) {
	t.Parallel()
	body := []byte(`{"error":{"code":"invalid_request","message":"missing field"}}`)
	err := formatAPIError(400, body)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "invalid_request") {
		t.Fatalf("expected code in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "missing field") {
		t.Fatalf("expected message in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected status code in error, got %v", err)
	}
}

func TestFormatAPIErrorMessageOnly(t *testing.T) {
	t.Parallel()
	body := []byte(`{"error":{"message":"something failed"}}`)
	err := formatAPIError(500, body)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "something failed") {
		t.Fatalf("expected message, got %v", err)
	}
	// Should not contain parens around empty code.
	if strings.Contains(err.Error(), "()") {
		t.Fatalf("did not expect empty code parens, got %v", err)
	}
}

func TestFormatAPIErrorInvalidJSON(t *testing.T) {
	t.Parallel()
	body := []byte(`not json at all`)
	err := formatAPIError(502, body)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "not json at all") {
		t.Fatalf("expected raw body in error, got %v", err)
	}
}

func TestFormatAPIErrorEmptyBody(t *testing.T) {
	t.Parallel()
	err := formatAPIError(404, []byte(""))
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "Not Found") {
		t.Fatalf("expected HTTP status text for 404, got %v", err)
	}
}

func TestParseSSEBlockMissingEvent(t *testing.T) {
	t.Parallel()
	_, err := parseSSEBlock("data: {\"id\":\"1\"}")
	if err == nil {
		t.Fatalf("expected error for missing event field")
	}
	if !strings.Contains(err.Error(), "invalid sse block") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseSSEBlockMissingData(t *testing.T) {
	t.Parallel()
	_, err := parseSSEBlock("event: run.started")
	if err == nil {
		t.Fatalf("expected error for missing data field")
	}
	if !strings.Contains(err.Error(), "invalid sse block") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDecodeEventFallsBackToEnvelopeEvent(t *testing.T) {
	t.Parallel()
	// When the JSON data has no "type" field, decodeEvent should use the envelope Event.
	envelope := sseEnvelope{
		Event: "custom.event",
		Data:  `{"id":"e1","run_id":"r1"}`,
	}
	event, err := decodeEvent(envelope)
	if err != nil {
		t.Fatalf("decodeEvent: %v", err)
	}
	if string(event.Type) != "custom.event" {
		t.Fatalf("expected type 'custom.event', got %q", event.Type)
	}
}

func TestDecodeEventInvalidJSON(t *testing.T) {
	t.Parallel()
	_, err := decodeEvent(sseEnvelope{Event: "test", Data: "not-json"})
	if err == nil {
		t.Fatalf("expected error for invalid JSON")
	}
}

func TestStartRunMissingRunID(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"status":"queued"}`)
	}))
	defer ts.Close()

	_, err := startRun(context.Background(), ts.Client(), ts.URL, runCreateRequest{Prompt: "test"})
	if err == nil {
		t.Fatalf("expected error for missing run_id")
	}
	if !strings.Contains(err.Error(), "missing run_id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStreamRunEventsStreamEndsBeforeTerminal(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		// Send a non-terminal event, then close the stream.
		_, _ = io.WriteString(w, "event: run.started\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"e1\",\"run_id\":\"r1\",\"type\":\"run.started\"}\n\n")
	}))
	defer ts.Close()

	_, err := streamRunEvents(context.Background(), ts.Client(), ts.URL, "r1", io.Discard)
	if err == nil {
		t.Fatalf("expected error for stream ending before terminal event")
	}
	if !strings.Contains(err.Error(), "stream ended before terminal event") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStreamRunEventsTrailingBlockTerminal(t *testing.T) {
	t.Parallel()
	// Test the trailing block path: data is at EOF without a trailing blank line.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		// No trailing blank line after the last block.
		_, _ = io.WriteString(w, "event: run.completed\ndata: {\"id\":\"e1\",\"run_id\":\"r1\",\"type\":\"run.completed\"}")
	}))
	defer ts.Close()

	term, err := streamRunEvents(context.Background(), ts.Client(), ts.URL, "r1", io.Discard)
	if err != nil {
		t.Fatalf("streamRunEvents: %v", err)
	}
	if term != "run.completed" {
		t.Fatalf("expected terminal event 'run.completed', got %q", term)
	}
}

func TestStreamRunEventsIgnoresKeepaliveComments(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, ": ping\n\n")
		_, _ = io.WriteString(w, "event: run.completed\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"e1\",\"run_id\":\"r1\",\"type\":\"run.completed\"}\n\n")
	}))
	defer ts.Close()

	term, err := streamRunEvents(context.Background(), ts.Client(), ts.URL, "r1", io.Discard)
	if err != nil {
		t.Fatalf("streamRunEvents: %v", err)
	}
	if term != "run.completed" {
		t.Fatalf("expected terminal event 'run.completed', got %q", term)
	}
}

func TestRunBadFlagParse(t *testing.T) {
	origStdout := stdout
	origStderr := stderr
	defer func() {
		stdout = origStdout
		stderr = origStderr
	}()

	stdout = &bytes.Buffer{}
	var errOut bytes.Buffer
	stderr = &errOut

	code := run([]string{"-unknown-flag=bad"})
	if code != 1 {
		t.Fatalf("expected exit code 1 for bad flags, got %d", code)
	}
}

func TestRunWithNoExtensionFlags(t *testing.T) {
	// Test that run works when no extension flags are provided (extensions should be nil).
	var captured runCreateRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"run_id":"run_noext","status":"queued"}`)
	})
	mux.HandleFunc("/v1/runs/run_noext/events", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, "event: run.completed\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"e1\",\"run_id\":\"run_noext\",\"type\":\"run.completed\"}\n\n")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	origRequestClient := requestHTTPClient
	origStreamClient := streamHTTPClient
	origStdout := stdout
	origStderr := stderr
	defer func() {
		requestHTTPClient = origRequestClient
		streamHTTPClient = origStreamClient
		stdout = origStdout
		stderr = origStderr
	}()

	requestHTTPClient = ts.Client()
	streamHTTPClient = ts.Client()
	stdout = &bytes.Buffer{}
	stderr = &bytes.Buffer{}

	code := run([]string{"-base-url=" + ts.URL, "-prompt=simple"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if captured.PromptExtensions != nil {
		t.Fatalf("expected nil extensions, got %+v", captured.PromptExtensions)
	}
}

func TestRunWithSystemPromptAndModel(t *testing.T) {
	var captured runCreateRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"run_id":"run_sp","status":"queued"}`)
	})
	mux.HandleFunc("/v1/runs/run_sp/events", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, "event: run.completed\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"e1\",\"run_id\":\"run_sp\",\"type\":\"run.completed\"}\n\n")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	origRequestClient := requestHTTPClient
	origStreamClient := streamHTTPClient
	origStdout := stdout
	origStderr := stderr
	defer func() {
		requestHTTPClient = origRequestClient
		streamHTTPClient = origStreamClient
		stdout = origStdout
		stderr = origStderr
	}()

	requestHTTPClient = ts.Client()
	streamHTTPClient = ts.Client()
	stdout = &bytes.Buffer{}
	stderr = &bytes.Buffer{}

	code := run([]string{
		"-base-url=" + ts.URL,
		"-prompt=hello",
		"-model=gpt-5",
		"-system-prompt=be helpful",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if captured.Model != "gpt-5" {
		t.Fatalf("expected model gpt-5, got %q", captured.Model)
	}
	if captured.SystemPrompt != "be helpful" {
		t.Fatalf("expected system prompt 'be helpful', got %q", captured.SystemPrompt)
	}
}
