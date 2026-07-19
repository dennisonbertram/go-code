package harness

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	runstore "go-agent-harness/internal/store"
)

func TestRunner_PruneCompletedRunsFromMemory(t *testing.T) {
	t.Parallel()

	runner := NewRunner(staticContentProvider{content: "done"}, NewRegistry(), RunnerConfig{
		DefaultModel:          "test-model",
		MaxSteps:              1,
		MaxCompletedRetention: 3,
		Store:                 runstore.NewMemoryStore(),
	})

	for i := 0; i < 8; i++ {
		run, err := runner.StartRun(RunRequest{Prompt: fmt.Sprintf("run %d", i)})
		if err != nil {
			t.Fatalf("start run %d: %v", i, err)
		}
		if _, err := collectRunEvents(t, runner, run.ID); err != nil {
			t.Fatalf("collect run %d events: %v", i, err)
		}
	}

	waitForRunnerPrune(t, runner, func() bool {
		runner.mu.RLock()
		defer runner.mu.RUnlock()
		return len(runner.runs) <= 3
	})
}

func TestRunner_PruneWaitsForTerminalEventPersistence(t *testing.T) {
	runner := NewRunner(staticContentProvider{content: "done"}, NewRegistry(), RunnerConfig{
		DefaultModel:          "test-model",
		MaxSteps:              1,
		MaxCompletedRetention: 1,
		Store:                 &terminalAppendFailStore{Store: runstore.NewMemoryStore()},
	})

	for i := 0; i < 3; i++ {
		run, err := runner.StartRun(RunRequest{Prompt: fmt.Sprintf("unpersisted %d", i)})
		if err != nil {
			t.Fatalf("StartRun: %v", err)
		}
		waitForStatus(t, runner, run.ID, RunStatusCompleted)
	}

	runner.mu.RLock()
	defer runner.mu.RUnlock()
	if got := len(runner.runs); got != 3 {
		t.Fatalf("pruned terminal runs before terminal events persisted: got %d, want 3", got)
	}
}

type terminalAppendFailStore struct{ runstore.Store }

func (s *terminalAppendFailStore) AppendEvent(_ context.Context, event *runstore.Event) error {
	if event.EventType == string(EventRunCompleted) {
		return errors.New("terminal event store unavailable")
	}
	return s.Store.AppendEvent(context.Background(), event)
}

func TestRunner_PruneKeepsCompletedRunWithActiveSubscriber(t *testing.T) {
	t.Parallel()

	runner := NewRunner(staticContentProvider{content: "done"}, NewRegistry(), RunnerConfig{
		DefaultModel:          "test-model",
		MaxSteps:              1,
		MaxCompletedRetention: 1,
		Store:                 runstore.NewMemoryStore(),
	})

	pinned, err := runner.StartRun(RunRequest{Prompt: "keep subscriber"})
	if err != nil {
		t.Fatalf("start pinned run: %v", err)
	}
	history, stream, cancelPinned, err := runner.Subscribe(pinned.ID)
	if err != nil {
		t.Fatalf("subscribe pinned run: %v", err)
	}
	if !hasTerminalEvent(history) {
		for ev := range stream {
			if IsTerminalEvent(ev.Type) {
				break
			}
		}
	}

	for i := 0; i < 3; i++ {
		run, err := runner.StartRun(RunRequest{Prompt: fmt.Sprintf("extra %d", i)})
		if err != nil {
			t.Fatalf("start extra run %d: %v", i, err)
		}
		if _, err := collectRunEvents(t, runner, run.ID); err != nil {
			t.Fatalf("collect extra run %d events: %v", i, err)
		}
	}

	runner.mu.RLock()
	_, ok := runner.runs[pinned.ID]
	runner.mu.RUnlock()
	if !ok {
		t.Fatal("completed run with an active subscriber was pruned")
	}

	cancelPinned()

	replacement, err := runner.StartRun(RunRequest{Prompt: "replacement"})
	if err != nil {
		t.Fatalf("start replacement run: %v", err)
	}
	if _, err := collectRunEvents(t, runner, replacement.ID); err != nil {
		t.Fatalf("collect replacement events: %v", err)
	}

	waitForRunnerPrune(t, runner, func() bool {
		runner.mu.RLock()
		defer runner.mu.RUnlock()
		_, stillPresent := runner.runs[pinned.ID]
		return !stillPresent && len(runner.runs) <= 1
	})
}

