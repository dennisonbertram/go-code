package benchresult_test

import (
	"encoding/json"
	"testing"
	"time"

	"go-agent-harness/internal/benchresult"
	"go-agent-harness/internal/harness"
)

// makeTestPair returns a minimal but complete RunSummary + Run pair for assertions.
func makeTestPair() (harness.RunSummary, harness.Run) {
	now := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
	later := now.Add(37 * time.Second)

	summary := harness.RunSummary{
		RunID:                 "run-abc123",
		Status:                harness.RunStatusCompleted,
		StepsTaken:            4,
		TotalPromptTokens:     800,
		TotalCompletionTokens: 200,
		TotalCostUSD:          0.0042,
		CostStatus:            harness.CostStatusAvailable,
		ToolCalls: []harness.ToolCallSummary{
			{ToolName: "bash", Step: 1},
			{ToolName: "read_file", Step: 2},
		},
		CacheHitRate: 0.25,
		Error:        "",
	}

	run := harness.Run{
		ID:             "run-abc123",
		Prompt:         "Do something useful",
		Model:          "gpt-4.1-mini",
		ProviderName:   "openai",
		Status:         harness.RunStatusCompleted,
		Output:         "Done.",
		Error:          "",
		TenantID:       "tenant-1",
		ConversationID: "conv-99",
		AgentID:        "agent-7",
		CreatedAt:      now,
		UpdatedAt:      later,
	}

	return summary, run
}

// TestFromRun_MapsRunSummaryFields asserts that every RunSummary field appears
// verbatim in the returned BenchmarkResult (run_id, status, steps, tokens,
// cost, cache_hit_rate, error).
func TestFromRun_MapsRunSummaryFields(t *testing.T) {
	summary, run := makeTestPair()
	result := benchresult.FromRun(summary, run)

	if result.RunID != summary.RunID {
		t.Errorf("RunID: got %q, want %q", result.RunID, summary.RunID)
	}
	if result.Status != string(summary.Status) {
		t.Errorf("Status: got %q, want %q", result.Status, summary.Status)
	}
	if result.StepsTaken != summary.StepsTaken {
		t.Errorf("StepsTaken: got %d, want %d", result.StepsTaken, summary.StepsTaken)
	}
	if result.TotalPromptTokens != summary.TotalPromptTokens {
		t.Errorf("TotalPromptTokens: got %d, want %d", result.TotalPromptTokens, summary.TotalPromptTokens)
	}
	if result.TotalCompletionTokens != summary.TotalCompletionTokens {
		t.Errorf("TotalCompletionTokens: got %d, want %d", result.TotalCompletionTokens, summary.TotalCompletionTokens)
	}
	if result.TotalCostUSD != summary.TotalCostUSD {
		t.Errorf("TotalCostUSD: got %v, want %v", result.TotalCostUSD, summary.TotalCostUSD)
	}
	if result.CostStatus != string(summary.CostStatus) {
		t.Errorf("CostStatus: got %q, want %q", result.CostStatus, summary.CostStatus)
	}
	if result.CacheHitRate != summary.CacheHitRate {
		t.Errorf("CacheHitRate: got %v, want %v", result.CacheHitRate, summary.CacheHitRate)
	}
	if result.ErrorMessage != summary.Error {
		t.Errorf("ErrorMessage: got %q, want %q", result.ErrorMessage, summary.Error)
	}
}

// TestFromRun_MapsRunFields asserts that Run fields (model, provider, prompt,
// output, tenant/conversation/agent IDs, created_at, updated_at) appear in the result.
func TestFromRun_MapsRunFields(t *testing.T) {
	summary, run := makeTestPair()
	result := benchresult.FromRun(summary, run)

	if result.Model != run.Model {
		t.Errorf("Model: got %q, want %q", result.Model, run.Model)
	}
	if result.ProviderName != run.ProviderName {
		t.Errorf("ProviderName: got %q, want %q", result.ProviderName, run.ProviderName)
	}
	if result.Prompt != run.Prompt {
		t.Errorf("Prompt: got %q, want %q", result.Prompt, run.Prompt)
	}
	if result.Output != run.Output {
		t.Errorf("Output: got %q, want %q", result.Output, run.Output)
	}
	if result.TenantID != run.TenantID {
		t.Errorf("TenantID: got %q, want %q", result.TenantID, run.TenantID)
	}
	if result.ConversationID != run.ConversationID {
		t.Errorf("ConversationID: got %q, want %q", result.ConversationID, run.ConversationID)
	}
	if result.AgentID != run.AgentID {
		t.Errorf("AgentID: got %q, want %q", result.AgentID, run.AgentID)
	}
	if !result.CreatedAt.Equal(run.CreatedAt) {
		t.Errorf("CreatedAt: got %v, want %v", result.CreatedAt, run.CreatedAt)
	}
	if !result.UpdatedAt.Equal(run.UpdatedAt) {
		t.Errorf("UpdatedAt: got %v, want %v", result.UpdatedAt, run.UpdatedAt)
	}
}

