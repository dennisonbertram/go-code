package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
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
