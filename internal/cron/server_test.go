package cron

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestServer(t *testing.T) (http.Handler, *mockStore) {
	t.Helper()
	store := &mockStore{}
	clock := newMockClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	executor := &mockExecutor{}
	scheduler := NewScheduler(store, executor, clock, SchedulerConfig{MaxConcurrent: 1})
	handler := NewServer(store, scheduler, clock)
	return handler, store
}

func TestServerHealth(t *testing.T) {
	handler, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected ok, got %q", body["status"])
	}
}

func TestServerCreateJob(t *testing.T) {
	handler, store := newTestServer(t)
	store.CreateJobFunc = func(_ context.Context, job Job) (Job, error) {
		return job, nil
	}

	payload := `{"name":"test-job","schedule":"*/5 * * * *","execution_type":"shell","execution_config":"{\"command\":\"echo hi\"}"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(payload))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var job Job
	if err := json.NewDecoder(w.Body).Decode(&job); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if job.Name != "test-job" {
		t.Fatalf("expected test-job, got %q", job.Name)
	}
	if job.Status != StatusActive {
		t.Fatalf("expected active, got %q", job.Status)
	}
	if job.ID == "" {
		t.Fatalf("expected non-empty ID")
	}
}

func TestServerCreateJobValidation(t *testing.T) {
	handler, _ := newTestServer(t)

	tests := []struct {
		name    string
		payload string
		errMsg  string
	}{
		{"missing name", `{"schedule":"* * * * *","execution_type":"shell"}`, "name is required"},
		{"missing schedule", `{"name":"x","execution_type":"shell"}`, "schedule is required"},
		{"bad schedule", `{"name":"x","schedule":"bad","execution_type":"shell"}`, "invalid schedule"},
		{"bad exec type", `{"name":"x","schedule":"* * * * *","execution_type":"bad"}`, "execution_type"},
		{"invalid json", `{bad`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(tt.payload))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
			if tt.errMsg != "" && !strings.Contains(w.Body.String(), tt.errMsg) {
				t.Fatalf("expected error containing %q, got %s", tt.errMsg, w.Body.String())
			}
		})
	}
}

func TestServerListJobs(t *testing.T) {
	handler, store := newTestServer(t)
	j := testJob("list-test")
	store.ListJobsFunc = func(_ context.Context) ([]Job, error) {
		return []Job{j}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/jobs", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result struct {
		Jobs []Job `json:"jobs"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(result.Jobs))
	}
}

