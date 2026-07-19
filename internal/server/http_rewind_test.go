package server

import (
	"bytes"
	"context"
	"encoding/json"
	"go-agent-harness/internal/harness"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRewindPointsEndpointListsPoints(t *testing.T) {
	store := newTestSQLiteStore(t)
	if err := store.SaveConversation(context.Background(), "rewind-http", []harness.Message{{Role: "user", Content: "x"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRewindPoint(context.Background(), harness.RewindPoint{ID: "p", ConversationID: "rewind-http", Tool: "write"}); err != nil {
		t.Fatal(err)
	}
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{ConversationStore: store})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/conversations/rewind-http/rewind-points", nil)
	New(runner).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// TestRestoreRewindEndpointRestoresFileAndTruncatesMessages verifies
// POST /v1/conversations/{id}/rewind writes the pre-image back to disk and
// truncates messages after the rewind point, through the real HTTP handler.
func TestRestoreRewindEndpointRestoresFileAndTruncatesMessages(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()
	workspace := t.TempDir()
	path := filepath.Join(workspace, "notes.txt")
	if err := os.WriteFile(path, []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}

	convID := "rewind-restore-http"
	if err := store.SaveConversation(ctx, convID, []harness.Message{
		{Role: "user", Content: "keep"},
		{Role: "assistant", Content: "drop"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateConversationMeta(ctx, convID, workspace, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRewindPoint(ctx, harness.RewindPoint{
		ID:             "restore-point",
		ConversationID: convID,
		Step:           0,
		Tool:           "write",
		Files: []harness.RewindFileSnapshot{{
			Path:         "notes.txt",
			Content:      []byte("before"),
			Exists:       true,
			ExpectedHash: harness.RewindContentHash([]byte("after")),
		}},
	}); err != nil {
		t.Fatal(err)
	}

	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{ConversationStore: store})

	body, _ := json.Marshal(map[string]any{"point_id": "restore-point"})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/conversations/"+convID+"/rewind", bytes.NewReader(body))
	New(runner).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	var result harness.RewindRestoreResult
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rr.Body.String())
	}
	if result.FilesRestored != 1 {
		t.Errorf("FilesRestored = %d, want 1", result.FilesRestored)
	}
	if result.MessagesTruncated != 1 {
		t.Errorf("MessagesTruncated = %d, want 1", result.MessagesTruncated)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "before" {
		t.Fatalf("file content = %q, err=%v, want %q", got, err, "before")
	}
}

// TestRestoreRewindEndpointRequiresPointID verifies a missing point_id is
// rejected as a client error rather than reaching the store.
func TestRestoreRewindEndpointRequiresPointID(t *testing.T) {
	store := newTestSQLiteStore(t)
	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{ConversationStore: store})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/conversations/rewind-missing-point/rewind", bytes.NewReader([]byte(`{}`)))
	New(runner).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rr.Code, rr.Body.String())
	}
}

// TestRestoreRewindEndpointRefusesExternalModificationWithoutForce verifies
// the HTTP handler surfaces the store's refusal as a 409 without a force flag,
// and that force:true proceeds anyway.
func TestRestoreRewindEndpointRefusesExternalModificationWithoutForce(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()
	workspace := t.TempDir()
	path := filepath.Join(workspace, "notes.txt")
	if err := os.WriteFile(path, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}

	convID := "rewind-refuse-http"
	if err := store.SaveConversation(ctx, convID, []harness.Message{{Role: "user", Content: "keep"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateConversationMeta(ctx, convID, workspace, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRewindPoint(ctx, harness.RewindPoint{
		ID:             "refuse-point",
		ConversationID: convID,
		Files: []harness.RewindFileSnapshot{{
			Path:         "notes.txt",
			Content:      []byte("before"),
			Exists:       true,
			ExpectedHash: harness.RewindContentHash([]byte("agent")),
		}},
	}); err != nil {
		t.Fatal(err)
	}

	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "ok"}}, harness.NewRegistry(), harness.RunnerConfig{ConversationStore: store})

	body, _ := json.Marshal(map[string]any{"point_id": "refuse-point"})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/conversations/"+convID+"/rewind", bytes.NewReader(body))
	New(runner).ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want 409", rr.Code, rr.Body.String())
	}

	forceBody, _ := json.Marshal(map[string]any{"point_id": "refuse-point", "force": true})
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/v1/conversations/"+convID+"/rewind", bytes.NewReader(forceBody))
	New(runner).ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("force restore status=%d body=%s, want 200", rr2.Code, rr2.Body.String())
	}
}
