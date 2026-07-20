package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-agent-harness/internal/harness"
)

// ---------------------------------------------------------------------------
// Epic #805 Slice 2: POST /v1/conversations/{id}/undo tests
// ---------------------------------------------------------------------------

// seedUndoConversation saves an alternating user/assistant history of nPairs
// prompt-response pairs (2*nPairs messages) and returns the convID.
func seedUndoConversation(t *testing.T, store *harness.SQLiteConversationStore, convID string, nPairs int) string {
	t.Helper()
	msgs := make([]harness.Message, 0, 2*nPairs)
	for i := 1; i <= nPairs; i++ {
		msgs = append(msgs,
			harness.Message{Role: "user", Content: fmt.Sprintf("%s-q%d", convID, i)},
			harness.Message{Role: "assistant", Content: fmt.Sprintf("%s-a%d", convID, i)},
		)
	}
	if err := store.SaveConversation(context.Background(), convID, msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}
	return convID
}

func newUndoTestServer(t *testing.T, store harness.ConversationStore) *httptest.Server {
	t.Helper()
	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "ok"}},
		harness.NewRegistry(),
		harness.RunnerConfig{ConversationStore: store},
	)
	ts := httptest.NewServer(New(runner))
	t.Cleanup(ts.Close)
	return ts
}

func TestUndoConversationEndpoint_Basic(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ctx := context.Background()
	seedUndoConversation(t, store, "conv-http-undo", 3) // steps 0..5

	ts := newUndoTestServer(t, store)

	body := bytes.NewBufferString(`{"count":2}`)
	res, err := http.Post(ts.URL+"/v1/conversations/conv-http-undo/undo", "application/json", body)
	if err != nil {
		t.Fatalf("POST undo: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, b)
	}

	var resp map[string]any
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["undone"] != true {
		t.Errorf("expected undone=true, got %v", resp["undone"])
	}
	// count=2 targets the 2nd-from-last prompt: user msg at step 2.
	if got, ok := resp["removed_from_step"].(float64); !ok || int(got) != 2 {
		t.Errorf("removed_from_step: got %v (%T), want 2", resp["removed_from_step"], resp["removed_from_step"])
	}
	// Steps 0,1 kept + undo-boundary marker at step 2 = 3 messages.
	if got, ok := resp["remaining_messages"].(float64); !ok || int(got) != 3 {
		t.Errorf("remaining_messages: got %v (%T), want 3", resp["remaining_messages"], resp["remaining_messages"])
	}

	// Verify the persisted conversation reflects the truncation.
	loaded, err := store.LoadMessages(ctx, "conv-http-undo")
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 messages after undo, got %d: %+v", len(loaded), loaded)
	}
	if loaded[0].Content != "conv-http-undo-q1" || loaded[1].Content != "conv-http-undo-a1" {
		t.Errorf("kept messages wrong: %+v", loaded)
	}
	if !loaded[2].IsMeta {
		t.Errorf("expected trailing is_meta undo-boundary marker, got %+v", loaded[2])
	}
}

func TestUndoConversationEndpoint_DefaultCount(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	seedUndoConversation(t, store, "conv-undo-default", 2) // steps 0..3
	seedUndoConversation(t, store, "conv-undo-empty", 2)

	ts := newUndoTestServer(t, store)

	// Absent count in an otherwise valid JSON body defaults to 1.
	res, err := http.Post(ts.URL+"/v1/conversations/conv-undo-default/undo", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatalf("POST undo: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 200 for {}, got %d: %s", res.StatusCode, b)
	}
	var resp map[string]any
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got, ok := resp["removed_from_step"].(float64); !ok || int(got) != 2 {
		t.Errorf("removed_from_step: got %v, want 2 (last prompt)", resp["removed_from_step"])
	}

	// An empty body is accepted as all-defaults (undo last prompt).
	res2, err := http.Post(ts.URL+"/v1/conversations/conv-undo-empty/undo", "application/json", bytes.NewBufferString(""))
	if err != nil {
		t.Fatalf("POST undo empty body: %v", err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res2.Body)
		t.Fatalf("expected 200 for empty body, got %d: %s", res2.StatusCode, b)
	}
	var resp2 map[string]any
	if err := json.NewDecoder(res2.Body).Decode(&resp2); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got, ok := resp2["removed_from_step"].(float64); !ok || int(got) != 2 {
		t.Errorf("empty-body removed_from_step: got %v, want 2", resp2["removed_from_step"])
	}
}

func TestUndoConversationEndpoint_BadCount(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ctx := context.Background()
	seedUndoConversation(t, store, "conv-undo-badcount", 2) // 2 prompts

	ts := newUndoTestServer(t, store)

	for _, body := range []string{`{"count":0}`, `{"count":-3}`, `{"count":9}`} {
		res, err := http.Post(ts.URL+"/v1/conversations/conv-undo-badcount/undo", "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatalf("POST undo %s: %v", body, err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("POST %s: expected 400, got %d: %s", body, res.StatusCode, b)
		}
	}

	// Failed undos must leave the conversation untouched.
	loaded, err := store.LoadMessages(ctx, "conv-undo-badcount")
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != 4 {
		t.Fatalf("conversation mutated by failed undos: got %d messages, want 4", len(loaded))
	}
}

