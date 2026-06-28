package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/server"
	"go-agent-harness/internal/store"
)

type blockingCancelScopeProvider struct {
	mu        sync.Mutex
	calls     int
	blockCh   chan struct{}
	releaseCh chan struct{}
}

func newBlockingCancelScopeProvider() *blockingCancelScopeProvider {
	return &blockingCancelScopeProvider{
		blockCh:   make(chan struct{}),
		releaseCh: make(chan struct{}),
	}
}

func (p *blockingCancelScopeProvider) Complete(ctx context.Context, _ harness.CompletionRequest) (harness.CompletionResult, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.mu.Unlock()

	if idx == 0 {
		select {
		case <-p.blockCh:
		default:
			close(p.blockCh)
		}
		select {
		case <-p.releaseCh:
			return harness.CompletionResult{Content: "done"}, nil
		case <-ctx.Done():
			return harness.CompletionResult{}, ctx.Err()
		}
	}

	return harness.CompletionResult{Content: "done"}, nil
}

func TestScope_ReadOnly_CannotCancelRun(t *testing.T) {
	t.Parallel()

	ms := store.NewMemoryStore()
	readOnlyToken, readOnlyKey := generateFastAPIKey(t, "tenant-cancel-scope", "read only", []string{store.ScopeRunsRead})
	if err := ms.CreateAPIKey(context.Background(), readOnlyKey); err != nil {
		t.Fatalf("CreateAPIKey(read_only): %v", err)
	}
	writeToken, writeKey := generateFastAPIKey(t, "tenant-cancel-scope", "write", []string{store.ScopeRunsRead, store.ScopeRunsWrite})
	if err := ms.CreateAPIKey(context.Background(), writeKey); err != nil {
		t.Fatalf("CreateAPIKey(write): %v", err)
	}

	provider := newBlockingCancelScopeProvider()
	runner := harness.NewRunner(provider, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     5,
	})
	handler := server.NewWithOptions(server.ServerOptions{
		Runner: runner,
		Store:  ms,
	})

	// Stamp the run with the same tenant the API keys belong to. This test
	// exercises scope enforcement (read-only vs write), not tenant isolation;
	// without a matching tenant the by-ID tenant-ownership gate would 404 the
	// write key's cancel before scope even mattered.
	run, err := runner.StartRun(harness.RunRequest{Prompt: "hello", TenantID: "tenant-cancel-scope"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	select {
	case <-provider.blockCh:
	case <-time.After(3 * time.Second):
		t.Fatal("provider never started blocking")
	}

	readOnlyReq := httptest.NewRequest(http.MethodPost, "/v1/runs/"+run.ID+"/cancel", nil)
	readOnlyReq.Header.Set("Authorization", "Bearer "+readOnlyToken)
	readOnlyRec := httptest.NewRecorder()
	handler.ServeHTTP(readOnlyRec, readOnlyReq)

	if readOnlyRec.Code != http.StatusForbidden {
		t.Fatalf("read-only cancel status = %d, want %d; body=%s", readOnlyRec.Code, http.StatusForbidden, readOnlyRec.Body.String())
	}
	var denied struct {
		Error    string `json:"error"`
		Required string `json:"required"`
	}
	if err := json.NewDecoder(readOnlyRec.Body).Decode(&denied); err != nil {
		t.Fatalf("decode forbidden response: %v", err)
	}
	if denied.Error != "insufficient_scope" {
		t.Fatalf("forbidden error = %q, want %q", denied.Error, "insufficient_scope")
	}
	if denied.Required != store.ScopeRunsWrite {
		t.Fatalf("forbidden required = %q, want %q", denied.Required, store.ScopeRunsWrite)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("run not found after forbidden cancel")
	}
	if state.Status == harness.RunStatusCancelled {
		t.Fatal("read-only cancel request should not cancel the run")
	}

	writeReq := httptest.NewRequest(http.MethodPost, "/v1/runs/"+run.ID+"/cancel", nil)
	writeReq.Header.Set("Authorization", "Bearer "+writeToken)
	writeRec := httptest.NewRecorder()
	handler.ServeHTTP(writeRec, writeReq)

	if writeRec.Code != http.StatusOK {
		t.Fatalf("write cancel status = %d, want %d; body=%s", writeRec.Code, http.StatusOK, writeRec.Body.String())
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state, ok = runner.GetRun(run.ID)
		if !ok {
			t.Fatal("run not found while waiting for cancellation")
		}
		if state.Status == harness.RunStatusCancelled {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	state, _ = runner.GetRun(run.ID)
	t.Fatalf("timed out waiting for cancelled status, got %q", state.Status)
}
