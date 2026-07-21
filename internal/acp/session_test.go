package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// scriptedClient drives a Server over interactive pipes: requests are written
// line by line and responses read back as they arrive, so tests can stage
// mid-turn interactions (e.g. session/cancel while session/prompt is open).
type scriptedClient struct {
	t      *testing.T
	inW    *io.PipeWriter
	out    *bufio.Reader
	done   chan error
	nextID int
}

func newScriptedClient(t *testing.T, srv *Server, inW *io.PipeWriter, outR *io.PipeReader) *scriptedClient {
	t.Helper()
	c := &scriptedClient{
		t:    t,
		inW:  inW,
		out:  bufio.NewReaderSize(outR, 1024*1024),
		done: make(chan error, 1),
	}
	go func() { c.done <- srv.Serve(context.Background()) }()
	return c
}

// newSessionServer wires a Server to the fake harnessd and returns the
// scripted client plus pipe handles.
func newSessionServer(t *testing.T, fh *fakeHarness, apiKey string) *scriptedClient {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	t.Cleanup(func() { outR.Close(); outW.Close(); inR.Close() })
	srv := NewServer(inR, outW, io.Discard)
	srv.EnableSessions(NewRunsClient(fh.URL, apiKey))
	return newScriptedClient(t, srv, inW, outR)
}

func (c *scriptedClient) send(method string, params any) int {
	c.t.Helper()
	c.nextID++
	id := c.nextID
	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			c.t.Fatalf("marshal params: %v", err)
		}
		msg["params"] = json.RawMessage(b)
	}
	b, _ := json.Marshal(msg)
	if _, err := c.inW.Write(append(b, '\n')); err != nil {
		c.t.Fatalf("write request: %v", err)
	}
	return id
}

func (c *scriptedClient) notify(method string, params any) {
	c.t.Helper()
	msg := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	b, _ := json.Marshal(msg)
	if _, err := c.inW.Write(append(b, '\n')); err != nil {
		c.t.Fatalf("write notification: %v", err)
	}
}

// readResponse reads one response line, with a timeout so a wedged server
// fails the test instead of hanging it.
func (c *scriptedClient) readResponse() rpcResponse {
	c.t.Helper()
	type lineRes struct {
		line string
		err  error
	}
	ch := make(chan lineRes, 1)
	go func() {
		line, err := c.out.ReadString('\n')
		ch <- lineRes{line, err}
	}()
	select {
	case lr := <-ch:
		if lr.err != nil {
			c.t.Fatalf("read response: %v", lr.err)
		}
		var resp rpcResponse
		if err := json.Unmarshal([]byte(strings.TrimRight(lr.line, "\n")), &resp); err != nil {
			c.t.Fatalf("response line is not JSON: %q: %v", lr.line, err)
		}
		return resp
	case <-time.After(15 * time.Second):
		c.t.Fatal("timed out waiting for server response")
		return rpcResponse{}
	}
}

// request sends a request and reads its response, asserting the id echoes.
func (c *scriptedClient) request(method string, params any) rpcResponse {
	c.t.Helper()
	id := c.send(method, params)
	resp := c.readResponse()
	if string(resp.ID) != fmt.Sprintf("%d", id) {
		c.t.Fatalf("response id %s does not match request id %d", resp.ID, id)
	}
	return resp
}

// close closes stdin and asserts the server drains and exits cleanly.
func (c *scriptedClient) close() {
	c.t.Helper()
	c.inW.Close()
	select {
	case err := <-c.done:
		if err != nil {
			c.t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(15 * time.Second):
		c.t.Fatal("Serve did not return after stdin close")
	}
}

func initialize(t *testing.T, c *scriptedClient) {
	t.Helper()
	resp := c.request("initialize", map[string]any{"protocolVersion": 1})
	if resp.Error != nil {
		t.Fatalf("initialize failed: %+v", resp.Error)
	}
}

func sessionNew(t *testing.T, c *scriptedClient) string {
	t.Helper()
	resp := c.request("session/new", map[string]any{"cwd": "/tmp/work", "mcpServers": []any{}})
	if resp.Error != nil {
		t.Fatalf("session/new failed: %+v", resp.Error)
	}
	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("session/new result shape: %v (%s)", err, resp.Result)
	}
	if result.SessionID == "" {
		t.Fatal("session/new returned empty sessionId")
	}
	return result.SessionID
}

