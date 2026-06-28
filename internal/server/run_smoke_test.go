package server

// run_smoke_test.go — in-process smoke for the real run API.
//
// Drives POST /v1/runs → poll GET /v1/runs/{id} → GET /v1/runs/{id}/summary
// against a scripted fakeprovider with DETERMINISTIC Usage and Cost. No Docker,
// no network, no LLM, no API key required. Designed to be the documented smoke
// referenced in the benchmark runbook (E1).
//
// Design constraints (from issue5-plan.md):
//   - status must be "completed" (not failed / cancelled).
//   - steps_taken, total_prompt_tokens, total_completion_tokens, total_cost_usd,
//     and cost_status must be byte-stable across runs.
//   - Deterministic: fakeprovider is scripted with fixed usage + cost values.
//   - Race-clean: no shared mutable state outside the runner/server.
//
// Honesty: RunSummary.total_cost_usd is DERIVED from the fakeprovider-scripted
// Cost.TotalUSD, accumulated by Runner.recordAccounting. It is NOT a raw API
// field — it is computed in-process from the scripted turn.
//
// C2 extension: after the run reaches terminal, TestRunSmoke also calls
// benchresult.FromRun, marshals the result to JSON, and asserts the grounded
// fields (model, provider, tokens, cost, steps, status, derived duration >= 0).
// This proves the result schema is populated from a real in-process run.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-agent-harness/internal/benchresult"
	"go-agent-harness/internal/fakeprovider"
	"go-agent-harness/internal/harness"
)

