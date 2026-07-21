package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStartRunCmdIncludesWorkspacePath(t *testing.T) {
	t.Parallel()

	var got runCreateRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(runCreateResponse{RunID: "run-workspace"}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer ts.Close()

	msg := startRunCmd(ts.URL, "hello", "", "gpt-test", "openai", "low", "default", "/tmp/project-root", "", nil, nil)()
	if _, ok := msg.(RunStartedMsg); !ok {
		t.Fatalf("expected RunStartedMsg, got %T: %+v", msg, msg)
	}
	if got.WorkspacePath != "/tmp/project-root" {
		t.Fatalf("workspace_path = %q, want /tmp/project-root", got.WorkspacePath)
	}
}

func TestStartRunCmdSendsCapabilityProfileAsProfileField(t *testing.T) {
	t.Parallel()

	var rawBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&rawBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(runCreateResponse{RunID: "run-profile"})
	}))
	defer ts.Close()

	// A capability profile selected via /profiles (e.g. "researcher") must be
	// sent in the "profile" field (harness.RunRequest.ProfileName), NOT in
	// "prompt_profile" — the server rejects unknown prompt profiles with HTTP 400.
	msg := startRunCmd(ts.URL, "hello", "", "gpt-test", "openai", "", "researcher", "/tmp/x", "", nil, nil)()
	if _, ok := msg.(RunStartedMsg); !ok {
		t.Fatalf("expected RunStartedMsg, got %T: %+v", msg, msg)
	}
	if got, ok := rawBody["profile"]; !ok || got != "researcher" {
		t.Errorf(`request must include "profile":"researcher"; got profile=%v (present=%v)`, got, ok)
	}
	if _, ok := rawBody["prompt_profile"]; ok {
		t.Errorf(`capability profile must NOT be sent as "prompt_profile"; body=%v`, rawBody)
	}
}

func TestLoadSubagentsCmdReturnsDecodedSubagents(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/subagents" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"subagents": []RemoteSubagent{{
				ID:            "sub-1",
				Status:        "running",
				Isolation:     "worktree",
				CleanupPolicy: "destroy",
			}},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer ts.Close()

	msg := loadSubagentsCmd(ts.URL, "")()
	loaded, ok := msg.(SubagentsLoadedMsg)
	if !ok {
		t.Fatalf("expected SubagentsLoadedMsg, got %T", msg)
	}
	if len(loaded.Subagents) != 1 || loaded.Subagents[0].ID != "sub-1" {
		t.Fatalf("unexpected subagents payload: %+v", loaded.Subagents)
	}
}

func TestFormatRunErrorRendersJSONFields(t *testing.T) {
	t.Parallel()

	lines := formatRunError(`provider completion failed: {"error":{"message":"boom","type":"invalid_request"},"request_id":"req_123","ignored":null}`)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "✗ provider completion failed") {
		t.Fatalf("expected failure prefix, got %q", joined)
	}
	if !strings.Contains(joined, "message: boom") {
		t.Fatalf("expected nested message field, got %q", joined)
	}
	if !strings.Contains(joined, "type: invalid_request") {
		t.Fatalf("expected nested type field, got %q", joined)
	}
	if !strings.Contains(joined, "request_id: req_123") {
		t.Fatalf("expected top-level request id, got %q", joined)
	}
	if strings.Contains(joined, "ignored") {
		t.Fatalf("expected nil field to be omitted, got %q", joined)
	}
}

func TestFlattenJSONRendersNestedMapsAndSkipsNil(t *testing.T) {
	t.Parallel()

	lines := flattenJSON(map[string]any{
		"outer": map[string]any{"inner": "value"},
		"count": 3,
		"skip":  nil,
	}, "  ")
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "outer:") {
		t.Fatalf("expected parent key, got %q", joined)
	}
	if !strings.Contains(joined, "inner: value") {
		t.Fatalf("expected nested key/value, got %q", joined)
	}
	if !strings.Contains(joined, "count: 3") {
		t.Fatalf("expected scalar field, got %q", joined)
	}
	if strings.Contains(joined, "skip") {
		t.Fatalf("expected nil field to be skipped, got %q", joined)
	}
}

func TestStartRunCmdSetsAllowFallback(t *testing.T) {
	t.Parallel()

	var got runCreateRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(runCreateResponse{RunID: "run-fallback"}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer ts.Close()

	msg := startRunCmd(ts.URL, "hello", "", "gpt-test", "openai", "low", "default", "", "", nil, nil)()
	if _, ok := msg.(RunStartedMsg); !ok {
		t.Fatalf("expected RunStartedMsg, got %T: %+v", msg, msg)
	}
	if !got.AllowFallback {
		t.Fatalf("expected allow_fallback=true in POST body, got false")
	}
}

func TestFormatSubagentsLinesRendersSummaryAndDetails(t *testing.T) {
	t.Parallel()

	if got := formatSubagentsLines(nil, nil); len(got) != 1 || got[0] != "No managed subagents." {
		t.Fatalf("unexpected empty-state lines: %v", got)
	}

	lines := formatSubagentsLines([]RemoteSubagent{{
		ID:               "sub-1",
		Status:           "completed",
		Isolation:        "worktree",
		CleanupPolicy:    "destroy",
		WorkspaceCleaned: true,
		BranchName:       "codex/coverage-fix",
		BaseRef:          "main",
		WorkspacePath:    "/tmp/sub-1",
	}}, nil)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "sub-1 [completed] worktree (destroy) cleaned") {
		t.Fatalf("expected summary line, got %q", joined)
	}
	if !strings.Contains(joined, "branch=codex/coverage-fix") || !strings.Contains(joined, "base=main") || !strings.Contains(joined, "path=/tmp/sub-1") {
		t.Fatalf("expected detail line, got %q", joined)
	}
}
