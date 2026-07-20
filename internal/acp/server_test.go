package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// serveOnce drives a Server over the given input until EOF and returns the
// raw response lines (one per line) plus whatever was written to the
// diagnostic writer.
func serveOnce(t *testing.T, input string) (lines []string, diag string) {
	t.Helper()
	var out, logw bytes.Buffer
	s := NewServer(strings.NewReader(input), &out, &logw)
	if err := s.Serve(context.Background()); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	trimmed := strings.TrimRight(out.String(), "\n")
	if trimmed != "" {
		lines = strings.Split(trimmed, "\n")
	}
	return lines, logw.String()
}

// rpcResponse is a test-side decoding of a JSON-RPC response line.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decodeResponses(t *testing.T, lines []string) []rpcResponse {
	t.Helper()
	out := make([]rpcResponse, 0, len(lines))
	for _, ln := range lines {
		var r rpcResponse
		if err := json.Unmarshal([]byte(ln), &r); err != nil {
			t.Fatalf("response line is not valid JSON: %q: %v", ln, err)
		}
		if r.JSONRPC != "2.0" {
			t.Fatalf("response missing jsonrpc \"2.0\": %q", ln)
		}
		out = append(out, r)
	}
	return out
}

func TestServerInitializeReturnsCapabilities(t *testing.T) {
	lines, diag := serveOnce(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{"fs":{"readTextFile":true}}}}`+"\n")
	if diag != "" {
		t.Fatalf("unexpected diagnostics for a clean initialize: %q", diag)
	}
	resps := decodeResponses(t, lines)
	if len(resps) != 1 {
		t.Fatalf("got %d responses, want exactly 1", len(resps))
	}
	r := resps[0]
	if r.Error != nil {
		t.Fatalf("initialize returned error: %+v", r.Error)
	}
	if string(r.ID) != "1" {
		t.Fatalf("response id = %s, want 1 (echo of request id)", r.ID)
	}

	var result struct {
		ProtocolVersion   int `json:"protocolVersion"`
		AgentCapabilities struct {
			LoadSession        bool `json:"loadSession"`
			PromptCapabilities struct {
				Image           bool `json:"image"`
				Audio           bool `json:"audio"`
				EmbeddedContext bool `json:"embeddedContext"`
			} `json:"promptCapabilities"`
		} `json:"agentCapabilities"`
		AgentInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"agentInfo"`
		AuthMethods []any `json:"authMethods"`
	}
	if err := json.Unmarshal(r.Result, &result); err != nil {
		t.Fatalf("result is not an initialize result: %v (%s)", err, r.Result)
	}
	if result.ProtocolVersion != ProtocolVersion {
		t.Errorf("protocolVersion = %d, want %d", result.ProtocolVersion, ProtocolVersion)
	}
	if result.AgentCapabilities.LoadSession {
		t.Errorf("loadSession must be advertised as false (session/load is out of scope)")
	}
	pc := result.AgentCapabilities.PromptCapabilities
	if pc.Image || pc.Audio || pc.EmbeddedContext {
		t.Errorf("promptCapabilities must advertise text-only prompts (all false), got %+v", pc)
	}
	if result.AgentInfo.Name == "" {
		t.Errorf("agentInfo.name must be set")
	}
	if result.AuthMethods == nil {
		t.Errorf("authMethods must be present as an empty array, not null/omitted")
	}
	if len(result.AuthMethods) != 0 {
		t.Errorf("authMethods = %v, want empty (no auth required in v1)", result.AuthMethods)
	}
}

func TestServerInitializeVersionNegotiation(t *testing.T) {
	cases := []struct {
		name      string
		requested int
		want      int
	}{
		{"client at our version", 1, 1},
		{"client newer than us gets our latest", 7, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := `{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":` + jsonNumber(tc.requested) + `}}` + "\n"
			lines, _ := serveOnce(t, msg)
			resps := decodeResponses(t, lines)
			if len(resps) != 1 || resps[0].Error != nil {
				t.Fatalf("got %+v", resps)
			}
			var result struct {
				ProtocolVersion int `json:"protocolVersion"`
			}
			if err := json.Unmarshal(resps[0].Result, &result); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if result.ProtocolVersion != tc.want {
				t.Errorf("requested %d: protocolVersion = %d, want %d", tc.requested, result.ProtocolVersion, tc.want)
			}
		})
	}
}