func TestUndoConversationEndpoint_CrossesCompaction(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ctx := context.Background()

	msgs := []harness.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "system", Content: "summary of earlier context", IsCompactSummary: true},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2"},
	}
	if err := store.SaveConversation(ctx, "conv-undo-compacted", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	ts := newUndoTestServer(t, store)

	// count=2 targets q1 at step 0, below the compaction summary at step 2.
	res, err := http.Post(ts.URL+"/v1/conversations/conv-undo-compacted/undo", "application/json", bytes.NewBufferString(`{"count":2}`))
	if err != nil {
		t.Fatalf("POST undo: %v", err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", res.StatusCode, b)
	}
	if !strings.Contains(string(b), "undo_crosses_compaction") {
		t.Errorf("expected undo_crosses_compaction error code, got: %s", b)
	}

	// The conversation must be unchanged after a refused undo.
	loaded, err := store.LoadMessages(ctx, "conv-undo-compacted")
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != len(msgs) {
		t.Fatalf("conversation mutated by refused undo: got %d messages, want %d", len(loaded), len(msgs))
	}
}

func TestUndoConversationEndpoint_ToStep(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	seedUndoConversation(t, store, "conv-undo-tostep", 3) // steps 0..5

	ts := newUndoTestServer(t, store)

	// to_step=4 references the last user prompt: same result as count=1.
	res, err := http.Post(ts.URL+"/v1/conversations/conv-undo-tostep/undo", "application/json", bytes.NewBufferString(`{"to_step":4}`))
	if err != nil {
		t.Fatalf("POST undo: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, b)
	}
	var resp map[string]any
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got, ok := resp["removed_from_step"].(float64); !ok || int(got) != 4 {
		t.Errorf("removed_from_step: got %v, want 4", resp["removed_from_step"])
	}
	if got, ok := resp["remaining_messages"].(float64); !ok || int(got) != 5 {
		t.Errorf("remaining_messages: got %v, want 5 (4 kept + marker)", resp["remaining_messages"])
	}
}

func TestUndoConversationEndpoint_ToStepInvalid(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	seedUndoConversation(t, store, "conv-undo-tostep-bad", 2) // steps 0..3

	ts := newUndoTestServer(t, store)

	cases := map[string]string{
		"assistant message step": `{"to_step":1}`,
		"step beyond history":    `{"to_step":99}`,
		"negative step":          `{"to_step":-1}`,
		"count and to_step both": `{"count":1,"to_step":2}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			res, err := http.Post(ts.URL+"/v1/conversations/conv-undo-tostep-bad/undo", "application/json", bytes.NewBufferString(body))
			if err != nil {
				t.Fatalf("POST undo %s: %v", body, err)
			}
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode != http.StatusBadRequest {
				t.Errorf("POST %s: expected 400, got %d: %s", body, res.StatusCode, b)
			}
		})
	}
}

func TestUndoConversationEndpoint_ToStepCrossesCompaction(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	msgs := []harness.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "system", Content: "summary", IsCompactSummary: true},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2"},
	}
	if err := store.SaveConversation(context.Background(), "conv-undo-tostep-compact", msgs); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}

	ts := newUndoTestServer(t, store)

	// to_step=0 is a valid prompt reference, but it sits below the compaction
	// summary at step 2, so the store guard maps to 409.
	res, err := http.Post(ts.URL+"/v1/conversations/conv-undo-tostep-compact/undo", "application/json", bytes.NewBufferString(`{"to_step":0}`))
	if err != nil {
		t.Fatalf("POST undo: %v", err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", res.StatusCode, b)
	}
}

func TestUndoConversationEndpoint_NonExistentConversation(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ts := newUndoTestServer(t, store)

	res, err := http.Post(ts.URL+"/v1/conversations/does-not-exist/undo", "application/json", bytes.NewBufferString(`{"count":1}`))
	if err != nil {
		t.Fatalf("POST undo: %v", err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for non-existent conversation, got %d: %s", res.StatusCode, b)
	}
}

func TestUndoConversationEndpoint_InvalidJSON(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ts := newUndoTestServer(t, store)

	res, err := http.Post(ts.URL+"/v1/conversations/any-conv/undo", "application/json", bytes.NewBufferString(`not json`))
	if err != nil {
		t.Fatalf("POST undo: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", res.StatusCode)
	}
}

func TestUndoConversationEndpoint_WrongMethod(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ts := newUndoTestServer(t, store)

	res, err := http.Get(ts.URL + "/v1/conversations/any-conv/undo")
	if err != nil {
		t.Fatalf("GET undo: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", res.StatusCode)
	}
}

func TestUndoConversationEndpoint_NoStore(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "ok"}},
		harness.NewRegistry(),
		harness.RunnerConfig{}, // no store
	)
	ts := httptest.NewServer(New(runner))
	defer ts.Close()

	res, err := http.Post(ts.URL+"/v1/conversations/any-conv/undo", "application/json", bytes.NewBufferString(`{"count":1}`))
	if err != nil {
		t.Fatalf("POST undo: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", res.StatusCode)
	}
}
