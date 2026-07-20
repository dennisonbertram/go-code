package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// swapACPStdio redirects the package-level stdin/stdout/stderr used by runACP
// and restores them when the test ends.
func swapACPStdio(t *testing.T, in string) (outBuf, errBuf *bytes.Buffer) {
	t.Helper()
	origStdin, origStdout, origStderr := stdin, stdout, stderr
	outBuf, errBuf = &bytes.Buffer{}, &bytes.Buffer{}
	stdin = strings.NewReader(in)
	stdout, stderr = outBuf, errBuf
	t.Cleanup(func() {
		stdin, stdout, stderr = origStdin, origStdout, origStderr
	})
	return outBuf, errBuf
}

func TestRunACP_InitializeOverStdio(t *testing.T) {
	outBuf, errBuf := swapACPStdio(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}`+"\n")

	code := runACP(nil)
	if code != 0 {
		t.Fatalf("runACP = %d, want 0; stderr: %s", code, errBuf.String())
	}

	// stdout must carry exactly one protocol message and nothing else.
	out := outBuf.String()
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("stdout must contain exactly one newline-terminated message, got %q", out)
	}
	var resp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimRight(out, "\n")), &resp); err != nil {
		t.Fatalf("stdout is not a JSON-RPC response: %v (%q)", err, out)
	}
	if resp.JSONRPC != "2.0" || resp.ID != 1 || resp.Error != nil {
		t.Fatalf("bad response: %q", out)
	}
	var result struct {
		ProtocolVersion   int `json:"protocolVersion"`
		AgentCapabilities struct {
			LoadSession bool `json:"loadSession"`
		} `json:"agentCapabilities"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("result shape: %v", err)
	}
	if result.ProtocolVersion != 1 || result.AgentCapabilities.LoadSession {
		t.Fatalf("bad initialize result: %q", resp.Result)
	}
}

func TestRunACP_ExitsCleanlyOnEOF(t *testing.T) {
	outBuf, errBuf := swapACPStdio(t, "")
	if code := runACP(nil); code != 0 {
		t.Fatalf("runACP on empty stdin = %d, want 0; stderr: %s", code, errBuf.String())
	}
	if outBuf.Len() != 0 {
		t.Fatalf("no input must produce no protocol output, got %q", outBuf.String())
	}
}

func TestRunACP_DiagnosticsGoToStderrNotStdout(t *testing.T) {
	// An unknown-method notification produces no protocol response; the
	// diagnostic note must land on stderr, never stdout.
	outBuf, errBuf := swapACPStdio(t,
		`{"jsonrpc":"2.0","method":"session/cancel","params":{}}`+"\n")
	if code := runACP(nil); code != 0 {
		t.Fatalf("runACP = %d, want 0; stderr: %s", code, errBuf.String())
	}
	if outBuf.Len() != 0 {
		t.Fatalf("notification must not write to stdout, got %q", outBuf.String())
	}
	if !strings.Contains(errBuf.String(), "session/cancel") {
		t.Fatalf("expected a stderr diagnostic about the unknown notification, got %q", errBuf.String())
	}
}