func TestServerInitializeStringIDEchoed(t *testing.T) {
	lines, _ := serveOnce(t, `{"jsonrpc":"2.0","id":"zed-1","method":"initialize","params":{"protocolVersion":1}}`+"\n")
	resps := decodeResponses(t, lines)
	if len(resps) != 1 {
		t.Fatalf("got %d responses, want 1", len(resps))
	}
	if string(resps[0].ID) != `"zed-1"` {
		t.Fatalf("string id not echoed: %s", resps[0].ID)
	}
	if resps[0].Error != nil {
		t.Fatalf("unexpected error: %+v", resps[0].Error)
	}
}

func TestServerInitializeInvalidParams(t *testing.T) {
	cases := []struct {
		name string
		msg  string
	}{
		{"missing protocolVersion", `{"jsonrpc":"2.0","id":3,"method":"initialize","params":{}}`},
		{"missing params entirely", `{"jsonrpc":"2.0","id":3,"method":"initialize"}`},
		{"protocolVersion wrong type", `{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"protocolVersion":"one"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines, _ := serveOnce(t, tc.msg+"\n")
			resps := decodeResponses(t, lines)
			if len(resps) != 1 {
				t.Fatalf("got %d responses, want 1", len(resps))
			}
			if resps[0].Error == nil || resps[0].Error.Code != CodeInvalidParams {
				t.Fatalf("want -32602 invalid params, got %+v", resps[0].Error)
			}
			if string(resps[0].ID) != "3" {
				t.Fatalf("error response must echo request id, got %s", resps[0].ID)
			}
		})
	}
}

func TestServerMalformedJSONParseError(t *testing.T) {
	lines, _ := serveOnce(t, "{not json\n")
	resps := decodeResponses(t, lines)
	if len(resps) != 1 {
		t.Fatalf("got %d responses, want 1", len(resps))
	}
	if resps[0].Error == nil || resps[0].Error.Code != CodeParseError {
		t.Fatalf("want -32700 parse error, got %+v", resps[0].Error)
	}
	if string(resps[0].ID) != "null" {
		t.Fatalf("parse error id must be null, got %s", resps[0].ID)
	}
}

func TestServerInvalidRequest(t *testing.T) {
	cases := []struct {
		name string
		msg  string
	}{
		{"missing method", `{"jsonrpc":"2.0","id":5}`},
		{"missing jsonrpc member", `{"id":5,"method":"initialize"}`},
		{"wrong jsonrpc version", `{"jsonrpc":"1.0","id":5,"method":"initialize"}`},
		{"array instead of request object", `[1,2,3]`},
		{"bare string", `"hello"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines, _ := serveOnce(t, tc.msg+"\n")
			resps := decodeResponses(t, lines)
			if len(resps) != 1 {
				t.Fatalf("got %d responses, want 1", len(resps))
			}
			if resps[0].Error == nil || resps[0].Error.Code != CodeInvalidRequest {
				t.Fatalf("want -32600 invalid request, got line %q", lines[0])
			}
		})
	}
}

func TestServerUnknownMethod(t *testing.T) {
	lines, _ := serveOnce(t, `{"jsonrpc":"2.0","id":9,"method":"session/new","params":{}}`+"\n")
	resps := decodeResponses(t, lines)
	if len(resps) != 1 {
		t.Fatalf("got %d responses, want 1", len(resps))
	}
	if resps[0].Error == nil || resps[0].Error.Code != CodeMethodNotFound {
		t.Fatalf("want -32601 method not found, got %+v", resps[0].Error)
	}
	if string(resps[0].ID) != "9" {
		t.Fatalf("error must echo request id, got %s", resps[0].ID)
	}
}

func TestServerNotificationsGetNoResponse(t *testing.T) {
	// An unknown-method notification (no id) must not produce a response, but
	// must be logged to the diagnostic writer so stdout stays a clean protocol
	// channel. The following initialize proves the stream stayed aligned.
	input := `{"jsonrpc":"2.0","method":"session/cancel","params":{}}` + "\n" +
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}` + "\n"
	lines, diag := serveOnce(t, input)
	resps := decodeResponses(t, lines)
	if len(resps) != 1 {
		t.Fatalf("notification must produce no response; got %d responses: %v", len(resps), lines)
	}
	if string(resps[0].ID) != "1" || resps[0].Error != nil {
		t.Fatalf("initialize after notification broken: %+v", resps[0])
	}
	if !strings.Contains(diag, "session/cancel") {
		t.Fatalf("unknown-method notification should be logged to diagnostics, got %q", diag)
	}
}

func TestServerResponseShapedMessagesAreIgnored(t *testing.T) {
	// A client→agent JSON-RPC response (id + result, no method) — e.g. an
	// answer to a future session/request_permission call — must not be
	// answered with an error.
	input := `{"jsonrpc":"2.0","id":42,"result":{"outcome":"selected"}}` + "\n" +
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}` + "\n"
	lines, _ := serveOnce(t, input)
	resps := decodeResponses(t, lines)
	if len(resps) != 1 {
		t.Fatalf("response-shaped message must not be answered; got %d responses: %v", len(resps), lines)
	}
	if string(resps[0].ID) != "1" {
		t.Fatalf("wrong response: %+v", resps[0])
	}
}

