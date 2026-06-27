package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
)

type countingBody struct {
	remaining int64
	read      int64
}

func (b *countingBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	for i := range p {
		p[i] = ' '
	}
	n := len(p)
	b.remaining -= int64(n)
	b.read += int64(n)
	return n, nil
}

func (b *countingBody) Close() error { return nil }

func TestPostRunRejectsOversizedBodyWithoutReadingAll(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "done"}},
		harness.NewRegistry(),
		harness.RunnerConfig{DefaultModel: "test-model", MaxSteps: 1},
	)
	body := &countingBody{remaining: 5 << 20}
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", body)
	req.ContentLength = 5 << 20
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	New(runner).ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized body, got %d: %s", rec.Code, rec.Body.String())
	}
	if body.read > (1<<20)+(128<<10) {
		t.Fatalf("expected body reads to stop near 1 MiB, read %d bytes", body.read)
	}
}

func TestHardenedHandlerTimesOutNonStreamingRequests(t *testing.T) {
	t.Parallel()

	s := &Server{handlerTimeout: 10 * time.Millisecond}
	handler := s.hardenHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		writeError(w, http.StatusTeapot, "unexpected", "handler should time out first")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected timeout status 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHardenedHandlerDoesNotTimeoutSSERequests(t *testing.T) {
	t.Parallel()

	s := &Server{handlerTimeout: 10 * time.Millisecond}
	handler := s.hardenHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/run-123/events", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected streaming path to bypass timeout wrapper, got %d: %s", rec.Code, rec.Body.String())
	}
}
