package run

import (
	"context"
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

func TestParseSSEExtractsToolEvents(t *testing.T) {
	input := strings.NewReader(`id: run:1
event: tool.call.started
data: {"id":"run:1","type":"tool.call.started","payload":{"tool":"read","call_id":"c1","arguments":"{\"path\":\"main.go\",\"first_lines\":30}","step":1}}

id: run:2
event: run.completed
data: {"id":"run:2","type":"run.completed","payload":{"output":"done"}}

`)
	events, err := parseSSE(input)
	if err != nil {
		t.Fatalf("parseSSE error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2", len(events))
	}
	if events[0].Type != "tool.call.started" || stringValue(events[0].Payload["tool"]) != "read" {
		t.Fatalf("unexpected first event: %+v", events[0])
	}
	if stringValue(events[0].Payload["arguments"]) != `{"path":"main.go","first_lines":30}` {
		t.Fatalf("arguments = %q", events[0].Payload["arguments"])
	}
}

func TestExecuteAPIRecordsHarnessSSEArtifacts(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	suite := `{
	  "id":"api-contract-test",
	  "max_turns":2,
	  "scenarios":[{
	    "id":"read-window",
	    "prompt":"Read first 30 lines.",
	    "tool_names":["read"],
	    "required_tools":["read"],
	    "min_tool_calls":1,
	    "workspace_files":{"main.go":"package main\n"},
	    "expectations":[{
	      "tool":"read",
	      "any_of":[
	        {"required_keys":["first_lines"],"exact_args":{"first_lines":30}},
	        {"required_keys":["offset","limit"],"exact_args":{"offset":0,"limit":30}}
	      ]
	    }]
	  }]
	}`
	if err := os.WriteFile(suitePath, []byte(suite), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"run_id":"server-run","status":"queued"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/server-run/events":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `id: server-run:1
event: tool.call.started
data: {"id":"server-run:1","type":"tool.call.started","payload":{"tool":"read","call_id":"c1","arguments":"{\"path\":\"main.go\",\"first_lines\":30}","step":1}}

id: server-run:2
event: tool.call.completed
data: {"id":"server-run:2","type":"tool.call.completed","payload":{"tool":"read","call_id":"c1","output":"{\"ok\":true}","step":1}}

id: server-run:3
event: run.completed
data: {"id":"server-run:3","type":"run.completed","payload":{"output":"done"}}

`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := Execute(context.Background(), Options{
		SuitePath:  suitePath,
		OutDir:     filepath.Join(dir, "runs"),
		RunID:      "api-fixed",
		Model:      "deepseek-v4-pro",
		Provider:   "deepseek",
		Mode:       "api",
		APIBaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	calls, err := record.ReadJSONL[record.ToolCall](filepath.Join(result.RunDir, "tool-calls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || !calls[0].Valid || calls[0].ArgumentsRaw != `{"path":"main.go","first_lines":30}` {
		t.Fatalf("calls = %+v", calls)
	}
	events, err := record.ReadJSONL[record.APIEvent](filepath.Join(result.RunDir, "api-events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("api events len = %d, want 3", len(events))
	}
	failures, err := record.ReadJSONL[record.ValidationFailure](filepath.Join(result.RunDir, "validation-failures.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 {
		t.Fatalf("failures = %+v, want none", failures)
	}
}

func TestExecuteAPIForwardsAndRecordsSystemPromptVariant(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	suite := `{
	  "id":"api-prompt-variant-test",
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
	var captured struct {
		SystemPrompt string `json:"system_prompt"`
		Prompt       string `json:"prompt"`
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

	result, err := Execute(context.Background(), Options{
		SuitePath:         suitePath,
		OutDir:            filepath.Join(dir, "runs"),
		RunID:             "api-prompt",
		Model:             "deepseek-v4-pro",
		Provider:          "deepseek",
		Mode:              "api",
		APIBaseURL:        server.URL,
		SystemPrompt:      "DEEPSEEK PROMPT\nfollow the schema",
		SystemPromptLabel: "deepseek-schema",
		SystemPromptPath:  "prompts/deepseek/schema.md",
	})
	if err != nil {
		t.Fatal(err)
	}
	if captured.SystemPrompt != "DEEPSEEK PROMPT\nfollow the schema" {
		t.Fatalf("system_prompt = %q", captured.SystemPrompt)
	}
	var manifest record.Manifest
	data, err := os.ReadFile(filepath.Join(result.RunDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SystemPromptLabel != "deepseek-schema" || manifest.SystemPromptPath != "prompts/deepseek/schema.md" {
		t.Fatalf("manifest prompt metadata = %+v", manifest)
	}
	if manifest.SystemPromptChars != len("DEEPSEEK PROMPT\nfollow the schema") {
		t.Fatalf("SystemPromptChars = %d", manifest.SystemPromptChars)
	}
	if manifest.SystemPromptSHA256 == "" {
		t.Fatal("expected manifest system prompt hash")
	}
	promptCopy, err := os.ReadFile(filepath.Join(result.RunDir, "system-prompt.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(promptCopy) != "DEEPSEEK PROMPT\nfollow the schema\n" {
		t.Fatalf("system-prompt.md = %q", string(promptCopy))
	}
}

func TestExecuteRejectsNonAPIMode(t *testing.T) {
	_, err := Execute(context.Background(), Options{Mode: "stub"})
	if err == nil || !strings.Contains(err.Error(), "only through harnessd API") {
		t.Fatalf("error = %v, want API-only rejection", err)
	}
}

func TestExecuteAPIRejectsSuiteDefinedTools(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	suite := `{
	  "id":"api-suite-defined-tool-test",
	  "tools":[{
	    "name":"shape_probe",
	    "description":"suite-defined fake tool",
	    "parameters":{"type":"object","properties":{}}
	  }],
	  "scenarios":[{"id":"shape","prompt":"Call shape_probe.","tool_names":["shape_probe"]}]
	}`
	if err := os.WriteFile(suitePath, []byte(suite), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Execute(context.Background(), Options{
		SuitePath:  suitePath,
		OutDir:     filepath.Join(dir, "runs"),
		RunID:      "api-fixed",
		Model:      "deepseek-v4-pro",
		Provider:   "deepseek",
		Mode:       "api",
		APIBaseURL: "http://127.0.0.1:1",
	})
	if err == nil || !strings.Contains(err.Error(), "cannot inject suite-defined tool") {
		t.Fatalf("error = %v, want suite-defined tool rejection", err)
	}
}

func TestExecuteAPIRecordsMissingRequiredToolAsScenarioFailure(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	suite := `{
	  "id":"api-required-tool-test",
	  "max_turns":1,
	  "scenarios":[{
	    "id":"must-read",
	    "prompt":"Read main.go.",
	    "tool_names":["read"],
	    "required_tools":["read"],
	    "workspace_files":{"main.go":"package main\n"}
	  }]
	}`
	if err := os.WriteFile(suitePath, []byte(suite), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"run_id":"server-run","status":"queued"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/server-run/events":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `id: server-run:1
event: run.completed
data: {"id":"server-run:1","type":"run.completed","payload":{"output":"done without tools"}}

`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := Execute(context.Background(), Options{
		SuitePath:  suitePath,
		OutDir:     filepath.Join(dir, "runs"),
		RunID:      "api-fixed",
		Model:      "deepseek-v4-pro",
		Provider:   "deepseek",
		Mode:       "api",
		APIBaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	failures, err := record.ReadJSONL[record.ValidationFailure](filepath.Join(result.RunDir, "validation-failures.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || failures[0].Issue.Code != "scenario_required_tool_missing" {
		t.Fatalf("failures = %+v, want required tool missing", failures)
	}
}

func TestExecuteAPIRecordsForbiddenArgumentAndMaxToolFailures(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	suite := `{
	  "id":"api-behavior-test",
	  "max_turns":3,
	  "scenarios":[{
	    "id":"workspace-discipline",
	    "prompt":"List files and answer.",
	    "tool_names":["bash"],
	    "required_tools":["bash"],
	    "min_tool_calls":1,
	    "max_tool_calls":1,
	    "forbidden_argument_substrings":["/Users/dennisonbertram/Develop/go-agent-harness"]
	  }]
	}`
	if err := os.WriteFile(suitePath, []byte(suite), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"run_id":"server-run","status":"queued"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/server-run/events":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `id: server-run:1
event: tool.call.started
data: {"id":"server-run:1","type":"tool.call.started","payload":{"tool":"bash","call_id":"c1","arguments":"{\"command\":\"cd /Users/dennisonbertram/Develop/go-agent-harness && ls\"}","step":1}}

id: server-run:2
event: tool.call.started
data: {"id":"server-run:2","type":"tool.call.started","payload":{"tool":"bash","call_id":"c2","arguments":"{\"command\":\"pwd\"}","step":2}}

id: server-run:3
event: run.completed
data: {"id":"server-run:3","type":"run.completed","payload":{"output":"done"}}

`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := Execute(context.Background(), Options{
		SuitePath:  suitePath,
		OutDir:     filepath.Join(dir, "runs"),
		RunID:      "api-fixed",
		Model:      "deepseek-v4-pro",
		Provider:   "deepseek",
		Mode:       "api",
		APIBaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	failures, err := record.ReadJSONL[record.ValidationFailure](filepath.Join(result.RunDir, "validation-failures.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, failure := range failures {
		codes[failure.Issue.Code] = true
	}
	for _, code := range []string{"scenario_forbidden_argument_substring", "scenario_max_tool_calls"} {
		if !codes[code] {
			t.Fatalf("failure codes = %+v, want %s", codes, code)
		}
	}
}

func TestExecuteAPIRecordsForbiddenArgumentKeysAndFinalSignals(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	suite := `{
	  "id":"api-forbidden-key-test",
	  "max_turns":1,
	  "scenarios":[{
	    "id":"path-alias-bait",
	    "prompt":"Read main.go and do not mention TODO.",
	    "tool_names":["read"],
	    "required_tools":["read"],
	    "min_tool_calls":1,
	    "workspace_files":{"main.go":"package main\n"},
	    "expectations":[{
	      "tool":"read",
	      "required_keys":["path"],
	      "forbidden_keys":["file_path"],
	      "exact_args":{"path":"main.go"}
	    }],
	    "forbidden_success_signals":["TODO"]
	  }]
	}`
	if err := os.WriteFile(suitePath, []byte(suite), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"run_id":"server-run","status":"queued"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/server-run/events":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `id: server-run:1
event: tool.call.started
data: {"id":"server-run:1","type":"tool.call.started","payload":{"tool":"read","call_id":"c1","arguments":"{\"path\":\"main.go\",\"file_path\":\"main.go\"}","step":1}}

id: server-run:2
event: run.completed
data: {"id":"server-run:2","type":"run.completed","payload":{"output":"TODO found"}}

`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := Execute(context.Background(), Options{
		SuitePath:  suitePath,
		OutDir:     filepath.Join(dir, "runs"),
		RunID:      "api-fixed",
		Model:      "deepseek-v4-pro",
		Provider:   "deepseek",
		Mode:       "api",
		APIBaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	failures, err := record.ReadJSONL[record.ValidationFailure](filepath.Join(result.RunDir, "validation-failures.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, failure := range failures {
		codes[failure.Issue.Code] = true
	}
	for _, code := range []string{"scenario_forbidden_argument_key", "scenario_forbidden_success_signal_present"} {
		if !codes[code] {
			t.Fatalf("failure codes = %+v, want %s", codes, code)
		}
	}
}

func TestExecuteAPIRecordsWorkspaceExpectationFailures(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	suite := `{
	  "id":"api-workspace-expectation-test",
	  "max_turns":1,
	  "scenarios":[{
	    "id":"must-edit-file",
	    "prompt":"Make the config production-ready.",
	    "workspace_files":{"config.txt":"mode=dev\n"},
	    "workspace_expectations":[{
	      "path":"config.txt",
	      "must_contain":["mode=prod"],
	      "must_not_contain":["mode=dev"]
	    }]
	  }]
	}`
	if err := os.WriteFile(suitePath, []byte(suite), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
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

	result, err := Execute(context.Background(), Options{
		SuitePath:  suitePath,
		OutDir:     filepath.Join(dir, "runs"),
		RunID:      "api-fixed",
		Model:      "deepseek-v4-pro",
		Provider:   "deepseek",
		Mode:       "api",
		APIBaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	failures, err := record.ReadJSONL[record.ValidationFailure](filepath.Join(result.RunDir, "validation-failures.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, failure := range failures {
		codes[failure.Issue.Code] = true
	}
	for _, code := range []string{"scenario_workspace_must_contain_missing", "scenario_workspace_must_not_contain_present"} {
		if !codes[code] {
			t.Fatalf("failure codes = %+v, want %s", codes, code)
		}
	}
}
