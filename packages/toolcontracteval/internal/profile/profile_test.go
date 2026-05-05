package profile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-agent-harness/packages/toolcontracteval/internal/record"
	"go-agent-harness/packages/toolcontracteval/internal/schema"
)

func TestBuildProfilesReadWindowAndSkippedToolUse(t *testing.T) {
	manifest := record.Manifest{RunID: "r1", SuiteID: "suite", Model: "grok-4.3", Provider: "xai", Mode: "api", StartedAt: time.Now()}
	calls := []record.ToolCall{
		{Scenario: "read-window-relational", Tool: "read", Valid: true, ArgumentsRaw: `{"path":"src/server.go","first_lines":30}`},
	}
	scenarios := []record.ScenarioResult{
		{Scenario: "array-container-pressure", ToolCalls: 0, Completed: true},
		{Scenario: "optional-null-pressure", ToolCalls: 0, Completed: true},
		{Scenario: "read-window-relational", ToolCalls: 1, Completed: true},
	}

	p := Build(manifest, calls, nil, scenarios)
	if p.Capabilities["read_window_intent"] != "clean" {
		t.Fatalf("read_window_intent = %q, want clean", p.Capabilities["read_window_intent"])
	}
	if p.Capabilities["required_tool_use"] != "weak" {
		t.Fatalf("required_tool_use = %q, want weak", p.Capabilities["required_tool_use"])
	}
	if len(p.HarnessTuning) < 2 {
		t.Fatalf("harness tuning = %+v, want read and tool-choice recommendations", p.HarnessTuning)
	}
	md := Markdown(p)
	if !strings.Contains(md, "Model Contract Profile") || !strings.Contains(md, "read_window_intent") {
		t.Fatalf("markdown missing profile content:\n%s", md)
	}
}

func TestBuildProfilesReadWindowWeakness(t *testing.T) {
	manifest := record.Manifest{RunID: "r1", SuiteID: "suite", Model: "grok-code-fast-1", Provider: "xai", Mode: "api"}
	calls := []record.ToolCall{
		{Scenario: "read-window-relational", Tool: "read", Valid: false, ArgumentsRaw: `{"path":"src/server.go"}`},
	}
	failures := []record.ValidationFailure{
		{Scenario: "read-window-relational", Tool: "read", Issue: schema.Issue{Code: "scenario_expected_argument"}},
	}
	scenarios := []record.ScenarioResult{
		{Scenario: "read-window-relational", ToolCalls: 1, InvalidCalls: 1, ValidationHits: 1, Completed: false},
	}

	p := Build(manifest, calls, failures, scenarios)
	if p.Capabilities["read_window_intent"] != "weak" {
		t.Fatalf("read_window_intent = %q, want weak", p.Capabilities["read_window_intent"])
	}
	if len(p.Tools) != 1 || p.Tools[0].InvalidCalls != 1 {
		t.Fatalf("tools = %+v, want invalid read profile", p.Tools)
	}
}

func TestBuildProfilesScenarioContractMismatch(t *testing.T) {
	manifest := record.Manifest{RunID: "r1", SuiteID: "suite", Model: "deepseek-v4-pro", Provider: "deepseek", Mode: "api"}
	scenarios := []record.ScenarioResult{
		{Scenario: "decoy-final-answer", ToolCalls: 2, InvalidCalls: 0, ValidationHits: 1, Completed: true},
	}

	p := Build(manifest, nil, nil, scenarios)
	if len(p.Scenarios) != 1 || p.Scenarios[0].Assessment != "scenario contract mismatch" {
		t.Fatalf("scenarios = %+v, want scenario contract mismatch", p.Scenarios)
	}
	md := Markdown(p)
	if !strings.Contains(md, "validation_hits=1") {
		t.Fatalf("markdown missing validation hit count:\n%s", md)
	}
}

func TestBuildProfilesPromptVariantMetadata(t *testing.T) {
	manifest := record.Manifest{
		RunID:              "r1",
		SuiteID:            "suite",
		Model:              "deepseek-v4-pro",
		Provider:           "deepseek",
		Mode:               "api",
		SystemPromptLabel:  "deepseek-gauntlet-v1",
		SystemPromptPath:   "prompts/deepseek/gauntlet-v1.md",
		SystemPromptSHA256: "abc123",
		SystemPromptChars:  42,
	}

	p := Build(manifest, nil, nil, []record.ScenarioResult{{Scenario: "done", Completed: true}})
	if p.PromptVariant.Label != "deepseek-gauntlet-v1" || p.PromptVariant.SHA256 != "abc123" {
		t.Fatalf("prompt variant = %+v", p.PromptVariant)
	}
	md := Markdown(p)
	if !strings.Contains(md, "deepseek-gauntlet-v1") || !strings.Contains(md, "sha256=abc123") {
		t.Fatalf("markdown missing prompt variant metadata:\n%s", md)
	}
}

func TestBuildProfilesPathAliasPreferenceTelemetry(t *testing.T) {
	manifest := record.Manifest{RunID: "r1", SuiteID: "suite", Model: "deepseek-v4-pro", Provider: "deepseek", Mode: "api"}
	calls := []record.ToolCall{
		{Scenario: "read-one", Tool: "read", Valid: true, ArgumentsRaw: `{"path":"a.go"}`},
		{Scenario: "read-two", Tool: "read", Valid: true, ArgumentsRaw: `{"file_path":"b.go"}`},
		{Scenario: "read-three", Tool: "read", Valid: true, ArgumentsRaw: `{"path":"c.go","file_path":"different.go"}`},
	}

	p := Build(manifest, calls, nil, []record.ScenarioResult{{Scenario: "read-one", ToolCalls: 3, Completed: true}})
	if len(p.Tools) != 1 {
		t.Fatalf("tools = %+v, want one read profile", p.Tools)
	}
	read := p.Tools[0]
	if read.CanonicalPathCalls != 2 || read.FilePathAliasCalls != 2 || read.MixedPathAndFilePathCalls != 1 || read.AliasConflictCalls != 1 {
		t.Fatalf("read alias telemetry = %+v", read)
	}
	md := Markdown(p)
	if !strings.Contains(md, "canonical_path=2") || !strings.Contains(md, "file_path_alias=2") {
		t.Fatalf("markdown missing alias telemetry:\n%s", md)
	}
}