// TestServerPipelinedRequestsAllAnswered replaces the slice-1 ordering test:
// handlers now run concurrently (so a mid-turn session/cancel can be read
// while session/prompt is open), which means pipelined responses may arrive
// in any order. JSON-RPC clients correlate responses by id, not position.
func TestServerPipelinedRequestsAllAnswered(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"bogus/method"}` + "\n" +
		"garbage\n" +
		`{"jsonrpc":"2.0","id":4,"method":"initialize","params":{"protocolVersion":1}}` + "\n"
	lines, _ := serveOnce(t, input)
	resps := decodeResponses(t, lines)
	if len(resps) != 4 {
		t.Fatalf("got %d responses, want 4: %v", len(resps), lines)
	}
	byID := map[string]rpcResponse{}
	for _, r := range resps {
		byID[string(r.ID)] = r
	}
	if r, ok := byID["1"]; !ok || r.Error != nil {
		t.Errorf("want initialize result id=1, got %+v", r)
	}
	if r, ok := byID["2"]; !ok || r.Error == nil || r.Error.Code != CodeMethodNotFound {
		t.Errorf("want -32601 id=2, got %+v", r)
	}
	if r, ok := byID["null"]; !ok || r.Error == nil || r.Error.Code != CodeParseError {
		t.Errorf("want -32700 id=null, got %+v", r)
	}
	if r, ok := byID["4"]; !ok || r.Error != nil {
		t.Errorf("want initialize result id=4, got %+v", r)
	}
}

// TestServerHandlersRunConcurrently proves a blocked handler does not stall
// later requests — the property session/cancel relies on while session/prompt
// is holding its response open.
func TestServerHandlersRunConcurrently(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	t.Cleanup(func() { inR.Close(); outR.Close(); outW.Close() })

	srv := NewServer(inR, outW, io.Discard)
	unblock := make(chan struct{})
	started := make(chan struct{})
	srv.Handle("test/block", func(ctx context.Context, params json.RawMessage) (any, *rpcError) {
		close(started)
		<-unblock
		return map[string]any{"done": true}, nil
	})

	c := newScriptedClient(t, srv, inW, outR)

	blockedID := c.send("test/block", nil)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked handler never started")
	}

	// While test/block is stuck, initialize must still be answered.
	initResp := c.request("initialize", map[string]any{"protocolVersion": 1})
	if initResp.Error != nil {
		t.Fatalf("initialize failed while handler blocked: %+v", initResp.Error)
	}

	close(unblock)
	blockedResp := c.readResponse()
	if string(blockedResp.ID) != fmt.Sprintf("%d", blockedID) || blockedResp.Error != nil {
		t.Fatalf("blocked request response = %+v, want result id=%d", blockedResp, blockedID)
	}
	c.close()
}

func TestServerOversizedMessageRejectedStreamStaysAligned(t *testing.T) {
	big := strings.Repeat("x", maxMessageSize+10)
	input := big + "\n" + `{"jsonrpc":"2.0","id":7,"method":"initialize","params":{"protocolVersion":1}}` + "\n"
	lines, _ := serveOnce(t, input)
	resps := decodeResponses(t, lines)
	if len(resps) != 2 {
		t.Fatalf("got %d responses, want 2: first %v", len(resps), lines[0][:min(len(lines[0]), 80)])
	}
	if resps[0].Error == nil || (resps[0].Error.Code != CodeInvalidRequest && resps[0].Error.Code != CodeParseError) {
		t.Fatalf("oversized message must be rejected with -32600 or -32700, got %+v", resps[0].Error)
	}
	if string(resps[1].ID) != "7" || resps[1].Error != nil {
		t.Fatalf("stream misaligned after oversized message: %+v", resps[1])
	}
}

func jsonNumber(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
