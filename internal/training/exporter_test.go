package training

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestExportFromJSONL_BasicRun verifies that a complete run with the real
// JSONL event format (tool.call.started / tool.call.completed / usage.delta)
// produces a correctly populated TraceBundle.
func TestExportFromJSONL_BasicRun(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "run_abc.jsonl")
	// Real format: conversation_id (not run_id), tool.call.started/completed,
	// usage.delta for cost/tokens, run.completed with "step" (not "steps").
	data := `{"ts":"2026-03-14T12:00:00Z","seq":1,"type":"run.started","data":{"prompt":"fix the bug","conversation_id":"run_abc","schema_version":"1","step":0}}
{"ts":"2026-03-14T12:00:01Z","seq":2,"type":"tool.call.started","data":{"tool":"read_file","arguments":"{\"path\":\"/foo.go\"}","call_id":"tc_1","step":1,"schema_version":"1"}}
{"ts":"2026-03-14T12:00:02Z","seq":3,"type":"tool.call.completed","data":{"call_id":"tc_1","tool":"read_file","output":"package main","step":1,"schema_version":"1"}}
{"ts":"2026-03-14T12:00:03Z","seq":4,"type":"usage.delta","data":{"cumulative_cost_usd":0.01,"cumulative_usage":{"total_tokens":150},"turn_cost_usd":0.01,"turn_usage":{"total_tokens":150},"schema_version":"1","step":1}}
{"ts":"2026-03-14T12:00:04Z","seq":5,"type":"run.completed","data":{"output":"done","step":1,"cost_totals":{"cost_usd_total":0.01},"usage_totals":{"total_tokens":150},"schema_version":"1"}}
`
	if err := os.WriteFile(fp, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle, err := ExportFromJSONL(fp)
	if err != nil {
		t.Fatalf("ExportFromJSONL: %v", err)
	}

	if bundle.RunID != "run_abc" {
		t.Errorf("RunID = %q, want run_abc", bundle.RunID)
	}
	if bundle.Outcome != "pass" {
		t.Errorf("Outcome = %q, want pass", bundle.Outcome)
	}
	if bundle.Steps != 1 {
		t.Errorf("Steps = %d, want 1", bundle.Steps)
	}
	if len(bundle.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(bundle.ToolCalls))
	}
	tc := bundle.ToolCalls[0]
	if tc.Name != "read_file" {
		t.Errorf("ToolCall.Name = %q, want read_file", tc.Name)
	}
	if !tc.Success {
		t.Error("ToolCall.Success = false, want true")
	}
	if bundle.CostUSD != 0.01 {
		t.Errorf("CostUSD = %f, want 0.01", bundle.CostUSD)
	}
	if bundle.TokenCount != 150 {
		t.Errorf("TokenCount = %d, want 150", bundle.TokenCount)
	}
}

// TestExportFromJSONL_ToolCallArgs verifies that tool.call.started arguments
// (a JSON string) are correctly decoded into the ToolCallTrace.Args map.
func TestExportFromJSONL_ToolCallArgs(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "run_args.jsonl")
	data := `{"ts":"2026-03-14T12:00:00Z","seq":1,"type":"run.started","data":{"prompt":"go","conversation_id":"run_args","step":0}}
{"ts":"2026-03-14T12:00:01Z","seq":2,"type":"tool.call.started","data":{"tool":"bash","arguments":"{\"command\":\"ls -la\"}","call_id":"tc_1","step":1}}
{"ts":"2026-03-14T12:00:02Z","seq":3,"type":"tool.call.completed","data":{"call_id":"tc_1","tool":"bash","output":"total 0","step":1}}
{"ts":"2026-03-14T12:00:03Z","seq":4,"type":"run.completed","data":{"output":"done","step":1,"cost_totals":{"cost_usd_total":0},"usage_totals":{"total_tokens":100}}}
`
	if err := os.WriteFile(fp, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle, err := ExportFromJSONL(fp)
	if err != nil {
		t.Fatalf("ExportFromJSONL: %v", err)
	}
	if len(bundle.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(bundle.ToolCalls))
	}
	tc := bundle.ToolCalls[0]
	if tc.Args == nil {
		t.Fatal("ToolCall.Args is nil, want decoded map")
	}
	if v, ok := tc.Args["command"].(string); !ok || v != "ls -la" {
		t.Errorf("ToolCall.Args[command] = %v, want ls -la", tc.Args["command"])
	}
}

