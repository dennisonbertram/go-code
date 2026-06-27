package harness

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestRunnerPruneCompletedRunsFromMemory(t *testing.T) {
	t.Parallel()

	runner := NewRunner(&stubProvider{turns: []CompletionResult{
		{Content: "done-1"},
		{Content: "done-2"},
		{Content: "done-3"},
		{Content: "done-4"},
		{Content: "done-5"},
	}}, NewRegistry(), RunnerConfig{
		DefaultModel:          "test-model",
		MaxSteps:              2,
		MaxCompletedRetention: 2,
	})

	var runIDs []string
	for i := 0; i < 5; i++ {
		run, err := runner.StartRun(RunRequest{Prompt: fmt.Sprintf("run %d", i)})
		if err != nil {
			t.Fatalf("StartRun %d: %v", i, err)
		}
		if _, err := collectRunEvents(t, runner, run.ID); err != nil {
			t.Fatalf("collectRunEvents %d: %v", i, err)
		}
		runIDs = append(runIDs, run.ID)
	}

	waitForRunMapSize(t, runner, 2)

	for _, id := range runIDs[:3] {
		if _, ok := runner.GetRun(id); ok {
			t.Fatalf("old terminal run %s still retained beyond cap", id)
		}
	}
	for _, id := range runIDs[3:] {
		if run, ok := runner.GetRun(id); !ok {
			t.Fatalf("recent terminal run %s was pruned", id)
		} else if run.Status != RunStatusCompleted {
			t.Fatalf("recent run %s status = %q, want completed", id, run.Status)
		}
	}
}

func TestRunnerPruneKeepsTerminalRunsWithSubscribers(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	runner := NewRunner(&blockingProvider{blocker: release}, NewRegistry(), RunnerConfig{
		DefaultModel:          "test-model",
		MaxSteps:              2,
		MaxCompletedRetention: 1,
	})
	run1, err := runner.StartRun(RunRequest{Prompt: "keep subscribed"})
	if err != nil {
		t.Fatalf("StartRun run1: %v", err)
	}
	history, stream, cancel, err := runner.Subscribe(run1.ID)
	if err != nil {
		t.Fatalf("Subscribe run1: %v", err)
	}
	close(release)
	if _, err := waitForTerminalFromSubscription(history, stream); err != nil {
		t.Fatalf("wait run1 terminal: %v", err)
	}

	run2, err := runner.StartRun(RunRequest{Prompt: "prune around subscriber"})
	if err != nil {
		t.Fatalf("StartRun run2: %v", err)
	}
	if _, err := collectRunEvents(t, runner, run2.ID); err != nil {
		t.Fatalf("collectRunEvents run2: %v", err)
	}

	if _, ok := runner.GetRun(run1.ID); !ok {
		t.Fatal("subscribed terminal run was pruned before subscriber canceled")
	}

	cancel()
	waitForRunMapSize(t, runner, 1)
	if _, ok := runner.GetRun(run1.ID); ok {
		t.Fatal("subscribed terminal run remained after subscriber canceled")
	}
	if _, ok := runner.GetRun(run2.ID); !ok {
		t.Fatal("recent terminal run should remain after pruning subscriber")
	}
}

func TestRunnerPruneConversationMemoryMirror(t *testing.T) {
	t.Parallel()

	runner := NewRunner(&stubProvider{turns: []CompletionResult{
		{Content: "done-1"},
		{Content: "done-2"},
		{Content: "done-3"},
		{Content: "done-4"},
		{Content: "done-5"},
	}}, NewRegistry(), RunnerConfig{
		DefaultModel:             "test-model",
		MaxSteps:                 2,
		MaxCompletedRetention:    10,
		MaxConversationRetention: 2,
	})

	var convIDs []string
	for i := 0; i < 5; i++ {
		run, err := runner.StartRun(RunRequest{Prompt: fmt.Sprintf("conversation %d", i)})
		if err != nil {
			t.Fatalf("StartRun %d: %v", i, err)
		}
		if _, err := collectRunEvents(t, runner, run.ID); err != nil {
			t.Fatalf("collectRunEvents %d: %v", i, err)
		}
		convIDs = append(convIDs, run.ConversationID)
	}

	waitForConversationMapSize(t, runner, 2)

	for _, id := range convIDs[:3] {
		if _, ok := runner.ConversationMessages(id); ok {
			t.Fatalf("old conversation %s still retained beyond cap", id)
		}
	}
	for _, id := range convIDs[3:] {
		if _, ok := runner.ConversationMessages(id); !ok {
			t.Fatalf("recent conversation %s was pruned", id)
		}
	}
}

func waitForTerminalFromSubscription(history []Event, stream <-chan Event) ([]Event, error) {
	events := append([]Event(nil), history...)
	if hasTerminalEvent(events) {
		return events, nil
	}
	timeout := time.After(4 * time.Second)
	for {
		select {
		case ev, ok := <-stream:
			if !ok {
				return events, nil
			}
			events = append(events, ev)
			if IsTerminalEvent(ev.Type) {
				return events, nil
			}
		case <-timeout:
			return nil, context.DeadlineExceeded
		}
	}
}

func waitForRunMapSize(t *testing.T, runner *Runner, wantMax int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for {
		runner.mu.RLock()
		got := len(runner.runs)
		runner.mu.RUnlock()
		if got <= wantMax {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("run map size remained above cap: got %d, want <= %d", got, wantMax)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForConversationMapSize(t *testing.T, runner *Runner, wantMax int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for {
		runner.mu.RLock()
		gotConversations := len(runner.conversations)
		gotOwners := len(runner.conversationOwners)
		runner.mu.RUnlock()
		if gotConversations <= wantMax && gotOwners <= wantMax {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("conversation maps remained above cap: conversations=%d owners=%d want <= %d", gotConversations, gotOwners, wantMax)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