// waitFor polls cond until it holds or the timeout expires.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func TestExtractPromptText(t *testing.T) {
	cases := []struct {
		name    string
		blocks  []contentBlock
		want    string
		wantErr bool
	}{
		{"single text block", []contentBlock{{Type: "text", Text: "hello world"}}, "hello world", false},
		{"text blocks joined", []contentBlock{{Type: "text", Text: "a"}, {Type: "text", Text: "b"}}, "a\nb", false},
		{"resource link contributes its URI", []contentBlock{
			{Type: "text", Text: "check this"},
			{Type: "resource_link", URI: "file:///repo/main.go", Name: "main.go"},
		}, "check this\nfile:///repo/main.go", false},
		{"resource link alone", []contentBlock{{Type: "resource_link", URI: "file:///repo/main.go"}}, "file:///repo/main.go", false},
		{"unsupported block types are skipped", []contentBlock{
			{Type: "image"},
			{Type: "text", Text: "real"},
		}, "real", false},
		{"only unsupported blocks is an error", []contentBlock{{Type: "image"}}, "", true},
		{"empty prompt is an error", []contentBlock{}, "", true},
		{"blank text blocks are an error", []contentBlock{{Type: "text", Text: "  "}}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, rpcErr := extractPromptText(tc.blocks)
			if tc.wantErr {
				if rpcErr == nil || rpcErr.Code != CodeInvalidParams {
					t.Fatalf("want -32602, got text %q err %+v", got, rpcErr)
				}
				return
			}
			if rpcErr != nil {
				t.Fatalf("unexpected error: %+v", rpcErr)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStopReasonMapping(t *testing.T) {
	cases := []struct {
		name string
		out  terminalOutcome
		want string
	}{
		{"completed", terminalOutcome{eventType: "run.completed"}, "end_turn"},
		{"completed after cost limit", terminalOutcome{eventType: "run.completed", costLimit: true}, "max_turn_requests"},
		{"failed", terminalOutcome{eventType: "run.failed", errText: "boom"}, "refusal"},
		{"cancelled", terminalOutcome{eventType: "run.cancelled"}, "cancelled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stopReasonFor(tc.out); got != tc.want {
				t.Fatalf("stopReasonFor(%+v) = %q, want %q", tc.out, got, tc.want)
			}
		})
	}
}

func TestSessionNewReturnsUniqueIDs(t *testing.T) {
	fh := newFakeHarness(t)
	c := newSessionServer(t, fh, "")
	defer c.close()

	a, b := sessionNew(t, c), sessionNew(t, c)
	if a == b {
		t.Fatalf("session ids must be unique, got %q twice", a)
	}
}

func TestSessionPromptUnknownSession(t *testing.T) {
	fh := newFakeHarness(t)
	c := newSessionServer(t, fh, "")
	defer c.close()
	initialize(t, c)

	resp := c.request("session/prompt", map[string]any{
		"sessionId": "sess_does_not_exist",
		"prompt":    []map[string]any{{"type": "text", "text": "hi"}},
	})
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("want -32602 for unknown session, got %+v", resp.Error)
	}
}

func TestSessionPromptStartsRunAndCompletesEndTurn(t *testing.T) {
	fh := newFakeHarness(t)
	c := newSessionServer(t, fh, "secret-key")
	defer c.close()
	initialize(t, c)
	sid := sessionNew(t, c)

	respCh := make(chan rpcResponse, 1)
	go func() {
		respCh <- c.request("session/prompt", map[string]any{
			"sessionId": sid,
			"prompt": []map[string]any{
				{"type": "text", "text": "say hi"},
				{"type": "resource_link", "uri": "file:///repo/a.go", "name": "a.go"},
			},
		})
	}()

	// The run must exist (prompt blocked in flight) before we finish it.
	waitFor(t, "run to be created", func() bool {
		fh.mu.Lock()
		defer fh.mu.Unlock()
		return len(fh.runs) == 1
	})
	fh.mu.Lock()
	var runID string
	for id := range fh.runs {
		runID = id
	}
	fh.mu.Unlock()

	// Assert the extracted prompt text and bearer auth reached harnessd.
	if got := fh.promptOf(runID); got != "say hi\nfile:///repo/a.go" {
		t.Fatalf("harnessd received prompt %q, want %q", got, "say hi\nfile:///repo/a.go")
	}
	fh.mu.Lock()
	auth := fh.runAuths[runID]
	fh.mu.Unlock()
	if auth != "Bearer secret-key" {
		t.Fatalf("Authorization = %q, want Bearer secret-key", auth)
	}

	run := fh.run(runID)
	go func() {
		run.emit("run.started", "{}")
		run.finish("run.completed", `{"output":"hi there"}`)
	}()

	resp := <-respCh
	if resp.Error != nil {
		t.Fatalf("session/prompt failed: %+v", resp.Error)
	}
	var result struct {
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("result shape: %v (%s)", err, resp.Result)
	}
	if result.StopReason != "end_turn" {
		t.Fatalf("stopReason = %q, want end_turn", result.StopReason)
	}
}