// TestExportFromJSONL_ToolCallError verifies that tool.call.completed with an
// "error" field sets Success=false on the ToolCallTrace.
func TestExportFromJSONL_ToolCallError(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "run_toolerr.jsonl")
	data := `{"ts":"2026-03-14T12:00:00Z","seq":1,"type":"run.started","data":{"prompt":"go","conversation_id":"run_toolerr","step":0}}
{"ts":"2026-03-14T12:00:01Z","seq":2,"type":"tool.call.started","data":{"tool":"write","arguments":"{\"path\":\"/abs/path\"}","call_id":"tc_1","step":1}}
{"ts":"2026-03-14T12:00:02Z","seq":3,"type":"tool.call.completed","data":{"call_id":"tc_1","tool":"write","output":"{\"error\":\"absolute paths are not allowed\"}","error":"absolute paths are not allowed","step":1}}
{"ts":"2026-03-14T12:00:03Z","seq":4,"type":"run.completed","data":{"output":"done","step":1,"cost_totals":{"cost_usd_total":0},"usage_totals":{"total_tokens":50}}}
`
	if err := os.WriteFile(fp, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle, err := ExportFromJSONL(fp)
	if err != nil {
		t.Fatalf("ExportFromJSONL: %v", err)
	}
	if len(bundle.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(bundle.ToolCalls))
	}
	if bundle.ToolCalls[0].Success {
		t.Error("ToolCall.Success = true, want false (error field present)")
	}
}

// TestExportFromJSONL_FailedRun verifies that run.failed sets Outcome=fail and
// correctly extracts the step count from the "step" field (not "steps").
func TestExportFromJSONL_FailedRun(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "run_fail.jsonl")
	data := `{"ts":"2026-03-14T12:00:00Z","seq":1,"type":"run.started","data":{"prompt":"do stuff","conversation_id":"run_fail","step":0}}
{"ts":"2026-03-14T12:00:01Z","seq":2,"type":"run.failed","data":{"error":"timeout","step":3,"cost_totals":{"cost_usd_total":0},"usage_totals":{"total_tokens":0}}}
`
	if err := os.WriteFile(fp, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle, err := ExportFromJSONL(fp)
	if err != nil {
		t.Fatalf("ExportFromJSONL: %v", err)
	}
	if bundle.Outcome != "fail" {
		t.Errorf("Outcome = %q, want fail", bundle.Outcome)
	}
	if bundle.Steps != 3 {
		t.Errorf("Steps = %d, want 3", bundle.Steps)
	}
}

// TestExportFromJSONL_CostFromUsageDelta verifies cost is accumulated from
// usage.delta events (the real event carrying cost data).
func TestExportFromJSONL_CostFromUsageDelta(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "run_cost.jsonl")
	data := `{"ts":"2026-03-14T12:00:00Z","seq":1,"type":"run.started","data":{"prompt":"go","conversation_id":"run_cost","step":0}}
{"ts":"2026-03-14T12:00:01Z","seq":2,"type":"usage.delta","data":{"cumulative_cost_usd":0.03,"cumulative_usage":{"total_tokens":300},"step":1}}
{"ts":"2026-03-14T12:00:02Z","seq":3,"type":"usage.delta","data":{"cumulative_cost_usd":0.07,"cumulative_usage":{"total_tokens":700},"step":2}}
{"ts":"2026-03-14T12:00:03Z","seq":4,"type":"run.completed","data":{"output":"ok","step":2,"cost_totals":{"cost_usd_total":0.07},"usage_totals":{"total_tokens":700}}}
`
	if err := os.WriteFile(fp, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle, err := ExportFromJSONL(fp)
	if err != nil {
		t.Fatalf("ExportFromJSONL: %v", err)
	}
	if bundle.CostUSD != 0.07 {
		t.Errorf("CostUSD = %f, want 0.07", bundle.CostUSD)
	}
	if bundle.TokenCount != 700 {
		t.Errorf("TokenCount = %d, want 700", bundle.TokenCount)
	}
	if bundle.Steps != 2 {
		t.Errorf("Steps = %d, want 2", bundle.Steps)
	}
}

