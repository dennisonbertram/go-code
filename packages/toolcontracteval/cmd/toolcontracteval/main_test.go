package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-agent-harness/packages/toolcontracteval/internal/record"
)

func TestRunCommandAcceptsSystemPromptFile(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	suite := `{
	  "id":"cli-prompt-file-test",
	  "max_turns":1,
	  "scenarios":[{
	    "id":"answer-only",
	    "prompt":"Answer done.",
	    "workspace_files":{"main.go":"package main\n"}
	  }]
	}`
	if err := os.WriteFile(suitePath, []byte(suite), 0o644); err != nil {
		t.Fatal(err)
	}
	promptPath := filepath.Join(dir, "deepseek.md")
	if err := os.WriteFile(promptPath, []byte("PROMPT FROM FILE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var captured struct {
		SystemPrompt string `json:"system_prompt"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"run_id":"server-run","status":"queued"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/server-run/events":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `id: server-run:1
event: run.completed
data: {"id":"server-run:1","type":"run.completed","payload":{"output":"done"}}

`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	err := runCommand([]string{
		"--suite", suitePath,
		"--provider", "deepseek",
		"--model", "deepseek-v4-pro",
		"--api-base-url", server.URL,
		"--out", filepath.Join(dir, "runs"),
		"--run-id", "prompt-file-run",
		"--system-prompt-file", promptPath,
		"--system-prompt-label", "deepseek-candidate",
	})
	if err != nil {
		t.Fatal(err)
	}
	if captured.SystemPrompt != "PROMPT FROM FILE\n" {
		t.Fatalf("system_prompt = %q", captured.SystemPrompt)
	}
	var manifest struct {
		SystemPromptLabel string `json:"system_prompt_label"`
		SystemPromptPath  string `json:"system_prompt_path"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "runs", "prompt-file-run", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SystemPromptLabel != "deepseek-candidate" || manifest.SystemPromptPath != promptPath {
		t.Fatalf("manifest prompt metadata = %+v", manifest)
	}
}

func TestPromoteProfileDryRunDoesNotWritePromptCatalog(t *testing.T) {
	dir := t.TempDir()
	runDir := writePromotableRun(t, dir, "deepseek-dry-run", true)
	promptsDir := writePromptCatalogFixture(t, dir)

	err := promoteProfileCommand([]string{
		"--run", runDir,
		"--prompts-dir", promptsDir,
		"--profile-name", "deepseek",
		"--match", "deepseek-*",
		"--dry-run",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(promptsDir, "models", "deepseek.md")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote profile file, stat err=%v", err)
	}
	data, err := os.ReadFile(filepath.Join(promptsDir, "catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "deepseek") {
		t.Fatalf("dry-run changed catalog:\n%s", string(data))
	}
}

func TestPromoteProfileWritesApprovedProfileAndCatalogMapping(t *testing.T) {
	dir := t.TempDir()
	runDir := writePromotableRun(t, dir, "deepseek-promote", true)
	promptsDir := writePromptCatalogFixture(t, dir)

	err := promoteProfileCommand([]string{
		"--run", runDir,
		"--prompts-dir", promptsDir,
		"--profile-name", "deepseek",
		"--match", "deepseek-*",
	})
	if err != nil {
		t.Fatal(err)
	}
	profileData, err := os.ReadFile(filepath.Join(promptsDir, "models", "deepseek.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(profileData)) != "PROMOTED PROMPT" {
		t.Fatalf("profile content = %q", string(profileData))
	}
	catalogData, err := os.ReadFile(filepath.Join(promptsDir, "catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	catalog := string(catalogData)
	if !strings.Contains(catalog, "name: deepseek") || !strings.Contains(catalog, "match: deepseek-*") || !strings.Contains(catalog, "file: models/deepseek.md") {
		t.Fatalf("catalog missing deepseek mapping:\n%s", catalog)
	}
	if strings.Index(catalog, "name: deepseek") > strings.Index(catalog, "name: default") {
		t.Fatalf("deepseek mapping should be before default fallback:\n%s", catalog)
	}
}

func TestPromoteProfileRejectsNoisyRunWithoutForce(t *testing.T) {
	dir := t.TempDir()
	runDir := writePromotableRun(t, dir, "deepseek-noisy", false)
	promptsDir := writePromptCatalogFixture(t, dir)

	err := promoteProfileCommand([]string{
		"--run", runDir,
		"--prompts-dir", promptsDir,
		"--profile-name", "deepseek",
	})
	if err == nil {
		t.Fatal("expected noisy run promotion to fail")
	}
	if !strings.Contains(err.Error(), "not clean enough to promote") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func writePromotableRun(t *testing.T, root, runID string, clean bool) string {
	t.Helper()
	runDir := filepath.Join(root, "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(runDir, "manifest.json"), record.Manifest{
		RunID: runID, SuiteID: "suite", Model: "deepseek-v4-pro", Provider: "deepseek", Mode: "api",
		SystemPromptLabel: "deepseek-candidate",
	})
	scenario := record.ScenarioResult{RunID: runID, Scenario: "scenario", ToolCalls: 1, Completed: true}
	if !clean {
		scenario.ValidationHits = 1
	}
	if err := record.AppendJSONL(filepath.Join(runDir, "scenario-results.jsonl"), scenario); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "system-prompt.md"), []byte("PROMOTED PROMPT\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return runDir
}

func writePromptCatalogFixture(t *testing.T, root string) string {
	t.Helper()
	promptsDir := filepath.Join(root, "prompts")
	for _, rel := range []string{
		"base/main.md",
		"intents/general.md",
		"models/default.md",
		"extensions/behaviors/.keep",
		"extensions/talents/.keep",
	} {
		path := filepath.Join(promptsDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	catalog := `version: 1
defaults:
  intent: general
  model_profile: default
intents:
  general: intents/general.md
model_profiles:
  - name: default
    match: "*"
    file: models/default.md
extensions:
  behaviors_dir: extensions/behaviors
  talents_dir: extensions/talents
`
	if err := os.WriteFile(filepath.Join(promptsDir, "catalog.yaml"), []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
	return promptsDir
}

func writeJSON(t *testing.T, path string, v any) {
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
