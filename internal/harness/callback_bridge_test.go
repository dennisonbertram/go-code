package harness

import (
	"context"
	"testing"
	"time"

	htools "go-agent-harness/internal/harness/tools"
)

func TestRunnerNewCallbackManagerEmitsScheduledEvent(t *testing.T) {
	t.Parallel()

	provider := &blockingCallbackProvider{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     1,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:         "keep callback stream open",
		ConversationID: "conv-callback",
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not start")
	}

	history, stream, cancelSub, err := runner.Subscribe(run.ID)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancelSub()

	manager := runner.NewCallbackManager(callbackStarterStub{})
	defer manager.Shutdown()

	info, err := manager.Set(htools.SetRequest{
		ConversationID: "conv-callback",
		Delay:          htools.MinCallbackDelay,
		Prompt:         "check status later",
	})
	if err != nil {
		t.Fatalf("Set callback: %v", err)
	}

	event := waitForCallbackEvent(t, history, stream, EventCallbackScheduled)
	if event.RunID != run.ID {
		t.Fatalf("callback event run id = %q, want %q", event.RunID, run.ID)
	}
	if event.Payload["callback_id"] != info.ID {
		t.Fatalf("callback_id = %v, want %s", event.Payload["callback_id"], info.ID)
	}
	if event.Payload["conversation_id"] != "conv-callback" {
		t.Fatalf("conversation_id = %v, want conv-callback", event.Payload["conversation_id"])
	}

	close(provider.release)
}

type blockingCallbackProvider struct {
	started chan struct{}
	release chan struct{}
}

func (p *blockingCallbackProvider) Complete(ctx context.Context, _ CompletionRequest) (CompletionResult, error) {
	select {
	case <-p.started:
	default:
		close(p.started)
	}
	select {
	case <-p.release:
		return CompletionResult{Content: "done"}, nil
	case <-ctx.Done():
		return CompletionResult{}, ctx.Err()
	}
}

type callbackStarterStub struct{}

func (callbackStarterStub) StartRun(string, string, string, string) error { return nil }

func waitForCallbackEvent(t *testing.T, history []Event, stream <-chan Event, want EventType) Event {
	t.Helper()
	for _, event := range history {
		if event.Type == want {
			return event
		}
	}
	timeout := time.After(2 * time.Second)
	for {
		select {
		case event, ok := <-stream:
			if !ok {
				t.Fatalf("stream closed before %s", want)
			}
			if event.Type == want {
				return event
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %s", want)
		}
	}
}
