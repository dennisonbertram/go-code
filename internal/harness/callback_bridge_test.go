package harness

import (
	"testing"
	"time"

	htools "go-agent-harness/internal/harness/tools"
)

type callbackBridgeStarter struct{}

func (callbackBridgeStarter) StartRun(_, _, _, _ string) error { return nil }

func TestRunnerNewCallbackManagerSchedulesWithBridge(t *testing.T) {
	t.Parallel()

	runner := NewRunner(&stubProvider{}, NewRegistry(), RunnerConfig{DefaultModel: "gpt-test"})
	manager := runner.NewCallbackManager(callbackBridgeStarter{})
	t.Cleanup(manager.Shutdown)

	info, err := manager.Set(htools.SetRequest{
		ConversationID: "conversation-1",
		Delay:          10 * time.Second,
		Prompt:         "check the eval run",
	})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if info.ConversationID != "conversation-1" {
		t.Fatalf("conversation id = %q, want conversation-1", info.ConversationID)
	}
	if info.State != htools.CallbackStatePending {
		t.Fatalf("state = %q, want %q", info.State, htools.CallbackStatePending)
	}
}