func TestDispatch_ACPRoutedToRunACP(t *testing.T) {
	outBuf, errBuf := swapACPStdio(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}`+"\n")
	code := dispatch([]string{"acp"})
	if code != 0 {
		t.Fatalf("dispatch(acp) = %d, want 0; stderr: %s", code, errBuf.String())
	}
	// Proof we reached runACP and not the default run() path: stdout carries
	// an ACP initialize result.
	if !strings.Contains(outBuf.String(), `"agentCapabilities"`) {
		t.Fatalf("dispatch(acp) did not serve the ACP handshake; stdout: %q", outBuf.String())
	}
}

// --- Slice 2: session flow against a fake harnessd ---

// acpFakeHarness is a minimal harnessd double for the CLI-level ACP tests:
// POST /v1/runs, GET /v1/runs/{id}/events, POST /v1/runs/{id}/cancel.
type acpFakeHarness struct {
	*httptest.Server
	mu       sync.Mutex
	prompt   string
	auth     string
	runID    string
	cancelCh chan string
	events   chan string // terminal event type to emit
	created  chan struct{}
}

func newACPFakeHarness(t *testing.T) *acpFakeHarness {
	t.Helper()
	fh := &acpFakeHarness{
		cancelCh: make(chan string, 1),
		events:   make(chan string),
		created:  make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/runs", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Prompt string `json:"prompt"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fh.mu.Lock()
		fh.prompt = body.Prompt
		fh.auth = r.Header.Get("Authorization")
		fh.runID = "run-1"
		fh.mu.Unlock()
		close(fh.created)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"run_id":"run-1","status":"running"}`)
	})
	mux.HandleFunc("GET /v1/runs/run-1/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "id: run-1:1\nevent: run.started\ndata: {\"type\":\"run.started\"}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case typ := <-fh.events:
			fmt.Fprintf(w, "id: run-1:2\nevent: %s\ndata: {\"type\":%q,\"payload\":{}}\n\n", typ, typ)
			if flusher != nil {
				flusher.Flush()
			}
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("POST /v1/runs/run-1/cancel", func(w http.ResponseWriter, r *http.Request) {
		fh.cancelCh <- "run-1"
		// Mirror harnessd: cancel terminates the run on the event stream.
		go func() { fh.events <- "run.cancelled" }()
		fmt.Fprint(w, `{"status":"cancelling"}`)
	})
	fh.Server = httptest.NewServer(mux)
	t.Cleanup(fh.Server.Close)
	return fh
}

// acpScript drives runACP through interactive pipes.
type acpScript struct {
	t      *testing.T
	inW    *io.PipeWriter
	out    *bufio.Reader
	done   chan int
	nextID int
}

func startACPScript(t *testing.T, args []string) *acpScript {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	origStdin, origStdout, origStderr := stdin, stdout, stderr
	errBuf := &bytes.Buffer{}
	stdin, stdout, stderr = inR, outW, errBuf
	t.Cleanup(func() {
		stdin, stdout, stderr = origStdin, origStdout, origStderr
		inR.Close()
		outR.Close()
		outW.Close()
	})
	s := &acpScript{t: t, inW: inW, out: bufio.NewReader(outR), done: make(chan int, 1)}
	go func() { s.done <- runACP(args) }()
	return s
}

func (s *acpScript) send(method string, params any) int {
	s.t.Helper()
	s.nextID++
	msg := map[string]any{"jsonrpc": "2.0", "id": s.nextID, "method": method}
	if params != nil {
		msg["params"] = params
	}
	b, _ := json.Marshal(msg)
	if _, err := s.inW.Write(append(b, '\n')); err != nil {
		s.t.Fatalf("write: %v", err)
	}
	return s.nextID
}

func (s *acpScript) notify(method string, params any) {
	s.t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if _, err := s.inW.Write(append(b, '\n')); err != nil {
		s.t.Fatalf("write notification: %v", err)
	}
}

func (s *acpScript) read() map[string]any {
	s.t.Helper()
	type res struct {
		line string
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		line, err := s.out.ReadString('\n')
		ch <- res{line, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			s.t.Fatalf("read: %v", r.err)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(strings.TrimRight(r.line, "\n")), &m); err != nil {
			s.t.Fatalf("bad response line %q: %v", r.line, err)
		}
		return m
	case <-time.After(15 * time.Second):
		s.t.Fatal("timed out waiting for response")
		return nil
	}
}

func (s *acpScript) close() {
	s.t.Helper()
	s.inW.Close()
	select {
	case code := <-s.done:
		if code != 0 {
			s.t.Fatalf("runACP = %d, want 0", code)
		}
	case <-time.After(15 * time.Second):
		s.t.Fatal("runACP did not return after stdin close")
	}
}

func (s *acpScript) newSession() string {
	s.t.Helper()
	s.send("session/new", map[string]any{"cwd": "/tmp", "mcpServers": []any{}})
	resp := s.read()
	if errObj, ok := resp["error"]; ok {
		s.t.Fatalf("session/new failed: %v", errObj)
	}
	result, _ := resp["result"].(map[string]any)
	sid, _ := result["sessionId"].(string)
	if sid == "" {
		s.t.Fatalf("session/new returned no sessionId: %v", resp)
	}
	return sid
}

func TestRunACP_SessionPromptAgainstFakeServer(t *testing.T) {
	fh := newACPFakeHarness(t)
	s := startACPScript(t, []string{"-server", fh.URL})

	s.send("initialize", map[string]any{"protocolVersion": 1})
	if resp := s.read(); resp["error"] != nil {
		t.Fatalf("initialize failed: %v", resp["error"])
	}
	sid := s.newSession()

	promptID := s.send("session/prompt", map[string]any{
		"sessionId": sid,
		"prompt":    []map[string]any{{"type": "text", "text": "build me a thing"}},
	})
	select {
	case <-fh.created:
	case <-time.After(10 * time.Second):
		t.Fatal("fake harnessd never received POST /v1/runs")
	}
	if fh.prompt != "build me a thing" {
		t.Fatalf("harnessd received prompt %q, want %q", fh.prompt, "build me a thing")
	}
	go func() { fh.events <- "run.completed" }()

	resp := s.read()
	if fmt.Sprintf("%v", resp["id"]) != fmt.Sprintf("%d", promptID) {
		t.Fatalf("response id %v, want %d", resp["id"], promptID)
	}
	result, _ := resp["result"].(map[string]any)
	if result["stopReason"] != "end_turn" {
		t.Fatalf("stopReason = %v, want end_turn (full response: %v)", result["stopReason"], resp)
	}
	s.close()
}

func TestRunACP_UsesConfigServerAndAPIKey(t *testing.T) {
	fh := newACPFakeHarness(t)

	// Write a config the way `harness auth login` would, in a temp HOME.
	tmpHome := t.TempDir()
	oldHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", oldHome) })
	os.Setenv("HOME", tmpHome)
	cfgDir := filepath.Join(tmpHome, ".harness")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]string{"server": fh.URL, "api_key": "cfg-secret"}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	// No -server flag: server URL and credentials must come from the config.
	s := startACPScript(t, nil)
	s.send("initialize", map[string]any{"protocolVersion": 1})
	s.read()
	sid := s.newSession()
	s.send("session/prompt", map[string]any{
		"sessionId": sid,
		"prompt":    []map[string]any{{"type": "text", "text": "go"}},
	})
	select {
	case <-fh.created:
	case <-time.After(10 * time.Second):
		t.Fatal("run request did not reach the config-specified server")
	}
	if fh.auth != "Bearer cfg-secret" {
		t.Fatalf("Authorization = %q, want Bearer cfg-secret", fh.auth)
	}
	go func() { fh.events <- "run.completed" }()
	resp := s.read()
	result, _ := resp["result"].(map[string]any)
	if result["stopReason"] != "end_turn" {
		t.Fatalf("stopReason = %v, want end_turn", result["stopReason"])
	}
	s.close()
}

func TestRunACP_CancelMidRunIssuesCancelPOST(t *testing.T) {
	fh := newACPFakeHarness(t)
	s := startACPScript(t, []string{"-server", fh.URL})

	s.send("initialize", map[string]any{"protocolVersion": 1})
	s.read()
	sid := s.newSession()
	promptID := s.send("session/prompt", map[string]any{
		"sessionId": sid,
		"prompt":    []map[string]any{{"type": "text", "text": "long running"}},
	})
	select {
	case <-fh.created:
	case <-time.After(10 * time.Second):
		t.Fatal("run never started")
	}

	s.notify("session/cancel", map[string]any{"sessionId": sid})

	select {
	case <-fh.cancelCh:
	case <-time.After(10 * time.Second):
		t.Fatal("cancel POST never reached harnessd")
	}

	resp := s.read()
	if fmt.Sprintf("%v", resp["id"]) != fmt.Sprintf("%d", promptID) {
		t.Fatalf("response id %v, want prompt id %d", resp["id"], promptID)
	}
	if resp["error"] != nil {
		t.Fatalf("cancelled prompt must not error, got %v", resp["error"])
	}
	result, _ := resp["result"].(map[string]any)
	if result["stopReason"] != "cancelled" {
		t.Fatalf("stopReason = %v, want cancelled", result["stopReason"])
	}
	s.close()
}
