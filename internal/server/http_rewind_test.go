package server

import (
	"context"
	"go-agent-harness/internal/harness"
	"net/http"
	"net/http/httptest"
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