func TestWriteSnapshotWritesDurableMarkdownAndJSON(t *testing.T) {
	p := &Profile{
		RunID:        "run",
		SuiteID:      "suite",
		Model:        "grok-4.3",
		Provider:     "xai",
		Mode:         "api",
		Capabilities: map[string]string{"json_shape_validity": "clean"},
	}

	mdPath, err := WriteSnapshot(t.TempDir(), p)
	if err != nil {
		t.Fatalf("WriteSnapshot error: %v", err)
	}
	if filepath.Base(mdPath) != "grok-4.3.md" {
		t.Fatalf("md path = %q, want grok-4.3.md", mdPath)
	}
	if _, err := os.Stat(mdPath); err != nil {
		t.Fatalf("markdown snapshot missing: %v", err)
	}
	if _, err := os.Stat(strings.TrimSuffix(mdPath, ".md") + ".json"); err != nil {
		t.Fatalf("json snapshot missing: %v", err)
	}
}

func TestBuildProfilesAPIHarnessProductionCapabilities(t *testing.T) {
	manifest := record.Manifest{RunID: "api", SuiteID: "api-harness-production", Model: "deepseek-v4-pro", Provider: "deepseek", Mode: "api"}
	calls := []record.ToolCall{
		{Scenario: "read-first-lines-contract", Tool: "read", Valid: true, ArgumentsRaw: `{"path":"cmd/harnessd/main.go","first_lines":30}`},
		{Scenario: "read-bad-path-recovery", Tool: "read", Valid: true, ArgumentsRaw: `{"path":"missing.go"}`},
		{Scenario: "read-bad-path-recovery", Tool: "bash", Valid: true, ArgumentsRaw: `{"command":"ls"}`},
		{Scenario: "read-bad-path-recovery", Tool: "read", Valid: true, ArgumentsRaw: `{"path":"service.go"}`},
		{Scenario: "path-string-no-markdown", Tool: "read", Valid: true, ArgumentsRaw: `{"path":"docs/notes.md"}`},
	}
	scenarios := []record.ScenarioResult{
		{Scenario: "read-first-lines-contract", ToolCalls: 1, Completed: true},
		{Scenario: "read-bad-path-recovery", ToolCalls: 3, Completed: true},
		{Scenario: "path-string-no-markdown", ToolCalls: 1, Completed: true},
	}

	p := Build(manifest, calls, nil, scenarios)
	for key, want := range map[string]string{
		"json_shape_validity":   "clean",
		"required_tool_use":     "observed",
		"read_window_intent":    "clean",
		"markdown_path_leakage": "clean",
		"retry_recovery":        "clean",
	} {
		if got := p.Capabilities[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestGenerateAddsCandidateRuntimePromptProfileForCleanPromptVariant(t *testing.T) {
	runDir := t.TempDir()
	manifest := record.Manifest{
		RunID:              "deepseek-clean",
		SuiteID:            "api-harness-gauntlet",
		Model:              "deepseek-v4-pro",
		Provider:           "deepseek",
		Mode:               "api",
		SystemPromptLabel:  "deepseek-harness-compact-v10",
		SystemPromptSHA256: "abc123",
	}
	writeJSONFile(t, filepath.Join(runDir, "manifest.json"), manifest)
	if err := record.AppendJSONL(filepath.Join(runDir, "scenario-results.jsonl"), record.ScenarioResult{
		RunID: "deepseek-clean", Scenario: "clean", ToolCalls: 1, Completed: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "system-prompt.md"), []byte("PROMOTE ME\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := Generate(runDir)
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if p.CandidateRuntimePromptProfile == nil {
		t.Fatalf("expected candidate runtime prompt profile")
	}
	candidate := p.CandidateRuntimePromptProfile
	if candidate.Name != "deepseek" || candidate.Match != "deepseek-*" || candidate.Content != "PROMOTE ME" {
		t.Fatalf("candidate = %+v", candidate)
	}
	md, err := os.ReadFile(filepath.Join(runDir, "model-profile.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "Candidate Runtime Prompt Profile") || !strings.Contains(string(md), "promote-profile") {
		t.Fatalf("markdown missing candidate promotion guidance:\n%s", string(md))
	}
}

func TestGenerateDoesNotAddCandidateRuntimePromptProfileForNoisyRun(t *testing.T) {
	runDir := t.TempDir()
	manifest := record.Manifest{RunID: "deepseek-noisy", SuiteID: "suite", Model: "deepseek-v4-pro", Provider: "deepseek", Mode: "api"}
	writeJSONFile(t, filepath.Join(runDir, "manifest.json"), manifest)
	if err := record.AppendJSONL(filepath.Join(runDir, "scenario-results.jsonl"), record.ScenarioResult{
		RunID: "deepseek-noisy", Scenario: "noisy", ToolCalls: 1, ValidationHits: 1, Completed: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "system-prompt.md"), []byte("DO NOT PROMOTE\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := Generate(runDir)
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if p.CandidateRuntimePromptProfile != nil {
		t.Fatalf("candidate should be omitted for noisy run: %+v", p.CandidateRuntimePromptProfile)
	}
}

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
