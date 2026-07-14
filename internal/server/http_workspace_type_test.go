package server

// http_workspace_type_test.go — issue #561: POST /v1/runs must reject
// workspace_type values that can never provision with a synchronous 400,
// instead of returning 202/queued and failing the run asynchronously during
// provisioning. Environment-dependent types (container needs Docker, vm needs
// HETZNER_API_KEY) are still accepted at creation and fail at provisioning
// time, so they are deliberately not asserted on here.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
)

func newWorkspaceTypeTestServer(t *testing.T) *httptest.Server {
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

func postWorkspaceTypeRun(t *testing.T, ts *httptest.Server, body string) (int, string) {
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

func decodeWorkspaceTypeError(t *testing.T, raw string) (code, message string) {
	t.Helper()

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode error body %q: %v", raw, err)
	}
	return body.Error.Code, body.Error.Message
}

func TestPostRunWorkspaceTypeUnknownReturns400(t *testing.T) {
	t.Parallel()

	ts := newWorkspaceTypeTestServer(t)
	status, raw := postWorkspaceTypeRun(t, ts, `{"prompt":"hello","workspace_type":"wroktree"}`)

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", status, raw)
	}
	code, message := decodeWorkspaceTypeError(t, raw)
	if code != "invalid_request" {
		t.Fatalf("error code = %q, want invalid_request: %s", code, raw)
	}
	if !strings.Contains(message, `"wroktree"`) {
		t.Fatalf("message should name the invalid workspace_type, got %q", message)
	}
	if !strings.Contains(message, "local, worktree, container, vm") {
		t.Fatalf("message should list supported values, got %q", message)
	}
	if strings.Contains(raw, "run_id") || strings.Contains(raw, "queued") {
		t.Fatalf("invalid workspace request must not create a queued run: %s", raw)
	}
}

func TestPostRunWorkspaceTypeWorktreeWithoutRepoReturns400(t *testing.T) {
	t.Parallel()

	// The test runner is configured without WorkspaceBaseOptions.RepoPath, so
	// worktree provisioning can never succeed — the request must fail at
	// creation, not after the run is queued.
	ts := newWorkspaceTypeTestServer(t)
	status, raw := postWorkspaceTypeRun(t, ts, `{"prompt":"hello","workspace_type":"worktree"}`)

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", status, raw)
	}
	code, message := decodeWorkspaceTypeError(t, raw)
	if code != "invalid_request" {
		t.Fatalf("error code = %q, want invalid_request: %s", code, raw)
	}
	for _, want := range []string{"workspace_type=worktree", "RepoPath"} {
		if !strings.Contains(message, want) {
			t.Fatalf("message should contain %q, got %q", want, message)
		}
	}
	if strings.Contains(raw, "run_id") || strings.Contains(raw, "queued") {
		t.Fatalf("unconfigured worktree request must not create a queued run: %s", raw)
	}
}

func TestPostRunWorkspaceTypeLocalStillProvisions(t *testing.T) {
	t.Parallel()

	ts := newWorkspaceTypeTestServer(t)
	status, raw := postWorkspaceTypeRun(t, ts, `{"prompt":"hello","workspace_type":"local"}`)

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

	// Poll to terminal so the events replay below is complete and non-blocking.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not reach terminal state within 10s", created.RunID)
		}
		res, err := http.Get(ts.URL + "/v1/runs/" + created.RunID)
		if err == nil {
			var runState struct {
				Status string `json:"status"`
			}
			decodeErr := json.NewDecoder(res.Body).Decode(&runState)
			res.Body.Close()
			if decodeErr == nil {
				if runState.Status == "failed" || runState.Status == "cancelled" {
					t.Fatalf("run %s status = %q, want completed", created.RunID, runState.Status)
				}
				if runState.Status == "completed" {
					break
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	eventsRes, err := http.Get(ts.URL + "/v1/runs/" + created.RunID + "/events")
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer eventsRes.Body.Close()
	rawEvents, err := io.ReadAll(eventsRes.Body)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if !strings.Contains(string(rawEvents), "workspace.provisioned") {
		t.Fatalf("expected workspace.provisioned event for workspace_type=local, got: %s", string(rawEvents))
	}
}