func TestExportFromJSONL_EfficiencyScore(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "run_eff.jsonl")
	data := `{"ts":"2026-03-14T12:00:00Z","seq":1,"type":"run.started","data":{"prompt":"go","conversation_id":"run_eff","step":0}}
{"ts":"2026-03-14T12:00:01Z","seq":2,"type":"usage.delta","data":{"cumulative_cost_usd":0.05,"cumulative_usage":{"total_tokens":500},"step":5}}
{"ts":"2026-03-14T12:00:02Z","seq":3,"type":"run.completed","data":{"output":"ok","step":5,"cost_totals":{"cost_usd_total":0.05},"usage_totals":{"total_tokens":500}}}
`
	if err := os.WriteFile(fp, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle, err := ExportFromJSONL(fp)
	if err != nil {
		t.Fatalf("ExportFromJSONL: %v", err)
	}
	// EfficiencyScore = 1.0 / (steps * cost) normalized
	// = 1.0 / (5 * 0.05) = 1.0 / 0.25 = 4.0, capped at 1.0
	if bundle.EfficiencyScore < 0 || bundle.EfficiencyScore > 1.0 {
		t.Errorf("EfficiencyScore = %f, want in [0,1]", bundle.EfficiencyScore)
	}
}

func TestExportFromJSONL_FirstTryRate(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "run_ftr.jsonl")
	// 3 tool calls: first two unique, third is retry of first (same name+args).
	data := `{"ts":"2026-03-14T12:00:00Z","seq":1,"type":"run.started","data":{"prompt":"go","conversation_id":"run_ftr","step":0}}
{"ts":"2026-03-14T12:00:01Z","seq":2,"type":"tool.call.started","data":{"tool":"read_file","arguments":"{\"path\":\"/a.go\"}","call_id":"tc_1","step":1}}
{"ts":"2026-03-14T12:00:02Z","seq":3,"type":"tool.call.completed","data":{"call_id":"tc_1","tool":"read_file","output":"ok","error":"read failed","step":1}}
{"ts":"2026-03-14T12:00:03Z","seq":4,"type":"tool.call.started","data":{"tool":"write_file","arguments":"{\"path\":\"/b.go\"}","call_id":"tc_2","step":2}}
{"ts":"2026-03-14T12:00:04Z","seq":5,"type":"tool.call.completed","data":{"call_id":"tc_2","tool":"write_file","output":"ok","step":2}}
{"ts":"2026-03-14T12:00:05Z","seq":6,"type":"tool.call.started","data":{"tool":"read_file","arguments":"{\"path\":\"/a.go\"}","call_id":"tc_3","step":3}}
{"ts":"2026-03-14T12:00:06Z","seq":7,"type":"tool.call.completed","data":{"call_id":"tc_3","tool":"read_file","output":"ok","step":3}}
{"ts":"2026-03-14T12:00:07Z","seq":8,"type":"run.completed","data":{"output":"done","step":3,"cost_totals":{"cost_usd_total":0},"usage_totals":{"total_tokens":0}}}
`
	if err := os.WriteFile(fp, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle, err := ExportFromJSONL(fp)
	if err != nil {
		t.Fatalf("ExportFromJSONL: %v", err)
	}
	// 3 tool calls total; tc_3 is a retry (same name+args as tc_1)
	// FirstTryRate = non-retried / total = 2/3
	if len(bundle.ToolCalls) != 3 {
		t.Fatalf("ToolCalls len = %d, want 3", len(bundle.ToolCalls))
	}
	expected := 2.0 / 3.0
	if bundle.FirstTryRate < expected-0.01 || bundle.FirstTryRate > expected+0.01 {
		t.Errorf("FirstTryRate = %f, want ~%f", bundle.FirstTryRate, expected)
	}
}

