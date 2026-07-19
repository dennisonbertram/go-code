package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
)

// forkHistoryConvStore is a minimal, concurrency-safe harness.ConversationStore
// fake used only by the C3 (replay fork history) tests below. It actually
// persists messages (unlike the no-op SaveConversation in the shared
// mockConversationStore), which is required to prove that a forked run's
// reconstructed history round-trips through the store and reaches the
// provider.
type forkHistoryConvStore struct {
	mu       sync.Mutex
	messages map[string][]harness.Message
	tenants  map[string]string
}

func newForkHistoryConvStore() *forkHistoryConvStore {
	return &forkHistoryConvStore{
		messages: make(map[string][]harness.Message),
		tenants:  make(map[string]string),
	}
}

func (f *forkHistoryConvStore) Migrate(_ context.Context) error { return nil }
func (f *forkHistoryConvStore) Close() error                    { return nil }

func (f *forkHistoryConvStore) SaveConversation(_ context.Context, convID string, msgs []harness.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]harness.Message, len(msgs))
	copy(cp, msgs)
	f.messages[convID] = cp
	return nil
}

func (f *forkHistoryConvStore) SaveConversationWithCost(ctx context.Context, convID string, msgs []harness.Message, _ harness.ConversationTokenCost) error {
	return f.SaveConversation(ctx, convID, msgs)
}

func (f *forkHistoryConvStore) LoadMessages(_ context.Context, convID string) ([]harness.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	msgs, ok := f.messages[convID]
	if !ok {
		return nil, nil
	}
	cp := make([]harness.Message, len(msgs))
	copy(cp, msgs)
	return cp, nil
}

func (f *forkHistoryConvStore) ListConversations(_ context.Context, _ harness.ConversationFilter, _, _ int) ([]harness.Conversation, error) {
	return nil, nil
}
func (f *forkHistoryConvStore) DeleteConversation(_ context.Context, _ string) error { return nil }

func (f *forkHistoryConvStore) UpdateConversationMeta(_ context.Context, convID, _, tenantID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tenants[convID] = tenantID
	return nil
}

func (f *forkHistoryConvStore) GetConversationOwner(_ context.Context, convID string) (*harness.Conversation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.messages[convID]; !ok {
		return nil, nil
	}
	return &harness.Conversation{ID: convID, TenantID: f.tenants[convID]}, nil
}

func (f *forkHistoryConvStore) SearchMessages(_ context.Context, _, _ string, _ int) ([]harness.MessageSearchResult, error) {
	return nil, nil
}
func (f *forkHistoryConvStore) DeleteOldConversations(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
}
func (f *forkHistoryConvStore) PinConversation(_ context.Context, _ string, _ bool) error { return nil }
func (f *forkHistoryConvStore) CompactConversation(_ context.Context, _ string, _ int, _ harness.Message) error {
	return nil
}
func (f *forkHistoryConvStore) UndoPrompts(_ context.Context, _ string, _ int) (int, error) {
	return 0, nil
}
func (f *forkHistoryConvStore) ForkConversation(_ context.Context, srcID, newID string) (*harness.Conversation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	msgs, ok := f.messages[srcID]
	if !ok {
		return nil, fmt.Errorf("fork: source conversation %q not found", srcID)
	}
	if _, taken := f.messages[newID]; taken {
		return nil, fmt.Errorf("fork: target conversation %q already exists", newID)
	}
	cp := make([]harness.Message, len(msgs))
	copy(cp, msgs)
	f.messages[newID] = cp
	f.tenants[newID] = f.tenants[srcID]
	return &harness.Conversation{ID: newID, TenantID: f.tenants[newID], MsgCount: len(msgs)}, nil
}

// multiTurnForkRollout is a hand-authored rollout with two user turns
// (run.started's prompt, then a steering.received turn) so that forking at
// the final step reconstructs history that includes messages BEFORE the
// final user prompt — the exact case the pre-fix code silently discarded.
const multiTurnForkRollout = `{"ts":"2026-03-12T10:00:00Z","seq":1,"type":"run.started","data":{"step":0,"prompt":"first question","system_prompt":"be helpful"}}
{"ts":"2026-03-12T10:00:01Z","seq":2,"type":"llm.turn.completed","data":{"step":1,"content":"first answer"}}
{"ts":"2026-03-12T10:00:02Z","seq":3,"type":"steering.received","data":{"step":2,"content":"second question"}}
{"ts":"2026-03-12T10:00:03Z","seq":4,"type":"llm.turn.completed","data":{"step":3,"content":"second answer"}}
{"ts":"2026-03-12T10:00:04Z","seq":5,"type":"run.completed","data":{"step":4}}`

