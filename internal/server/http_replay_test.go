package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"go-agent-harness/internal/harness"
)

func newTestReplayServer(t *testing.T) *httptest.Server {
	t.Helper()
	registry := harness.NewRegistry()
	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "forked output"}},
		registry,
		harness.RunnerConfig{
			DefaultModel:        "gpt-4.1-mini",
			DefaultSystemPrompt: "test",
			MaxSteps:            2,
		},
	)
	handler := NewWithOptions(ServerOptions{
		Runner:       runner,
		AuthDisabled: true,
	})
	return httptest.NewServer(handler)
}

func newTestReplayServerWithRolloutDir(t *testing.T, rolloutDir string) *httptest.Server {
	t.Helper()
	registry := harness.NewRegistry()
	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "forked output"}},
		registry,
		harness.RunnerConfig{
			DefaultModel:        "gpt-4.1-mini",
			DefaultSystemPrompt: "test",
			MaxSteps:            2,
			RolloutDir:          rolloutDir,
		},
	)
	handler := NewWithOptions(ServerOptions{
		Runner:       runner,
		AuthDisabled: true,
		RolloutDir:   rolloutDir,
	})
	return httptest.NewServer(handler)
}

func writeTestRollout(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHandleRunReplay_Simulate(t *testing.T) {
	t.Parallel()
	ts := newTestReplayServer(t)
	defer ts.Close()

	dir := t.TempDir()
	content := `{"ts":"2026-03-12T10:00:00Z","seq":1,"type":"run.started","data":{"step":0,"prompt":"hello"}}
{"ts":"2026-03-12T10:00:01Z","seq":2,"type":"llm.turn.completed","data":{"step":1,"content":"running bash","tool_calls":[{"id":"c1","name":"bash","arguments":"{}"}]}}
{"ts":"2026-03-12T10:00:02Z","seq":3,"type":"tool.call.started","data":{"step":1,"call_id":"c1","tool":"bash","arguments":"{}"}}
{"ts":"2026-03-12T10:00:03Z","seq":4,"type":"tool.call.completed","data":{"step":1,"call_id":"c1","tool":"bash","result":"ok"}}
{"ts":"2026-03-12T10:00:04Z","seq":5,"type":"run.completed","data":{"step":2}}`
	rolloutPath := writeTestRollout(t, dir, "test.jsonl", content)

	body, _ := json.Marshal(map[string]any{
		"rollout_path": rolloutPath,
		"mode":         "simulate",
	})

	resp, err := http.Post(ts.URL+"/v1/runs/replay", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if result["mode"] != "simulate" {
		t.Errorf("expected mode=simulate, got %v", result["mode"])
	}
	eventsReplayed, ok := result["events_replayed"].(float64)
	if !ok || eventsReplayed != 5 {
		t.Errorf("expected 5 events_replayed, got %v", result["events_replayed"])
	}
	if result["matched"] != true {
		t.Errorf("expected matched=true, got %v", result["matched"])
	}
}

func TestHandleRunReplay_DetectDriftReturns503WhenSemaphoreFull(t *testing.T) {
	t.Parallel()

	var constructed int32
	s := &Server{
		authDisabled:   true,
		replayDriftSem: make(chan struct{}, 2),
		driftRunnerFactory: func(provider harness.Provider, registry *harness.Registry, cfg harness.RunnerConfig) *harness.Runner {
			atomic.AddInt32(&constructed, 1)
			return harness.NewRunner(provider, registry, cfg)
		},
	}
	s.replayDriftSem <- struct{}{}
	s.replayDriftSem <- struct{}{}

	ts := httptest.NewServer(s.hardenHandler(http.HandlerFunc(s.handleRunReplay)))
	defer ts.Close()

	dir := t.TempDir()
	content := `{"ts":"2026-03-12T10:00:00Z","seq":1,"type":"run.started","data":{"step":0,"prompt":"hello"}}
{"ts":"2026-03-12T10:00:01Z","seq":2,"type":"llm.turn.completed","data":{"step":1,"content":"hi"}}
{"ts":"2026-03-12T10:00:02Z","seq":3,"type":"run.completed","data":{"step":2}}`
	rolloutPath := writeTestRollout(t, dir, "busy.jsonl", content)

	body, _ := json.Marshal(map[string]any{
		"rollout_path": rolloutPath,
		"mode":         "simulate",
		"detect_drift": true,
	})
	resp, err := http.Post(ts.URL+"/v1/runs/replay", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&constructed); got != 0 {
		t.Fatalf("expected saturated drift gate to skip runner construction, got %d", got)
	}
}

func TestHandleRunReplay_SimulateResolvesBareRunID(t *testing.T) {
	t.Parallel()
	rolloutDir := t.TempDir()
	ts := newTestReplayServerWithRolloutDir(t, rolloutDir)
	defer ts.Close()

	content := `{"ts":"2026-03-12T10:00:00Z","seq":1,"type":"run.started","data":{"step":0,"prompt":"hello"}}
{"ts":"2026-03-12T10:00:01Z","seq":2,"type":"run.completed","data":{"step":1}}`
	datedDir := filepath.Join(rolloutDir, "2026-03-12")
	if err := os.MkdirAll(datedDir, 0o755); err != nil {
		t.Fatalf("mkdir rollout date dir: %v", err)
	}
	writeTestRollout(t, datedDir, "run_bare.jsonl", content)

	body, _ := json.Marshal(map[string]any{
		"rollout_path": "run_bare",
		"mode":         "simulate",
	})
	resp, err := http.Post(ts.URL+"/v1/runs/replay", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if result["events_replayed"] != float64(2) {
		t.Fatalf("events_replayed = %v, want 2", result["events_replayed"])
	}
}

func TestHandleRunReplay_Fork(t *testing.T) {
	t.Parallel()
	ts := newTestReplayServer(t)
	defer ts.Close()

	dir := t.TempDir()
	content := `{"ts":"2026-03-12T10:00:00Z","seq":1,"type":"run.started","data":{"step":0,"prompt":"hello","system_prompt":"be helpful"}}
{"ts":"2026-03-12T10:00:01Z","seq":2,"type":"llm.turn.completed","data":{"step":1,"content":"I will help"}}
{"ts":"2026-03-12T10:00:02Z","seq":3,"type":"run.completed","data":{"step":2}}`
	rolloutPath := writeTestRollout(t, dir, "test.jsonl", content)

	body, _ := json.Marshal(map[string]any{
		"rollout_path": rolloutPath,
		"mode":         "fork",
		"fork_step":    1,
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

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if result["mode"] != "fork" {
		t.Errorf("expected mode=fork, got %v", result["mode"])
	}
	if result["run_id"] == nil || result["run_id"] == "" {
		t.Error("expected non-empty run_id")
	}
	if fromStep, ok := result["from_step"].(float64); !ok || fromStep != 1 {
		t.Errorf("expected from_step=1, got %v", result["from_step"])
	}
}

func TestHandleRunReplay_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	ts := newTestReplayServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/runs/replay")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
}

func TestHandleRunReplay_MissingRolloutPath(t *testing.T) {
	t.Parallel()
	ts := newTestReplayServer(t)
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{
		"mode": "simulate",
	})

	resp, err := http.Post(ts.URL+"/v1/runs/replay", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleRunReplay_InvalidMode(t *testing.T) {
	t.Parallel()
	ts := newTestReplayServer(t)
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{
		"rollout_path": "/tmp/test.jsonl",
		"mode":         "invalid",
	})

	resp, err := http.Post(ts.URL+"/v1/runs/replay", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleRunReplay_RolloutNotFound(t *testing.T) {
	t.Parallel()
	ts := newTestReplayServer(t)
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{
		"rollout_path": "/nonexistent/path/rollout.jsonl",
		"mode":         "simulate",
	})

	resp, err := http.Post(ts.URL+"/v1/runs/replay", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestHandleRunReplay_ForkStepExceedsMax(t *testing.T) {
	t.Parallel()
	ts := newTestReplayServer(t)
	defer ts.Close()

	dir := t.TempDir()
	content := `{"ts":"2026-03-12T10:00:00Z","seq":1,"type":"run.started","data":{"step":0,"prompt":"hello"}}
{"ts":"2026-03-12T10:00:01Z","seq":2,"type":"run.completed","data":{"step":1}}`
	rolloutPath := writeTestRollout(t, dir, "test.jsonl", content)

	body, _ := json.Marshal(map[string]any{
		"rollout_path": rolloutPath,
		"mode":         "fork",
		"fork_step":    99,
	})

	resp, err := http.Post(ts.URL+"/v1/runs/replay", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleRunReplay_InvalidJSON(t *testing.T) {
	t.Parallel()
	ts := newTestReplayServer(t)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/v1/runs/replay", "application/json",
		bytes.NewReader([]byte("{invalid")))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestExtractLastUserPrompt(t *testing.T) {
	tests := []struct {
		name     string
		msgs     []harness.Message
		expected string
	}{
		{
			name:     "single_user",
			msgs:     []harness.Message{{Role: "user", Content: "hello"}},
			expected: "hello",
		},
		{
			name: "multiple_users",
			msgs: []harness.Message{
				{Role: "user", Content: "first"},
				{Role: "assistant", Content: "reply"},
				{Role: "user", Content: "second"},
			},
			expected: "second",
		},
		{
			name:     "no_user",
			msgs:     []harness.Message{{Role: "system", Content: "sys"}},
			expected: "forked run",
		},
		{
			name:     "empty",
			msgs:     nil,
			expected: "forked run",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractLastUserPrompt(tt.msgs)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

// Ensure the /v1/runs/replay route doesn't interfere with normal run lookups.
func TestHandleRunByID_StillWorks(t *testing.T) {
	t.Parallel()
	ts := newTestReplayServer(t)
	defer ts.Close()

	// A GET to /v1/runs/nonexistent should return 404, not be treated as replay.
	resp, err := http.Get(ts.URL + "/v1/runs/nonexistent")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

// Suppress unused import warning for context.
var _ = context.Background