func TestRunner_PruneConversationMirrorFallsBackToPersistentStore(t *testing.T) {
	t.Parallel()

	store := newMemoryConversationStore()
	runner := NewRunner(staticContentProvider{content: "done"}, NewRegistry(), RunnerConfig{
		DefaultModel:             "test-model",
		MaxSteps:                 1,
		MaxCompletedRetention:    8,
		MaxConversationRetention: 2,
		ConversationStore:        store,
		Store:                    runstore.NewMemoryStore(),
	})

	const oldConvID = "conv-0"
	for i := 0; i < 5; i++ {
		run, err := runner.StartRun(RunRequest{
			Prompt:         fmt.Sprintf("conversation %d", i),
			ConversationID: fmt.Sprintf("conv-%d", i),
		})
		if err != nil {
			t.Fatalf("start run %d: %v", i, err)
		}
		if _, err := collectRunEvents(t, runner, run.ID); err != nil {
			t.Fatalf("collect run %d events: %v", i, err)
		}
	}

	waitForRunnerPrune(t, runner, func() bool {
		runner.mu.RLock()
		defer runner.mu.RUnlock()
		return len(runner.conversations) <= 2
	})

	runner.mu.RLock()
	_, inMemory := runner.conversations[oldConvID]
	runner.mu.RUnlock()
	if inMemory {
		t.Fatalf("%s still present in in-memory conversation mirror", oldConvID)
	}

	msgs, ok := runner.ConversationMessages(oldConvID)
	if !ok {
		t.Fatalf("%s should still load from the persistent conversation store", oldConvID)
	}
	if len(msgs) == 0 {
		t.Fatalf("%s loaded from store with no messages", oldConvID)
	}
}

func waitForRunnerPrune(t *testing.T, runner *Runner, done func() bool) {
	t.Helper()

	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if done() {
			return
		}
		select {
		case <-deadline:
			runner.mu.RLock()
			runCount := len(runner.runs)
			conversationCount := len(runner.conversations)
			runner.mu.RUnlock()
			t.Fatalf("timed out waiting for prune; runs=%d conversations=%d", runCount, conversationCount)
		case <-ticker.C:
		}
	}
}

type staticContentProvider struct {
	content string
}

func (p staticContentProvider) Complete(context.Context, CompletionRequest) (CompletionResult, error) {
	return CompletionResult{Content: p.content}, nil
}

type memoryConversationStore struct {
	mu       sync.Mutex
	messages map[string][]Message
	owners   map[string]*Conversation
}

func newMemoryConversationStore() *memoryConversationStore {
	return &memoryConversationStore{
		messages: make(map[string][]Message),
		owners:   make(map[string]*Conversation),
	}
}

func (s *memoryConversationStore) Migrate(context.Context) error { return nil }

func (s *memoryConversationStore) Close() error { return nil }

func (s *memoryConversationStore) SaveConversation(ctx context.Context, convID string, msgs []Message) error {
	return s.SaveConversationWithCost(ctx, convID, msgs, ConversationTokenCost{})
}

func (s *memoryConversationStore) SaveConversationWithCost(_ context.Context, convID string, msgs []Message, _ ConversationTokenCost) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.messages[convID] = copyMessages(msgs)
	now := time.Now().UTC()
	conv := s.owners[convID]
	if conv == nil {
		conv = &Conversation{ID: convID, CreatedAt: now}
		s.owners[convID] = conv
	}
	conv.UpdatedAt = now
	conv.MsgCount = len(msgs)
	return nil
}

func (s *memoryConversationStore) LoadMessages(_ context.Context, convID string) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyMessages(s.messages[convID]), nil
}

func (s *memoryConversationStore) ListConversations(context.Context, ConversationFilter, int, int) ([]Conversation, error) {
	return nil, nil
}

func (s *memoryConversationStore) DeleteConversation(_ context.Context, convID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.messages, convID)
	delete(s.owners, convID)
	return nil
}

func (s *memoryConversationStore) UpdateConversationMeta(_ context.Context, convID, workspace, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv := s.owners[convID]
	if conv == nil {
		conv = &Conversation{ID: convID, CreatedAt: time.Now().UTC()}
		s.owners[convID] = conv
	}
	conv.Workspace = workspace
	conv.TenantID = tenantID
	return nil
}

func (s *memoryConversationStore) GetConversationOwner(_ context.Context, convID string) (*Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv := s.owners[convID]
	if conv == nil {
		return nil, nil
	}
	out := *conv
	return &out, nil
}

func (s *memoryConversationStore) SearchMessages(context.Context, string, string, int) ([]MessageSearchResult, error) {
	return nil, nil
}

func (s *memoryConversationStore) DeleteOldConversations(context.Context, time.Time) (int, error) {
	return 0, nil
}

func (s *memoryConversationStore) PinConversation(context.Context, string, bool) error {
	return nil
}

func (s *memoryConversationStore) CompactConversation(context.Context, string, int, Message) error {
	return nil
}

func (s *memoryConversationStore) UndoPrompts(context.Context, string, int) (int, error) {
	return 0, nil
}

func (s *memoryConversationStore) ForkConversation(_ context.Context, srcID, newID string) (*Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.owners[srcID]
	if !ok {
		return nil, fmt.Errorf("fork: source conversation %q not found", srcID)
	}
	if _, taken := s.owners[newID]; taken {
		return nil, fmt.Errorf("fork: target conversation %q already exists", newID)
	}
	s.messages[newID] = copyMessages(s.messages[srcID])
	now := time.Now().UTC()
	fork := &Conversation{
		ID:        newID,
		Title:     src.Title,
		CreatedAt: now,
		UpdatedAt: now,
		MsgCount:  len(s.messages[newID]),
		Workspace: src.Workspace,
		TenantID:  src.TenantID,
	}
	s.owners[newID] = fork
	out := *fork
	return &out, nil
}
