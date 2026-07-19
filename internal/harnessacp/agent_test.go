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
