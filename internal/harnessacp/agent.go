// Package harnessacp translates ACP requests into harnessd HTTP/SSE calls.
package harnessacp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"
	"go-agent-harness/internal/harnessmcp"
)

// Agent is the ACP-facing adapter. It never embeds a harness runner.
type Agent struct {
	addr       string
	client     *harnessmcp.HarnessClient
	conn       *acp.AgentSideConnection
	update     func(context.Context, acp.SessionId, acp.SessionUpdate) error
	permission func(context.Context, acp.SessionId, string, string) (bool, error)
	mu         sync.Mutex
	sessions   map[acp.SessionId]session
}

type session struct{ conversationID, runID string }

func NewAgent(addr string) *Agent {
	return &Agent{addr: addr, client: harnessmcp.NewHarnessClient(addr), sessions: make(map[acp.SessionId]session)}
}

func (a *Agent) SetAgentConnection(conn *acp.AgentSideConnection) {
	a.conn = conn
	a.update = func(ctx context.Context, id acp.SessionId, update acp.SessionUpdate) error {
		return conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: id, Update: update})
	}
}

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
func (a *Agent) Cancel(ctx context.Context, params acp.CancelNotification) error {
	a.mu.Lock()
	session, ok := a.sessions[params.SessionId]
	a.mu.Unlock()
	if !ok || session.runID == "" {
		return nil
	}
	return a.client.CancelRun(ctx, session.runID)
}
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
func (a *Agent) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error) {
	a.mu.Lock()
	s, ok := a.sessions[params.SessionId]
	a.mu.Unlock()
	if !ok {
		return acp.PromptResponse{}, fmt.Errorf("unknown ACP session %q", params.SessionId)
	}
	var parts []string
	for _, block := range params.Prompt {
		if block.Text != nil {
			parts = append(parts, block.Text.Text)
		}
	}
	var result harnessmcp.StartRunResponse
	var err error
	if s.runID == "" {
		result, err = a.client.StartRun(ctx, harnessmcp.StartRunRequest{Prompt: strings.Join(parts, "\n"), ConversationID: s.conversationID})
	} else {
		result, err = a.client.ContinueRun(ctx, s.runID, strings.Join(parts, "\n"))
	}
	if err != nil {
		return acp.PromptResponse{}, err
	}
	a.mu.Lock()
	s.runID = result.RunID
	a.sessions[params.SessionId] = s
	a.mu.Unlock()
	stop := acp.StopReasonEndTurn
	err = a.client.StreamRunEvents(ctx, result.RunID, func(event harnessmcp.RunEvent) error {
		if event.Type == "run.cancelled" {
			stop = acp.StopReasonCancelled
		}
		if event.Type == "tool.approval_required" {
			return a.handleApproval(ctx, params.SessionId, result.RunID, event)
		}
		return a.projectEvent(ctx, params.SessionId, event)
	})
	if err != nil {
		return acp.PromptResponse{}, err
	}
	return acp.PromptResponse{StopReason: stop}, nil
}

func (a *Agent) handleApproval(ctx context.Context, id acp.SessionId, runID string, event harnessmcp.RunEvent) error {
	callID, _ := event.Data["call_id"].(string)
	tool, _ := event.Data["tool"].(string)
	allow := false
	if a.permission != nil {
		var err error
		allow, err = a.permission(ctx, id, callID, tool)
		if err != nil {
			return err
		}
	} else if a.conn != nil {
		response, err := a.conn.RequestPermission(ctx, acp.RequestPermissionRequest{SessionId: id, ToolCall: acp.ToolCallUpdate{ToolCallId: acp.ToolCallId(callID)}, Options: []acp.PermissionOption{{OptionId: "approve", Name: "Approve", Kind: acp.PermissionOptionKindAllowOnce}, {OptionId: "deny", Name: "Deny", Kind: acp.PermissionOptionKindRejectOnce}}})
		if err != nil {
			return err
		}
		allow = response.Outcome.Selected != nil && response.Outcome.Selected.OptionId == "approve"
	}
	if allow {
		return a.client.ApproveRun(ctx, runID)
	}
	return a.client.DenyRun(ctx, runID)
}

func (a *Agent) projectEvent(ctx context.Context, id acp.SessionId, event harnessmcp.RunEvent) error {
	if a.update == nil {
		return nil
	}
	text, _ := event.Data["text"].(string)
	switch event.Type {
	case "assistant.message.delta":
		return a.update(ctx, id, acp.UpdateAgentMessageText(text))
	case "assistant.thinking.delta":
		return a.update(ctx, id, acp.UpdateAgentThoughtText(text))
	case "tool.call.started":
		callID, _ := event.Data["call_id"].(string)
		tool, _ := event.Data["tool"].(string)
		return a.update(ctx, id, acp.StartToolCall(acp.ToolCallId(callID), tool, acp.WithStartStatus(acp.ToolCallStatusInProgress), acp.WithStartRawInput(event.Data["arguments"])))
	case "tool.call.delta", "tool.output.delta":
		callID, _ := event.Data["call_id"].(string)
		return a.update(ctx, id, acp.UpdateToolCall(acp.ToolCallId(callID), acp.WithUpdateStatus(acp.ToolCallStatusInProgress)))
	case "tool.call.completed":
		callID, _ := event.Data["call_id"].(string)
		return a.update(ctx, id, acp.UpdateToolCall(acp.ToolCallId(callID), acp.WithUpdateStatus(acp.ToolCallStatusCompleted)))
	case "todos.updated":
		raw, _ := event.Data["todos"].([]any)
		entries := make([]acp.PlanEntry, 0, len(raw))
		for _, value := range raw {
			todo, _ := value.(map[string]any)
			text, _ := todo["text"].(string)
			status, _ := todo["status"].(string)
			entries = append(entries, acp.PlanEntry{Content: text, Status: acp.PlanEntryStatus(status)})
		}
		return a.update(ctx, id, acp.UpdatePlan(entries...))
	}
	return nil
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