func TestExportFromJSONL_Truncation(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "run_trunc.jsonl")
	data := `{"ts":"2026-03-14T12:00:00Z","seq":1,"type":"run.started","data":{"prompt":"go","conversation_id":"run_trunc","step":0}}
{"ts":"2026-03-14T12:00:01Z","seq":2,"type":"usage.delta","data":{"cumulative_cost_usd":1.0,"cumulative_usage":{"total_tokens":200000},"step":1}}
{"ts":"2026-03-14T12:00:02Z","seq":3,"type":"run.completed","data":{"output":"done","step":1,"cost_totals":{"cost_usd_total":1.0},"usage_totals":{"total_tokens":200000}}}
`
	if err := os.WriteFile(fp, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle, err := ExportFromJSONL(fp)
	if err != nil {
		t.Fatalf("ExportFromJSONL: %v", err)
	}
	if bundle.TokenCount != 200000 {
		t.Errorf("TokenCount = %d, want 200000", bundle.TokenCount)
	}
	if !bundle.Truncated {
		t.Error("Truncated = false, want true (tokens > 180000)")
	}
	if bundle.TruncationStrategy != "middle_drop" {
		t.Errorf("TruncationStrategy = %q, want middle_drop", bundle.TruncationStrategy)
	}
}

func TestExportFromJSONL_ContextSnapshots(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "run_ctx.jsonl")
	data := `{"ts":"2026-03-14T12:00:00Z","seq":1,"type":"run.started","data":{"prompt":"go","conversation_id":"run_ctx","step":0}}
{"ts":"2026-03-14T12:00:01Z","seq":2,"type":"context.window.snapshot","data":{"step":1,"estimated_total_tokens":50000,"max_context_tokens":128000,"usage_ratio":0.39}}
{"ts":"2026-03-14T12:00:02Z","seq":3,"type":"context.window.snapshot","data":{"step":2,"estimated_total_tokens":80000,"max_context_tokens":128000,"usage_ratio":0.625}}
{"ts":"2026-03-14T12:00:03Z","seq":4,"type":"run.completed","data":{"output":"done","step":2,"cost_totals":{"cost_usd_total":0},"usage_totals":{"total_tokens":0}}}
`
	if err := os.WriteFile(fp, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle, err := ExportFromJSONL(fp)
	if err != nil {
		t.Fatalf("ExportFromJSONL: %v", err)
	}
	if len(bundle.ContextSnapshots) != 2 {
		t.Fatalf("ContextSnapshots len = %d, want 2", len(bundle.ContextSnapshots))
	}
	if bundle.MaxContextRatio < 0.62 || bundle.MaxContextRatio > 0.63 {
		t.Errorf("MaxContextRatio = %f, want ~0.625", bundle.MaxContextRatio)
	}
}

func TestExportFromJSONL_AntiPatterns(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "run_ap.jsonl")
	// Real event format: "tool.antipattern" with "tool" field (not "anti_pattern.detected" with "tool_name").
	data := `{"ts":"2026-03-14T12:00:00Z","seq":1,"type":"run.started","data":{"prompt":"go","conversation_id":"run_ap","step":0}}
{"ts":"2026-03-14T12:00:01Z","seq":2,"type":"tool.antipattern","data":{"type":"retry_loop","tool":"bash","call_count":3,"step":2}}
{"ts":"2026-03-14T12:00:02Z","seq":3,"type":"run.completed","data":{"output":"done","step":3,"cost_totals":{"cost_usd_total":0},"usage_totals":{"total_tokens":0}}}
`
	if err := os.WriteFile(fp, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle, err := ExportFromJSONL(fp)
	if err != nil {
		t.Fatalf("ExportFromJSONL: %v", err)
	}
	if len(bundle.AntiPatterns) != 1 {
		t.Fatalf("AntiPatterns len = %d, want 1", len(bundle.AntiPatterns))
	}
	if bundle.AntiPatterns[0].Type != "retry_loop" {
		t.Errorf("AntiPattern.Type = %q, want retry_loop", bundle.AntiPatterns[0].Type)
	}
	if bundle.AntiPatterns[0].Message != "retry_loop: bash" {
		t.Errorf("AntiPattern.Message = %q, want 'retry_loop: bash'", bundle.AntiPatterns[0].Message)
	}
}

