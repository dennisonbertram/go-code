package harness

import (
	"context"
	"testing"

	"go-agent-harness/internal/store"
)

type terminalOrderingStore struct {
	*store.MemoryStore
	terminalAppendStarted chan struct{}
	releaseTerminalAppend chan struct{}
}

func newTerminalOrderingStore() *terminalOrderingStore {
	return &terminalOrderingStore{
		MemoryStore:           store.NewMemoryStore(),
		terminalAppendStarted: make(chan struct{}),
		releaseTerminalAppend: make(chan struct{}),
	}
}

func (s *terminalOrderingStore) AppendEvent(ctx context.Context, ev *store.Event) error {
	if IsTerminalEvent(EventType(ev.EventType)) {
		select {
		case <-s.terminalAppendStarted:
		default:
			close(s.terminalAppendStarted)
		}
		<-s.releaseTerminalAppend
	}
	return s.MemoryStore.AppendEvent(ctx, ev)
}

func TestEventJournalDispatch_TerminalStoreAppendPrecedesSubscriberNotification(t *testing.T) {
	t.Parallel()

	st := newTerminalOrderingStore()
	runner := NewRunner(&stubProvider{}, NewRegistry(), RunnerConfig{
		DefaultModel: "test-model",
		Store:        st,
	})

	sub := make(chan Event, 1)
	state := &runState{
		run: Run{
			ID:             "run_terminal_order",
			ConversationID: "conv_terminal_order",
		},
		subscribers: map[chan Event]struct{}{
			sub: {},
		},
		nextEventSeq: 7,
	}

	journal := newEventJournal(runner)

	runner.mu.Lock()
	delivery, ok := journal.prepareLocked(state, state.run.ID, EventRunCompleted, map[string]any{
		"output": "done",
	})
	runner.mu.Unlock()
	if !ok {
		t.Fatal("prepareLocked returned ok=false for terminal event")
	}

	delivered := make(chan Event, 1)
	go func() {
		delivered <- <-sub
	}()

	go func() {
		journal.publishTerminal(delivery)
		journal.dispatch(delivery)
	}()

	select {
	case ev := <-delivered:
		t.Fatalf("subscriber observed terminal event %q before store append started", ev.Type)
	case <-st.terminalAppendStarted:
	}

	select {
	case ev := <-delivered:
		t.Fatalf("subscriber observed terminal event %q before terminal store append completed", ev.Type)
	default:
	}

	close(st.releaseTerminalAppend)

	ev := <-delivered
	if ev.Type != EventRunCompleted {
		t.Fatalf("subscriber event type = %q, want %q", ev.Type, EventRunCompleted)
	}
}
