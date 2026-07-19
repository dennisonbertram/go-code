package acp

import (
	"context"
	"encoding/json"
)

// Version is the agent implementation version reported in agentInfo. It is a
// var so builds can stamp it via -ldflags "-X go-agent-harness/internal/acp.Version=...".
var Version = "dev"

// initializeRequest is the params shape of the ACP `initialize` method
// (https://agentclientprotocol.com/protocol/initialization).
type initializeRequest struct {
	ProtocolVersion *int                `json:"protocolVersion"`
	ClientInfo      *implementationInfo `json:"clientInfo,omitempty"`
	// clientCapabilities is accepted but not acted on in this slice; it is
	// kept in the raw params for later slices (fs, terminal).
}

// implementationInfo identifies a protocol peer (agentInfo / clientInfo).
type implementationInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

// initializeResult is the result shape returned by `initialize`.
type initializeResult struct {
	ProtocolVersion   int                `json:"protocolVersion"`
	AgentCapabilities agentCapabilities  `json:"agentCapabilities"`
	AgentInfo         implementationInfo `json:"agentInfo"`
	AuthMethods       []authMethod       `json:"authMethods"`
}

// agentCapabilities advertises what this agent supports. loadSession is
// always false: session/load is out of scope for the epic. Only text and
// resource-link prompt content is supported (the ACP baseline), so all
// promptCapabilities flags are false.
type agentCapabilities struct {
	LoadSession        bool               `json:"loadSession"`
	PromptCapabilities promptCapabilities `json:"promptCapabilities"`
}

type promptCapabilities struct {
	Image           bool `json:"image"`
	Audio           bool `json:"audio"`
	EmbeddedContext bool `json:"embeddedContext"`
}

// authMethod describes one authentication method. None are supported in v1,
// so initialize always returns an empty (non-nil) slice.
type authMethod struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// handleInitialize negotiates the protocol version and returns the agent's
// capabilities. The agent supports exactly ProtocolVersion, so per the spec
// ("otherwise the Agent MUST respond with the latest version it supports") it
// always answers ProtocolVersion regardless of the requested value; a client
// that cannot speak it closes the connection.
func handleInitialize(_ context.Context, params json.RawMessage) (any, *rpcError) {
	var req initializeRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &rpcError{Code: CodeInvalidParams, Message: "Invalid params: " + err.Error()}
	}
	if req.ProtocolVersion == nil {
		return nil, &rpcError{Code: CodeInvalidParams, Message: "Invalid params: protocolVersion is required"}
	}
	return initializeResult{
		ProtocolVersion: ProtocolVersion,
		AgentCapabilities: agentCapabilities{
			LoadSession:        false,
			PromptCapabilities: promptCapabilities{Image: false, Audio: false, EmbeddedContext: false},
		},
		AgentInfo: implementationInfo{
			Name:    "go-code",
			Title:   "go-code",
			Version: Version,
		},
		AuthMethods: []authMethod{},
	}, nil
}
