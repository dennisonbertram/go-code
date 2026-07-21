package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// tool.approval_required payload fields (internal/harness/events.go):
// call_id, tool, arguments, deadline_at (RFC3339).

// permissionParams builds the session/request_permission params for one
// tool.approval_required event. Two options are offered per the epic: allow
// this call once, or reject it.
func permissionParams(sessionID, callID, tool, arguments string) map[string]any {
	toolCall := map[string]any{
		"toolCallId": callID,
		"title":      tool,
	}
	if arguments != "" {
		var raw any
		if json.Unmarshal([]byte(arguments), &raw) == nil {
			toolCall["rawInput"] = raw
		}
	}
	return map[string]any{
		"sessionId": sessionID,
		"toolCall":  toolCall,
		"options": []map[string]any{
			{"optionId": "allow-once", "name": "Allow once", "kind": "allow_once"},
			{"optionId": "reject-once", "name": "Reject", "kind": "reject_once"},
		},
	}
}

// parsePermissionOutcome reports whether the editor's response grants the
// tool call. Only an explicit selection of allow-once grants; reject,
// cancelled, unknown options, and malformed results all deny (fail closed).
func parsePermissionOutcome(result json.RawMessage) bool {
	var r struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
		} `json:"outcome"`
	}
	if json.Unmarshal(result, &r) != nil {
		return false
	}
	return r.Outcome.Outcome == "selected" && r.Outcome.OptionID == "allow-once"
}

// bridgeApproval runs one tool.approval_required event through the editor and
// posts the decision back to harnessd. It returns nothing: failures are
// logged to diag, and a run whose approval expires is denied server-side at
// the deadline. Decisions (and the no-broker note) ride the turn's update
// queue so they stay ordered with the rest of the stream.
func (h *sessionHandlers) bridgeApproval(ctx context.Context, sessionID, runID string, ev runEvent, queue *updateQueue) {
	callID := payloadString(ev.Data, "call_id")
	tool := payloadString(ev.Data, "tool")
	arguments := payloadString(ev.Data, "arguments")
	deadlineStr := payloadString(ev.Data, "deadline_at")

	callCtx := ctx
	cancel := func() {}
	if deadline, err := time.Parse(time.RFC3339, deadlineStr); err == nil {
		callCtx, cancel = context.WithDeadline(ctx, deadline)
	}
	defer cancel()

	result, rpcErr := h.srv.callClient(callCtx, "session/request_permission", permissionParams(sessionID, callID, tool, arguments))
	if rpcErr != nil {
		if callCtx.Err() != nil {
			// Deadline passed or turn ended: harnessd auto-denies at the
			// deadline; there is nothing to POST.
			fmt.Fprintf(h.diag, "acp: approval for %s (%s) expired without an editor answer; harnessd will deny at the deadline\n", callID, tool)
			return
		}
		// The editor answered with a JSON-RPC error: deny (fail closed).
		fmt.Fprintf(h.diag, "acp: permission request for %s failed (%s); denying\n", callID, rpcErr.Message)
		h.postDecision(ctx, runID, "deny", callID, queue)
		return
	}

	if parsePermissionOutcome(result) {
		h.postDecision(ctx, runID, "approve", callID, queue)
	} else {
		h.postDecision(ctx, runID, "deny", callID, queue)
	}
}

// postDecision POSTs the approval decision, translating the 501 no-broker
// case into a session/update note instead of a silent hang.
func (h *sessionHandlers) postDecision(ctx context.Context, runID, action, callID string, queue *updateQueue) {
	var err error
	if action == "approve" {
		err = h.client.ApproveRun(ctx, runID)
	} else {
		err = h.client.DenyRun(ctx, runID)
	}
	switch {
	case err == nil:
		fmt.Fprintf(h.diag, "acp: %sd %s for run %s\n", action, callID, runID)
	case errors.Is(err, ErrApprovalNotConfigured):
		fmt.Fprintf(h.diag, "acp: %s %s: harnessd has no approval broker configured\n", action, callID)
		queue.push(map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content": map[string]any{
				"type": "text",
				"text": fmt.Sprintf("Tool call %s requires approval, but harnessd has no approval broker configured, so the decision cannot be delivered. The call will be denied when its approval deadline passes.", callID),
			},
		}, kindDelta)
	default:
		fmt.Fprintf(h.diag, "acp: %s %s: %v\n", action, callID, err)
	}
}
