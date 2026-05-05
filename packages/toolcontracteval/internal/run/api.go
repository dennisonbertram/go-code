package run

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-agent-harness/internal/harness"
	tcatalog "go-agent-harness/packages/toolcontracteval/internal/catalog"
	"go-agent-harness/packages/toolcontracteval/internal/cluster"
	"go-agent-harness/packages/toolcontracteval/internal/profile"
	"go-agent-harness/packages/toolcontracteval/internal/record"
	"go-agent-harness/packages/toolcontracteval/internal/repair"
	"go-agent-harness/packages/toolcontracteval/internal/report"
	"go-agent-harness/packages/toolcontracteval/internal/scenario"
	"go-agent-harness/packages/toolcontracteval/internal/schema"
)

type apiClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

type sseEvent struct {
	ID      string
	Type    string
	Payload map[string]any
	Raw     string
}

func ExecuteAPI(ctx context.Context, opts Options) (Result, error) {
	if strings.TrimSpace(opts.APIBaseURL) == "" {
		return Result{}, fmt.Errorf("api mode requires --api-base-url")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.OutDir == "" {
		opts.OutDir = ".runs"
	}
	suite, err := scenario.Load(opts.SuitePath)
	if err != nil {
		return Result{}, err
	}
	runID := opts.RunID
	if runID == "" {
		runID = fmt.Sprintf("%s-%s-api", opts.Now().UTC().Format("20060102T150405Z"), sanitizeID(opts.Model))
	}
	runDir := filepath.Join(opts.OutDir, runID)
	writer, err := record.NewWriter(runDir)
	if err != nil {
		return Result{}, err
	}
	manifest := record.Manifest{
		RunID:              runID,
		SuiteID:            suite.ID,
		Model:              opts.Model,
		Provider:           opts.Provider,
		Mode:               "api",
		SystemPromptLabel:  strings.TrimSpace(opts.SystemPromptLabel),
		SystemPromptPath:   strings.TrimSpace(opts.SystemPromptPath),
		SystemPromptSHA256: systemPromptSHA256(opts.SystemPrompt),
		SystemPromptChars:  len([]rune(opts.SystemPrompt)),
		StartedAt:          opts.Now().UTC(),
	}
	if err := writer.WriteJSON("manifest.json", manifest); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(opts.SystemPrompt) != "" {
		if err := os.WriteFile(filepath.Join(writer.Dir(), "system-prompt.md"), []byte(ensureTrailingNewline(opts.SystemPrompt)), 0o644); err != nil {
			return Result{}, err
		}
	}

	client := apiClient{
		baseURL: strings.TrimRight(opts.APIBaseURL, "/"),
		apiKey:  opts.APIKey,
		http:    &http.Client{},
	}

	allDefinitions := map[string]harness.ToolDefinition{}
	for _, sc := range suite.Scenarios {
		workspaceRoot, cleanup, err := seedWorkspace(sc)
		if err != nil {
			return Result{}, err
		}
		defer cleanup()

		productionDefs := tcatalog.ProductionDefinitions(workspaceRoot)
		toolDefs := tcatalog.MergeAndSelect(productionDefs, suite.Tools, sc.ToolNames)
		if len(toolDefs) == 0 {
			return Result{}, fmt.Errorf("scenario %q selected no tools", sc.ID)
		}
		defMap := tcatalog.DefinitionMap(toolDefs)
		productionDefMap := tcatalog.DefinitionMap(productionDefs)
		for name := range defMap {
			if _, ok := productionDefMap[name]; !ok {
				return Result{}, fmt.Errorf("api mode cannot inject suite-defined tool %q for scenario %q; use production harness tools", name, sc.ID)
			}
		}
		for _, def := range toolDefs {
			allDefinitions[def.Name] = def.Clone()
		}

		maxTurns := suite.MaxTurns
		if sc.MaxTurns > 0 {
			maxTurns = sc.MaxTurns
		}
		if opts.MaxTurns > 0 {
			maxTurns = opts.MaxTurns
		}
		if err := runAPIScenario(ctx, client, writer, runID, opts, sc, workspaceRoot, defMap, maxTurns); err != nil {
			return Result{}, err
		}
	}

	defs := make([]harness.ToolDefinition, 0, len(allDefinitions))
	for _, def := range allDefinitions {
		defs = append(defs, def.Clone())
	}
	if err := writer.WriteJSON("tool-definitions.json", defs); err != nil {
		return Result{}, err
	}
	failures, err := record.ReadJSONL[record.ValidationFailure](filepath.Join(runDir, "validation-failures.jsonl"))
	if err != nil {
		return Result{}, err
	}
	if err := writer.WriteJSON("clusters.json", cluster.FromFailures(failures)); err != nil {
		return Result{}, err
	}
	if _, err := report.Generate(runDir); err != nil {
		return Result{}, err
	}
	if _, err := profile.Generate(runDir); err != nil {
		return Result{}, err
	}
	manifest.CompletedAt = opts.Now().UTC()
	if err := writer.WriteJSON("manifest.json", manifest); err != nil {
		return Result{}, err
	}
	return Result{RunID: runID, RunDir: writer.Dir()}, nil
}

func runAPIScenario(ctx context.Context, client apiClient, writer *record.Writer, evalRunID string, opts Options, sc scenario.Scenario, workspaceRoot string, defMap map[string]harness.ToolDefinition, maxTurns int) error {
	payload := map[string]any{
		"prompt":         sc.Prompt,
		"model":          opts.Model,
		"provider_name":  opts.Provider,
		"workspace_path": workspaceRoot,
		"max_steps":      maxTurns,
	}
	if strings.TrimSpace(opts.SystemPrompt) != "" {
		payload["system_prompt"] = opts.SystemPrompt
	}
	serverRunID, err := client.startRun(ctx, payload)
	result := record.ScenarioResult{RunID: evalRunID, Scenario: sc.ID}
	if err != nil {
		result.Error = err.Error()
		return writer.AppendJSONL("scenario-results.jsonl", result)
	}

	events, err := client.streamEvents(ctx, serverRunID)
	if err != nil {
		result.Error = err.Error()
		_ = writer.AppendJSONL("scenario-results.jsonl", result)
		return nil
	}
	calledTools := map[string]int{}
	finalOutput := ""
	for _, event := range events {
		if err := writer.AppendJSONL("api-events.jsonl", record.APIEvent{RunID: evalRunID, Scenario: sc.ID, EventID: event.ID, Type: event.Type, Payload: event.Payload, Raw: event.Raw}); err != nil {
			return err
		}
		if event.Type == "run.completed" {
			result.Completed = true
			finalOutput = stringValue(event.Payload["output"])
		}
		if event.Type == "run.failed" {
			result.Error = stringValue(event.Payload["error"])
		}
		if event.Type != "tool.call.started" {
			if event.Type == "tool.call.completed" {
				if err := recordToolResultFromAPI(writer, evalRunID, sc.ID, event); err != nil {
					return err
				}
			}
			continue
		}
		result.ToolCalls++
		tool := stringValue(event.Payload["tool"])
		calledTools[tool]++
		callID := stringValue(event.Payload["call_id"])
		argsRaw := stringValue(event.Payload["arguments"])
		turn := intValue(event.Payload["step"])
		def, ok := defMap[tool]
		var issues []schema.Issue
		if !ok {
			issues = []schema.Issue{{
				Code:     "unknown_tool",
				Expected: "known tool name",
				Received: tool,
				Message:  fmt.Sprintf("tool %q is not available in this scenario", tool),
			}}
		} else {
			validation := schema.ValidateRaw(tool, json.RawMessage(argsRaw), def.Parameters)
			issues = validation.Issues
			if len(issues) == 0 {
				issues = append(issues, expectationIssues(sc, tool, validation.Args)...)
			}
			issues = append(issues, forbiddenArgumentIssues(sc, tool, argsRaw)...)
		}
		valid := len(issues) == 0
		if err := writer.AppendJSONL("tool-calls.jsonl", record.ToolCall{
			RunID:        evalRunID,
			Model:        opts.Model,
			Provider:     opts.Provider,
			Scenario:     sc.ID,
			Turn:         turn,
			Tool:         tool,
			CallID:       callID,
			ArgumentsRaw: argsRaw,
			Valid:        valid,
		}); err != nil {
			return err
		}
		if valid {
			continue
		}
		result.InvalidCalls++
		result.ValidationHits += len(issues)
		for _, issue := range issues {
			failure := record.ValidationFailure{RunID: evalRunID, Model: opts.Model, Provider: opts.Provider, Scenario: sc.ID, Turn: turn, Tool: tool, CallID: callID, ArgumentsRaw: argsRaw, Issue: issue}
			if err := writer.AppendJSONL("validation-failures.jsonl", failure); err != nil {
				return err
			}
		}
		if ok {
			for _, sim := range repair.SimulateAll(tool, json.RawMessage(argsRaw), def.Parameters) {
				if err := writer.AppendJSONL("repair-simulation.jsonl", map[string]any{
					"run_id":                 evalRunID,
					"scenario":               sc.ID,
					"turn":                   turn,
					"tool":                   tool,
					"call_id":                callID,
					"repair":                 sim.Repair,
					"safety":                 sim.Safety,
					"before_valid":           sim.BeforeValid,
					"applied":                sim.Applied,
					"after_valid":            sim.AfterValid,
					"semantic_note_required": sim.SemanticNoteRequired,
					"repaired_arguments":     sim.RepairedArguments,
					"issues_after":           sim.IssuesAfter,
				}); err != nil {
					return err
				}
			}
		}
	}
	if err := recordScenarioAssertions(writer, evalRunID, opts, sc, &result, calledTools, finalOutput, workspaceRoot); err != nil {
		return err
	}
	return writer.AppendJSONL("scenario-results.jsonl", result)
}

func recordScenarioAssertions(writer *record.Writer, evalRunID string, opts Options, sc scenario.Scenario, result *record.ScenarioResult, calledTools map[string]int, finalOutput, workspaceRoot string) error {
	var issues []schema.Issue
	if sc.MinToolCalls > 0 && result.ToolCalls < sc.MinToolCalls {
		issues = append(issues, schema.Issue{
			Code:     "scenario_min_tool_calls",
			Expected: fmt.Sprintf("%d tool calls", sc.MinToolCalls),
			Received: fmt.Sprintf("%d tool calls", result.ToolCalls),
			Message:  fmt.Sprintf("scenario %q expected at least %d tool calls", sc.ID, sc.MinToolCalls),
		})
	}
	if sc.MaxToolCalls > 0 && result.ToolCalls > sc.MaxToolCalls {
		issues = append(issues, schema.Issue{
			Code:     "scenario_max_tool_calls",
			Expected: fmt.Sprintf("no more than %d tool calls", sc.MaxToolCalls),
			Received: fmt.Sprintf("%d tool calls", result.ToolCalls),
			Message:  fmt.Sprintf("scenario %q expected no more than %d tool calls", sc.ID, sc.MaxToolCalls),
		})
	}
	for _, tool := range sc.RequiredTools {
		if calledTools[tool] == 0 {
			issues = append(issues, schema.Issue{
				Code:     "scenario_required_tool_missing",
				Expected: tool,
				Received: "not called",
				Message:  fmt.Sprintf("scenario %q expected tool %q to be called", sc.ID, tool),
			})
		}
	}
	for _, tool := range sc.ForbiddenTools {
		if calledTools[tool] > 0 {
			issues = append(issues, schema.Issue{
				Code:     "scenario_forbidden_tool_called",
				Expected: "not called",
				Received: tool,
				Message:  fmt.Sprintf("scenario %q forbade tool %q", sc.ID, tool),
			})
		}
	}
	for _, signal := range sc.SuccessSignals {
		if !strings.Contains(strings.ToLower(finalOutput), strings.ToLower(signal)) {
			issues = append(issues, schema.Issue{
				Code:     "scenario_success_signal_missing",
				Expected: signal,
				Received: "missing from final output",
				Message:  fmt.Sprintf("scenario %q expected final output to contain %q", sc.ID, signal),
			})
		}
	}
	for _, signal := range sc.ForbiddenSuccessSignals {
		if strings.Contains(strings.ToLower(finalOutput), strings.ToLower(signal)) {
			issues = append(issues, schema.Issue{
				Code:     "scenario_forbidden_success_signal_present",
				Expected: fmt.Sprintf("final output not to contain %q", signal),
				Received: signal,
				Message:  fmt.Sprintf("scenario %q expected final output not to contain %q", sc.ID, signal),
			})
		}
	}
	issues = append(issues, workspaceExpectationIssues(sc, workspaceRoot)...)
	if len(issues) == 0 {
		return nil
	}
	result.ValidationHits += len(issues)
	if result.ToolCalls == 0 {
		result.InvalidCalls++
	}
	for _, issue := range issues {
		if err := writer.AppendJSONL("validation-failures.jsonl", record.ValidationFailure{
			RunID:    evalRunID,
			Model:    opts.Model,
			Provider: opts.Provider,
			Scenario: sc.ID,
			Issue:    issue,
		}); err != nil {
			return err
		}
	}
	return nil
}

func workspaceExpectationIssues(sc scenario.Scenario, workspaceRoot string) []schema.Issue {
	var out []schema.Issue
	for _, expectation := range sc.WorkspaceExpectations {
		clean := filepath.Clean(expectation.Path)
		if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			out = append(out, schema.Issue{
				Path:     []string{"workspace_expectations", expectation.Path},
				Code:     "scenario_workspace_path_unsafe",
				Expected: "relative path inside workspace",
				Received: expectation.Path,
				Message:  fmt.Sprintf("scenario %q has unsafe workspace expectation path %q", sc.ID, expectation.Path),
			})
			continue
		}
		abs := filepath.Join(workspaceRoot, clean)
		data, err := os.ReadFile(abs)
		exists := err == nil
		if expectation.Exists != nil {
			if *expectation.Exists && !exists {
				out = append(out, schema.Issue{
					Path:     []string{clean},
					Code:     "scenario_workspace_file_missing",
					Expected: "file to exist",
					Received: "missing",
					Message:  fmt.Sprintf("scenario %q expected workspace file %q to exist", sc.ID, clean),
				})
				continue
			}
			if !*expectation.Exists && exists {
				out = append(out, schema.Issue{
					Path:     []string{clean},
					Code:     "scenario_workspace_file_present",
					Expected: "file to be absent",
					Received: "present",
					Message:  fmt.Sprintf("scenario %q expected workspace file %q to be absent", sc.ID, clean),
				})
			}
		}
		if !exists {
			if len(expectation.MustContain) > 0 || len(expectation.MustNotContain) > 0 || expectation.Equals != "" {
				out = append(out, schema.Issue{
					Path:     []string{clean},
					Code:     "scenario_workspace_file_missing",
					Expected: "file with expected content",
					Received: "missing",
					Message:  fmt.Sprintf("scenario %q expected workspace file %q for content checks", sc.ID, clean),
				})
			}
			continue
		}
		if err != nil {
			out = append(out, schema.Issue{
				Path:     []string{clean},
				Code:     "scenario_workspace_file_read_error",
				Expected: "readable file",
				Received: err.Error(),
				Message:  fmt.Sprintf("scenario %q could not read workspace file %q: %v", sc.ID, clean, err),
			})
			continue
		}
		content := string(data)
		if expectation.Equals != "" && content != expectation.Equals {
			out = append(out, schema.Issue{
				Path:     []string{clean},
				Code:     "scenario_workspace_equals_mismatch",
				Expected: expectation.Equals,
				Received: truncateForIssue(content),
				Message:  fmt.Sprintf("scenario %q expected workspace file %q to equal configured content", sc.ID, clean),
			})
		}
		for _, needle := range expectation.MustContain {
			if !strings.Contains(content, needle) {
				out = append(out, schema.Issue{
					Path:     []string{clean},
					Code:     "scenario_workspace_must_contain_missing",
					Expected: needle,
					Received: "missing",
					Message:  fmt.Sprintf("scenario %q expected workspace file %q to contain %q", sc.ID, clean, needle),
				})
			}
		}
		for _, needle := range expectation.MustNotContain {
			if strings.Contains(content, needle) {
				out = append(out, schema.Issue{
					Path:     []string{clean},
					Code:     "scenario_workspace_must_not_contain_present",
					Expected: fmt.Sprintf("not %q", needle),
					Received: needle,
					Message:  fmt.Sprintf("scenario %q expected workspace file %q not to contain %q", sc.ID, clean, needle),
				})
			}
		}
	}
	return out
}