func TestSessionPromptStopReasonVariants(t *testing.T) {
	cases := []struct {
		name   string
		finish func(run *fakeRun)
		want   string
	}{
		{"completed", func(run *fakeRun) { run.finish("run.completed", `{}`) }, "end_turn"},
		{"cost limited", func(run *fakeRun) {
			run.emit("run.cost_limit_reached", `{}`)
			run.finish("run.completed", `{}`)
		}, "max_turn_requests"},
		{"failed", func(run *fakeRun) { run.finish("run.failed", `{"error":"boom"}`) }, "refusal"},
		{"cancelled server-side", func(run *fakeRun) { run.finish("run.cancelled", `{}`) }, "cancelled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fh := newFakeHarness(t)
			c := newSessionServer(t, fh, "")
			defer c.close()
			initialize(t, c)
			sid := sessionNew(t, c)

			respCh := make(chan rpcResponse, 1)
			go func() {
				respCh <- c.request("session/prompt", map[string]any{
					"sessionId": sid,
					"prompt":    []map[string]any{{"type": "text", "text": "go"}},
				})
			}()
			waitFor(t, "run to be created", func() bool {
				fh.mu.Lock()
				defer fh.mu.Unlock()
				return len(fh.runs) == 1
			})
			go tc.finish(fh.run(""))

			resp := <-respCh
			if resp.Error != nil {
				t.Fatalf("session/prompt failed: %+v", resp.Error)
			}
			var result struct {
				StopReason string `json:"stopReason"`
			}
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				t.Fatalf("result shape: %v", err)
			}
			if result.StopReason != tc.want {
				t.Fatalf("stopReason = %q, want %q", result.StopReason, tc.want)
			}
		})
	}
}

func TestSessionPromptSecondPromptOnSameSessionRejected(t *testing.T) {
	fh := newFakeHarness(t)
	c := newSessionServer(t, fh, "")
	defer c.close()
	initialize(t, c)
	sid := sessionNew(t, c)

	respCh := make(chan rpcResponse, 1)
	go func() {
		respCh <- c.request("session/prompt", map[string]any{
			"sessionId": sid,
			"prompt":    []map[string]any{{"type": "text", "text": "go"}},
		})
	}()
	waitFor(t, "run to be created", func() bool {
		fh.mu.Lock()
		defer fh.mu.Unlock()
		return len(fh.runs) == 1
	})
	go fh.run("").finish("run.completed", `{}`)
	first := <-respCh
	if first.Error != nil {
		t.Fatalf("first prompt failed: %+v", first.Error)
	}

	second := c.request("session/prompt", map[string]any{
		"sessionId": sid,
		"prompt":    []map[string]any{{"type": "text", "text": "again"}},
	})
	if second.Error == nil {
		t.Fatalf("second prompt on a used session must fail, got result %s", second.Result)
	}
}

func TestSessionCancelMidRunCancelsAndRespondsCancelled(t *testing.T) {
	fh := newFakeHarness(t)
	c := newSessionServer(t, fh, "")
	defer c.close()
	initialize(t, c)
	sid := sessionNew(t, c)

	promptID := c.send("session/prompt", map[string]any{
		"sessionId": sid,
		"prompt":    []map[string]any{{"type": "text", "text": "long task"}},
	})
	waitFor(t, "run to be created", func() bool {
		fh.mu.Lock()
		defer fh.mu.Unlock()
		return len(fh.runs) == 1
	})
	runID := fh.run("").id

	// The fake harnessd answers cancel by terminating the run, like the real one.
	c.notify("session/cancel", map[string]any{"sessionId": sid})

	waitFor(t, "cancel POST to reach harnessd", func() bool { return fh.cancelled(runID) })

	resp := c.readResponse()
	if string(resp.ID) != fmt.Sprintf("%d", promptID) {
		t.Fatalf("response id %s, want prompt id %d", resp.ID, promptID)
	}
	if resp.Error != nil {
		t.Fatalf("cancelled prompt must not error (spec: return cancelled stop reason), got %+v", resp.Error)
	}
	var result struct {
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("result shape: %v", err)
	}
	if result.StopReason != "cancelled" {
		t.Fatalf("stopReason = %q, want cancelled", result.StopReason)
	}
}

