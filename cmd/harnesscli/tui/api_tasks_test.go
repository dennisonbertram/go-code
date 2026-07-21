package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// --- slice 4: task output fetch + stop actions ---

// TestFetchTaskOutputCmdBashJob verifies the bash_job output path calls
// GET /v1/jobs/{id}/output and maps the payload into TaskOutputLoadedMsg.
func TestFetchTaskOutputCmdBashJob(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/jobs/jm1:job_1/output" || r.Method != http.MethodGet {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"shell_id": "job_1", "running": false, "exit_code": 0, "timed_out": false, "output": "hello-from-job",
		})
	}))
	defer ts.Close()

	task := RemoteTask{ID: "jm1:job_1", Type: "bash_job", Label: "echo hello-from-job"}
	msg := fetchTaskOutputCmd(ts.URL, "", task)()
	loaded, ok := msg.(TaskOutputLoadedMsg)
	if !ok {
		t.Fatalf("expected TaskOutputLoadedMsg, got %T: %+v", msg, msg)
	}
	if loaded.TaskID != "jm1:job_1" {
		t.Errorf("TaskID = %q, want jm1:job_1", loaded.TaskID)
	}
	if !strings.Contains(loaded.Output, "hello-from-job") {
		t.Errorf("Output = %q, want it to contain 'hello-from-job'", loaded.Output)
	}
}

// TestFetchTaskOutputCmdSubagent verifies the subagent output path calls
// GET /v1/subagents/{id} and uses the subagent's Output field.
func TestFetchTaskOutputCmdSubagent(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/subagents/sub-1" || r.Method != http.MethodGet {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "sub-1", "run_id": "run-1", "status": "completed", "output": "subagent did the thing",
		})
	}))
	defer ts.Close()

	task := RemoteTask{ID: "sub-1", Type: "subagent", Label: "workspace-sub-1"}
	msg := fetchTaskOutputCmd(ts.URL, "", task)()
	loaded, ok := msg.(TaskOutputLoadedMsg)
	if !ok {
		t.Fatalf("expected TaskOutputLoadedMsg, got %T: %+v", msg, msg)
	}
	if !strings.Contains(loaded.Output, "subagent did the thing") {
		t.Errorf("Output = %q, want subagent output", loaded.Output)
	}
}

// TestFetchTaskOutputCmdError verifies fetch failures surface as
// TaskActionResultMsg with the output action.
func TestFetchTaskOutputCmdError(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	task := RemoteTask{ID: "jm1:job_9", Type: "bash_job", Label: "x"}
	msg := fetchTaskOutputCmd(ts.URL, "", task)()
	result, ok := msg.(TaskActionResultMsg)
	if !ok {
		t.Fatalf("expected TaskActionResultMsg, got %T: %+v", msg, msg)
	}
	if result.Action != "output" || result.Err == "" {
		t.Errorf("result = %+v, want Action=output with non-empty Err", result)
	}
}

// TestCancelTaskCmdDispatch verifies each task type maps to its
// type-appropriate stop endpoint and method.
func TestCancelTaskCmdDispatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		task       RemoteTask
		wantMethod string
		wantPath   string
	}{
		{"bash job kill", RemoteTask{ID: "jm1:job_1", Type: "bash_job"}, http.MethodPost, "/v1/jobs/jm1:job_1/kill"},
		{"subagent cancel", RemoteTask{ID: "sub-1", Type: "subagent"}, http.MethodPost, "/v1/subagents/sub-1/cancel"},
		{"cron delete", RemoteTask{ID: "job-1", Type: "cron"}, http.MethodDelete, "/v1/cron/jobs/job-1"},
		{"callback cancel", RemoteTask{ID: "cb-1", Type: "callback"}, http.MethodPost, "/v1/callbacks/cb-1/cancel"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			called := false
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if r.Method != tc.wantMethod || r.URL.Path != tc.wantPath {
					t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, tc.wantMethod, tc.wantPath)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer ts.Close()

			msg := cancelTaskCmd(ts.URL, "", tc.task)()
			result, ok := msg.(TaskActionResultMsg)
			if !ok {
				t.Fatalf("expected TaskActionResultMsg, got %T: %+v", msg, msg)
			}
			if !called {
				t.Error("no request was made")
			}
			if result.Err != "" {
				t.Errorf("result.Err = %q, want empty on success", result.Err)
			}
			if result.TaskID != tc.task.ID {
				t.Errorf("result.TaskID = %q, want %q", result.TaskID, tc.task.ID)
			}
		})
	}
}

// TestCancelTaskCmdServerError verifies non-2xx responses surface as errors.
func TestCancelTaskCmdServerError(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	task := RemoteTask{ID: "job-x", Type: "cron"}
	msg := cancelTaskCmd(ts.URL, "", task)()
	result, ok := msg.(TaskActionResultMsg)
	if !ok {
		t.Fatalf("expected TaskActionResultMsg, got %T: %+v", msg, msg)
	}
	if result.Err == "" {
		t.Error("result.Err should describe the failure")
	}
}
