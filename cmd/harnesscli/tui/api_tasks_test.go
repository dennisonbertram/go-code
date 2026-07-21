package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestLoadTasksCmdSuccess verifies loadTasksCmd fetches GET /v1/tasks and
// decodes the union into TasksLoadedMsg.
func TestLoadTasksCmdSuccess(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tasks" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"tasks": []map[string]any{{
				"id":          "jm1:job_1",
				"type":        "bash_job",
				"status":      "running",
				"label":       "sleep 30",
				"started_at":  time.Now().UTC().Add(-5 * time.Second),
				"age_seconds": 5,
				"actions":     []string{"cancel"},
			}, {
				"id":          "sub-1",
				"type":        "subagent",
				"status":      "running",
				"label":       "workspace-sub-1",
				"started_at":  time.Now().UTC().Add(-2 * time.Minute),
				"age_seconds": 120,
				"actions":     []string{"cancel"},
			}},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer ts.Close()

	msg := loadTasksCmd(ts.URL, "")()
	loaded, ok := msg.(TasksLoadedMsg)
	if !ok {
		t.Fatalf("expected TasksLoadedMsg, got %T: %+v", msg, msg)
	}
	if len(loaded.Tasks) != 2 {
		t.Fatalf("Tasks = %d, want 2: %+v", len(loaded.Tasks), loaded.Tasks)
	}
	first := loaded.Tasks[0]
	if first.ID != "jm1:job_1" || first.Type != "bash_job" || first.Status != "running" || first.Label != "sleep 30" {
		t.Errorf("first task = %+v", first)
	}
	if first.AgeSeconds != 5 {
		t.Errorf("first task AgeSeconds = %d, want 5", first.AgeSeconds)
	}
	if len(first.Actions) != 1 || first.Actions[0] != "cancel" {
		t.Errorf("first task Actions = %v, want [cancel]", first.Actions)
	}
}

// TestLoadTasksCmdHTTPError verifies non-200 responses map to
// TasksLoadFailedMsg.
func TestLoadTasksCmdHTTPError(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	msg := loadTasksCmd(ts.URL, "")()
	failed, ok := msg.(TasksLoadFailedMsg)
	if !ok {
		t.Fatalf("expected TasksLoadFailedMsg, got %T: %+v", msg, msg)
	}
	if failed.Err == "" {
		t.Error("TasksLoadFailedMsg.Err should describe the failure")
	}
}

// TestLoadTasksCmdInvalidJSON verifies undecodable responses map to
// TasksLoadFailedMsg rather than a partial Task list.
func TestLoadTasksCmdInvalidJSON(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tasks": not-json`))
	}))
	defer ts.Close()

	msg := loadTasksCmd(ts.URL, "")()
	if _, ok := msg.(TasksLoadFailedMsg); !ok {
		t.Fatalf("expected TasksLoadFailedMsg, got %T: %+v", msg, msg)
	}
}

// TestLoadTasksCmdUnreachable verifies connection failures map to
// TasksLoadFailedMsg.
func TestLoadTasksCmdUnreachable(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	ts.Close() // immediately unreachable

	msg := loadTasksCmd(ts.URL, "")()
	if _, ok := msg.(TasksLoadFailedMsg); !ok {
		t.Fatalf("expected TasksLoadFailedMsg, got %T: %+v", msg, msg)
	}
}
