package harness

import (
	"testing"
	"time"

	htools "go-agent-harness/internal/harness/tools"
)

type callbackStarterFunc func(prompt, conversationID, tenantID, agentID string) error

func (f callbackStarterFunc) StartRun(prompt, conversationID, tenantID, agentID string) error {
	return f(prompt, conversationID, tenantID, agentID)
}

func TestRunnerNewCallbackManagerCreatesBoundManager(t *testing.T) {
	t.Parallel()

	runner := NewRunner(nil, nil, RunnerConfig{})
	mgr := runner.NewCallbackManager(callbackStarterFunc(func(_, _, _, _ string) error { return nil }))
	defer mgr.Shutdown()

	info, err := mgr.Set(htools.SetRequest{
		ConversationID: "conv-callback",
		Delay:          10 * time.Second,
		Prompt:         "continue later",
	})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if info.ConversationID != "conv-callback" {
		t.Fatalf("conversation id = %q, want conv-callback", info.ConversationID)
	}
	if info.State != htools.CallbackStatePending {
		t.Fatalf("state = %q, want pending", info.State)
	}
}
