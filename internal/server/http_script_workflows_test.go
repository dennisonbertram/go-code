package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go-agent-harness/internal/workflow"
)

// =============================================================================
// Mock Script Workflow Manager
// =============================================================================

type mockScriptWorkflowMgr struct {
	mu        sync.Mutex
	runs      map[string]*workflow.Run
	events    map[string][]workflow.Event
	subs      map[string]map[chan workflow.Event]struct{}
	seqs      map[string]int64
	workflows []workflow.Meta
}

func newMockScriptWorkflowMgr(metas ...workflow.Meta) *mockScriptWorkflowMgr {
	if len(metas) == 0 {
		metas = []workflow.Meta{
			{Name: "test-workflow", Description: "A test workflow"},
			{Name: "review-workflow", Description: "Code review workflow", Phases: []workflow.PhaseInfo{
				{Title: "Review"}, {Title: "Verify"},
			}},
		}
	}
	return &mockScriptWorkflowMgr{
		runs:      make(map[string]*workflow.Run),
		events:    make(map[string][]workflow.Event),
		subs:      make(map[string]map[chan workflow.Event]struct{}),
		seqs:      make(map[string]int64),
		workflows: metas,
	}
}

func (m *mockScriptWorkflowMgr) List() []workflow.Meta {
	sorted := make([]workflow.Meta, len(m.workflows))
	copy(sorted, m.workflows)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	return sorted
}

func (m *mockScriptWorkflowMgr) Start(_ context.Context, name string, args any) (*workflow.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check workflow exists
	found := false
	for _, wf := range m.workflows {
		if wf.Name == name {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("workflow %q not found", name)
	}

	run := &workflow.Run{
		ID:           fmt.Sprintf("wf_%d", time.Now().UnixNano()),
		WorkflowName: name,
		Status:       workflow.RunStatusRunning,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	m.runs[run.ID] = run

	// Emit started event
	m.emitLocked(run.ID, workflow.EventWorkflowStarted, map[string]any{"workflow": name})

	// Simulate async completion after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		m.mu.Lock()
		if r, ok := m.runs[run.ID]; ok {
			r.Status = workflow.RunStatusCompleted
			r.ResultJSON = fmt.Sprintf(`{"result":"completed %s"}`, name)
			r.UpdatedAt = time.Now().UTC()
		}
		m.mu.Unlock()
		m.emit(run.ID, workflow.EventWorkflowCompleted, map[string]any{"workflow": name})
	}()

	// Return copy so callers don't race with completion goroutine.
	cp := *run
	return &cp, nil
}

func (m *mockScriptWorkflowMgr) GetRun(runID string) (*workflow.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[runID]
	if !ok {
		return nil, fmt.Errorf("run %q not found", runID)
	}
	cp := *run
	return &cp, nil
}

func (m *mockScriptWorkflowMgr) Resume(_ context.Context, runID string, _ any) (*workflow.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[runID]
	if !ok {
		return nil, fmt.Errorf("run %q not found", runID)
	}
	if run.Status != workflow.RunStatusFailed {
		return nil, fmt.Errorf("can only resume failed runs, got %s", run.Status)
	}
	run.Status = workflow.RunStatusRunning
	run.Error = ""
	run.UpdatedAt = time.Now().UTC()
	m.emitLocked(runID, workflow.EventWorkflowStarted, map[string]any{"workflow": run.WorkflowName, "resumed": true})

	go func() {
		time.Sleep(50 * time.Millisecond)
		m.mu.Lock()
		if r, ok := m.runs[runID]; ok {
			r.Status = workflow.RunStatusCompleted
			r.ResultJSON = `{"result":"resumed ok"}`
			r.UpdatedAt = time.Now().UTC()
		}
		m.mu.Unlock()
		m.emit(runID, workflow.EventWorkflowCompleted, map[string]any{"workflow": run.WorkflowName})
	}()
	// Return a copy so callers don't race.
	cp := *run
	return &cp, nil
}

func (m *mockScriptWorkflowMgr) Subscribe(runID string) ([]workflow.Event, <-chan workflow.Event, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	history := make([]workflow.Event, len(m.events[runID]))
	copy(history, m.events[runID])

	ch := make(chan workflow.Event, 64)
	if _, ok := m.subs[runID]; !ok {
		m.subs[runID] = make(map[chan workflow.Event]struct{})
	}
	m.subs[runID][ch] = struct{}{}

	cancel := func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.subs[runID], ch)
		close(ch)
	}
	return history, ch, cancel, nil
}

func (m *mockScriptWorkflowMgr) emit(runID string, typ workflow.EventType, payload map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.emitLocked(runID, typ, payload)
}

