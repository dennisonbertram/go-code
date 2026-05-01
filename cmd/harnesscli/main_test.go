package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-agent-harness/internal/harness"
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
		if len(args) != 1 || args[0] != "-prompt=test" {
			t.Fatalf("unexpected args: %v", args)
		}
		return 9
	}
	exitCode := -1
	exitFunc = func(code int) { exitCode = code }
	osArgs = []string{"harnesscli", "-prompt=test"}

	main()

	if exitCode != 9 {
		t.Fatalf("expected exit code 9, got %d", exitCode)
	}
}

func TestStartRunSendsExpectedPayload(t *testing.T) {
	var got runCreateRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"run_id":"run_123","status":"queued"}`)
	}))
	defer ts.Close()

	runID, err := startRun(context.Background(), ts.Client(), ts.URL, runCreateRequest{
		Prompt:       "write a file",
		Model:        "gpt-5-nano",
		SystemPrompt: "be concise",
	})
	if err != nil {
		t.Fatalf("startRun returned error: %v", err)
	}

	if runID != "run_123" {
		t.Fatalf("expected run id run_123, got %q", runID)
	}
	if got.Prompt != "write a file" {
		t.Fatalf("unexpected prompt: %q", got.Prompt)
	}
	if got.Model != "gpt-5-nano" {
		t.Fatalf("unexpected model: %q", got.Model)
	}
	if got.SystemPrompt != "be concise" {
		t.Fatalf("unexpected system_prompt: %q", got.SystemPrompt)
	}
}

func TestParseSSEBlockAndTerminalDetection(t *testing.T) {
	raw := "event: run.completed\ndata: {\"id\":\"ev_1\",\"run_id\":\"run_1\",\"type\":\"run.completed\",\"payload\":{\"output\":\"ok\"}}"
	envelope, err := parseSSEBlock(raw)
	if err != nil {
		t.Fatalf("parseSSEBlock returned error: %v", err)
	}
	if envelope.Event != "run.completed" {
		t.Fatalf("unexpected envelope event: %q", envelope.Event)
	}

	event, err := decodeEvent(envelope)
	if err != nil {
		t.Fatalf("decodeEvent returned error: %v", err)
	}
	if event.Type != "run.completed" {
		t.Fatalf("unexpected event type: %q", event.Type)
	}
	if !harness.IsTerminalEvent(event.Type) {
		t.Fatalf("expected terminal event type")
	}

	multiData := "event: assistant.message\ndata: {\"id\":\"x\"\ndata: ,\"run_id\":\"r\",\"type\":\"assistant.message\"}"
	envelope, err = parseSSEBlock(multiData)
	if err != nil {
		t.Fatalf("parseSSEBlock with multiline data returned error: %v", err)
	}
	if !strings.Contains(envelope.Data, "assistant.message") {
		t.Fatalf("expected multiline data to join, got %q", envelope.Data)
	}
}

func TestRunCreatesAndStreamsToCompletion(t *testing.T) {
	var got runCreateRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"run_id":"run_abc","status":"queued"}`)
	})
	mux.HandleFunc("/v1/runs/run_abc/events", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, "event: run.started\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"e1\",\"run_id\":\"run_abc\",\"type\":\"run.started\"}\n\n")
		_, _ = io.WriteString(w, "event: assistant.message\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"e2\",\"run_id\":\"run_abc\",\"type\":\"assistant.message\",\"payload\":{\"content\":\"hello\"}}\n\n")
		_, _ = io.WriteString(w, "event: run.completed\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"e3\",\"run_id\":\"run_abc\",\"type\":\"run.completed\"}\n\n")
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
	var out bytes.Buffer
	var errOut bytes.Buffer
	stdout = &out
	stderr = &errOut

	code := run([]string{
		"-base-url=" + ts.URL,
		"-prompt=build a small html page",
		"-model=gpt-5-nano",
		"-workspace=/tmp/harness-workspace",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%s)", code, errOut.String())
	}
	if !strings.Contains(out.String(), "run_id=run_abc") {
		t.Fatalf("expected run id output, got %q", out.String())
	}
	if !strings.Contains(out.String(), "run.completed") {
		t.Fatalf("expected run.completed output, got %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", errOut.String())
	}
	if got.WorkspacePath != "/tmp/harness-workspace" {
		t.Fatalf("workspace_path = %q, want /tmp/harness-workspace", got.WorkspacePath)
	}
}

func TestNewTUIConfigIncludesWorkspace(t *testing.T) {
	cfg := newTUIConfig("http://127.0.0.1:8080", "/tmp/tui-project")
	if cfg.BaseURL != "http://127.0.0.1:8080" {
		t.Fatalf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.Workspace != "/tmp/tui-project" {
		t.Fatalf("Workspace = %q, want /tmp/tui-project", cfg.Workspace)
	}
	if !cfg.EnableTUI {
		t.Fatal("EnableTUI = false, want true")
	}
}

func TestRunCreateFailureReturnsErrorExit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"code":"invalid_request","message":"missing prompt"}}`)
	}))
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
	var errOut bytes.Buffer
	stderr = &errOut

	code := run([]string{"-base-url=" + ts.URL, "-prompt=bad"})
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(errOut.String(), "start run") {
		t.Fatalf("expected start run error, got %q", errOut.String())
	}
}

func TestRunStreamFailureReturnsErrorExit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/runs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"run_id":"run_abc","status":"queued"}`)
	})
	mux.HandleFunc("/v1/runs/run_abc/events", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"code":"boom","message":"stream failed"}}`)
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
	var errOut bytes.Buffer
	stderr = &errOut

	code := run([]string{"-base-url=" + ts.URL, "-prompt=hello"})
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(errOut.String(), "stream events") {
		t.Fatalf("expected stream events error, got %q", errOut.String())
	}
}

func TestRunRejectsMissingPrompt(t *testing.T) {
	origStdout := stdout
	origStderr := stderr
	defer func() {
		stdout = origStdout
		stderr = origStderr
	}()

	stdout = &bytes.Buffer{}
	var errOut bytes.Buffer
	stderr = &errOut

	code := run([]string{"-base-url=http://127.0.0.1:9999"})
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(errOut.String(), "prompt is required") {
		t.Fatalf("expected prompt required message, got %q", errOut.String())
	}
}

func TestStreamRunEventsRejectsInvalidSSEData(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, "event: run.started\n")
		_, _ = io.WriteString(w, "data: {not-json}\n\n")
	}))
	defer ts.Close()

	_, err := streamRunEvents(context.Background(), ts.Client(), ts.URL, "run_abc", io.Discard)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, errInvalidSSEData) {
		t.Fatalf("expected errInvalidSSEData, got %v", err)
	}
}
