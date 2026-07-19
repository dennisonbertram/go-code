// Package harnessacp translates ACP requests into harnessd HTTP/SSE calls.
package harnessacp

import (
	"context"
	"fmt"
	"sync"

	acp "github.com/coder/acp-go-sdk"
)

// Agent is the ACP-facing adapter. It never embeds a harness runner.
type Agent struct {
	addr string
	mu   sync.Mutex
}

func NewAgent(addr string) *Agent { return &Agent{addr: addr} }

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
	return acp.NewSessionResponse{}, fmt.Errorf("ACP session/new is not implemented")
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
