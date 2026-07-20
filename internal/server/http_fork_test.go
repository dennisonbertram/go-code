package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
)

// forkResponse mirrors the POST /v1/conversations/{id}/fork response body.
type forkResponse struct {
	ConversationID string `json:"conversation_id"`
	ForkedFrom     string `json:"forked_from"`
	MessageCount   int    `json:"message_count"`
}

// postFork issues the fork POST and returns the HTTP status code plus the
// decoded response (valid only on 200).
func postFork(t *testing.T, baseURL, convID string) (int, forkResponse) {
	t.Helper()
	res, err := http.Post(baseURL+"/v1/conversations/"+convID+"/fork", "application/json", nil)
	if err != nil {
		t.Fatalf("POST fork: %v", err)
	}
	defer res.Body.Close()
	var out forkResponse
	if res.StatusCode == http.StatusOK {
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatalf("decode fork response: %v", err)
		}
	} else {
		// Drain for connection reuse and to keep failure output useful.
		b, _ := io.ReadAll(res.Body)
		t.Logf("fork status %d body: %s", res.StatusCode, b)
	}
	return res.StatusCode, out
}

// getConversationMessages GETs /v1/conversations/{id}/messages and returns the
// decoded messages (200 required).
func getConversationMessages(t *testing.T, baseURL, convID string) []harness.Message {
	t.Helper()
	res, err := http.Get(baseURL + "/v1/conversations/" + convID + "/messages")
	if err != nil {
		t.Fatalf("GET messages: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("GET messages: expected 200, got %d: %s", res.StatusCode, b)
	}
	var body struct {
		Messages []harness.Message `json:"messages"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	return body.Messages
}

func assertForkMessagesEqual(t *testing.T, want, got []harness.Message) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("fork message count: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content {
			t.Errorf("msg[%d]: got %s: %q, want %s: %q", i, got[i].Role, got[i].Content, want[i].Role, want[i].Content)
		}
	}
}

// completeRun starts a run and waits until the runner's conversation view
// shows at least wantMin messages (i.e. the run's turns have landed).
func completeRun(t *testing.T, runner *harness.Runner, convID, prompt string, wantMin int) {
	t.Helper()
	if _, err := runner.StartRun(harness.RunRequest{Prompt: prompt, ConversationID: convID}); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if msgs, ok := runner.ConversationMessages(convID); ok && len(msgs) >= wantMin {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for conversation %q to reach %d messages", convID, wantMin)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestForkConversationEndpoint_Basic forks a store-backed conversation and
// verifies the response shape plus message equality via the messages endpoint.
func TestForkConversationEndpoint_Basic(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ctx := context.Background()
	msgs := []harness.Message{
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "second question"},
		{Role: "assistant", Content: "second answer"},
	}
	if err := store.SaveConversation(ctx, "conv-fork-src", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "ok"}},
		harness.NewRegistry(),
		harness.RunnerConfig{ConversationStore: store},
	)
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	code, fork := postFork(t, ts.URL, "conv-fork-src")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if fork.ConversationID == "" {
		t.Fatal("response missing conversation_id")
	}
	if fork.ConversationID == "conv-fork-src" {
		t.Fatal("fork ID must differ from the source ID")
	}
	if fork.ForkedFrom != "conv-fork-src" {
		t.Errorf("forked_from: got %q, want %q", fork.ForkedFrom, "conv-fork-src")
	}
	if fork.MessageCount != len(msgs) {
		t.Errorf("message_count: got %d, want %d", fork.MessageCount, len(msgs))
	}

	// The fork's messages must equal the source's, served over HTTP...
	assertForkMessagesEqual(t, msgs, getConversationMessages(t, ts.URL, fork.ConversationID))

	// ...and persisted in the store.
	loaded, err := store.LoadMessages(ctx, fork.ConversationID)
	if err != nil {
		t.Fatalf("LoadMessages on fork: %v", err)
	}
	assertForkMessagesEqual(t, msgs, loaded)

	// The source must be unchanged.
	srcLoaded, err := store.LoadMessages(ctx, "conv-fork-src")
	if err != nil {
		t.Fatalf("LoadMessages on source: %v", err)
	}
	assertForkMessagesEqual(t, msgs, srcLoaded)
}

// TestForkConversationEndpoint_InMemoryOnly forks a conversation that exists
// only in the runner's in-memory mirror (its store row was deleted), proving
// the in-memory-first resolution path, and that the fork includes the latest
// (final assistant) message.
func TestForkConversationEndpoint_InMemoryOnly(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ctx := context.Background()
	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "latest reply"}},
		harness.NewRegistry(),
		harness.RunnerConfig{
			DefaultModel:      "test",
			MaxSteps:          1,
			ConversationStore: store,
		},
	)
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	// Complete a run: the runner mirrors the history in memory AND persists it.
	completeRun(t, runner, "conv-live", "hello live", 2)

	// Remove the store row so only the in-memory mirror remains.
	if err := store.DeleteConversation(ctx, "conv-live"); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}
	if owner, err := store.GetConversationOwner(ctx, "conv-live"); err != nil || owner != nil {
		t.Fatalf("expected store row gone, owner=%v err=%v", owner, err)
	}

	mirrored, ok := runner.ConversationMessages("conv-live")
	if !ok || len(mirrored) < 2 {
		t.Fatalf("expected in-memory mirror to hold the conversation, ok=%v len=%d", ok, len(mirrored))
	}

	code, fork := postFork(t, ts.URL, "conv-live")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if fork.MessageCount != len(mirrored) {
		t.Errorf("message_count: got %d, want %d", fork.MessageCount, len(mirrored))
	}

	// The fork must include the latest message (the final assistant reply) and
	// now be persisted in the store.
	loaded, err := store.LoadMessages(ctx, fork.ConversationID)
	if err != nil {
		t.Fatalf("LoadMessages on fork: %v", err)
	}
	assertForkMessagesEqual(t, mirrored, loaded)
	if loaded[len(loaded)-1].Content != "latest reply" {
		t.Errorf("fork is missing the latest message; last=%q", loaded[len(loaded)-1].Content)
	}
}

// TestForkConversationEndpoint_MirrorAheadOfStore verifies the mid-run case:
// when the in-memory mirror holds more turns than the store has persisted,
// the fork must capture the newer mirror view, not the stale store copy.
func TestForkConversationEndpoint_MirrorAheadOfStore(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ctx := context.Background()
	seed := []harness.Message{
		{Role: "user", Content: "original question"},
		{Role: "assistant", Content: "original answer"},
	}
	if err := store.SaveConversation(ctx, "conv-hybrid", seed); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "follow-up answer"}},
		harness.NewRegistry(),
		harness.RunnerConfig{
			DefaultModel:      "test",
			MaxSteps:          1,
			ConversationStore: store,
		},
	)
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	// Continue the conversation: the mirror and the store both advance past
	// the 2 seeded messages (user turn, assistant turn, possibly a system
	// message — so compare against the mirror, not a hardcoded count).
	completeRun(t, runner, "conv-hybrid", "follow-up question", 3)

	// Roll the store back to the 2-message view, leaving the mirror ahead —
	// the state a mid-run fork would see before the next persist boundary.
	if err := store.SaveConversation(ctx, "conv-hybrid", seed); err != nil {
		t.Fatalf("re-seed store: %v", err)
	}

	mirrored, ok := runner.ConversationMessages("conv-hybrid")
	if !ok || len(mirrored) <= len(seed) {
		t.Fatalf("expected mirror ahead of the %d seeded messages, ok=%v len=%d", len(seed), ok, len(mirrored))
	}

	code, fork := postFork(t, ts.URL, "conv-hybrid")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if fork.MessageCount != len(mirrored) {
		t.Errorf("message_count: got %d, want %d (mirror view, not stale store copy)", fork.MessageCount, len(mirrored))
	}
	loaded, err := store.LoadMessages(ctx, fork.ConversationID)
	if err != nil {
		t.Fatalf("LoadMessages on fork: %v", err)
	}
	assertForkMessagesEqual(t, mirrored, loaded)
}

// TestForkConversationEndpoint_NotFound covers forking an unknown conversation.
func TestForkConversationEndpoint_NotFound(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "ok"}},
		harness.NewRegistry(),
		harness.RunnerConfig{ConversationStore: store},
	)
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	code, _ := postFork(t, ts.URL, "conv-does-not-exist")
	if code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", code)
	}
}

// TestForkConversationEndpoint_MethodNotAllowed covers GET on the fork path.
func TestForkConversationEndpoint_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "ok"}},
		harness.NewRegistry(),
		harness.RunnerConfig{ConversationStore: store},
	)
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/conversations/any-conv/fork")
	if err != nil {
		t.Fatalf("GET fork: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", res.StatusCode)
	}
}

// TestForkConversationEndpoint_NoStore covers the 501 path when no
// conversation store is configured.
func TestForkConversationEndpoint_NoStore(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "ok"}},
		harness.NewRegistry(),
		harness.RunnerConfig{}, // no conversation store
	)
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	code, _ := postFork(t, ts.URL, "any-conv")
	if code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", code)
	}
}

// TestForkConversationEndpoint_ForkIDsAreUnique verifies that two forks of the
// same source get distinct server-minted IDs.
func TestForkConversationEndpoint_ForkIDsAreUnique(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ctx := context.Background()
	if err := store.SaveConversation(ctx, "conv-uniq", []harness.Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}
	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "ok"}},
		harness.NewRegistry(),
		harness.RunnerConfig{ConversationStore: store},
	)
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	code1, fork1 := postFork(t, ts.URL, "conv-uniq")
	code2, fork2 := postFork(t, ts.URL, "conv-uniq")
	if code1 != http.StatusOK || code2 != http.StatusOK {
		t.Fatalf("expected both forks 200, got %d and %d", code1, code2)
	}
	if fork1.ConversationID == fork2.ConversationID {
		t.Fatalf("fork IDs must be unique, both were %q", fork1.ConversationID)
	}
}
