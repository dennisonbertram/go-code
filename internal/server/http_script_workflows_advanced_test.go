package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"go-agent-harness/internal/workflow"
)

// =============================================================================
// Advanced mock: supports configurable script execution
// =============================================================================

type advancedMockMgr struct {
	*mockScriptWorkflowMgr
	executeFunc func(name string, args any) (any, error)
}

func newAdvancedMockMgr() *advancedMockMgr {
	return &advancedMockMgr{
		mockScriptWorkflowMgr: newMockScriptWorkflowMgr(),
	}
}

// =============================================================================
// POC 11: Adversarial Verify pattern via HTTP API
// =============================================================================

func TestPOC11_AdversarialVerifyPattern(t *testing.T) {
	mgr := newMockScriptWorkflowMgr(
		workflow.Meta{Name: "find-bugs", Description: "Find bugs in code"},
		workflow.Meta{Name: "verify-bug", Description: "Adversarially verify a bug"},
		workflow.Meta{Name: "assess-severity", Description: "Assess bug severity"},
	)
	srv := newTestServerWithScriptWorkflows(mgr)

	mux := http.NewServeMux()
	srv.registerScriptWorkflowRoutes(mux, testAuth)

	// Step 1: Start find-bugs workflow
	body := strings.NewReader(`{"args": {"target": "main.go"}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/script-workflows/find-bugs/runs", body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusAccepted, rec.Code)
	var findResp struct{ RunID string `json:"run_id"` }
	json.Unmarshal(rec.Body.Bytes(), &findResp)

	// Step 2: Start verify-bug workflow (in parallel)
	req = httptest.NewRequest(http.MethodPost, "/v1/script-workflows/verify-bug/runs", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusAccepted, rec.Code)
	var verifyResp struct{ RunID string `json:"run_id"` }
	json.Unmarshal(rec.Body.Bytes(), &verifyResp)

	// Step 3: Start assess-severity workflow
	req = httptest.NewRequest(http.MethodPost, "/v1/script-workflows/assess-severity/runs", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusAccepted, rec.Code)
	var assessResp struct{ RunID string `json:"run_id"` }
	json.Unmarshal(rec.Body.Bytes(), &assessResp)

	// Wait for all to complete
	time.Sleep(200 * time.Millisecond)

	// Verify all completed
	for _, rid := range []string{findResp.RunID, verifyResp.RunID, assessResp.RunID} {
		req = httptest.NewRequest(http.MethodGet, "/v1/script-workflow-runs/"+rid, nil)
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		var resp struct{ Status string `json:"status"` }
		json.Unmarshal(rec.Body.Bytes(), &resp)
		assert.Equal(t, "completed", resp.Status)
	}
}

// =============================================================================
// POC 12: Loop-until-dry pattern
// =============================================================================

func TestPOC12_LoopUntilDryPattern(t *testing.T) {
	mgr := newMockScriptWorkflowMgr(
		workflow.Meta{Name: "search-issues", Description: "Search for issues until dry"},
	)
	srv := newTestServerWithScriptWorkflows(mgr)

	mux := http.NewServeMux()
	srv.registerScriptWorkflowRoutes(mux, testAuth)

	// Simulate multiple rounds of search
	var runIDs []string
	for round := 0; round < 3; round++ {
		body := strings.NewReader(fmt.Sprintf(`{"args": {"round": %d}}`, round))
		req := httptest.NewRequest(http.MethodPost, "/v1/script-workflows/search-issues/runs", body)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusAccepted, rec.Code)
		var resp struct{ RunID string `json:"run_id"` }
		json.Unmarshal(rec.Body.Bytes(), &resp)
		runIDs = append(runIDs, resp.RunID)
	}

	// Wait for all to complete
	time.Sleep(200 * time.Millisecond)

	// Verify each round completed and has results
	for i, rid := range runIDs {
		req := httptest.NewRequest(http.MethodGet, "/v1/script-workflow-runs/"+rid, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		var resp struct {
			Status     string `json:"status"`
			ResultJSON string `json:"result_json"`
		}
		json.Unmarshal(rec.Body.Bytes(), &resp)
		assert.Equal(t, "completed", resp.Status)
		assert.Contains(t, resp.ResultJSON, fmt.Sprintf("completed search-issues"), "round %d", i)
	}
}

// =============================================================================
// POC 13: Multi-phase workflow with event tracking
// =============================================================================

func TestPOC13_MultiPhaseEventTracking(t *testing.T) {
	mgr := newMockScriptWorkflowMgr(
		workflow.Meta{Name: "ci-pipeline", Description: "CI/CD pipeline", Phases: []workflow.PhaseInfo{
			{Title: "Build"}, {Title: "Test"}, {Title: "Deploy"},
		}},
	)
	srv := newTestServerWithScriptWorkflows(mgr)

	mux := http.NewServeMux()
	srv.registerScriptWorkflowRoutes(mux, testAuth)

	// Start pipeline
	req := httptest.NewRequest(http.MethodPost, "/v1/script-workflows/ci-pipeline/runs", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusAccepted, rec.Code)
	var startResp struct{ RunID string `json:"run_id"` }
	json.Unmarshal(rec.Body.Bytes(), &startResp)

	// Wait for completion
	time.Sleep(200 * time.Millisecond)

	// Get run with full details
	req = httptest.NewRequest(http.MethodGet, "/v1/script-workflow-runs/"+startResp.RunID, nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	var runResp struct {
		Status       string `json:"status"`
		WorkflowName string `json:"workflow_name"`
		ResultJSON   string `json:"result_json"`
		CreatedAt    string `json:"created_at"`
		UpdatedAt    string `json:"updated_at"`
	}
	json.Unmarshal(rec.Body.Bytes(), &runResp)
	assert.Equal(t, "completed", runResp.Status)
	assert.Equal(t, "ci-pipeline", runResp.WorkflowName)
	assert.NotEmpty(t, runResp.CreatedAt)
	assert.NotEmpty(t, runResp.UpdatedAt)
}

// =============================================================================
// POC 14: Concurrent fan-out with result collection
// =============================================================================

func TestPOC14_ConcurrentFanOut(t *testing.T) {
	mgr := newMockScriptWorkflowMgr(
		workflow.Meta{Name: "fanout", Description: "Fan-out to multiple targets"},
	)
	srv := newTestServerWithScriptWorkflows(mgr)

	mux := http.NewServeMux()
	srv.registerScriptWorkflowRoutes(mux, testAuth)

	const fanoutCount = 8
	var wg sync.WaitGroup
	var successCount atomic.Int64
	var failCount atomic.Int64

	for i := 0; i < fanoutCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			body := strings.NewReader(fmt.Sprintf(`{"args": {"shard": %d}}`, idx))
			req := httptest.NewRequest(http.MethodPost, "/v1/script-workflows/fanout/runs", body)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code == http.StatusAccepted {
				successCount.Add(1)
			} else {
				failCount.Add(1)
			}
		}(i)
	}
	wg.Wait()

	assert.Equal(t, int64(fanoutCount), successCount.Load())
	assert.Equal(t, int64(0), failCount.Load())
}

// =============================================================================
// POC 15: Error recovery and resume chain
// =============================================================================

func TestPOC15_ErrorRecoveryChain(t *testing.T) {
	mgr := newMockScriptWorkflowMgr(
		workflow.Meta{Name: "deploy", Description: "Deployment workflow"},
	)
	srv := newTestServerWithScriptWorkflows(mgr)

	mux := http.NewServeMux()
	srv.registerScriptWorkflowRoutes(mux, testAuth)

	// Step 1: Create a failed run manually
	failedRun := &workflow.Run{
		ID:           "wf_deploy_failed",
		WorkflowName: "deploy",
		Status:       workflow.RunStatusFailed,
		Error:        "connection timeout",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	mgr.mu.Lock()
	mgr.runs[failedRun.ID] = failedRun
	mgr.mu.Unlock()

	// Step 2: Get the failed run status
	req := httptest.NewRequest(http.MethodGet, "/v1/script-workflow-runs/wf_deploy_failed", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	var getResp struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	json.Unmarshal(rec.Body.Bytes(), &getResp)
	assert.Equal(t, "failed", getResp.Status)
	assert.Contains(t, getResp.Error, "connection timeout")

	// Step 3: Resume with retry args
	body := strings.NewReader(`{"args": {"retry": true, "timeout": 60}}`)
	req = httptest.NewRequest(http.MethodPost, "/v1/script-workflow-runs/wf_deploy_failed/resume", body)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusAccepted, rec.Code)

	// Step 4: Wait for completion
	time.Sleep(200 * time.Millisecond)

	// Step 5: Verify recovery
	req = httptest.NewRequest(http.MethodGet, "/v1/script-workflow-runs/wf_deploy_failed", nil)
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
// Integration: Verify interface satisfaction
// =============================================================================

func TestAdvancedMgrSatisfiesInterface(t *testing.T) {
	// Verify the mock satisfies the interface at compile time
	var _ scriptWorkflowManager = newMockScriptWorkflowMgr()
}