// TestExportFromJSONL_AntiPatternsWithEvidence verifies that anti-pattern
// events carrying an evidence field correctly populate the Evidence field.
func TestExportFromJSONL_AntiPatternsWithEvidence(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "run_ap_evidence.jsonl")
	data := `{"ts":"2026-03-14T12:00:00Z","seq":1,"type":"run.started","data":{"prompt":"go","conversation_id":"run_ap_ev","step":0}}
{"ts":"2026-03-14T12:00:01Z","seq":2,"type":"tool.antipattern","data":{"type":"hedge_assertion","tool":"verify_skill","evidence":"Model said 'appears to be correct' instead of running verification","step":2}}
{"ts":"2026-03-14T12:00:02Z","seq":3,"type":"run.completed","data":{"output":"done","step":3,"cost_totals":{"cost_usd_total":0},"usage_totals":{"total_tokens":0}}}
`
	if err := os.WriteFile(fp, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle, err := ExportFromJSONL(fp)
	if err != nil {
		t.Fatalf("ExportFromJSONL: %v", err)
	}
	if len(bundle.AntiPatterns) != 1 {
		t.Fatalf("AntiPatterns len = %d, want 1", len(bundle.AntiPatterns))
	}
	if bundle.AntiPatterns[0].Type != "hedge_assertion" {
		t.Errorf("AntiPattern.Type = %q, want hedge_assertion", bundle.AntiPatterns[0].Type)
	}
	if bundle.AntiPatterns[0].Evidence != "Model said 'appears to be correct' instead of running verification" {
		t.Errorf("AntiPattern.Evidence = %q, want evidence string", bundle.AntiPatterns[0].Evidence)
	}
}

// TestExportFromJSONL_NamedAntiPatterns verifies all 5 named anti-pattern
// types from the conclusion-watcher are recognized by the exporter.
func TestExportFromJSONL_NamedAntiPatterns(t *testing.T) {
	patterns := []string{
		"hedge_assertion",
		"unverified_file_claim",
		"premature_completion",
		"skipped_diagnostic",
		"architecture_assumption",
	}

	for _, pt := range patterns {
		dir := t.TempDir()
		fp := filepath.Join(dir, "run_named_ap.jsonl")
		data := fmt.Sprintf(`{"ts":"2026-03-14T12:00:00Z","seq":1,"type":"run.started","data":{"prompt":"go","conversation_id":"run_named_%s","step":0}}
{"ts":"2026-03-14T12:00:01Z","seq":2,"type":"tool.antipattern","data":{"type":"%s","tool":"test_tool","step":1}}
{"ts":"2026-03-14T12:00:02Z","seq":3,"type":"run.completed","data":{"output":"done","step":1,"cost_totals":{"cost_usd_total":0},"usage_totals":{"total_tokens":0}}}
`, pt, pt)
		if err := os.WriteFile(fp, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}

		bundle, err := ExportFromJSONL(fp)
		if err != nil {
			t.Fatalf("ExportFromJSONL for pattern %q: %v", pt, err)
		}
		if len(bundle.AntiPatterns) != 1 {
			t.Fatalf("AntiPatterns len = %d, want 1 for pattern %q", len(bundle.AntiPatterns), pt)
		}
		if bundle.AntiPatterns[0].Type != pt {
			t.Errorf("AntiPattern.Type = %q, want %q", bundle.AntiPatterns[0].Type, pt)
		}
	}
}