// TestSessionCancelArrivingBeforeRunStartAppliesCancel pins the mid-start
// race: session/cancel arrives after session/prompt has begun (POST /v1/runs
// in flight) but before the run id is stored. The cancel must not be dropped
// — it is applied as soon as the run id is known.
func TestSessionCancelArrivingBeforeRunStartAppliesCancel(t *testing.T) {
	releasePost := make(chan struct{})
	postSeen := make(chan struct{})
	cancelSeen := make(chan string, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/runs", func(w http.ResponseWriter, r *http.Request) {
		close(postSeen)
		<-releasePost // hold the run creation until cancel has arrived
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"run_id":"run-gated","status":"running"}`)
	})
	mux.HandleFunc("POST /v1/runs/run-gated/cancel", func(w http.ResponseWriter, r *http.Request) {
		cancelSeen <- "run-gated"
		fmt.Fprint(w, `{"status":"cancelling"}`)
	})
	mux.HandleFunc("GET /v1/runs/run-gated/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "id: run-gated:1\nevent: run.cancelled\ndata: {\"type\":\"run.cancelled\"}\n\n")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	t.Cleanup(func() { inR.Close(); outR.Close(); outW.Close() })
	s := NewServer(inR, outW, io.Discard)
	s.EnableSessions(NewRunsClient(srv.URL, ""))
	c := newScriptedClient(t, s, inW, outR)
	defer c.close()

	initialize(t, c)
	sid := sessionNew(t, c)
	promptID := c.send("session/prompt", map[string]any{
		"sessionId": sid,
		"prompt":    []map[string]any{{"type": "text", "text": "go"}},
	})
	select {
	case <-postSeen:
	case <-time.After(10 * time.Second):
		t.Fatal("run creation POST never started")
	}

	// Cancel while the run creation is still in flight (no run id stored yet).
	c.notify("session/cancel", map[string]any{"sessionId": sid})
	close(releasePost)

	select {
	case got := <-cancelSeen:
		if got != "run-gated" {
			t.Fatalf("cancelled run %q, want run-gated", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancel POST was dropped when it arrived mid-start")
	}

	resp := c.readResponse()
	if string(resp.ID) != fmt.Sprintf("%d", promptID) {
		t.Fatalf("response id %s, want %d", resp.ID, promptID)
	}
	var result struct {
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("result shape: %v", err)
	}
	if result.StopReason != "cancelled" {
		t.Fatalf("stopReason = %q, want cancelled", result.StopReason)
	}
}

func TestConcurrentSessionsStayIsolated(t *testing.T) {
	fh := newFakeHarness(t)
	c := newSessionServer(t, fh, "")
	defer c.close()
	initialize(t, c)
	sidA := sessionNew(t, c)
	sidB := sessionNew(t, c)

	idA := c.send("session/prompt", map[string]any{
		"sessionId": sidA,
		"prompt":    []map[string]any{{"type": "text", "text": "task-A"}},
	})
	idB := c.send("session/prompt", map[string]any{
		"sessionId": sidB,
		"prompt":    []map[string]any{{"type": "text", "text": "task-B"}},
	})

	waitFor(t, "both runs to exist", func() bool {
		fh.mu.Lock()
		defer fh.mu.Unlock()
		return len(fh.runs) == 2
	})

	// Finish whichever run got prompt A as failed, the other as completed.
	fh.mu.Lock()
	var runA, runB *fakeRun
	for _, r := range fh.runs {
		if r.prompt == "task-A" {
			runA = r
		} else {
			runB = r
		}
	}
	fh.mu.Unlock()
	if runA == nil || runB == nil {
		t.Fatalf("could not map runs to prompts (A=%p B=%p)", runA, runB)
	}
	go runA.finish("run.failed", `{"error":"A exploded"}`)
	go runB.finish("run.completed", `{}`)

	got := map[string]string{}
	for i := 0; i < 2; i++ {
		resp := c.readResponse()
		if resp.Error != nil {
			t.Fatalf("prompt failed: %+v", resp.Error)
		}
		var result struct {
			StopReason string `json:"stopReason"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("result shape: %v", err)
		}
		got[string(resp.ID)] = result.StopReason
	}
	if got[fmt.Sprintf("%d", idA)] != "refusal" {
		t.Fatalf("session A stopReason = %q, want refusal", got[fmt.Sprintf("%d", idA)])
	}
	if got[fmt.Sprintf("%d", idB)] != "end_turn" {
		t.Fatalf("session B stopReason = %q, want end_turn", got[fmt.Sprintf("%d", idB)])
	}
}