// TestFromRun_DerivedDuration asserts that DurationMs is DERIVED from
// updated_at - created_at (not a raw API field).
func TestFromRun_DerivedDuration(t *testing.T) {
	summary, run := makeTestPair()
	result := benchresult.FromRun(summary, run)

	wantMs := run.UpdatedAt.Sub(run.CreatedAt).Milliseconds()
	if result.DurationMs != wantMs {
		t.Errorf("DurationMs (derived): got %d, want %d", result.DurationMs, wantMs)
	}
}

// TestFromRun_ToolCallRecords asserts that each ToolCallSummary maps into a
// ToolCallRecord with ToolName and Step preserved exactly.
func TestFromRun_ToolCallRecords(t *testing.T) {
	summary, run := makeTestPair()
	result := benchresult.FromRun(summary, run)

	if len(result.ToolCallRecords) != len(summary.ToolCalls) {
		t.Fatalf("ToolCallRecords length: got %d, want %d", len(result.ToolCallRecords), len(summary.ToolCalls))
	}
	for i, tc := range summary.ToolCalls {
		got := result.ToolCallRecords[i]
		if got.ToolName != tc.ToolName {
			t.Errorf("ToolCallRecords[%d].ToolName: got %q, want %q", i, got.ToolName, tc.ToolName)
		}
		if got.Step != tc.Step {
			t.Errorf("ToolCallRecords[%d].Step: got %d, want %d", i, got.Step, tc.Step)
		}
	}
}

// TestReconstructRolloutPath_WithDir asserts that a non-empty rolloutDir yields
// a path containing the UTC date of created_at and the run ID.
func TestReconstructRolloutPath_WithDir(t *testing.T) {
	_, run := makeTestPair()
	path := benchresult.ReconstructRolloutPath("/data/rollouts", run)

	// Expect the date subfolder in YYYY-MM-DD format based on run.CreatedAt UTC.
	wantDate := run.CreatedAt.UTC().Format("2006-01-02")
	wantSuffix := run.ID + ".jsonl"

	if path == "" {
		t.Fatal("expected non-empty rollout path when rolloutDir is given")
	}
	if !containsSubstr(path, wantDate) {
		t.Errorf("rollout path %q does not contain date %q", path, wantDate)
	}
	if !containsSubstr(path, wantSuffix) {
		t.Errorf("rollout path %q does not end with %q", path, wantSuffix)
	}
}

// TestReconstructRolloutPath_EmptyDir asserts that an empty rolloutDir returns
// an empty string (never guess).
func TestReconstructRolloutPath_EmptyDir(t *testing.T) {
	_, run := makeTestPair()
	path := benchresult.ReconstructRolloutPath("", run)
	if path != "" {
		t.Errorf("expected empty rollout path when rolloutDir is empty, got %q", path)
	}
}

// TestFromRun_RolloutPathEmpty asserts that FromRun with no rolloutDir leaves
// RolloutPath empty.
func TestFromRun_RolloutPathEmpty(t *testing.T) {
	summary, run := makeTestPair()
	result := benchresult.FromRun(summary, run)
	// FromRun does not have access to rolloutDir — path must be empty.
	if result.RolloutPath != "" {
		t.Errorf("RolloutPath: expected empty from FromRun, got %q", result.RolloutPath)
	}
}

// TestFromRun_DriftFieldsAbsent asserts that the optional EXTERNAL drift
// fields are zero/nil by default (FromRun never fills them).
func TestFromRun_DriftFieldsAbsent(t *testing.T) {
	summary, run := makeTestPair()
	result := benchresult.FromRun(summary, run)
	if result.Drift != nil {
		t.Errorf("Drift: expected nil (external/opt-in), got %+v", result.Drift)
	}
	if len(result.ForensicEvents) != 0 {
		t.Errorf("ForensicEvents: expected empty (external/opt-in), got %d entries", len(result.ForensicEvents))
	}
}

// TestJSONRoundTrip asserts that a BenchmarkResult survives a
// marshal/unmarshal cycle with all non-zero fields preserved.
func TestJSONRoundTrip(t *testing.T) {
	summary, run := makeTestPair()
	result := benchresult.FromRun(summary, run)

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var decoded benchresult.BenchmarkResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if decoded.RunID != result.RunID {
		t.Errorf("round-trip RunID: got %q, want %q", decoded.RunID, result.RunID)
	}
	if decoded.DurationMs != result.DurationMs {
		t.Errorf("round-trip DurationMs: got %d, want %d", decoded.DurationMs, result.DurationMs)
	}
	if decoded.TotalCostUSD != result.TotalCostUSD {
		t.Errorf("round-trip TotalCostUSD: got %v, want %v", decoded.TotalCostUSD, result.TotalCostUSD)
	}
	if len(decoded.ToolCallRecords) != len(result.ToolCallRecords) {
		t.Errorf("round-trip ToolCallRecords len: got %d, want %d",
			len(decoded.ToolCallRecords), len(result.ToolCallRecords))
	}
	if !decoded.CreatedAt.Equal(result.CreatedAt) {
		t.Errorf("round-trip CreatedAt: got %v, want %v", decoded.CreatedAt, result.CreatedAt)
	}
}

// containsSubstr is a simple helper avoiding import of strings in test file.
func containsSubstr(s, sub string) bool {
	return len(s) >= len(sub) && findSubstr(s, sub)
}

func findSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
