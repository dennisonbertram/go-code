// Package acp implements the Agent Client Protocol (ACP) transport for
// go-code: newline-delimited JSON-RPC 2.0 over stdio, as specified at
// https://agentclientprotocol.com/protocol/overview. Editors such as Zed
// spawn `harness acp` as a subprocess and drive it with methods like
// `initialize` and `session/prompt`.
//
// stdout is a pure protocol channel: only JSON-RPC messages are written to
// the outbound writer; all diagnostics go to the server's diagnostic writer
// (stderr in the real CLI).
package acp

import "encoding/json"

// ProtocolVersion is the ACP protocol major version this agent supports.
// The agent supports exactly one version, so it always answers `initialize`
// with this value; a client that cannot speak it closes the connection.
const ProtocolVersion = 1

// JSON-RPC 2.0 error codes (https://www.jsonrpc.org/specification#error_object).
const (
	// CodeParseError is returned when a line is not valid JSON.
	CodeParseError = -32700
	// CodeInvalidRequest is returned when a line is valid JSON but not a
	// valid JSON-RPC request object.
	CodeInvalidRequest = -32600
	// CodeMethodNotFound is returned when no handler is registered for the
	// requested method.
	CodeMethodNotFound = -32601
	// CodeInvalidParams is returned when a request's params fail validation.
	CodeInvalidParams = -32602
	// CodeInternalError is returned when the agent itself fails (e.g. the
	// harnessd runs API is unreachable or a session state invariant breaks).
	CodeInternalError = -32603
)

// request is the wire shape of a JSON-RPC 2.0 request or notification.
// ID is a pointer so that an absent "id" member (a notification) is
// distinguishable from an explicit `"id": null`.
type request struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params"`
}

// response is the wire shape of a JSON-RPC 2.0 response. Exactly one of
// Result or Error is set; ID echoes the request id (null when the request id
// could not be determined, e.g. a parse error).
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is the JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return e.Message }

// nullID is the response id used when the request id is unknown.
var nullID = json.RawMessage("null")

// notification is the wire shape of a JSON-RPC 2.0 notification the server
// sends (e.g. session/update): no id, no response expected.
type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// clientRequest is the wire shape of a JSON-RPC 2.0 request the server sends
// to the editor (e.g. session/request_permission): an answer is expected.
type clientRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}