// readAny reads one protocol line and decodes it generically, so tests can
// observe session/update notifications as well as responses.
func (c *scriptedClient) readAny() map[string]any {
	c.t.Helper()
	type lineRes struct {
		line string
		err  error
	}
	ch := make(chan lineRes, 1)
	go func() {
		line, err := c.out.ReadString('\n')
		ch <- lineRes{line, err}
	}()
	select {
	case lr := <-ch:
		if lr.err != nil {
			c.t.Fatalf("read: %v", lr.err)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(strings.TrimRight(lr.line, "\n")), &m); err != nil {
			c.t.Fatalf("line is not JSON: %q: %v", lr.line, err)
		}
		return m
	case <-time.After(15 * time.Second):
		c.t.Fatal("timed out waiting for server output")
		return nil
	}
}

// TestSessionPromptStreamsUpdatesInOrder is the slice-3 acceptance test: a
// scripted client performing session/prompt observes the exact ordered
// session/update notification stream — agent_message_chunk deltas, a thought
// chunk, tool_call, tool_call_update with a stable toolCallId — before the
// session/prompt result arrives.
func TestSessionPromptStreamsUpdatesInOrder(t *testing.T) {
	fh := newFakeHarness(t)
	c := newSessionServer(t, fh, "")
	defer c.close()
	initialize(t, c)
	sid := sessionNew(t, c)

	promptID := c.send("session/prompt", map[string]any{
		"sessionId": sid,
		"prompt":    []map[string]any{{"type": "text", "text": "go"}},
	})
	waitFor(t, "run to be created", func() bool {
		fh.mu.Lock()
		defer fh.mu.Unlock()
		return len(fh.runs) == 1
	})
	run := fh.run("")
	go func() {
		run.emit("run.started", "{}")
		run.emit("assistant.message.delta", `{"step":1,"content":"Hello"}`)
		run.emit("assistant.message.delta", `{"step":1,"content":", world"}`)
		run.emit("assistant.thinking.delta", `{"step":1,"content":"hmm"}`)
		run.emit("tool.call.started", `{"call_id":"call-1","tool":"bash","arguments":"ls"}`)
		run.emit("tool.call.completed", `{"call_id":"call-1","tool":"bash","output":"file.txt","duration_ms":5}`)
		run.finish("run.completed", `{"output":"done"}`)
	}()

	type wantUpdate struct {
		sessionUpdate string
		toolCallID    string
		status        string
		text          string
	}
	want := []wantUpdate{
		{sessionUpdate: "agent_message_chunk", text: "Hello"},
		{sessionUpdate: "agent_message_chunk", text: ", world"},
		{sessionUpdate: "agent_thought_chunk", text: "hmm"},
		{sessionUpdate: "tool_call", toolCallID: "call-1", status: "in_progress"},
		{sessionUpdate: "tool_call_update", toolCallID: "call-1", status: "completed"},
	}

	for i, w := range want {
		msg := c.readAny()
		if msg["method"] != "session/update" {
			t.Fatalf("message %d: method = %v, want session/update (msg: %v)", i, msg["method"], msg)
		}
		params, _ := msg["params"].(map[string]any)
		if params["sessionId"] != sid {
			t.Fatalf("message %d: sessionId = %v, want %s", i, params["sessionId"], sid)
		}
		update, _ := params["update"].(map[string]any)
		if update["sessionUpdate"] != w.sessionUpdate {
			t.Fatalf("message %d: sessionUpdate = %v, want %v (update: %v)", i, update["sessionUpdate"], w.sessionUpdate, update)
		}
		if w.toolCallID != "" && update["toolCallId"] != w.toolCallID {
			t.Fatalf("message %d: toolCallId = %v, want %v", i, update["toolCallId"], w.toolCallID)
		}
		if w.status != "" && update["status"] != w.status {
			t.Fatalf("message %d: status = %v, want %v", i, update["status"], w.status)
		}
		if w.text != "" {
			content, _ := update["content"].(map[string]any)
			if content["text"] != w.text {
				t.Fatalf("message %d: text = %v, want %q", i, content["text"], w.text)
			}
		}
	}

	// Only after every update has streamed may the prompt result arrive.
	msg := c.readAny()
	if msg["method"] != nil {
		t.Fatalf("expected the prompt response after the updates, got notification: %v", msg)
	}
	if fmt.Sprintf("%v", msg["id"]) != fmt.Sprintf("%d", promptID) {
		t.Fatalf("response id = %v, want %d", msg["id"], promptID)
	}
	result, _ := msg["result"].(map[string]any)
	if result["stopReason"] != "end_turn" {
		t.Fatalf("stopReason = %v, want end_turn (full msg: %v)", result["stopReason"], msg)
	}
}
