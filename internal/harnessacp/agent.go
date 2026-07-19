// Package harnessacp translates ACP requests into harnessd HTTP/SSE calls.
package harnessacp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"

	acp "github.com/coder/acp-go-sdk"
)

// Agent is the ACP-facing adapter. It never embeds a harness runner.
type Agent struct {
	addr     string
	mu       sync.Mutex
	sessions map[acp.SessionId]session
}

type session struct{ conversationID, runID string }

func NewAgent(addr string) *Agent {
	return &Agent{addr: addr, sessions: make(map[acp.SessionId]session)}
}

func (a *Agent) SetAgentConnection(_ *acp.AgentSideConnection) {}

func (a *Agent) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentInfo:       &acp.Implementation{Name: "go-code", Version: "dev"},
		AgentCapabilities: acp.AgentCapabilities{SessionCapabilities: acp.SessionCapabilities{
			Close: &acp.SessionCloseCapabilities{},
		}},
	}, nil
}

func (a *Agent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}
func (a *Agent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}
func (a *Agent) Cancel(context.Context, acp.CancelNotification) error { return nil }
func (a *Agent) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, nil
}
func (a *Agent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionList)
}
func (a *Agent) NewSession(context.Context, acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	id, err := randomID()
	if err != nil {
		return acp.NewSessionResponse{}, err
	}
	conversationID, err := randomID()
	if err != nil {
		return acp.NewSessionResponse{}, err
	}
	sid := acp.SessionId(id)
	a.mu.Lock()
	a.sessions[sid] = session{conversationID: conversationID}
	a.mu.Unlock()
	return acp.NewSessionResponse{SessionId: sid}, nil
}

func (a *Agent) ConversationID(id acp.SessionId) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[id]
	return s.conversationID, ok
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random id: %w", err)
	}
	return hex.EncodeToString(b), nil
}
func (a *Agent) Prompt(context.Context, acp.PromptRequest) (acp.PromptResponse, error) {
	return acp.PromptResponse{}, fmt.Errorf("ACP session/prompt is not implemented")
}
func (a *Agent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionResume)
}
func (a *Agent) SetSessionConfigOption(context.Context, acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionSetConfigOption)
}
func (a *Agent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionSetMode)
}