func truncateForIssue(s string) string {
	s = strings.ReplaceAll(s, "\n", `\n`)
	if len(s) <= 180 {
		return s
	}
	return s[:177] + "..."
}

func recordToolResultFromAPI(writer *record.Writer, evalRunID, scenarioID string, event sseEvent) error {
	return writer.AppendJSONL("tool-results.jsonl", record.ToolResult{
		RunID:    evalRunID,
		Scenario: scenarioID,
		Turn:     intValue(event.Payload["step"]),
		Tool:     stringValue(event.Payload["tool"]),
		CallID:   stringValue(event.Payload["call_id"]),
		Content:  stringValue(event.Payload["output"]),
		Error:    stringValue(event.Payload["error"]),
	})
}

func (c apiClient) startRun(ctx context.Context, payload map[string]any) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/runs", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("POST /v1/runs returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var out struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", err
	}
	if out.RunID == "" {
		return "", fmt.Errorf("POST /v1/runs returned empty run_id")
	}
	return out.RunID, nil
}

func (c apiClient) streamEvents(ctx context.Context, runID string) ([]sseEvent, error) {
	escaped := url.PathEscape(runID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/runs/"+escaped+"/events", nil)
	if err != nil {
		return nil, err
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET /v1/runs/%s/events returned %d: %s", runID, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return parseSSE(resp.Body)
}

func (c apiClient) authorize(req *http.Request) {
	if strings.TrimSpace(c.apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.apiKey))
	}
}

