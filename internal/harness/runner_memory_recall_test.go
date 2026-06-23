package harness

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	om "go-agent-harness/internal/observationalmemory"
)

// recallModelStub is a tiny in-package observationalmemory.Model used by the
// real observational-memory Service's ModelObserver. It returns a fixed
// observation that carries a unique sentinel string so the test can prove the
// turn-2 provider request received the turn-1 observation purely via cross-run
// recall (memory snippet injection), not via the transcript.
type recallModelStub struct {
	observation string
	calls       int
}

func (m *recallModelStub) Complete(_ context.Context, _ om.ModelRequest) (string, error) {
	m.calls++
	// IMPORTANCE prefix so ParseObservationChunks records a scored chunk.
	return "IMPORTANCE:0.9\n" + m.observation, nil
}

// TestCrossRunObservationalMemoryRecall wires a REAL observationalmemory.Service
// (SQLite store in t.TempDir, local coordinator, tiny model stub, ObserveMinTokens=1)
// into a runner. Turn 1 runs against a fixed tenant+conversation+agent and crosses
// the observe threshold so the model stub's observation is persisted to the
// scope's SQLite record. Turn 2 runs against the SAME three axes and we assert
// the captured provider request for turn 2 contains a SYSTEM message carrying
// the turn-1 observation (recall == injection at turn start). We also assert the
// scope's Snippet directly shows persistence across the two runs.
func TestCrossRunObservationalMemoryRecall(t *testing.T) {
	t.Parallel()

	// Unique sentinel that appears nowhere in the prompts or transcript — the
	// ONLY path for it to reach the turn-2 request is cross-run memory recall.
	const sentinel = "SENTINEL_RECALL_user_prefers_tabs_over_spaces_42"

	dbPath := filepath.Join(t.TempDir(), "obsmem.sqlite")
	store, err := om.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	model := &recallModelStub{observation: sentinel}

	mem, err := om.NewService(om.ServiceOptions{
		Mode:        om.ModeLocalCoordinator,
		Store:       store,
		Coordinator: om.NewLocalCoordinator(),
		Observer:    om.ModelObserver{Model: model},
		// No Reflector: keep ReflectThresholdTokens high so only a plain
		// observation is persisted (no reflection compression).
		DefaultEnabled: true,
		DefaultConfig: om.Config{
			ObserveMinTokens:       1,
			SnippetMaxTokens:       900,
			ReflectThresholdTokens: 1 << 30,
		},
	})
	if err != nil {
		t.Fatalf("new memory service: %v", err)
	}
	t.Cleanup(func() { _ = mem.Close() })

	scope := om.ScopeKey{
		TenantID:       "tenant-a",
		ConversationID: "conv-recall-1",
		AgentID:        "agent-x",
	}

	// capturingProvider records every CompletionRequest so we can inspect the
	// turn-2 messages. Each run completes after a single content turn.
	provider := &capturingProvider{turns: []CompletionResult{
		{Content: "first turn answer"},
		{Content: "second turn answer"},
	}}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:   "gpt-4.1-mini",
		MaxSteps:       2,
		MemoryManager:  mem,
		AskUserTimeout: time.Second,
	})

	// --- Turn 1: observe and persist. ---
	run1, err := runner.StartRun(RunRequest{
		Prompt:         "Please remember my formatting choices for this project.",
		TenantID:       scope.TenantID,
		ConversationID: scope.ConversationID,
		AgentID:        scope.AgentID,
	})
	if err != nil {
		t.Fatalf("start run 1: %v", err)
	}
	if _, err := collectRunEvents(t, runner, run1.ID); err != nil {
		t.Fatalf("collect run 1 events: %v", err)
	}

	// The observer must have been invoked at least once during turn 1.
	if model.calls == 0 {
		t.Fatalf("expected observer model to be called during turn 1, got 0 calls")
	}

	// Persistence reality check: the scope's Snippet (read straight from the
	// SQLite-backed service) must now carry the turn-1 observation.
	snippet, status, err := mem.Snippet(context.Background(), scope)
	if err != nil {
		t.Fatalf("snippet after turn 1: %v", err)
	}
	if status.ObservationCount == 0 {
		t.Fatalf("expected at least one persisted observation after turn 1, got status %+v", status)
	}
	if !strings.Contains(snippet, sentinel) {
		t.Fatalf("scope snippet does not carry the turn-1 observation; got %q", snippet)
	}

	// Reset captured requests so we only inspect turn-2 traffic.
	provider.mu.Lock()
	provider.calls = nil
	provider.mu.Unlock()

	// --- Turn 2: SAME tenant+conversation+agent — must recall via injection. ---
	run2, err := runner.StartRun(RunRequest{
		Prompt:         "What did I tell you earlier?",
		TenantID:       scope.TenantID,
		ConversationID: scope.ConversationID,
		AgentID:        scope.AgentID,
	})
	if err != nil {
		t.Fatalf("start run 2: %v", err)
	}
	if _, err := collectRunEvents(t, runner, run2.ID); err != nil {
		t.Fatalf("collect run 2 events: %v", err)
	}

	provider.mu.Lock()
	turn2Calls := append([]CompletionRequest(nil), provider.calls...)
	provider.mu.Unlock()

	if len(turn2Calls) == 0 {
		t.Fatalf("expected at least one provider call on turn 2")
	}

	// Assert: turn 2's first request carries a SYSTEM message containing the
	// turn-1 observation. This proves recall == injection at turn start.
	var recalled bool
	for _, msg := range turn2Calls[0].Messages {
		if msg.Role == "system" &&
			strings.Contains(msg.Content, sentinel) &&
			strings.Contains(msg.Content, "<observational-memory>") {
			recalled = true
			break
		}
	}
	if !recalled {
		var b strings.Builder
		for i, msg := range turn2Calls[0].Messages {
			b.WriteString("\n  [")
			b.WriteString(msg.Role)
			b.WriteString("] ")
			content := msg.Content
			if len(content) > 200 {
				content = content[:200] + "..."
			}
			b.WriteString(content)
			_ = i
		}
		t.Fatalf("turn-2 request did not recall the turn-1 observation via a system memory message; messages:%s", b.String())
	}
}