func TestExportFromJSONL_AssistantMessage(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "run_msg.jsonl")
	// Real format: assistant.message (not llm.completion.finished) for content.
	data := `{"ts":"2026-03-14T12:00:00Z","seq":1,"type":"run.started","data":{"prompt":"fix bug","conversation_id":"run_msg","system_prompt":"You are a helper","step":0}}
{"ts":"2026-03-14T12:00:01Z","seq":2,"type":"assistant.message","data":{"content":"I will fix it","step":1}}
{"ts":"2026-03-14T12:00:02Z","seq":3,"type":"run.completed","data":{"output":"done","step":1,"cost_totals":{"cost_usd_total":0},"usage_totals":{"total_tokens":0}}}
`
	if err := os.WriteFile(fp, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle, err := ExportFromJSONL(fp)
	if err != nil {
		t.Fatalf("ExportFromJSONL: %v", err)
	}
	// Should have user message (from prompt) and assistant message.
	if len(bundle.Messages) < 2 {
		t.Fatalf("Messages len = %d, want >= 2", len(bundle.Messages))
	}
	if bundle.Messages[0].Role != "user" {
		t.Errorf("Messages[0].Role = %q, want user", bundle.Messages[0].Role)
	}
	if bundle.SystemPrompt != "You are a helper" {
		t.Errorf("SystemPrompt = %q, want 'You are a helper'", bundle.SystemPrompt)
	}
	// Verify assistant message is present.
	found := false
	for _, m := range bundle.Messages {
		if m.Role == "assistant" && m.Content == "I will fix it" {
			found = true
		}
	}
	if !found {
		t.Error("expected assistant message 'I will fix it' in Messages")
	}
}

