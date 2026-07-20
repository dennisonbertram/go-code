package training

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClaudeTrainer_Analyze(t *testing.T) {
	report := TrainerReport{RunID: "run_test"}
	report.Scores.ToolQuality = 0.85
	report.Scores.Efficiency = 0.70
	report.Scores.GoalAdherence = 0.90
	report.Scores.ErrorRecovery = 0.60
	report.Findings = []Finding{
		{Type: "behavior", Priority: "medium", Issue: "retry loop", Confidence: ConfidenceProbable},
	}
	report.TrainingLabels.PreferredSteps = []int{1, 3}
	report.TrainingLabels.RejectedSteps = []int{2}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Method = %s, want POST", r.Method)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("x-api-key = %q, want test-key", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Error("anthropic-version header missing")
		}

		reportJSON, _ := json.Marshal(report)
		resp := map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": string(reportJSON)},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	trainer := NewClaudeTrainer("test-key", WithBaseURL(srv.URL))
	bundle := TraceBundle{
		RunID:        "run_test",
		TaskID:       "task_1",
		Outcome:      "pass",
		Steps:        5,
		CostUSD:      0.10,
		SystemPrompt: "You are a coding assistant",
		ToolCalls: []ToolCallTrace{
			{Name: "read_file", Success: true, StepIdx: 1},
		},
	}

	got, err := trainer.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if got.RunID != "run_test" {
		t.Errorf("RunID = %q, want run_test", got.RunID)
	}
	if got.Scores.ToolQuality != 0.85 {
		t.Errorf("ToolQuality = %f, want 0.85", got.Scores.ToolQuality)
	}
	if len(got.Findings) != 1 {
		t.Errorf("Findings len = %d, want 1", len(got.Findings))
	}
}

func TestClaudeTrainer_AnalyzeHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal"}`))
	}))
	defer srv.Close()

	trainer := NewClaudeTrainer("test-key", WithBaseURL(srv.URL))
	_, err := trainer.Analyze(context.Background(), TraceBundle{RunID: "run_err"})
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestClaudeTrainer_AnalyzeMalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "not valid json"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	trainer := NewClaudeTrainer("test-key", WithBaseURL(srv.URL))
	_, err := trainer.Analyze(context.Background(), TraceBundle{RunID: "run_bad"})
	if err == nil {
		t.Error("expected error for malformed response")
	}
}

func TestClaudeTrainer_AnalyzeBatch(t *testing.T) {
	batchReport := BatchReport{
		BatchID:  "batch_test",
		RunIDs:   []string{"run_1", "run_2"},
		Findings: []Finding{{Type: "behavior", Priority: "high"}},
		Patterns: []Pattern{{FailureMode: "retry_loop", Frequency: 3}},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reportJSON, _ := json.Marshal(batchReport)
		resp := map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": string(reportJSON)},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	trainer := NewClaudeTrainer("test-key", WithBaseURL(srv.URL))
	bundles := []TraceBundle{
		{RunID: "run_1", Outcome: "pass"},
		{RunID: "run_2", Outcome: "fail"},
	}

	got, err := trainer.AnalyzeBatch(context.Background(), bundles)
	if err != nil {
		t.Fatalf("AnalyzeBatch: %v", err)
	}
	if got.BatchID != "batch_test" {
		t.Errorf("BatchID = %q, want batch_test", got.BatchID)
	}
	if len(got.Patterns) != 1 {
		t.Errorf("Patterns len = %d, want 1", len(got.Patterns))
	}
}

func TestClaudeTrainer_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Will never respond because context is canceled
		<-r.Context().Done()
	}))
	defer srv.Close()

	trainer := NewClaudeTrainer("test-key", WithBaseURL(srv.URL))
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := trainer.Analyze(ctx, TraceBundle{RunID: "run_cancel"})
	if err == nil {
		t.Error("expected error for canceled context")
	}
}

func TestClaudeTrainer_WithModel(t *testing.T) {
	ct := NewClaudeTrainer("key", WithModel("custom-model"))
	if ct.model != "custom-model" {
		t.Errorf("model = %q, want custom-model", ct.model)
	}
}

func TestClaudeTrainer_WithHTTPClient(t *testing.T) {
	custom := &http.Client{Timeout: 42 * time.Second}
	ct := NewClaudeTrainer("key", WithHTTPClient(custom))
	if ct.client != custom {
		t.Error("expected custom HTTP client to be set")
	}
}

func TestClaudeTrainer_ImplementsInterface(t *testing.T) {
	var _ Trainer = (*ClaudeTrainer)(nil)
}