func TestServerListJobsEmpty(t *testing.T) {
	handler, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/jobs", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result struct {
		Jobs []Job `json:"jobs"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Jobs) != 0 {
		t.Fatalf("expected 0 jobs, got %d", len(result.Jobs))
	}
}

func TestServerGetJobByID(t *testing.T) {
	handler, store := newTestServer(t)
	j := testJob("get-test")
	store.GetJobFunc = func(_ context.Context, id string) (Job, error) {
		if id == j.ID {
			return j, nil
		}
		return Job{}, sql.ErrNoRows
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+j.ID, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got Job
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "get-test" {
		t.Fatalf("expected get-test, got %q", got.Name)
	}
}

func TestServerGetJobByName(t *testing.T) {
	handler, store := newTestServer(t)
	j := testJob("named-job")
	store.GetJobFunc = func(_ context.Context, id string) (Job, error) {
		return Job{}, sql.ErrNoRows
	}
	store.GetJobByNameFunc = func(_ context.Context, name string) (Job, error) {
		if name == "named-job" {
			return j, nil
		}
		return Job{}, sql.ErrNoRows
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/named-job", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got Job
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "named-job" {
		t.Fatalf("expected named-job, got %q", got.Name)
	}
}

func TestServerGetJobNotFound(t *testing.T) {
	handler, store := newTestServer(t)
	store.GetJobFunc = func(_ context.Context, id string) (Job, error) {
		return Job{}, sql.ErrNoRows
	}
	store.GetJobByNameFunc = func(_ context.Context, name string) (Job, error) {
		return Job{}, sql.ErrNoRows
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/nonexistent", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestServerUpdateJobSchedule(t *testing.T) {
	handler, store := newTestServer(t)
	j := testJob("update-test")
	store.GetJobFunc = func(_ context.Context, id string) (Job, error) {
		if id == j.ID {
			return j, nil
		}
		return Job{}, sql.ErrNoRows
	}
	var updated Job
	store.UpdateJobFunc = func(_ context.Context, job Job) error {
		updated = job
		return nil
	}

	newSchedule := "0 * * * *"
	payload := fmt.Sprintf(`{"schedule":"%s"}`, newSchedule)
	req := httptest.NewRequest(http.MethodPatch, "/v1/jobs/"+j.ID, strings.NewReader(payload))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if updated.Schedule != newSchedule {
		t.Fatalf("expected schedule %q, got %q", newSchedule, updated.Schedule)
	}
}

// TestServerUpdateJobSchedule_PausedJobNotReArmed (BT-005, P2) reproduces
// BUG 4: PATCHing only the schedule of a PAUSED job re-arms it in the live
// scheduler. The re-arm condition in handleUpdateJob (~line 239) is
// `req.Schedule != nil && (req.Status == nil || *req.Status == StatusActive)`.
// A schedule-only PATCH leaves req.Status nil, so a paused job is added
// back to the live scheduler even though its stored status remains
// "paused" — a paused job starts firing (or, after the BUG-2 fix, at least
// sits incorrectly registered in the scheduler's live entry set).
//
// This test fails before the fix (the job ends up in scheduler.entries)
// and passes after the fix (gating on the job's effective post-update
// status rather than the request's status field).
func TestServerUpdateJobSchedule_PausedJobNotReArmed(t *testing.T) {
	store := &mockStore{}
	clock := newMockClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	executor := &mockExecutor{}
	scheduler := NewScheduler(store, executor, clock, SchedulerConfig{MaxConcurrent: 1})
	handler := NewServer(store, scheduler, clock)

	// j is already paused and, per the real job lifecycle, was removed
	// from the live scheduler when it was paused — it is NOT in
	// scheduler.entries at the start of this test.
	j := testJob("patch-paused-schedule")
	j.Status = StatusPaused

	store.GetJobFunc = func(_ context.Context, id string) (Job, error) {
		return j, nil
	}
	store.UpdateJobFunc = func(_ context.Context, job Job) error { return nil }

	// PATCH only the schedule — no "status" field in the request body.
	newSchedule := "0 * * * *"
	payload := fmt.Sprintf(`{"schedule":"%s"}`, newSchedule)
	req := httptest.NewRequest(http.MethodPatch, "/v1/jobs/"+j.ID, strings.NewReader(payload))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	scheduler.mu.Lock()
	_, scheduled := scheduler.entries[j.ID]
	scheduler.mu.Unlock()
	if scheduled {
		t.Fatal("expected a schedule-only PATCH on a paused job to NOT re-arm it in the live scheduler, but it was added to scheduler.entries")
	}
}

func TestServerUpdateJobPause(t *testing.T) {
	handler, store := newTestServer(t)
	j := testJob("pause-test")
	store.GetJobFunc = func(_ context.Context, id string) (Job, error) {
		return j, nil
	}
	var updated Job
	store.UpdateJobFunc = func(_ context.Context, job Job) error {
		updated = job
		return nil
	}

	payload := `{"status":"paused"}`
	req := httptest.NewRequest(http.MethodPatch, "/v1/jobs/"+j.ID, strings.NewReader(payload))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if updated.Status != StatusPaused {
		t.Fatalf("expected paused, got %q", updated.Status)
	}
}

func TestServerUpdateJobResume(t *testing.T) {
	handler, store := newTestServer(t)
	j := testJob("resume-test")
	j.Status = StatusPaused
	store.GetJobFunc = func(_ context.Context, id string) (Job, error) {
		return j, nil
	}
	store.UpdateJobFunc = func(_ context.Context, job Job) error {
		return nil
	}

	payload := `{"status":"active"}`
	req := httptest.NewRequest(http.MethodPatch, "/v1/jobs/"+j.ID, strings.NewReader(payload))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServerUpdateJobNotFound(t *testing.T) {
	handler, store := newTestServer(t)
	store.GetJobFunc = func(_ context.Context, id string) (Job, error) {
		return Job{}, sql.ErrNoRows
	}

	req := httptest.NewRequest(http.MethodPatch, "/v1/jobs/missing", strings.NewReader(`{"status":"paused"}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestServerUpdateJobInvalidJSON(t *testing.T) {
	handler, store := newTestServer(t)
	j := testJob("bad-json")
	store.GetJobFunc = func(_ context.Context, id string) (Job, error) {
		return j, nil
	}

	req := httptest.NewRequest(http.MethodPatch, "/v1/jobs/"+j.ID, strings.NewReader(`{bad`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestServerDeleteJob(t *testing.T) {
	handler, store := newTestServer(t)
	deleted := false
	store.DeleteJobFunc = func(_ context.Context, id string) error {
		deleted = true
		return nil
	}

	req := httptest.NewRequest(http.MethodDelete, "/v1/jobs/some-id", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	if !deleted {
		t.Fatalf("expected delete to be called")
	}
}

func TestServerHistory(t *testing.T) {
	handler, store := newTestServer(t)
	exec := Execution{
		ID:     "exec-1",
		JobID:  "job-1",
		Status: ExecStatusSuccess,
	}
	store.ListExecutionsFunc = func(_ context.Context, jobID string, limit, offset int) ([]Execution, error) {
		return []Execution{exec}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/job-1/history?limit=10&offset=0", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result struct {
		Executions []Execution `json:"executions"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Executions) != 1 {
		t.Fatalf("expected 1 execution, got %d", len(result.Executions))
	}
}

func TestServerHistoryEmpty(t *testing.T) {
	handler, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/job-1/history", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result struct {
		Executions []Execution `json:"executions"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Executions) != 0 {
		t.Fatalf("expected 0 executions, got %d", len(result.Executions))
	}
}

func TestServerMethodNotAllowed(t *testing.T) {
	handler, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodDelete, "/v1/jobs", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestServerJobByIDMethodNotAllowed(t *testing.T) {
	handler, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/jobs/some-id", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestServerHistoryMethodNotAllowed(t *testing.T) {
	handler, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/jobs/some-id/history", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestServerJobByIDNotFound(t *testing.T) {
	handler, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestServerJobByIDUnknownSubpath(t *testing.T) {
	handler, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/some-id/unknown", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestNextRunTime(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	next, err := NextRunTime("*/5 * * * *", from)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := time.Date(2025, 1, 1, 0, 5, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Fatalf("expected %v, got %v", expected, next)
	}

	_, err = NextRunTime("bad-schedule", from)
	if err == nil {
		t.Fatalf("expected error for bad schedule")
	}
}

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, map[string]string{"key": "value"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, "test_error", "test message")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object")
	}
	if errObj["code"] != "test_error" {
		t.Fatalf("expected test_error, got %v", errObj["code"])
	}
}

func TestWriteMethodNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	writeMethodNotAllowed(w, "GET, POST")
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
	if allow := w.Header().Get("Allow"); allow != "GET, POST" {
		t.Fatalf("expected Allow: GET, POST, got %q", allow)
	}
}

func TestServerPatchInvalidStatus(t *testing.T) {
	handler, store := newTestServer(t)
	j := testJob("invalid-status-test")
	store.GetJobFunc = func(_ context.Context, id string) (Job, error) {
		return j, nil
	}

	payload := `{"status":"banana"}`
	req := httptest.NewRequest(http.MethodPatch, "/v1/jobs/"+j.ID, strings.NewReader(payload))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid status, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "status must be") {
		t.Fatalf("expected status validation error, got %s", w.Body.String())
	}
}

func TestServerLargeRequestBody(t *testing.T) {
	handler, _ := newTestServer(t)

	// Create a body larger than 1MB.
	bigBody := strings.Repeat("a", 1<<20+1)
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(bigBody))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized body, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServerPatchEmptySchedule(t *testing.T) {
	handler, store := newTestServer(t)
	j := testJob("empty-sched")
	store.GetJobFunc = func(_ context.Context, id string) (Job, error) {
		return j, nil
	}

	payload := `{"schedule":""}`
	req := httptest.NewRequest(http.MethodPatch, "/v1/jobs/"+j.ID, strings.NewReader(payload))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty schedule, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServerPatchWhitespaceSchedule(t *testing.T) {
	handler, store := newTestServer(t)
	j := testJob("ws-sched")
	store.GetJobFunc = func(_ context.Context, id string) (Job, error) {
		return j, nil
	}

	payload := `{"schedule":"  "}`
	req := httptest.NewRequest(http.MethodPatch, "/v1/jobs/"+j.ID, strings.NewReader(payload))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for whitespace schedule, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServer_ConcurrentRequests(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "concurrent-server.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	clock := RealClock{}
	scheduler := NewScheduler(store, &ShellExecutor{}, clock, SchedulerConfig{MaxConcurrent: 2})
	handler := NewServer(store, scheduler, clock)

	// First create 20 jobs sequentially so they all exist.
	var jobIDs []string
	for g := 0; g < 20; g++ {
		name := fmt.Sprintf("concurrent-server-%d", g)
		payload := fmt.Sprintf(`{"name":"%s","schedule":"*/5 * * * *","execution_type":"shell","execution_config":"{\"command\":\"echo hi\"}"}`, name)
		req := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(payload))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("setup create %d: expected 201, got %d: %s", g, w.Code, w.Body.String())
		}
		var job Job
		if err := json.NewDecoder(w.Body).Decode(&job); err != nil {
			t.Fatalf("setup decode %d: %v", g, err)
		}
		jobIDs = append(jobIDs, job.ID)
	}

	// Now do concurrent reads, gets, and deletes. The goal is race detection.
	var wg sync.WaitGroup
	var panicCount int32

	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func(gID int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					atomic.AddInt32(&panicCount, 1)
				}
			}()

			// List jobs.
			req := httptest.NewRequest(http.MethodGet, "/v1/jobs", nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			// Get a job.
			req = httptest.NewRequest(http.MethodGet, "/v1/jobs/"+jobIDs[gID], nil)
			w = httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			// Delete the job (may get SQLITE_BUSY — that's OK for race detection).
			req = httptest.NewRequest(http.MethodDelete, "/v1/jobs/"+jobIDs[gID], nil)
			w = httptest.NewRecorder()
			handler.ServeHTTP(w, req)
		}(g)
	}

	wg.Wait()

	if atomic.LoadInt32(&panicCount) > 0 {
		t.Fatalf("concurrent requests caused %d panics", panicCount)
	}
}