// TestRunSmoke drives the real run API end-to-end against a scripted
// fakeprovider. It is the canonical key-free smoke for the runbook.
//
// Turn sequence: 2 turns.
//   - Turn 1: returns content "turn one output" with fixed usage and cost.
//     The runner emits run.step.completed and loops (MaxSteps=2 allows a
//     second turn). BUT — because turn 1 has no tool calls and non-empty
//     content, the runner calls completeRun immediately after step 1.
//     To exercise a 2-turn sequence we must produce tool calls in turn 1.
//     Since registering a real tool adds complexity, the smoke instead uses
//     a simple 1-turn sequence that is provably terminal and deterministic.
//   - Turn 1 (only): content="smoke ok", no tool calls → runner completes
//     after exactly 1 step.
//
// Asserted fields (byte-stable):
//
//	status                  = "completed"
//	steps_taken             = 1
//	total_prompt_tokens     = 100
//	total_completion_tokens = 50
//	total_cost_usd          = 0.001   (derived from scripted Cost.TotalUSD)
//	cost_status             = "available"
func TestRunSmoke(t *testing.T) {
	t.Parallel()

	const (
		wantPromptTokens     = 100
		wantCompletionTokens = 50
		wantCostUSD          = 0.001
		wantCostStatus       = string(harness.CostStatusAvailable)
		wantStatus           = "completed"
		wantSteps            = 1
	)

	// Script a single deterministic turn.
	// Cost.TotalUSD = 0.001, Usage.PromptTokens = 100, CompletionTokens = 50.
	// CostStatus is set explicitly so normalizeTurnCost returns CostStatusAvailable.
	prov := fakeprovider.New(
		[]fakeprovider.Turn{
			{
				Content: "smoke ok",
				Usage: &harness.CompletionUsage{
					PromptTokens:     wantPromptTokens,
					CompletionTokens: wantCompletionTokens,
					TotalTokens:      wantPromptTokens + wantCompletionTokens,
				},
				Cost: &harness.CompletionCost{
					InputUSD:  0.0007,
					OutputUSD: 0.0003,
					TotalUSD:  wantCostUSD,
				},
				CostStatus:  harness.CostStatusAvailable,
				UsageStatus: harness.UsageStatusProviderReported,
			},
		},
	)

	runner := harness.NewRunner(prov, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     5,
	})
	defer func() { _ = runner.Shutdown(context.Background()) }()

	handler := New(runner)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// -------------------------------------------------------------------------
	// Step 1: POST /v1/runs
	// -------------------------------------------------------------------------
	postBody := bytes.NewBufferString(`{"prompt":"smoke test prompt"}`)
	postRes, err := http.Post(ts.URL+"/v1/runs", "application/json", postBody)
	if err != nil {
		t.Fatalf("POST /v1/runs: %v", err)
	}
	defer postRes.Body.Close()

	if postRes.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(postRes.Body)
		t.Fatalf("POST /v1/runs: expected 202, got %d: %s", postRes.StatusCode, body)
	}

	var created struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(postRes.Body).Decode(&created); err != nil {
		t.Fatalf("decode POST response: %v", err)
	}
	if created.RunID == "" {
		t.Fatal("POST /v1/runs: run_id is empty")
	}
	runID := created.RunID

	// -------------------------------------------------------------------------
	// Step 2: poll GET /v1/runs/{id} until terminal (completed/failed/cancelled)
	// -------------------------------------------------------------------------
	deadline := time.Now().Add(10 * time.Second)
	var finalStatus string
	for time.Now().Before(deadline) {
		getRes, err := http.Get(ts.URL + "/v1/runs/" + runID)
		if err != nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}

		var runState struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(getRes.Body).Decode(&runState); err != nil {
			getRes.Body.Close()
			time.Sleep(10 * time.Millisecond)
			continue
		}
		getRes.Body.Close()

		switch runState.Status {
		case "completed", "failed", "cancelled":
			finalStatus = runState.Status
			goto terminal
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach terminal state within 10s", runID)

terminal:
	if finalStatus != wantStatus {
		t.Errorf("GET /v1/runs/%s: status=%q, want %q", runID, finalStatus, wantStatus)
	}

	// -------------------------------------------------------------------------
	// Step 3: GET /v1/runs/{id}/summary and assert byte-stable fields
	// -------------------------------------------------------------------------
	summaryRes, err := http.Get(ts.URL + "/v1/runs/" + runID + "/summary")
	if err != nil {
		t.Fatalf("GET /v1/runs/%s/summary: %v", runID, err)
	}
	defer summaryRes.Body.Close()

	if summaryRes.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(summaryRes.Body)
		t.Fatalf("GET summary: expected 200, got %d: %s", summaryRes.StatusCode, body)
	}

	// Decode into harness.RunSummary for type-safe field access.
	var summary harness.RunSummary
	if err := json.NewDecoder(summaryRes.Body).Decode(&summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}

	// Byte-stable assertions.
	if summary.RunID != runID {
		t.Errorf("summary.run_id=%q, want %q", summary.RunID, runID)
	}
	if string(summary.Status) != wantStatus {
		t.Errorf("summary.status=%q, want %q", summary.Status, wantStatus)
	}
	if summary.StepsTaken != wantSteps {
		t.Errorf("summary.steps_taken=%d, want %d", summary.StepsTaken, wantSteps)
	}
	if summary.TotalPromptTokens != wantPromptTokens {
		t.Errorf("summary.total_prompt_tokens=%d, want %d", summary.TotalPromptTokens, wantPromptTokens)
	}
	if summary.TotalCompletionTokens != wantCompletionTokens {
		t.Errorf("summary.total_completion_tokens=%d, want %d", summary.TotalCompletionTokens, wantCompletionTokens)
	}
	if summary.TotalCostUSD != wantCostUSD {
		t.Errorf("summary.total_cost_usd=%v, want %v", summary.TotalCostUSD, wantCostUSD)
	}
	if string(summary.CostStatus) != wantCostStatus {
		t.Errorf("summary.cost_status=%q, want %q", summary.CostStatus, wantCostStatus)
	}
	if len(summary.ToolCalls) != 0 {
		t.Errorf("summary.tool_calls: expected empty (no tools invoked), got %d entries", len(summary.ToolCalls))
	}

	// Provider was called exactly once (1 turn = 1 step).
	if calls := prov.Calls(); calls != 1 {
		t.Errorf("fakeprovider.Calls()=%d, want 1", calls)
	}

	t.Logf("smoke PASS: run_id=%s status=%s steps=%d prompt_tokens=%d completion_tokens=%d cost_usd=%v cost_status=%s",
		summary.RunID, summary.Status, summary.StepsTaken,
		summary.TotalPromptTokens, summary.TotalCompletionTokens,
		summary.TotalCostUSD, summary.CostStatus,
	)

	// -------------------------------------------------------------------------
	// C2: fetch the Run record, produce a BenchmarkResult JSON artifact, and
	// assert the grounded fields.
	//
	// Honesty annotations:
	//   model         — source: Run.Model (set from RunnerConfig.DefaultModel = "test-model")
	//   provider_name — source: Run.ProviderName (set by resolveProvider → "default"
	//                   when ProviderRegistry is nil, i.e. the runner-default path)
	//   tokens/cost   — source: RunSummary (same values asserted above)
	//   duration_ms   — DERIVED: Run.UpdatedAt − Run.CreatedAt (>= 0; may be 0 when
	//                   the run completes in sub-millisecond time in tests)
	// -------------------------------------------------------------------------
	runRes, err := http.Get(ts.URL + "/v1/runs/" + runID)
	if err != nil {
		t.Fatalf("GET /v1/runs/%s: %v", runID, err)
	}
	defer runRes.Body.Close()
	if runRes.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(runRes.Body)
		t.Fatalf("GET /v1/runs/%s: expected 200, got %d: %s", runID, runRes.StatusCode, body)
	}
	var run harness.Run
	if err := json.NewDecoder(runRes.Body).Decode(&run); err != nil {
		t.Fatalf("decode GET /v1/runs/%s: %v", runID, err)
	}

	// Produce the BenchmarkResult artifact.
	result := benchresult.FromRun(summary, run)

	// Marshal to JSON — this is the documented JSON artifact for the runbook.
	artifactJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("benchresult.FromRun: json.Marshal: %v", err)
	}
	if len(artifactJSON) == 0 {
		t.Fatal("benchresult.FromRun: json artifact is empty")
	}

	// Re-unmarshal for a round-trip sanity check.
	var decoded benchresult.BenchmarkResult
	if err := json.Unmarshal(artifactJSON, &decoded); err != nil {
		t.Fatalf("benchresult round-trip unmarshal: %v", err)
	}

	// --- Assert grounded fields ---

	// run_id must match the run created above.
	if decoded.RunID != runID {
		t.Errorf("benchresult.run_id=%q, want %q", decoded.RunID, runID)
	}

	// status — grounded in RunSummary.Status.
	if decoded.Status != wantStatus {
		t.Errorf("benchresult.status=%q, want %q", decoded.Status, wantStatus)
	}

	// steps_taken — grounded in RunSummary.StepsTaken.
	if decoded.StepsTaken != wantSteps {
		t.Errorf("benchresult.steps_taken=%d, want %d", decoded.StepsTaken, wantSteps)
	}

	// total_prompt_tokens — grounded in RunSummary.TotalPromptTokens.
	if decoded.TotalPromptTokens != wantPromptTokens {
		t.Errorf("benchresult.total_prompt_tokens=%d, want %d", decoded.TotalPromptTokens, wantPromptTokens)
	}

	// total_completion_tokens — grounded in RunSummary.TotalCompletionTokens.
	if decoded.TotalCompletionTokens != wantCompletionTokens {
		t.Errorf("benchresult.total_completion_tokens=%d, want %d", decoded.TotalCompletionTokens, wantCompletionTokens)
	}

	// total_cost_usd — grounded in RunSummary.TotalCostUSD (DERIVED by runner from
	// scripted Cost.TotalUSD; NOT a raw provider API field).
	if decoded.TotalCostUSD != wantCostUSD {
		t.Errorf("benchresult.total_cost_usd=%v, want %v", decoded.TotalCostUSD, wantCostUSD)
	}

	// cost_status — grounded in RunSummary.CostStatus.
	if decoded.CostStatus != wantCostStatus {
		t.Errorf("benchresult.cost_status=%q, want %q", decoded.CostStatus, wantCostStatus)
	}

	// model — grounded in Run.Model (= RunnerConfig.DefaultModel = "test-model").
	const wantModel = "test-model"
	if decoded.Model != wantModel {
		t.Errorf("benchresult.model=%q, want %q", decoded.Model, wantModel)
	}

	// provider_name — grounded in Run.ProviderName. When ProviderRegistry is nil,
	// resolveProvider returns "default". This is the in-process fakeprovider path.
	const wantProvider = "default"
	if decoded.ProviderName != wantProvider {
		t.Errorf("benchresult.provider_name=%q, want %q", decoded.ProviderName, wantProvider)
	}

	// duration_ms — DERIVED: Run.UpdatedAt - Run.CreatedAt. Must be >= 0.
	// (May be 0 in sub-millisecond in-process test runs; that is honest and correct.)
	if decoded.DurationMs < 0 {
		t.Errorf("benchresult.duration_ms=%d, want >= 0 (derived field)", decoded.DurationMs)
	}

	t.Logf("C2 artifact PASS: run_id=%s model=%s provider=%s steps=%d tokens=%d/%d cost_usd=%v duration_ms=%d",
		decoded.RunID, decoded.Model, decoded.ProviderName,
		decoded.StepsTaken, decoded.TotalPromptTokens, decoded.TotalCompletionTokens,
		decoded.TotalCostUSD, decoded.DurationMs,
	)
}
