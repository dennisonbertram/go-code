package harness

import (
	"context"
	"sync"
	"testing"
	"time"
)

// blockingCancelProvider blocks until its release channel is closed or ctx is done.
// It respects context cancellation so that CancelRun can interrupt it.
type blockingCancelProvider struct {
	mu        sync.Mutex
	blockCh   chan struct{} // closed to signal "now blocking"
	releaseCh chan struct{} // closed to unblock
	calls     int
}

func newBlockingCancelProvider() *blockingCancelProvider {
	return &blockingCancelProvider{
		blockCh:   make(chan struct{}),
		releaseCh: make(chan struct{}),
	}
}

func (p *blockingCancelProvider) Complete(ctx context.Context, _ CompletionRequest) (CompletionResult, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.mu.Unlock()

	if idx == 0 {
		// Signal that we are now blocking inside the first call
		select {
		case <-p.blockCh:
			// already closed
		default:
			close(p.blockCh)
		}
		// Wait until released or context cancelled
		select {
		case <-p.releaseCh:
			return CompletionResult{Content: "done"}, nil
		case <-ctx.Done():
			return CompletionResult{}, ctx.Err()
		}
	}
	return CompletionResult{Content: "done"}, nil
}

// TestCancelRun_ActiveRun verifies that cancelling an in-flight run
// terminates it promptly and sets status to RunStatusCancelled.
func TestCancelRun_ActiveRun(t *testing.T) {
	t.Parallel()

	prov := newBlockingCancelProvider()
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     5,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Wait until the provider is blocking inside the first LLM call
	select {
	case <-prov.blockCh:
	case <-time.After(3 * time.Second):
		t.Fatal("provider never started blocking")
	}

	// Cancel the run
	if err := runner.CancelRun(run.ID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	// Wait for the run to reach a terminal state
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state, ok := runner.GetRun(run.ID)
		if !ok {
			t.Fatalf("run not found")
		}
		if state.Status == RunStatusCancelled {
			return // success
		}
		if state.Status == RunStatusCompleted || state.Status == RunStatusFailed {
			t.Fatalf("expected RunStatusCancelled, got %q", state.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	state, _ := runner.GetRun(run.ID)
	t.Fatalf("timed out waiting for RunStatusCancelled, last status: %q", state.Status)
}

// TestCancelRun_EmitsEvent verifies that a run.cancelled SSE event is emitted.
func TestCancelRun_EmitsEvent(t *testing.T) {
	t.Parallel()

	prov := newBlockingCancelProvider()
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     5,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Subscribe to events
	history, eventCh, cancelSub, subErr := runner.Subscribe(run.ID)
	if subErr != nil {
		t.Fatalf("Subscribe: %v", subErr)
	}
	_ = history

	// Wait until blocking
	select {
	case <-prov.blockCh:
	case <-time.After(3 * time.Second):
		t.Fatal("provider never started blocking")
	}

	// Cancel the run
	if err := runner.CancelRun(run.ID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	// Drain events looking for run.cancelled
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	found := false
	for {
		select {
		case evt, ok := <-eventCh:
			if !ok {
				goto done
			}
			if evt.Type == EventRunCancelled {
				found = true
			}
			if IsTerminalEvent(evt.Type) {
				cancelSub()
				goto done
			}
		case <-timer.C:
			cancelSub()
			goto done
		}
	}
done:
	if !found {
		t.Error("run.cancelled event not emitted")
	}
}

// TestCancelRun_WaitingForUser verifies that cancelling a run that is in
// waiting_for_user state unblocks it and produces RunStatusCancelled.
func TestCancelRun_WaitingForUser(t *testing.T) {
	t.Parallel()

	// We need a run that actually enters waiting_for_user. We simulate this
	// by using a scripted provider that returns an ask_user_question tool call,
	// and wiring up a broker.
	askProvider := &scriptedAskProvider{
		turns: []CompletionResult{
			{
				ToolCalls: []ToolCall{{
					ID:   "call-ask-1",
					Name: "AskUserQuestion",
					Arguments: mustJSON(map[string]any{
						"questions": []map[string]any{
							{
								"question": "Which path?",
								"header":   "Choose",
								"options": []map[string]any{
									{"label": "A", "description": "Option A"},
									{"label": "B", "description": "Option B"},
								},
								"multiSelect": false,
							},
						},
					}),
				}},
			},
			{Content: "done"}, // if we somehow continue
		},
	}

	broker := NewInMemoryAskUserQuestionBroker(time.Now)
	registry := NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{
		ApprovalMode:   ToolApprovalModeFullAuto,
		AskUserBroker:  broker,
		AskUserTimeout: 30 * time.Second,
	})

	runner := NewRunner(askProvider, registry, RunnerConfig{
		DefaultModel:   "test-model",
		MaxSteps:       5,
		AskUserTimeout: 30 * time.Second,
		AskUserBroker:  broker,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Wait for the run to enter waiting_for_user
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, ok := runner.GetRun(run.ID)
		if !ok {
			t.Fatalf("run not found")
		}
		if state.Status == RunStatusWaitingForUser {
			break
		}
		if state.Status == RunStatusCompleted || state.Status == RunStatusFailed || state.Status == RunStatusCancelled {
			t.Fatalf("run terminated early with status %q", state.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}

	st, _ := runner.GetRun(run.ID)
	if st.Status != RunStatusWaitingForUser {
		t.Fatalf("expected waiting_for_user, got %q", st.Status)
	}

	// Cancel while waiting for user
	if err := runner.CancelRun(run.ID); err != nil {
		t.Fatalf("CancelRun while waiting: %v", err)
	}

	// Should reach RunStatusCancelled
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state, ok := runner.GetRun(run.ID)
		if !ok {
			t.Fatalf("run not found after cancel")
		}
		if state.Status == RunStatusCancelled {
			return // success
		}
		if state.Status == RunStatusCompleted || state.Status == RunStatusFailed {
			t.Fatalf("expected RunStatusCancelled, got %q", state.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	state, _ := runner.GetRun(run.ID)
	t.Fatalf("timed out waiting for RunStatusCancelled after waiting_for_user, last status: %q", state.Status)
}

// TestCancelRun_DoubleCancelIdempotent verifies that cancelling the same run
// twice does not panic and returns nil on the second call.
func TestCancelRun_DoubleCancelIdempotent(t *testing.T) {
	t.Parallel()

	prov := newBlockingCancelProvider()
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     5,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Wait until blocking
	select {
	case <-prov.blockCh:
	case <-time.After(3 * time.Second):
		t.Fatal("provider never started blocking")
	}

	// First cancel
	if err := runner.CancelRun(run.ID); err != nil {
		t.Fatalf("first CancelRun: %v", err)
	}

	// Wait for terminal state
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state, ok := runner.GetRun(run.ID)
		if !ok {
			t.Fatalf("run not found")
		}
		if state.Status == RunStatusCancelled || state.Status == RunStatusCompleted || state.Status == RunStatusFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Second cancel should not panic and return nil (idempotent)
	if err := runner.CancelRun(run.ID); err != nil {
		t.Fatalf("second CancelRun (idempotent) returned error: %v", err)
	}
}

// TestCancelRun_NotFound verifies that cancelling a non-existent run returns ErrRunNotFound.
func TestCancelRun_NotFound(t *testing.T) {
	t.Parallel()

	runner := NewRunner(&stubProvider{}, NewRegistry(), RunnerConfig{MaxSteps: 2})
	err := runner.CancelRun("nonexistent-run-id")
	if err == nil {
		t.Fatal("expected error for non-existent run")
	}
	if err != ErrRunNotFound {
		t.Errorf("expected ErrRunNotFound, got: %v", err)
	}
}

// TestCancelRun_AlreadyTerminal verifies that cancelling a completed run
// is idempotent (returns nil, not an error).
func TestCancelRun_AlreadyTerminal(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{
		turns: []CompletionResult{{Content: "done"}},
	}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Wait for run to complete
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state, ok := runner.GetRun(run.ID)
		if !ok {
			t.Fatalf("run not found")
		}
		if state.Status == RunStatusCompleted || state.Status == RunStatusFailed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Cancel a completed run — should be idempotent
	if err := runner.CancelRun(run.ID); err != nil {
		t.Fatalf("CancelRun on completed run returned error: %v", err)
	}
}

// scriptedAskProvider returns scripted CompletionResults.
type scriptedAskProvider struct {
	mu    sync.Mutex
	turns []CompletionResult
	calls int
}

func (p *scriptedAskProvider) Complete(_ context.Context, _ CompletionRequest) (CompletionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls >= len(p.turns) {
		return CompletionResult{Content: "done"}, nil
	}
	out := p.turns[p.calls]
	p.calls++
	return out, nil
}