func systemPromptSHA256(prompt string) string {
	if strings.TrimSpace(prompt) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(prompt))
	return fmt.Sprintf("%x", sum[:])
}

func ensureTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

func parseSSE(r io.Reader) ([]sseEvent, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var out []sseEvent
	var current sseEvent
	var data strings.Builder
	var raw strings.Builder
	flush := func() error {
		if current.Type == "" && data.Len() == 0 && current.ID == "" {
			raw.Reset()
			return nil
		}
		current.Raw = raw.String()
		if data.Len() > 0 {
			var envelope struct {
				ID      string         `json:"id"`
				Type    string         `json:"type"`
				Payload map[string]any `json:"payload"`
			}
			if err := json.Unmarshal([]byte(data.String()), &envelope); err != nil {
				return err
			}
			if current.ID == "" {
				current.ID = envelope.ID
			}
			if current.Type == "" {
				current.Type = envelope.Type
			}
			current.Payload = envelope.Payload
		}
		out = append(out, current)
		current = sseEvent{}
		data.Reset()
		raw.Reset()
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		raw.WriteString(line)
		raw.WriteByte('\n')
		if line == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		if strings.HasPrefix(line, "id:") {
			current.ID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			continue
		}
		if strings.HasPrefix(line, "event:") {
			current.Type = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if current.Type != "" || data.Len() > 0 || current.ID != "" {
		if err := flush(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func intValue(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}