func TestServerCreateJobStoreError(t *testing.T) {
	handler, store := newTestServer(t)
	store.CreateJobFunc = func(_ context.Context, job Job) (Job, error) {
		return Job{}, fmt.Errorf("store failure")
	}

	payload := `{"name":"err-job","schedule":"* * * * *","execution_type":"shell"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(payload))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestServerUpdateJobInvalidSchedule(t *testing.T) {
	handler, store := newTestServer(t)
	j := testJob("bad-sched")
	store.GetJobFunc = func(_ context.Context, id string) (Job, error) {
		return j, nil
	}

	payload := `{"schedule":"bad"}`
	req := httptest.NewRequest(http.MethodPatch, "/v1/jobs/"+j.ID, strings.NewReader(payload))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServerCreateJobDefaultTimeout(t *testing.T) {
	handler, store := newTestServer(t)
	var created Job
	store.CreateJobFunc = func(_ context.Context, job Job) (Job, error) {
		created = job
		return job, nil
	}

	payload := `{"name":"default-timeout","schedule":"* * * * *","execution_type":"shell"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(payload))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if created.TimeoutSec != 30 {
		t.Fatalf("expected default timeout 30, got %d", created.TimeoutSec)
	}
}

func TestServerListJobsStoreError(t *testing.T) {
	handler, store := newTestServer(t)
	store.ListJobsFunc = func(_ context.Context) ([]Job, error) {
		return nil, fmt.Errorf("db error")
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/jobs", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

// Verify we are using bytes.Buffer correctly by testing JSON encoding round-trip
func TestServerCreateJobRoundTrip(t *testing.T) {
	handler, store := newTestServer(t)
	store.CreateJobFunc = func(_ context.Context, job Job) (Job, error) {
		return job, nil
	}

	input := CreateJobRequest{
		Name:       "round-trip",
		Schedule:   "0 0 * * *",
		ExecType:   ExecTypeShell,
		ExecConfig: `{"command":"echo test"}`,
		TimeoutSec: 60,
		Tags:       "test,ci",
	}
	body, _ := json.Marshal(input)
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var job Job
	if err := json.NewDecoder(w.Body).Decode(&job); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if job.TimeoutSec != 60 {
		t.Fatalf("expected 60 timeout, got %d", job.TimeoutSec)
	}
	if job.Tags != "test,ci" {
		t.Fatalf("expected tags 'test,ci', got %q", job.Tags)
	}
}