// TestHandleReplayFork_PreservesReconstructedHistory is the C3 regression
// test: forking a multi-turn rollout must hand the FULL reconstructed
// conversation history to the new run, not just the final prompt. Verified
// by capturing the actual CompletionRequest the provider receives for the
// forked run and asserting the earlier turns are present.
func TestHandleReplayFork_PreservesReconstructedHistory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rolloutPath := writeTestRollout(t, dir, "multi-turn.jsonl", multiTurnForkRollout)

	prov := &capturingServerProvider{result: harness.CompletionResult{Content: "forked continuation"}}
	convStore := newForkHistoryConvStore()
	runner := harness.NewRunner(prov, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel:        "gpt-4.1-mini",
		DefaultSystemPrompt: "test",
		MaxSteps:            2,
		RolloutDir:          dir,
		ConversationStore:   convStore,
	})
	handler := NewWithOptions(ServerOptions{
		Runner:       runner,
		AuthDisabled: true,
		RolloutDir:   dir,
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{
		"rollout_path": rolloutPath,
		"mode":         "fork",
		"fork_step":    3,
	})
	resp, err := http.Post(ts.URL+"/v1/runs/replay", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		var errBody map[string]any
		json.NewDecoder(resp.Body).Decode(&errBody)
		t.Fatalf("expected 202, got %d: %v", resp.StatusCode, errBody)
	}
	var result struct {
		RunID            string `json:"run_id"`
		MessagesRestored int    `json:"messages_restored"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.MessagesRestored < 3 {
		t.Fatalf("expected messages_restored >= 3 (2 prior turns + final prompt), got %d", result.MessagesRestored)
	}

	// Wait for the forked run to reach the provider.
	deadline := time.Now().Add(4 * time.Second)
	for {
		if prov.lastRequest() != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for forked run to call the provider")
		}
		time.Sleep(10 * time.Millisecond)
	}

	last := prov.lastRequest()
	var sawFirstQuestion, sawFirstAnswer, sawSecondQuestion bool
	for _, m := range last.Messages {
		switch {
		case m.Role == "user" && m.Content == "first question":
			sawFirstQuestion = true
		case m.Role == "assistant" && m.Content == "first answer":
			sawFirstAnswer = true
		case m.Role == "user" && m.Content == "second question":
			sawSecondQuestion = true
		}
	}
	if !sawFirstQuestion || !sawFirstAnswer {
		var contents []string
		for _, m := range last.Messages {
			contents = append(contents, m.Role+": "+m.Content)
		}
		t.Fatalf("forked run is missing prior conversation history (only the final prompt survived).\ngot messages: %v", contents)
	}
	if !sawSecondQuestion {
		t.Fatalf("forked run is missing its own new prompt turn")
	}
}

// TestHandleReplayFork_NoConversationStore_ReturnsExplicitError is a
// regression test for the "no persistence configured" edge case: when the
// rollout has history to restore but the runner has no ConversationStore
// configured, the fork must fail loudly (501) rather than silently starting
// the run without the history it was asked to restore.
func TestHandleReplayFork_NoConversationStore_ReturnsExplicitError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rolloutPath := writeTestRollout(t, dir, "multi-turn.jsonl", multiTurnForkRollout)

	// Same runner setup as newTestReplayServerWithRolloutDir, but explicitly
	// with NO ConversationStore.
	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "forked output"}},
		harness.NewRegistry(),
		harness.RunnerConfig{
			DefaultModel:        "gpt-4.1-mini",
			DefaultSystemPrompt: "test",
			MaxSteps:            2,
			RolloutDir:          dir,
		},
	)
	handler := NewWithOptions(ServerOptions{
		Runner:       runner,
		AuthDisabled: true,
		RolloutDir:   dir,
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{
		"rollout_path": rolloutPath,
		"mode":         "fork",
		"fork_step":    3,
	})
	resp, err := http.Post(ts.URL+"/v1/runs/replay", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotImplemented {
		var errBody map[string]any
		json.NewDecoder(resp.Body).Decode(&errBody)
		t.Fatalf("expected 501 when history exists but no conversation store is configured, got %d: %v", resp.StatusCode, errBody)
	}
}
