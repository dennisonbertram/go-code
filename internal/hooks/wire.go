package hooks

import "encoding/json"

// Wire types for the JSON protocol between the harness and config-driven
// hooks. They are defined exactly once here and shared verbatim by the
// command adapter (stdin/stdout) and the HTTP adapter (request/response
// bodies). Field names are the documented public contract — see
// docs/design/plugins.md — and are pinned by golden tests.
//
// Protocol summary:
//   - The adapter sends one JSON object describing the lifecycle event.
//   - The hook replies with one JSON object (or empty output for
//     allow/no-op). Tool-use replies use decision fields; message-event
//     replies use action fields. Mutation of message requests/responses via
//     config hooks is not supported.

// toolUsePayload is sent to pre_tool_use and post_tool_use hooks. The
// Result/DurationMS/Error fields are populated only for post_tool_use.
type toolUsePayload struct {
	Event      string          `json:"event"` // pre_tool_use | post_tool_use
	RunID      string          `json:"run_id"`
	HookName   string          `json:"hook_name"`
	ToolName   string          `json:"tool_name"`
	CallID     string          `json:"call_id"`
	Args       json.RawMessage `json:"args"`
	Result     string          `json:"result,omitempty"`
	DurationMS int64           `json:"duration_ms,omitempty"`
	Error      string          `json:"error,omitempty"`
}

// preToolUseResponse is read from a pre_tool_use hook's output.
// Decision is "allow" or "deny"; empty means allow. ModifiedArgs, when
// present, replaces the tool call arguments.
type preToolUseResponse struct {
	Decision     string          `json:"decision"`
	Reason       string          `json:"reason"`
	ModifiedArgs json.RawMessage `json:"modified_args"`
}

// postToolUseResponse is read from a post_tool_use hook's output.
// ModifiedResult, when non-empty, replaces the tool result shown to the LLM.
type postToolUseResponse struct {
	ModifiedResult string `json:"modified_result"`
}

// Decision values for tool-use hook responses.
const (
	decisionAllow = "allow"
	decisionDeny  = "deny"
)

// messagePayload is sent to pre_message and post_message hooks. Full
// messages are included only when the hook def sets include_messages
// (payload-size guard); otherwise the hook sees model + message_count only.
// ResponseText/ToolCallCount are populated only for post_message.
type messagePayload struct {
	Event         string            `json:"event"` // pre_message | post_message
	RunID         string            `json:"run_id"`
	HookName      string            `json:"hook_name"`
	Step          int               `json:"step"`
	Model         string            `json:"model"`
	MessageCount  int               `json:"message_count"`
	Messages      []json.RawMessage `json:"messages,omitempty"`
	ResponseText  string            `json:"response_text,omitempty"`
	ToolCallCount int               `json:"tool_call_count,omitempty"`
}

// messageResponse is read from a pre_message or post_message hook's output.
// Action is "continue" or "block"; empty means continue.
type messageResponse struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
}

// Action values for message hook responses.
const (
	actionContinue = "continue"
	actionBlock    = "block"
)

// normalizeArgs keeps the args field valid JSON on the wire even when the
// runner passes empty arguments.
func normalizeArgs(args json.RawMessage) json.RawMessage {
	if len(args) == 0 {
		return json.RawMessage(`{}`)
	}
	return args
}
