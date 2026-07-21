package server

// http_extra_dirs_test.go — POST /v1/runs validates extra_dirs synchronously
// (TUI /add-dir, epic #822 slice 3): entries that are not absolute paths to
// existing directories get a 400 at creation, not a queued run that fails
// asynchronously. Mirrors http_workspace_type_test.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-agent-harness/internal/harness"
)

func newExtraDirsTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "done"}},
		harness.NewRegistry(),
		harness.RunnerConfig{DefaultModel: "test-model", MaxSteps: 1},
	)
	t.Cleanup(func() { _ = runner.Shutdown(context.Background()) })

	ts := httptest.NewServer(New(runner))
	t.Cleanup(ts.Close)
	return ts
}

func postExtraDirsRun(t *testing.T, ts *httptest.Server, body string) (int, string) {
	t.Helper()

	res, err := http.Post(ts.URL+"/v1/runs", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST /v1/runs: %v", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return res.StatusCode, string(raw)
}

func TestPostRunExtraDirsNonexistentReturns400(t *testing.T) {
	t.Parallel()

	ts := newExtraDirsTestServer(t)
	status, raw := postExtraDirsRun(t, ts, `{"prompt":"hello","extra_dirs":["/definitely/does/not/exist"]}`)

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", status, raw)
	}
	if !strings.Contains(raw, "extra_dirs") {
		t.Fatalf("error body should name extra_dirs, got %s", raw)
	}
	if strings.Contains(raw, "run_id") {
		t.Fatalf("invalid extra_dirs must not create a run: %s", raw)
	}
}

func TestPostRunExtraDirsRelativeReturns400(t *testing.T) {
	t.Parallel()

	ts := newExtraDirsTestServer(t)
	status, raw := postExtraDirsRun(t, ts, `{"prompt":"hello","extra_dirs":["../escape"]}`)

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", status, raw)
	}
	if !strings.Contains(raw, "extra_dirs") {
		t.Fatalf("error body should name extra_dirs, got %s", raw)
	}
}

func TestPostRunExtraDirsValidAccepted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	body := `{"prompt":"hello","extra_dirs":[` + `"` + dir + `"` + `]}`

	ts := newExtraDirsTestServer(t)
	status, raw := postExtraDirsRun(t, ts, body)

	if status != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", status, raw)
	}
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal([]byte(raw), &created); err != nil {
		t.Fatalf("decode create response %q: %v", raw, err)
	}
	if created.RunID == "" {
		t.Fatalf("expected run_id in create response: %s", raw)
	}
}
