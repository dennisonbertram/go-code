package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/harness/tools/deferred"
)

func TestTodosPutEmitsTodosUpdatedEventForActiveRun(t *testing.T) {
	blocker := make(chan struct{})
	runner := harness.NewRunner(&blockingServerProvider{blocker: blocker}, harness.NewRegistry(), harness.RunnerConfig{MaxSteps: 1})
	todos := newFakeTodoManager()
	ts := httptest.NewServer(NewWithOptions(ServerOptions{Runner: runner, Todos: todos}))
	defer ts.Close()
	defer close(blocker)
	run, err := runner.StartRun(harness.RunRequest{Prompt: "block"})
	if err != nil {
		t.Fatal(err)
	}
	_, stream, cancel, err := runner.Subscribe(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	req, err := http.NewRequest(http.MethodPut, ts.URL+"/v1/runs/"+run.ID+"/todos", bytes.NewBufferString(`{"todos":[{"text":"write regression","status":"in_progress"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT todos status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-stream:
			if event.Type == harness.EventTodosUpdated {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for todos.updated")
		}
	}
}

// fakeTodoManager is a simple in-memory TodoManager for testing.
type fakeTodoManager struct {
	mu    sync.Mutex
	items map[string][]deferred.TodoItem
}

func newFakeTodoManager() *fakeTodoManager {
	return &fakeTodoManager{items: make(map[string][]deferred.TodoItem)}
}

func (f *fakeTodoManager) GetTodos(runID string) []deferred.TodoItem {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := f.items[runID]
	if items == nil {
		return []deferred.TodoItem{}
	}
	out := make([]deferred.TodoItem, len(items))
	copy(out, items)
	return out
}

func (f *fakeTodoManager) SetTodos(runID string, todos []deferred.TodoItem) error {
	for _, td := range todos {
		st := td.Status
		if st == "" {
			st = "pending"
		}
		if st != "pending" && st != "in_progress" && st != "completed" {
			return fmt.Errorf("invalid todo status %q", td.Status)
		}
	}
	normalized := make([]deferred.TodoItem, len(todos))
	for i, td := range todos {
		if td.Status == "" {
			td.Status = "pending"
		}
		normalized[i] = td
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[runID] = normalized
	return nil
}

func newTodoTestServer(t *testing.T) (*httptest.Server, *fakeTodoManager) {
	t.Helper()
	registry := harness.NewRegistry()
	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "done"}},
		registry,
		harness.RunnerConfig{
			DefaultModel:        "gpt-4.1-mini",
			DefaultSystemPrompt: "You are helpful.",
			MaxSteps:            1,
		},
	)
	tm := newFakeTodoManager()
	handler := NewWithOptions(ServerOptions{Runner: runner, Todos: tm})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts, tm
}

func TestTodosGetEmpty(t *testing.T) {
	t.Parallel()
	ts, _ := newTodoTestServer(t)

	resp, err := http.Get(ts.URL + "/v1/runs/run-abc/todos")
	if err != nil {
		t.Fatalf("GET todos: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body struct {
		RunID string              `json:"run_id"`
		Todos []deferred.TodoItem `json:"todos"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.RunID != "run-abc" {
		t.Errorf("run_id = %q, want %q", body.RunID, "run-abc")
	}
	if len(body.Todos) != 0 {
		t.Errorf("expected empty todos, got %d", len(body.Todos))
	}
}

func TestTodosPutAndGet(t *testing.T) {
	t.Parallel()
	ts, _ := newTodoTestServer(t)

	payload := `{"todos":[{"id":"1","text":"Write tests","status":"pending"},{"id":"2","text":"Ship it","status":"in_progress"}]}`
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/runs/run-xyz/todos", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT todos: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body struct {
		RunID string              `json:"run_id"`
		Todos []deferred.TodoItem `json:"todos"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Todos) != 2 {
		t.Fatalf("expected 2 todos, got %d", len(body.Todos))
	}
	if body.Todos[0].Text != "Write tests" {
		t.Errorf("todo[0].Text = %q", body.Todos[0].Text)
	}
	if body.Todos[1].Status != "in_progress" {
		t.Errorf("todo[1].Status = %q", body.Todos[1].Status)
	}

	// Verify GET returns the same state.
	resp2, err := http.Get(ts.URL + "/v1/runs/run-xyz/todos")
	if err != nil {
		t.Fatalf("GET todos after PUT: %v", err)
	}
	defer resp2.Body.Close()
	var body2 struct {
		Todos []deferred.TodoItem `json:"todos"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&body2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body2.Todos) != 2 {
		t.Errorf("GET after PUT: expected 2 todos, got %d", len(body2.Todos))
	}
}

func TestTodosPutInvalidStatus(t *testing.T) {
	t.Parallel()
	ts, _ := newTodoTestServer(t)

	payload := `{"todos":[{"text":"bad","status":"invalid_status"}]}`
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/runs/run-1/todos", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT todos: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestTodosPutInvalidJSON(t *testing.T) {
	t.Parallel()
	ts, _ := newTodoTestServer(t)

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/runs/run-1/todos", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT todos: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestTodosMethodNotAllowed(t *testing.T) {
	t.Parallel()
	ts, _ := newTodoTestServer(t)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/v1/runs/run-1/todos", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE todos: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

func TestTodosNilManager(t *testing.T) {
	t.Parallel()
	registry := harness.NewRegistry()
	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "done"}},
		registry,
		harness.RunnerConfig{
			DefaultModel:        "gpt-4.1-mini",
			DefaultSystemPrompt: "You are helpful.",
			MaxSteps:            1,
		},
	)
	// No Todos option — nil manager.
	handler := NewWithOptions(ServerOptions{Runner: runner})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// GET should return empty list even without a manager.
	resp, err := http.Get(ts.URL + "/v1/runs/run-1/todos")
	if err != nil {
		t.Fatalf("GET todos: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// PUT should return 501 without a manager.
	payload := `{"todos":[]}`
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/runs/run-1/todos", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT todos: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", resp2.StatusCode)
	}
}

func TestExtractRunID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path       string
		wantRunID  string
		wantSuffix string
	}{
		{"/v1/runs/abc123/todos", "abc123", "todos"},
		{"/v1/runs/run-xyz/todos", "run-xyz", "todos"},
		{"/v1/runs/run-1", "run-1", ""},
		{"/v1/runs/", "", ""},
	}
	for _, tc := range cases {
		runID, suffix := extractRunID(tc.path)
		if runID != tc.wantRunID {
			t.Errorf("extractRunID(%q) runID = %q, want %q", tc.path, runID, tc.wantRunID)
		}
		if suffix != tc.wantSuffix {
			t.Errorf("extractRunID(%q) suffix = %q, want %q", tc.path, suffix, tc.wantSuffix)
		}
	}
}

func TestTodosConcurrentAccess(t *testing.T) {
	t.Parallel()
	ts, _ := newTodoTestServer(t)

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			runID := fmt.Sprintf("run-%d", i)
			payload := fmt.Sprintf(`{"todos":[{"id":"%d","text":"task","status":"pending"}]}`, i)
			req, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/runs/"+runID+"/todos", bytes.NewBufferString(payload))
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			resp.Body.Close()
		}(i)
	}
	wg.Wait()
}
