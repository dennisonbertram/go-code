package harnessacp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"go-agent-harness/internal/harnessmcp"
)

func TestAgentInitializeAdvertisesACPServerCapabilities(t *testing.T) {
	agent := NewAgent("http://example.test")
	got, err := agent.Initialize(context.Background(), acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber})
	if err != nil {
		t.Fatal(err)
	}
	if got.ProtocolVersion != acp.ProtocolVersionNumber || got.AgentInfo == nil || got.AgentInfo.Name != "go-code" {
		t.Fatalf("unexpected initialize response: %#v", got)
	}
	if got.AgentCapabilities.SessionCapabilities.Close == nil {
		t.Fatalf("close capability was not advertised: %#v", got.AgentCapabilities)
	}
}

func TestRequiredACPAgentLifecycleMethodsHaveDefinedBehavior(t *testing.T) {
	agent := NewAgent("http://example.test")
	agent.SetAgentConnection(nil)
	if _, err := agent.Authenticate(context.Background(), acp.AuthenticateRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.Logout(context.Background(), acp.LogoutRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.CloseSession(context.Background(), acp.CloseSessionRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.ListSessions(context.Background(), acp.ListSessionsRequest{}); err == nil {
		t.Fatal("list sessions unexpectedly supported")
	}
	if _, err := agent.ResumeSession(context.Background(), acp.ResumeSessionRequest{}); err == nil {
		t.Fatal("resume unexpectedly supported")
	}
	if _, err := agent.SetSessionConfigOption(context.Background(), acp.SetSessionConfigOptionRequest{}); err == nil {
		t.Fatal("config unexpectedly supported")
	}
	if _, err := agent.SetSessionMode(context.Background(), acp.SetSessionModeRequest{}); err == nil {
		t.Fatal("mode unexpectedly supported")
	}
	if err := agent.Cancel(context.Background(), acp.CancelNotification{SessionId: "unknown"}); err != nil {
		t.Fatal(err)
	}
}

func TestPromptStartsRunAndMapsTerminalStopReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runs" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"run_id":"run-1"}`))
			return
		}
		if r.URL.Path == "/v1/runs/run-1/events" {
			_, _ = w.Write([]byte("event: run.completed\\ndata: {}\\n\\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	agent := NewAgent(server.URL)
	session, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/workspace", McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := agent.Prompt(context.Background(), acp.PromptRequest{SessionId: session.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("hello")}})
	if err != nil || got.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("prompt = %#v, %v", got, err)
	}
}

func TestPromptContinuesExistingRunOnSecondTurn(t *testing.T) {
	started, continued := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/runs":
			started++
			_, _ = w.Write([]byte(`{"run_id":"run-1"}`))
		case "/v1/runs/run-1/continue":
			continued++
			_, _ = w.Write([]byte(`{"run_id":"run-1"}`))
		case "/v1/runs/run-1/events":
			_, _ = w.Write([]byte("event: run.completed\ndata: {}\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	agent := NewAgent(server.URL)
	session, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/workspace", McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"first", "second"} {
		if _, err := agent.Prompt(context.Background(), acp.PromptRequest{SessionId: session.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock(text)}}); err != nil {
			t.Fatal(err)
		}
	}
	if started != 1 || continued != 1 {
		t.Fatalf("starts=%d continues=%d", started, continued)
	}
}

func TestProjectEventStreamsMessageAndThoughtChunks(t *testing.T) {
	agent := NewAgent("http://example.test")
	var updates []acp.SessionUpdate
	agent.update = func(_ context.Context, _ acp.SessionId, update acp.SessionUpdate) error {
		updates = append(updates, update)
		return nil
	}
	if err := agent.projectEvent(context.Background(), "s", harnessmcp.RunEvent{Type: "assistant.message.delta", Data: map[string]any{"text": "hello"}}); err != nil {
		t.Fatal(err)
	}
	if err := agent.projectEvent(context.Background(), "s", harnessmcp.RunEvent{Type: "assistant.thinking.delta", Data: map[string]any{"text": "think"}}); err != nil {
		t.Fatal(err)
	}
	if len(updates) != 2 || updates[0].AgentMessageChunk == nil || updates[1].AgentThoughtChunk == nil {
		t.Fatalf("updates = %#v", updates)
	}
}

func TestProjectEventProjectsToolLifecycle(t *testing.T) {
	agent := NewAgent("http://example.test")
	var updates []acp.SessionUpdate
	agent.update = func(_ context.Context, _ acp.SessionId, u acp.SessionUpdate) error {
		updates = append(updates, u)
		return nil
	}
	_ = agent.projectEvent(context.Background(), "s", harnessmcp.RunEvent{Type: "tool.call.started", Data: map[string]any{"call_id": "call-1", "tool": "read_file"}})
	_ = agent.projectEvent(context.Background(), "s", harnessmcp.RunEvent{Type: "tool.call.completed", Data: map[string]any{"call_id": "call-1", "result": "done"}})
	if len(updates) != 2 || updates[0].ToolCall == nil || updates[1].ToolCallUpdate == nil {
		t.Fatalf("updates = %#v", updates)
	}
}

func TestApprovalEventRequestsPermissionAndApprovesRun(t *testing.T) {
	var approved bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runs/run-1/approve" {
			approved = true
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	agent := NewAgent(server.URL)
	agent.permission = func(context.Context, acp.SessionId, string, string) (bool, error) { return true, nil }
	if err := agent.handleApproval(context.Background(), "s", "run-1", harnessmcp.RunEvent{Data: map[string]any{"call_id": "c", "tool": "shell"}}); err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("approval was not forwarded to harnessd")
	}
}

func TestApprovalEventRequestsPermissionAndDeniesRun(t *testing.T) {
	var denied bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runs/run-1/deny" {
			denied = true
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	agent := NewAgent(server.URL)
	agent.permission = func(context.Context, acp.SessionId, string, string) (bool, error) { return false, nil }
	if err := agent.handleApproval(context.Background(), "s", "run-1", harnessmcp.RunEvent{Data: map[string]any{"call_id": "c", "tool": "shell"}}); err != nil {
		t.Fatal(err)
	}
	if !denied {
		t.Fatal("denial was not forwarded to harnessd")
	}
}

func TestCancelForwardsActiveSessionRunToHarnessd(t *testing.T) {
	var cancelled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runs/run-1/cancel" && r.Method == http.MethodPost {
			cancelled = true
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	agent := NewAgent(server.URL)
	session, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/workspace", McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	agent.mu.Lock()
	s := agent.sessions[session.SessionId]
	s.runID = "run-1"
	agent.sessions[session.SessionId] = s
	agent.mu.Unlock()
	if err := agent.Cancel(context.Background(), acp.CancelNotification{SessionId: session.SessionId}); err != nil {
		t.Fatal(err)
	}
	if !cancelled {
		t.Fatal("cancel was not forwarded to harnessd")
	}
}

func TestProjectEventProjectsTodosAsPlan(t *testing.T) {
	agent := NewAgent("http://example.test")
	var got acp.SessionUpdate
	agent.update = func(_ context.Context, _ acp.SessionId, u acp.SessionUpdate) error { got = u; return nil }
	if err := agent.projectEvent(context.Background(), "s", harnessmcp.RunEvent{Type: "todos.updated", Data: map[string]any{"todos": []any{map[string]any{"text": "write tests", "status": "in_progress"}}}}); err != nil {
		t.Fatal(err)
	}
	if got.Plan == nil || len(got.Plan.Entries) != 1 {
		t.Fatalf("plan = %#v", got.Plan)
	}
}

// TestFakeACPClientPromptTurn is a key-free ACP client/server round trip using
// the same fake harnessd HTTP/SSE contract used by the fake provider smoke.
func TestFakeACPClientPromptTurn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/runs":
			_, _ = w.Write([]byte(`{"run_id":"fake-run"}`))
		case "/v1/runs/fake-run/events":
			_, _ = w.Write([]byte("event: assistant.message.delta\ndata: {\"text\":\"fake answer\"}\n\nevent: run.completed\ndata: {}\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	agent := NewAgent(server.URL)
	var updates []acp.SessionUpdate
	agent.update = func(_ context.Context, _ acp.SessionId, u acp.SessionUpdate) error {
		updates = append(updates, u)
		return nil
	}
	session, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/workspace", McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	response, err := agent.Prompt(context.Background(), acp.PromptRequest{SessionId: session.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("hello")}})
	if err != nil || response.StopReason != acp.StopReasonEndTurn || len(updates) != 1 || updates[0].AgentMessageChunk == nil {
		t.Fatalf("response=%#v updates=%#v err=%v", response, updates, err)
	}
}

func TestNewSessionCreatesStableHarnessConversation(t *testing.T) {
	agent := NewAgent("http://example.test")
	got, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/workspace", McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	conversationID, ok := agent.ConversationID(got.SessionId)
	if !ok || conversationID == "" {
		t.Fatalf("session %q has no harness conversation", got.SessionId)
	}
	if _, ok := agent.ConversationID(got.SessionId); !ok {
		t.Fatal("session registry was not stable")
	}
}
