package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// runListResponse is the JSON shape returned by GET /v1/runs.
type runListResponse struct {
	Runs []runRecord `json:"runs"`
}

// runRecord is a single run entry from the list or get-by-ID response.
type runRecord struct {
	ID             string         `json:"id"`
	ConversationID string         `json:"conversation_id,omitempty"`
	TenantID       string         `json:"tenant_id,omitempty"`
	Model          string         `json:"model,omitempty"`
	Prompt         string         `json:"prompt,omitempty"`
	Output         string         `json:"output,omitempty"`
	Status         string         `json:"status"`
	Error          string         `json:"error,omitempty"`
	Recap          *workflowRecap `json:"recap,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

type workflowRecap struct {
	Goal                   string   `json:"goal,omitempty"`
	ChangedFiles           []string `json:"changed_files,omitempty"`
	TestsRun               []string `json:"tests_run,omitempty"`
	FailureCause           string   `json:"failure_cause,omitempty"`
	FixPattern             string   `json:"fix_pattern,omitempty"`
	UsefulCommands         []string `json:"useful_commands,omitempty"`
	NextContinuationPrompt string   `json:"next_continuation_prompt,omitempty"`
}

type continueRunResponse struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
}

// runList implements "harnesscli list".
// Sends GET /v1/runs (optionally filtered) and prints a table.
func runList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	baseURL := fs.String("base-url", "http://localhost:8080", "harness API base URL")
	statusFilter := fs.String("status", "", "filter by status (queued, running, completed, failed)")
	convID := fs.String("conversation-id", "", "filter by conversation ID")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "harnesscli list: %v\n", err)
		return 1
	}

	endpoint := strings.TrimRight(*baseURL, "/") + "/v1/runs"
	qv := url.Values{}
	if *statusFilter != "" {
		qv.Set("status", *statusFilter)
	}
	if *convID != "" {
		qv.Set("conversation_id", *convID)
	}
	if len(qv) > 0 {
		endpoint += "?" + qv.Encode()
	}

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli list: build request: %v\n", err)
		return 1
	}

	resp, err := requestHTTPClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli list: request failed: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli list: read response: %v\n", err)
		return 1
	}

	if resp.StatusCode >= 300 {
		fmt.Fprintf(stderr, "harnesscli list: %v\n", formatAPIError(resp.StatusCode, body))
		return 1
	}

	var lr runListResponse
	if err := json.Unmarshal(body, &lr); err != nil {
		fmt.Fprintf(stderr, "harnesscli list: decode response: %v\n", err)
		return 1
	}

	if len(lr.Runs) == 0 {
		fmt.Fprintln(stdout, "No runs found")
		return 0
	}

	printRunTable(lr.Runs)
	return 0
}

// runCancel implements "harnesscli cancel <run-id>".
// Sends POST /v1/runs/{id}/cancel and reports success or failure.
func runCancel(args []string) int {
	fs := flag.NewFlagSet("cancel", flag.ContinueOnError)
	fs.SetOutput(stderr)
	baseURL := fs.String("base-url", "http://localhost:8080", "harness API base URL")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "harnesscli cancel: %v\n", err)
		return 1
	}

	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "harnesscli cancel: run ID is required")
		return 1
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(stderr, "harnesscli cancel: too many arguments; accepts exactly one run ID")
		return 1
	}
	runID := fs.Arg(0)

	endpoint := strings.TrimRight(*baseURL, "/") + "/v1/runs/" + url.PathEscape(runID) + "/cancel"
	req, err := http.NewRequest(http.MethodPost, endpoint, nil)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli cancel: build request: %v\n", err)
		return 1
	}

	resp, err := requestHTTPClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli cancel: request failed: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli cancel: read response: %v\n", err)
		return 1
	}

	if resp.StatusCode == http.StatusNotFound {
		fmt.Fprintf(stderr, "harnesscli cancel: run %q not found\n", runID)
		return 1
	}

	if resp.StatusCode >= 300 {
		fmt.Fprintf(stderr, "harnesscli cancel: %v\n", formatAPIError(resp.StatusCode, body))
		return 1
	}

	fmt.Fprintf(stdout, "Run %s cancelling\n", runID)
	return 0
}

// runStatus implements "harnesscli status <run-id>".
// Sends GET /v1/runs/{id} and prints run details.
func runStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	baseURL := fs.String("base-url", "http://localhost:8080", "harness API base URL")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "harnesscli status: %v\n", err)
		return 1
	}

	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "harnesscli status: run ID is required")
		return 1
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(stderr, "harnesscli status: too many arguments; accepts exactly one run ID")
		return 1
	}
	runID := fs.Arg(0)

	endpoint := strings.TrimRight(*baseURL, "/") + "/v1/runs/" + url.PathEscape(runID)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli status: build request: %v\n", err)
		return 1
	}

	resp, err := requestHTTPClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli status: request failed: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli status: read response: %v\n", err)
		return 1
	}

	if resp.StatusCode == http.StatusNotFound {
		fmt.Fprintf(stderr, "harnesscli status: run %q not found\n", runID)
		return 1
	}

	if resp.StatusCode >= 300 {
		fmt.Fprintf(stderr, "harnesscli status: %v\n", formatAPIError(resp.StatusCode, body))
		return 1
	}

	var r runRecord
	if err := json.Unmarshal(body, &r); err != nil {
		fmt.Fprintf(stderr, "harnesscli status: decode response: %v\n", err)
		return 1
	}

	model := r.Model
	if model == "" {
		model = "(default)"
	}
	fmt.Fprintf(stdout, "ID:        %s\n", r.ID)
	fmt.Fprintf(stdout, "Status:    %s\n", r.Status)
	fmt.Fprintf(stdout, "Model:     %s\n", model)
	fmt.Fprintf(stdout, "Created:   %s\n", r.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(stdout, "Updated:   %s\n", r.UpdatedAt.Format(time.RFC3339))
	if r.Prompt != "" {
		prompt := r.Prompt
		if len(prompt) > 80 {
			prompt = prompt[:77] + "..."
		}
		fmt.Fprintf(stdout, "Prompt:    %s\n", prompt)
	}
	if r.Error != "" {
		fmt.Fprintf(stdout, "Error:     %s\n", r.Error)
	}
	if r.Output != "" {
		fmt.Fprintf(stdout, "Output:    %s\n", r.Output)
	}
	if r.Recap != nil {
		printWorkflowRecap(r.Recap)
	}
	return 0
}

// runContinue implements "harnesscli continue <run-id> <prompt>".
// It starts a continuation run and streams the new run's events by default.
func runContinue(args []string) int {
	fs := flag.NewFlagSet("continue", flag.ContinueOnError)
	fs.SetOutput(stderr)
	baseURL := fs.String("base-url", "http://localhost:8080", "harness API base URL")
	noStream := fs.Bool("no-stream", false, "create the continuation without streaming events")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "harnesscli continue: %v\n", err)
		return 1
	}
	if fs.NArg() < 2 {
		fmt.Fprintln(stderr, "harnesscli continue: run ID and prompt are required")
		return 1
	}
	runID := fs.Arg(0)
	prompt := strings.TrimSpace(strings.Join(fs.Args()[1:], " "))
	if prompt == "" {
		fmt.Fprintln(stderr, "harnesscli continue: prompt is required")
		return 1
	}

	body, err := json.Marshal(map[string]string{"prompt": prompt})
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli continue: encode request: %v\n", err)
		return 1
	}
	endpoint := strings.TrimRight(*baseURL, "/") + "/v1/runs/" + url.PathEscape(runID) + "/continue"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli continue: build request: %v\n", err)
		return 1
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := requestHTTPClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli continue: request failed: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli continue: read response: %v\n", err)
		return 1
	}
	if resp.StatusCode >= 300 {
		fmt.Fprintf(stderr, "harnesscli continue: %v\n", formatAPIError(resp.StatusCode, responseBody))
		return 1
	}

	var created continueRunResponse
	if err := json.Unmarshal(responseBody, &created); err != nil {
		fmt.Fprintf(stderr, "harnesscli continue: decode response: %v\n", err)
		return 1
	}
	if created.RunID == "" {
		fmt.Fprintln(stderr, "harnesscli continue: response missing run_id")
		return 1
	}
	fmt.Fprintf(stdout, "run_id=%s\n", created.RunID)
	if *noStream {
		return 0
	}
	terminalEvent, err := streamRunEvents(context.Background(), streamHTTPClient, *baseURL, created.RunID, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli continue: stream events: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "terminal_event=%s\n", terminalEvent)
	return 0
}

// runReplay implements "harnesscli replay <run-id-or-rollout-path>".
func runReplay(args []string) int {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	fs.SetOutput(stderr)
	baseURL := fs.String("base-url", "http://localhost:8080", "harness API base URL")
	mode := fs.String("mode", "simulate", "replay mode: simulate or fork")
	forkStep := fs.Int("fork-step", 0, "fork step when -mode=fork")
	detectDrift := fs.Bool("detect-drift", false, "run drift detection during simulate replay")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "harnesscli replay: %v\n", err)
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "harnesscli replay: exactly one run ID or rollout path is required")
		return 1
	}

	payload := map[string]any{
		"rollout_path": fs.Arg(0),
		"mode":         *mode,
	}
	if *mode == "fork" {
		payload["fork_step"] = *forkStep
	}
	if *detectDrift {
		payload["detect_drift"] = true
	}
	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli replay: encode request: %v\n", err)
		return 1
	}

	endpoint := strings.TrimRight(*baseURL, "/") + "/v1/runs/replay"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli replay: build request: %v\n", err)
		return 1
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := requestHTTPClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli replay: request failed: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli replay: read response: %v\n", err)
		return 1
	}
	if resp.StatusCode >= 300 {
		fmt.Fprintf(stderr, "harnesscli replay: %v\n", formatAPIError(resp.StatusCode, responseBody))
		return 1
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, responseBody, "", "  "); err == nil {
		_, _ = pretty.WriteTo(stdout)
		fmt.Fprintln(stdout)
		return 0
	}
	fmt.Fprintln(stdout, strings.TrimSpace(string(responseBody)))
	return 0
}

// runSearch implements "harnesscli search <query>" across run metadata.
func runSearch(args []string) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	baseURL := fs.String("base-url", "http://localhost:8080", "harness API base URL")
	statusFilter := fs.String("status", "", "filter by status before searching")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "harnesscli search: %v\n", err)
		return 1
	}
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		fmt.Fprintln(stderr, "harnesscli search: query is required")
		return 1
	}

	endpoint := strings.TrimRight(*baseURL, "/") + "/v1/runs"
	qv := url.Values{}
	if *statusFilter != "" {
		qv.Set("status", *statusFilter)
	}
	if len(qv) > 0 {
		endpoint += "?" + qv.Encode()
	}

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli search: build request: %v\n", err)
		return 1
	}
	resp, err := requestHTTPClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli search: request failed: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli search: read response: %v\n", err)
		return 1
	}
	if resp.StatusCode >= 300 {
		fmt.Fprintf(stderr, "harnesscli search: %v\n", formatAPIError(resp.StatusCode, body))
		return 1
	}

	var lr runListResponse
	if err := json.Unmarshal(body, &lr); err != nil {
		fmt.Fprintf(stderr, "harnesscli search: decode response: %v\n", err)
		return 1
	}
	needle := strings.ToLower(query)
	matches := make([]runRecord, 0, len(lr.Runs))
	for _, r := range lr.Runs {
		haystack := strings.ToLower(strings.Join([]string{
			r.ID,
			r.ConversationID,
			r.TenantID,
			r.Model,
			r.Prompt,
			r.Output,
			r.Status,
			r.Error,
			workflowRecapSearchText(r.Recap),
		}, "\n"))
		if strings.Contains(haystack, needle) {
			matches = append(matches, r)
		}
	}
	if len(matches) == 0 {
		fmt.Fprintln(stdout, "No matching runs found")
		return 0
	}
	printRunTable(matches)
	return 0
}

func printRunTable(runs []runRecord) {
	fmt.Fprintf(stdout, "%-24s  %-18s  %-20s  %s\n", "ID", "STATUS", "MODEL", "PROMPT")
	fmt.Fprintf(stdout, "%s\n", strings.Repeat("-", 90))
	for _, r := range runs {
		prompt := r.Prompt
		if len(prompt) > 40 {
			prompt = prompt[:37] + "..."
		}
		model := r.Model
		if model == "" {
			model = "(default)"
		}
		fmt.Fprintf(stdout, "%-24s  %-18s  %-20s  %s\n", r.ID, r.Status, model, prompt)
	}
}

func workflowRecapSearchText(recap *workflowRecap) string {
	if recap == nil {
		return ""
	}
	return strings.Join([]string{
		recap.Goal,
		strings.Join(recap.ChangedFiles, "\n"),
		strings.Join(recap.TestsRun, "\n"),
		recap.FailureCause,
		recap.FixPattern,
		strings.Join(recap.UsefulCommands, "\n"),
		recap.NextContinuationPrompt,
	}, "\n")
}

func printWorkflowRecap(recap *workflowRecap) {
	fmt.Fprintln(stdout, "Recap:")
	if recap.Goal != "" {
		fmt.Fprintf(stdout, "  Goal: %s\n", recap.Goal)
	}
	if len(recap.ChangedFiles) > 0 {
		fmt.Fprintf(stdout, "  Changed files: %s\n", strings.Join(recap.ChangedFiles, ", "))
	}
	if len(recap.TestsRun) > 0 {
		fmt.Fprintf(stdout, "  Tests run: %s\n", strings.Join(recap.TestsRun, ", "))
	}
	if recap.FailureCause != "" {
		fmt.Fprintf(stdout, "  Failure cause: %s\n", recap.FailureCause)
	}
	if recap.FixPattern != "" {
		fmt.Fprintf(stdout, "  Fix pattern: %s\n", recap.FixPattern)
	}
	if len(recap.UsefulCommands) > 0 {
		fmt.Fprintf(stdout, "  Useful commands: %s\n", strings.Join(recap.UsefulCommands, ", "))
	}
	if recap.NextContinuationPrompt != "" {
		fmt.Fprintf(stdout, "  Next: %s\n", recap.NextContinuationPrompt)
	}
}
