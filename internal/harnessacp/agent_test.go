package harnessacp

import (
	"context"
	"testing"

	acp "github.com/coder/acp-go-sdk"
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