func TestExportFromJSONL_FileNotFound(t *testing.T) {
	_, err := ExportFromJSONL("/nonexistent/path.jsonl")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestExportFromJSONL_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(fp, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	bundle, err := ExportFromJSONL(fp)
	if err != nil {
		t.Fatalf("ExportFromJSONL: %v", err)
	}
	if bundle.Outcome != "unknown" {
		t.Errorf("Outcome = %q, want unknown", bundle.Outcome)
	}
}

func TestExportFromJSONL_MalformedLine(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "bad.jsonl")
	data := `{"ts":"2026-03-14T12:00:00Z","seq":1,"type":"run.started","data":{"prompt":"go","conversation_id":"run_bad","step":0}}
NOT VALID JSON
{"ts":"2026-03-14T12:00:02Z","seq":3,"type":"run.completed","data":{"output":"done","step":1,"cost_totals":{"cost_usd_total":0},"usage_totals":{"total_tokens":0}}}
`
	if err := os.WriteFile(fp, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	// Should skip malformed lines, not fail entirely.
	bundle, err := ExportFromJSONL(fp)
	if err != nil {
		t.Fatalf("ExportFromJSONL: %v", err)
	}
	if bundle.RunID != "run_bad" {
		t.Errorf("RunID = %q, want run_bad", bundle.RunID)
	}
}

// TestExportFromJSONL_RealFormatIntegration uses an event sequence that matches
// exactly what the runner emits, verifying end-to-end that the exporter handles
// the real production JSONL format correctly.
func TestExportFromJSONL_RealFormatIntegration(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "run_real.jsonl")
	// This mirrors the real event sequence from ~/.trainerd/rollouts/:
	// - run.started has conversation_id (not run_id)
	// - tool calls use tool.call.started / tool.call.completed with "tool" field
	// - arguments is a JSON string (not a decoded map)
	// - cost/tokens come from usage.delta (cumulative_cost_usd, cumulative_usage)
	// - run.completed has "step" (not "steps"), cost_totals, usage_totals
	data := `{"ts":"2026-03-15T10:00:00Z","seq":0,"type":"run.started","data":{"conversation_id":"run_real_001","prompt":"fix the pipeline","schema_version":"1","step":0}}
{"ts":"2026-03-15T10:00:01Z","seq":1,"type":"run.step.started","data":{"step":1}}
{"ts":"2026-03-15T10:00:02Z","seq":2,"type":"tool.call.started","data":{"tool":"bash","arguments":"{\"command\":\"ls /tmp\"}","call_id":"call_abc","step":1}}
{"ts":"2026-03-15T10:00:03Z","seq":3,"type":"tool.call.completed","data":{"call_id":"call_abc","tool":"bash","output":"{\"exit_code\":0,\"output\":\"file.txt\"}","step":1}}
{"ts":"2026-03-15T10:00:04Z","seq":4,"type":"usage.delta","data":{"cumulative_cost_usd":0.002,"cumulative_usage":{"total_tokens":500,"prompt_tokens":400,"completion_tokens":100},"turn_cost_usd":0.002,"step":1}}
{"ts":"2026-03-15T10:00:05Z","seq":5,"type":"tool.call.started","data":{"tool":"bash","arguments":"{\"command\":\"ls /tmp\"}","call_id":"call_def","step":2}}
{"ts":"2026-03-15T10:00:06Z","seq":6,"type":"tool.call.completed","data":{"call_id":"call_def","tool":"bash","output":"{\"exit_code\":0,\"output\":\"file.txt\"}","step":2}}
{"ts":"2026-03-15T10:00:07Z","seq":7,"type":"usage.delta","data":{"cumulative_cost_usd":0.004,"cumulative_usage":{"total_tokens":1000,"prompt_tokens":800,"completion_tokens":200},"turn_cost_usd":0.002,"step":2}}
{"ts":"2026-03-15T10:00:08Z","seq":8,"type":"assistant.message","data":{"content":"Pipeline fixed successfully.","step":2}}
{"ts":"2026-03-15T10:00:09Z","seq":9,"type":"run.completed","data":{"conversation_id":"run_real_001","output":"Pipeline fixed successfully.","step":2,"cost_totals":{"cost_usd_total":0.004,"cost_status":"priced"},"usage_totals":{"prompt_tokens_total":800,"completion_tokens_total":200,"total_tokens":1000},"schema_version":"1"}}
`
	if err := os.WriteFile(fp, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle, err := ExportFromJSONL(fp)
	if err != nil {
		t.Fatalf("ExportFromJSONL: %v", err)
	}

	// RunID should come from conversation_id.
	if bundle.RunID != "run_real_001" {
		t.Errorf("RunID = %q, want run_real_001", bundle.RunID)
	}
	// Steps should come from "step" in run.completed.
	if bundle.Steps != 2 {
		t.Errorf("Steps = %d, want 2", bundle.Steps)
	}
	// CostUSD from cost_totals in run.completed (authoritative).
	if bundle.CostUSD != 0.004 {
		t.Errorf("CostUSD = %f, want 0.004", bundle.CostUSD)
	}
	// Two tool calls, both with same name+args = second is a retry.
	if len(bundle.ToolCalls) != 2 {
		t.Fatalf("ToolCalls len = %d, want 2", len(bundle.ToolCalls))
	}
	if bundle.ToolCalls[0].Retried {
		t.Error("ToolCalls[0].Retried = true, want false (first occurrence)")
	}
	if !bundle.ToolCalls[1].Retried {
		t.Error("ToolCalls[1].Retried = false, want true (same name+args = retry)")
	}
	// FirstTryRate = 1 non-retried / 2 total = 0.5
	if bundle.FirstTryRate < 0.49 || bundle.FirstTryRate > 0.51 {
		t.Errorf("FirstTryRate = %f, want ~0.5", bundle.FirstTryRate)
	}
	// Outcome should be pass.
	if bundle.Outcome != "pass" {
		t.Errorf("Outcome = %q, want pass", bundle.Outcome)
	}
}
