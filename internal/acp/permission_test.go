package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func TestPermissionParamsShape(t *testing.T) {
	params := permissionParams("sess_1", "call-1", "bash", `"ls -la"`)
	if params["sessionId"] != "sess_1" {
		t.Fatalf("sessionId = %v", params["sessionId"])
	}
	toolCall, _ := params["toolCall"].(map[string]any)
	if toolCall["toolCallId"] != "call-1" || toolCall["title"] != "bash" {
		t.Fatalf("toolCall = %v", toolCall)
	}
	options, _ := params["options"].([]map[string]any)
	if len(options) != 2 {
		t.Fatalf("options = %v, want exactly allow/reject", options)
	}
	allow, reject := options[0], options[1]
	if allow["optionId"] != "allow-once" || allow["kind"] != "allow_once" {
		t.Fatalf("allow option = %v", allow)
	}
	if reject["optionId"] != "reject-once" || reject["kind"] != "reject_once" {
		t.Fatalf("reject option = %v", reject)
	}
}

func TestParsePermissionOutcome(t *testing.T) {
	cases := []struct {
		name   string
		result string
		want   bool
	}{
		{"selected allow-once", `{"outcome":{"outcome":"selected","optionId":"allow-once"}}`, true},
		{"selected reject-once", `{"outcome":{"outcome":"selected","optionId":"reject-once"}}`, false},
		{"selected unknown option", `{"outcome":{"outcome":"selected","optionId":"allow-always-forever"}}`, false},
		{"cancelled", `{"outcome":{"outcome":"cancelled"}}`, false},
		{"empty result", `{}`, false},
		{"garbage", `{"outcome":42}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parsePermissionOutcome(json.RawMessage(tc.result)); got != tc.want {
				t.Fatalf("parsePermissionOutcome(%s) = %v, want %v", tc.result, got, tc.want)
			}
		})
	}
}

// TestCallClientRoutesResponses proves the server can call the editor and get
// the response routed back to the waiter by id.
func TestCallClientRoutesResponses(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	t.Cleanup(func() { inR.Close(); outR.Close(); outW.Close() })

	srv := NewServer(inR, outW, io.Discard)
	type callResult struct {
		result json.RawMessage
		rpcErr *rpcError
	}
	resultCh := make(chan callResult, 1)
	srv.Handle("test/call-editor", func(ctx context.Context, params json.RawMessage) (any, *rpcError) {
		result, rpcErr := srv.callClient(ctx, "session/request_permission", map[string]any{"sessionId": "s"})
		resultCh <- callResult{result, rpcErr}
		if rpcErr != nil {
			return nil, rpcErr
		}
		return map[string]any{"echo": json.RawMessage(result)}, nil
	})
	c := newScriptedClient(t, srv, inW, outR)

	reqID := c.send("test/call-editor", nil)
	permID, method, _ := c.readRequest()
	if method != "session/request_permission" {
		t.Fatalf("editor-facing method = %q", method)
	}
	c.respond(permID, map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "allow-once"}})

	select {
	case cr := <-resultCh:
		if cr.rpcErr != nil {
			t.Fatalf("callClient returned error: %+v", cr.rpcErr)
		}
		if !strings.Contains(string(cr.result), "allow-once") {
			t.Fatalf("callClient result = %s", cr.result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("callClient never returned")
	}

	resp := c.readResponse()
	if string(resp.ID) != fmt.Sprintf("%d", reqID) || resp.Error != nil {
		t.Fatalf("handler response = %+v", resp)
	}
	c.close()
}

// approvalEvent is the harness payload for tool.approval_required.
func approvalEvent(callID, tool, args, deadline string) string {
	b, _ := json.Marshal(map[string]any{
		"call_id": callID, "tool": tool, "arguments": args, "deadline_at": deadline,
	})
	return string(b)
}

// startPromptWithApproval drives a session to the point where harnessd asks
// for tool approval, and returns the permission request the agent issued.
func startPromptWithApproval(t *testing.T, c *scriptedClient, fh *fakeHarness, deadlineAt time.Time) (promptID int, permID json.RawMessage, permParams json.RawMessage) {
	t.Helper()
	initialize(t, c)
	sid := sessionNew(t, c)
	promptID = c.send("session/prompt", map[string]any{
		"sessionId": sid,
		"prompt":    []map[string]any{{"type": "text", "text": "do something risky"}},
	})
	waitFor(t, "run to be created", func() bool {
		fh.mu.Lock()
		defer fh.mu.Unlock()
		return len(fh.runs) == 1
	})
	run := fh.run("")
	go run.emit("tool.approval_required", approvalEvent("call-1", "bash", `"rm -rf /tmp/x"`, deadlineAt.UTC().Format(time.RFC3339)))

	permID, method, params := c.readRequest()
	if method != "session/request_permission" {
		t.Fatalf("method = %q, want session/request_permission", method)
	}
	return promptID, permID, params
}

func TestSessionPromptApprovalGrantedRunsToCompletion(t *testing.T) {
	fh := newFakeHarness(t)
	c := newSessionServer(t, fh, "")
	defer c.close()

	deadline := time.Now().Add(5 * time.Minute)
	promptID, permID, permParams := startPromptWithApproval(t, c, fh, deadline)

	// The permission request must describe the tool call and offer allow/reject.
	var params struct {
		SessionID string `json:"sessionId"`
		ToolCall  struct {
			ToolCallID string `json:"toolCallId"`
			Title      string `json:"title"`
		} `json:"toolCall"`
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	if err := json.Unmarshal(permParams, &params); err != nil {
		t.Fatalf("permission params: %v (%s)", err, permParams)
	}
	if params.ToolCall.ToolCallID != "call-1" || params.ToolCall.Title != "bash" {
		t.Fatalf("toolCall = %+v", params.ToolCall)
	}
	if len(params.Options) != 2 || params.Options[0].OptionID != "allow-once" || params.Options[0].Kind != "allow_once" ||
		params.Options[1].OptionID != "reject-once" || params.Options[1].Kind != "reject_once" {
		t.Fatalf("options = %+v", params.Options)
	}

	// Grant permission: the agent must POST /approve, and the run then completes.
	c.respond(permID, map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "allow-once"}})
	runID := fh.run("").id
	waitFor(t, "approve POST", func() bool { return fh.decision(runID) == "approve" })

	run := fh.run(runID)
	go func() {
		run.emit("tool.call.completed", `{"call_id":"call-1","tool":"bash","output":"done","duration_ms":5}`)
		run.finish("run.completed", `{"output":"all done"}`)
	}()

	// The tool_call_update notification precedes the prompt result.
	msg := c.readAny()
	if msg["method"] != "session/update" {
		t.Fatalf("expected tool_call_update notification before the result, got %v", msg)
	}
	noteParams, _ := msg["params"].(map[string]any)
	noteUpdate, _ := noteParams["update"].(map[string]any)
	if noteUpdate["sessionUpdate"] != "tool_call_update" || noteUpdate["toolCallId"] != "call-1" || noteUpdate["status"] != "completed" {
		t.Fatalf("update = %v, want tool_call_update completed for call-1", noteUpdate)
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
	if result.StopReason != "end_turn" {
		t.Fatalf("stopReason = %q, want end_turn", result.StopReason)
	}
}

func TestSessionPromptPermissionRejectedDenies(t *testing.T) {
	fh := newFakeHarness(t)
	c := newSessionServer(t, fh, "")
	defer c.close()
	_, permID, _ := startPromptWithApproval(t, c, fh, time.Now().Add(5*time.Minute))

	c.respond(permID, map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "reject-once"}})
	runID := fh.run("").id
	waitFor(t, "deny POST", func() bool { return fh.decision(runID) == "deny" })

	go fh.run(runID).finish("run.completed", `{"output":"denied path"}`)
	resp := c.readResponse()
	var result struct {
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("result shape: %v", err)
	}
	if result.StopReason != "end_turn" {
		t.Fatalf("stopReason = %q, want end_turn", result.StopReason)
	}
}

func TestSessionPromptPermissionOutcomeCancelledDenies(t *testing.T) {
	fh := newFakeHarness(t)
	c := newSessionServer(t, fh, "")
	defer c.close()
	_, permID, _ := startPromptWithApproval(t, c, fh, time.Now().Add(5*time.Minute))

	c.respond(permID, map[string]any{"outcome": map[string]any{"outcome": "cancelled"}})
	runID := fh.run("").id
	waitFor(t, "deny POST after cancelled outcome", func() bool { return fh.decision(runID) == "deny" })

	go fh.run(runID).finish("run.completed", `{"output":"over"}`)
	resp := c.readResponse()
	if resp.Error != nil {
		t.Fatalf("prompt errored: %+v", resp.Error)
	}
}

func TestSessionPromptPermissionClientErrorDenies(t *testing.T) {
	fh := newFakeHarness(t)
	c := newSessionServer(t, fh, "")
	defer c.close()
	_, permID, _ := startPromptWithApproval(t, c, fh, time.Now().Add(5*time.Minute))

	c.respondError(permID, -32603, "editor exploded")
	runID := fh.run("").id
	waitFor(t, "deny POST after client error", func() bool { return fh.decision(runID) == "deny" })

	go fh.run(runID).finish("run.completed", `{"output":"over"}`)
	resp := c.readResponse()
	if resp.Error != nil {
		t.Fatalf("prompt errored: %+v", resp.Error)
	}
}

// TestSessionPromptApprovalDeadlineExpires: when the approval deadline passes
// without an editor answer, the pending permission call is cancelled — no
// approve/deny is POSTed (harnessd auto-denies server-side) and a late
// response is ignored.
func TestSessionPromptApprovalDeadlineExpires(t *testing.T) {
	fh := newFakeHarness(t)
	c := newSessionServer(t, fh, "")
	defer c.close()

	deadline := time.Now().Add(300 * time.Millisecond)
	promptID, permID, _ := startPromptWithApproval(t, c, fh, deadline)
	runID := fh.run("").id

	// Let the deadline pass; the bridge must not POST any decision.
	time.Sleep(deadline.Sub(time.Now()) + 500*time.Millisecond)
	if got := fh.decision(runID); got != "" {
		t.Fatalf("decision %q POSTed after the deadline; want none", got)
	}

	// A late answer must be ignored (pending call deregistered) and the run
	// can still finish.
	c.respond(permID, map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "allow-once"}})
	time.Sleep(200 * time.Millisecond)
	if got := fh.decision(runID); got != "" {
		t.Fatalf("late answer produced a %q POST; the pending call must have been cancelled", got)
	}

	go fh.run(runID).finish("run.completed", `{"output":"timed out"}`)
	resp := c.readResponse()
	if string(resp.ID) != fmt.Sprintf("%d", promptID) {
		t.Fatalf("response id %s, want %d", resp.ID, promptID)
	}
	if resp.Error != nil {
		t.Fatalf("prompt errored after approval timeout: %+v", resp.Error)
	}
}

// TestSessionPromptApprovalNoBrokerSurfacesNote: when harnessd answers the
// approve POST with 501 (no approval broker configured), the bridge surfaces
// a session/update note instead of hanging until the deadline.
func TestSessionPromptApprovalNoBrokerSurfacesNote(t *testing.T) {
	fh := newFakeHarness(t)
	fh.noBroker = true
	c := newSessionServer(t, fh, "")
	defer c.close()

	// Long deadline: if the bridge hung for the deadline, the test would time out.
	_, permID, _ := startPromptWithApproval(t, c, fh, time.Now().Add(5*time.Minute))
	c.respond(permID, map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "allow-once"}})

	// Expect a session/update note explaining the missing broker.
	msg := c.readAny()
	if msg["method"] != "session/update" {
		t.Fatalf("expected a session/update note after 501, got %v", msg)
	}
	params, _ := msg["params"].(map[string]any)
	update, _ := params["update"].(map[string]any)
	if update["sessionUpdate"] != "agent_message_chunk" {
		t.Fatalf("update = %v, want agent_message_chunk note", update)
	}
	content, _ := update["content"].(map[string]any)
	text, _ := content["text"].(string)
	if !strings.Contains(text, "approval broker") {
		t.Fatalf("note text = %q, want it to mention the approval broker", text)
	}

	go fh.run("").finish("run.completed", `{"output":"over"}`)
	resp := c.readResponse()
	if resp.Error != nil {
		t.Fatalf("prompt errored: %+v", resp.Error)
	}
}