func (m *mockScriptWorkflowMgr) emitLocked(runID string, typ workflow.EventType, payload map[string]any) {
	m.seqs[runID]++
	ev := workflow.Event{
		Seq:       m.seqs[runID],
		RunID:     runID,
		Type:      typ,
		Payload:   payload,
		Timestamp: time.Now().UTC(),
	}
	m.events[runID] = append(m.events[runID], ev)
	for ch := range m.subs[runID] {
		select {
		case ch <- ev:
		default:
		}
	}
}

// =============================================================================
// Server Test Helpers
// =============================================================================

func newTestServerWithScriptWorkflows(mgr scriptWorkflowManager) *Server {
	return &Server{
		scriptWorkflows: mgr,
	}
}

func testAuth(handler http.Handler) http.Handler {
	return handler // no-op auth for tests
}

// =============================================================================
// POC 1: List script workflows
// =============================================================================

func TestPOC1_ListScriptWorkflows(t *testing.T) {
	mgr := newMockScriptWorkflowMgr()
	srv := newTestServerWithScriptWorkflows(mgr)

	mux := http.NewServeMux()
	srv.registerScriptWorkflowRoutes(mux, testAuth)

	req := httptest.NewRequest(http.MethodGet, "/v1/script-workflows", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Workflows []workflow.Meta `json:"workflows"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Len(t, resp.Workflows, 2)
	assert.Equal(t, "review-workflow", resp.Workflows[0].Name)
	assert.Equal(t, "test-workflow", resp.Workflows[1].Name)
}

// =============================================================================
// POC 2: Get workflow metadata by name
// =============================================================================

func TestPOC2_GetScriptWorkflowByName(t *testing.T) {
	mgr := newMockScriptWorkflowMgr()
	srv := newTestServerWithScriptWorkflows(mgr)

	mux := http.NewServeMux()
	srv.registerScriptWorkflowRoutes(mux, testAuth)

	// Valid workflow
	req := httptest.NewRequest(http.MethodGet, "/v1/script-workflows/test-workflow", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	var meta workflow.Meta
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &meta))
	assert.Equal(t, "test-workflow", meta.Name)

	// Unknown workflow
	req = httptest.NewRequest(http.MethodGet, "/v1/script-workflows/nonexistent", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// =============================================================================
// POC 3: Start a script workflow run
// =============================================================================

func TestPOC3_StartScriptWorkflowRun(t *testing.T) {
	mgr := newMockScriptWorkflowMgr()
	srv := newTestServerWithScriptWorkflows(mgr)

	mux := http.NewServeMux()
	srv.registerScriptWorkflowRoutes(mux, testAuth)

	body := strings.NewReader(`{"args": {"target": "production"}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/script-workflows/test-workflow/runs", body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)

	var resp struct {
		RunID        string `json:"run_id"`
		Status       string `json:"status"`
		WorkflowName string `json:"workflow_name"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.RunID)
	assert.Equal(t, "running", resp.Status)
	assert.Equal(t, "test-workflow", resp.WorkflowName)
}

// =============================================================================
// POC 4: Get run status and stream events via SSE
// =============================================================================

func TestPOC4_GetRunStatusAndStreamEvents(t *testing.T) {
	mgr := newMockScriptWorkflowMgr()
	srv := newTestServerWithScriptWorkflows(mgr)

	mux := http.NewServeMux()
	srv.registerScriptWorkflowRoutes(mux, testAuth)

	// Start a run
	req := httptest.NewRequest(http.MethodPost, "/v1/script-workflows/test-workflow/runs", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusAccepted, rec.Code)

	var startResp struct {
		RunID string `json:"run_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &startResp)
	runID := startResp.RunID

	// Wait for completion
	time.Sleep(200 * time.Millisecond)

	// Get run status
	req = httptest.NewRequest(http.MethodGet, "/v1/script-workflow-runs/"+runID, nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	var runResp struct {
		Status string `json:"status"`
	}
	json.Unmarshal(rec.Body.Bytes(), &runResp)
	assert.Equal(t, "completed", runResp.Status)
}

func TestPOC4_StreamEvents(t *testing.T) {
	mgr := newMockScriptWorkflowMgr()

	// Start a run directly via the manager
	run, err := mgr.Start(context.Background(), "test-workflow", nil)
	require.NoError(t, err)
	runID := run.ID

	// Wait for completion
	time.Sleep(200 * time.Millisecond)

	// Verify events via Subscribe (bypasses mux redirect issues)
	history, _, cancel, err := mgr.Subscribe(runID)
	require.NoError(t, err)
	defer cancel()

	hasStarted, hasCompleted := false, false
	for _, ev := range history {
		if ev.Type == workflow.EventWorkflowStarted {
			hasStarted = true
		}
		if ev.Type == workflow.EventWorkflowCompleted {
			hasCompleted = true
		}
	}
	assert.True(t, hasStarted, "should have started event")
	assert.True(t, hasCompleted, "should have completed event")
}

// =============================================================================
// POC 5: Resume a failed run
// =============================================================================

func TestPOC5_ResumeFailedRun(t *testing.T) {
	mgr := newMockScriptWorkflowMgr()
	srv := newTestServerWithScriptWorkflows(mgr)

	mux := http.NewServeMux()
	srv.registerScriptWorkflowRoutes(mux, testAuth)

	// Manually create a failed run
	run := &workflow.Run{
		ID:           "wf_failed_test",
		WorkflowName: "test-workflow",
		Status:       workflow.RunStatusFailed,
		Error:        "simulated failure",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	mgr.mu.Lock()
	mgr.runs[run.ID] = run
	mgr.mu.Unlock()

	// Resume the failed run
	body := strings.NewReader(`{"args": {"retry": true}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/script-workflow-runs/wf_failed_test/resume", body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)

	var resp struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.Equal(t, "wf_failed_test", resp.RunID)
	assert.Equal(t, "running", resp.Status)

	// Wait for completion
	time.Sleep(200 * time.Millisecond)

	// Verify run completed
	req = httptest.NewRequest(http.MethodGet, "/v1/script-workflow-runs/wf_failed_test", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	var finalResp struct {
		Status     string `json:"status"`
		ResultJSON string `json:"result_json"`
	}
	json.Unmarshal(rec.Body.Bytes(), &finalResp)
	assert.Equal(t, "completed", finalResp.Status)
	assert.Contains(t, finalResp.ResultJSON, "resumed ok")
}

// =============================================================================
// POC 6: End-to-end: register, start, stream, check result
// =============================================================================

func TestPOC6_EndToEndWorkflowLifecycle(t *testing.T) {
	mgr := newMockScriptWorkflowMgr(
		workflow.Meta{Name: "analyze", Description: "Analysis workflow"},
		workflow.Meta{Name: "deploy", Description: "Deployment workflow"},
		workflow.Meta{Name: "test", Description: "Test workflow"},
	)
	srv := newTestServerWithScriptWorkflows(mgr)

	mux := http.NewServeMux()
	srv.registerScriptWorkflowRoutes(mux, testAuth)

	// Step 1: List all workflows
	req := httptest.NewRequest(http.MethodGet, "/v1/script-workflows", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	var listResp struct {
		Workflows []workflow.Meta `json:"workflows"`
	}
	json.Unmarshal(rec.Body.Bytes(), &listResp)
	assert.Len(t, listResp.Workflows, 3)

	// Step 2: Start "analyze" workflow
	body := strings.NewReader(`{"args": {"mode": "full"}}`)
	req = httptest.NewRequest(http.MethodPost, "/v1/script-workflows/analyze/runs", body)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusAccepted, rec.Code)
	var startResp struct {
		RunID string `json:"run_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &startResp)

	// Step 3: Start "deploy" workflow
	req = httptest.NewRequest(http.MethodPost, "/v1/script-workflows/deploy/runs", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusAccepted, rec.Code)
	var deployResp struct {
		RunID string `json:"run_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &deployResp)

	// Step 4: Stream events for analyze — blocks until workflow.completed received.
	req = httptest.NewRequest(http.MethodGet, "/v1/script-workflow-runs/"+startResp.RunID+"/events", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Contains(t, rec.Body.String(), "event: workflow.completed")

	// Step 4b: Stream events for deploy — ensures the deploy goroutine has finished
	// before we poll its status in Step 5 (avoids a timing race where the 50 ms
	// async completion goroutine has not yet marked the run "completed").
	req = httptest.NewRequest(http.MethodGet, "/v1/script-workflow-runs/"+deployResp.RunID+"/events", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Contains(t, rec.Body.String(), "event: workflow.completed")

	// Step 5: Get final status of both runs
	for _, rid := range []string{startResp.RunID, deployResp.RunID} {
		req = httptest.NewRequest(http.MethodGet, "/v1/script-workflow-runs/"+rid, nil)
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		var runResp struct {
			Status string `json:"status"`
		}
		json.Unmarshal(rec.Body.Bytes(), &runResp)
		assert.Equal(t, "completed", runResp.Status)
	}

	// Step 6: Verify metadata access
	req = httptest.NewRequest(http.MethodGet, "/v1/script-workflows/test", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	var meta workflow.Meta
	json.Unmarshal(rec.Body.Bytes(), &meta)
	assert.Equal(t, "Test workflow", meta.Description)
}

// =============================================================================
// POC 7: Concurrent workflow runs
// =============================================================================

func TestPOC7_ConcurrentWorkflowRuns(t *testing.T) {
	mgr := newMockScriptWorkflowMgr()
	srv := newTestServerWithScriptWorkflows(mgr)

	mux := http.NewServeMux()
	srv.registerScriptWorkflowRoutes(mux, testAuth)

	var wg sync.WaitGroup
	runIDs := make([]string, 5)
	var mu sync.Mutex

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			body := strings.NewReader(fmt.Sprintf(`{"args": {"index": %d}}`, idx))
			req := httptest.NewRequest(http.MethodPost, "/v1/script-workflows/test-workflow/runs", body)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusAccepted, rec.Code)
			var resp struct {
				RunID string `json:"run_id"`
			}
			json.Unmarshal(rec.Body.Bytes(), &resp)
			mu.Lock()
			runIDs[idx] = resp.RunID
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	// All should have unique IDs
	mu.Lock()
	defer mu.Unlock()
	seen := make(map[string]bool)
	for _, id := range runIDs {
		assert.NotEmpty(t, id)
		assert.False(t, seen[id], "duplicate run ID: %s", id)
		seen[id] = true
	}
}

// =============================================================================
// POC 8: Error handling - unknown workflow
// =============================================================================

func TestPOC8_ErrorHandling(t *testing.T) {
	mgr := newMockScriptWorkflowMgr()
	srv := newTestServerWithScriptWorkflows(mgr)

	mux := http.NewServeMux()
	srv.registerScriptWorkflowRoutes(mux, testAuth)

	// Start unknown workflow
	req := httptest.NewRequest(http.MethodPost, "/v1/script-workflows/nonexistent/runs", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Get unknown run
	req = httptest.NewRequest(http.MethodGet, "/v1/script-workflow-runs/nonexistent", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// Resume non-failed run
	run := &workflow.Run{
		ID:           "wf_completed",
		WorkflowName: "test-workflow",
		Status:       workflow.RunStatusCompleted,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	mgr.mu.Lock()
	mgr.runs[run.ID] = run
	mgr.mu.Unlock()

	req = httptest.NewRequest(http.MethodPost, "/v1/script-workflow-runs/wf_completed/resume", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Wrong method
	req = httptest.NewRequest(http.MethodDelete, "/v1/script-workflows", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// =============================================================================
// POC 9: SSE event parsing - verify event format
// =============================================================================

func TestPOC9_SSEEventFormat(t *testing.T) {
	mgr := newMockScriptWorkflowMgr()
	srv := newTestServerWithScriptWorkflows(mgr)

	mux := http.NewServeMux()
	srv.registerScriptWorkflowRoutes(mux, testAuth)

	// Start a run
	req := httptest.NewRequest(http.MethodPost, "/v1/script-workflows/test-workflow/runs", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var startResp struct {
		RunID string `json:"run_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &startResp)

	// Stream events
	req = httptest.NewRequest(http.MethodGet, "/v1/script-workflow-runs/"+startResp.RunID+"/events", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	body := rec.Body.String()
	scanner := bufio.NewScanner(strings.NewReader(body))

	eventCount := 0
	var currentEvent string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			eventCount++
			data := strings.TrimPrefix(line, "data: ")
			// Verify data is valid JSON
			var payload map[string]any
			assert.NoError(t, json.Unmarshal([]byte(data), &payload), "event %s data should be valid JSON", currentEvent)
		}
	}
	assert.GreaterOrEqual(t, eventCount, 2, "should have at least started + completed events")
}

// =============================================================================
// POC 10: Nil manager returns 501 Not Implemented
// =============================================================================

func TestPOC10_NilManagerReturnsNotImplemented(t *testing.T) {
	srv := &Server{scriptWorkflows: nil} // no manager configured

	mux := http.NewServeMux()
	srv.registerScriptWorkflowRoutes(mux, testAuth)

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1/script-workflows"},
		{http.MethodGet, "/v1/script-workflows/test"},
		{http.MethodPost, "/v1/script-workflows/test/runs"},
		{http.MethodGet, "/v1/script-workflow-runs/wf_123"},
		{http.MethodPost, "/v1/script-workflow-runs/wf_123/resume"},
		{http.MethodGet, "/v1/script-workflow-runs/wf_123/events"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusNotImplemented, rec.Code, "endpoint %s %s", ep.method, ep.path)
		})
	}
}

// =============================================================================
// Integration: Verify the mock manager satisfies scriptWorkflowManager
// =============================================================================

var _ scriptWorkflowManager = (*mockScriptWorkflowMgr)(nil)
