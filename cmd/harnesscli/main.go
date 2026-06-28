package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"go-agent-harness/cmd/harnesscli/tui"
	"go-agent-harness/internal/harness"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

var (
	runCommand = dispatch
	exitFunc   = os.Exit
	osArgs     = os.Args

	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr

	requestHTTPClient = &http.Client{Timeout: 60 * time.Second}

	// streamHTTPClient is used exclusively for SSE event streaming. It must have no
	// client-level timeout (Timeout: 0) and a transport whose IdleConnTimeout is
	// disabled (0) so that long-running tool-call pauses between SSE events do not
	// cause the connection to be reaped. The default transport's IdleConnTimeout of
	// 90s is designed for short-lived request/response cycles and is inappropriate
	// for streaming connections that may be silent for minutes while the harness
	// executes tool calls.
	streamHTTPClient = &http.Client{
		Transport: &http.Transport{
			// Disable idle-connection reaping; SSE connections stay open for the
			// duration of a run and must never be closed by the transport.
			IdleConnTimeout: 0,
			// No response-header timeout; the server controls when events arrive.
			ResponseHeaderTimeout: 0,
			// Keep-alives are essential for long-lived SSE connections.
			DisableKeepAlives: false,
			// Preserve sensible dial and TLS timeouts from the default transport
			// so that initial connection setup remains bounded.
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          10,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}

	errInvalidSSEData  = errors.New("invalid sse data")
	errIgnoredSSEBlock = errors.New("ignored sse block")
)

type runCreateRequest struct {
	Prompt           string                   `json:"prompt"`
	Model            string                   `json:"model,omitempty"`
	SystemPrompt     string                   `json:"system_prompt,omitempty"`
	AgentIntent      string                   `json:"agent_intent,omitempty"`
	TaskContext      string                   `json:"task_context,omitempty"`
	PromptProfile    string                   `json:"prompt_profile,omitempty"`
	PromptExtensions *runCreatePromptSettings `json:"prompt_extensions,omitempty"`
	WorkspacePath    string                   `json:"workspace_path,omitempty"`
}

type runCreatePromptSettings struct {
	Behaviors []string `json:"behaviors,omitempty"`
	Talents   []string `json:"talents,omitempty"`
	Skills    []string `json:"skills,omitempty"`
	Custom    string   `json:"custom,omitempty"`
}

type runCreateResponse struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
}

type apiErrorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type sseEnvelope struct {
	Event string
	Data  string
}

type csvListFlag struct {
	values []string
}

func (f *csvListFlag) String() string {
	return strings.Join(f.values, ",")
}

func (f *csvListFlag) Set(value string) error {
	for _, raw := range strings.Split(value, ",") {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		f.values = append(f.values, item)
	}
	return nil
}

func main() {
	exitFunc(runCommand(osArgs[1:]))
}

func run(args []string) int {
	flags := flag.NewFlagSet("harnesscli", flag.ContinueOnError)
	flags.SetOutput(stderr)

	baseURL := flags.String("base-url", "http://localhost:8080", "harness API base URL")
	prompt := flags.String("prompt", "", "prompt to send to harness")
	model := flags.String("model", "", "model override for this run")
	systemPrompt := flags.String("system-prompt", "", "system prompt override for this run")
	agentIntent := flags.String("agent-intent", "", "startup intent for prompt routing (for example code_review)")
	taskContext := flags.String("task-context", "", "harness task context injected into startup prompt")
	promptProfile := flags.String("prompt-profile", "", "prompt profile override for model routing")
	promptCustom := flags.String("prompt-custom", "", "custom prompt extension text")
	workspace := flags.String("workspace", "", "workspace directory for this run (defaults to current working directory)")
	enableTUI := flags.Bool("tui", false, "launch interactive BubbleTea TUI (experimental)")
	listProfiles := flags.Bool("list-profiles", false, "list available profiles and exit")
	var behaviorFlags csvListFlag
	var talentFlags csvListFlag
	flags.Var(&behaviorFlags, "prompt-behavior", "behavior extension ids (repeatable or comma-separated)")
	flags.Var(&talentFlags, "prompt-talent", "talent extension ids (repeatable or comma-separated)")

	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(stderr, "harnesscli: parse failed: %v\n", err)
		return 1
	}

	if *listProfiles {
		return listProfilesCmd(requestHTTPClient, *baseURL)
	}

	workspacePath := resolveWorkspacePath(*workspace)

	if *enableTUI {
		if err := runTUI(*baseURL, workspacePath); err != nil {
			fmt.Fprintf(stderr, "harnesscli: tui: %v\n", err)
			return 1
		}
		return 0
	}

	if strings.TrimSpace(*prompt) == "" {
		fmt.Fprintln(stderr, "harnesscli: prompt is required")
		return 1
	}

	var extensions *runCreatePromptSettings
	if len(behaviorFlags.values) > 0 || len(talentFlags.values) > 0 || strings.TrimSpace(*promptCustom) != "" {
		extensions = &runCreatePromptSettings{
			Behaviors: append([]string(nil), behaviorFlags.values...),
			Talents:   append([]string(nil), talentFlags.values...),
			Custom:    *promptCustom,
		}
	}

	ctx := context.Background()
	runID, err := startRun(ctx, requestHTTPClient, *baseURL, runCreateRequest{
		Prompt:           *prompt,
		Model:            *model,
		SystemPrompt:     *systemPrompt,
		AgentIntent:      *agentIntent,
		TaskContext:      *taskContext,
		PromptProfile:    *promptProfile,
		PromptExtensions: extensions,
		WorkspacePath:    workspacePath,
	})
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli: start run: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "run_id=%s\n", runID)
	terminalEvent, err := streamRunEvents(ctx, streamHTTPClient, *baseURL, runID, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli: stream events: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "terminal_event=%s\n", terminalEvent)
	return 0
}

func startRun(ctx context.Context, client *http.Client, baseURL string, req runCreateRequest) (string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("encode run request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/runs", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build run request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpRes, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("send run request: %w", err)
	}
	defer httpRes.Body.Close()

	responseBody, err := io.ReadAll(httpRes.Body)
	if err != nil {
		return "", fmt.Errorf("read run response: %w", err)
	}

	if httpRes.StatusCode >= 300 {
		return "", formatAPIError(httpRes.StatusCode, responseBody)
	}

	var created runCreateResponse
	if err := json.Unmarshal(responseBody, &created); err != nil {
		return "", fmt.Errorf("decode run response: %w", err)
	}
	if created.RunID == "" {
		return "", fmt.Errorf("decode run response: missing run_id")
	}
	return created.RunID, nil
}

func streamRunEvents(ctx context.Context, client *http.Client, baseURL, runID string, out io.Writer) (string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/runs/"+runID+"/events", nil)
	if err != nil {
		return "", fmt.Errorf("build event stream request: %w", err)
	}

	httpRes, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("send event stream request: %w", err)
	}
	defer httpRes.Body.Close()

	if httpRes.StatusCode >= 300 {
		responseBody, readErr := io.ReadAll(httpRes.Body)
		if readErr != nil {
			return "", fmt.Errorf("read event stream error body: %w", readErr)
		}
		return "", formatAPIError(httpRes.StatusCode, responseBody)
	}

	scanner := bufio.NewScanner(httpRes.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	lines := make([]string, 0, 8)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if len(lines) == 0 {
				continue
			}

			terminalEvent, done, processErr := processSSEBlock(strings.Join(lines, "\n"), out)
			if processErr != nil {
				return "", processErr
			}
			if done {
				return terminalEvent, nil
			}
			lines = lines[:0]
			continue
		}

		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan event stream: %w", err)
	}

	if len(lines) > 0 {
		terminalEvent, done, processErr := processSSEBlock(strings.Join(lines, "\n"), out)
		if processErr != nil {
			return "", processErr
		}
		if done {
			return terminalEvent, nil
		}
	}

	return "", fmt.Errorf("stream ended before terminal event")
}

func processSSEBlock(raw string, out io.Writer) (string, bool, error) {
	envelope, err := parseSSEBlock(raw)
	if err != nil {
		if errors.Is(err, errIgnoredSSEBlock) {
			return "", false, nil
		}
		return "", false, err
	}

	event, err := decodeEvent(envelope)
	if err != nil {
		return "", false, fmt.Errorf("%w: %v", errInvalidSSEData, err)
	}

	encoded, err := json.Marshal(event)
	if err != nil {
		return "", false, fmt.Errorf("encode event output: %w", err)
	}
	if _, err := fmt.Fprintf(out, "%s %s\n", event.Type, string(encoded)); err != nil {
		return "", false, fmt.Errorf("write event output: %w", err)
	}

	if harness.IsTerminalEvent(event.Type) {
		return string(event.Type), true, nil
	}
	return "", false, nil
}

func parseSSEBlock(raw string) (sseEnvelope, error) {
	lines := strings.Split(raw, "\n")
	envelope := sseEnvelope{}
	dataLines := make([]string, 0, len(lines))

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "event:"):
			envelope.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}

	envelope.Data = strings.Join(dataLines, "")
	if envelope.Event == "" && envelope.Data == "" {
		return sseEnvelope{}, errIgnoredSSEBlock
	}
	if envelope.Event == "" || envelope.Data == "" {
		return sseEnvelope{}, fmt.Errorf("invalid sse block")
	}
	return envelope, nil
}

func decodeEvent(envelope sseEnvelope) (harness.Event, error) {
	var event harness.Event
	if err := json.Unmarshal([]byte(envelope.Data), &event); err != nil {
		return harness.Event{}, err
	}
	if event.Type == "" {
		event.Type = harness.EventType(envelope.Event)
	}
	return event, nil
}

func resolveWorkspacePath(workspace string) string {
	workspacePath := strings.TrimSpace(workspace)
	if workspacePath == "" {
		if cwd, err := os.Getwd(); err == nil {
			workspacePath = cwd
		}
	}
	return workspacePath
}

func newTUIConfig(baseURL, workspace string) tui.TUIConfig {
	return tui.TUIConfig{
		BaseURL:      baseURL,
		Workspace:    workspace,
		EnableTUI:    true,
		ColorProfile: "truecolor",
		AltScreen:    true,
	}
}

func runTUI(baseURL, workspace string) error {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("--tui requires a terminal; pipe output or use without --tui for streaming mode")
	}
	tuiCfg := newTUIConfig(baseURL, workspace)
	p := tea.NewProgram(
		tui.New(tuiCfg),
		tea.WithAltScreen(),
	)
	_, err := p.Run()
	return err
}

// profileListResponse is the JSON shape returned by GET /v1/profiles.
type profileListResponse struct {
	Profiles []profileSummary `json:"profiles"`
	Count    int              `json:"count"`
}

// profileSummary is a single entry from the profiles list response.
type profileSummary struct {
	Name             string `json:"name"`
	Description      string `json:"description"`
	Model            string `json:"model"`
	AllowedToolCount int    `json:"allowed_tool_count"`
	SourceTier       string `json:"source_tier"`
}

// listProfilesCmd fetches GET /v1/profiles and prints each profile.
// Returns 0 on success, 1 on error.
func listProfilesCmd(client *http.Client, baseURL string) int {
	url := strings.TrimRight(baseURL, "/") + "/v1/profiles"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli: list-profiles: build request: %v\n", err)
		return 1
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli: list-profiles: request failed: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli: list-profiles: read response: %v\n", err)
		return 1
	}

	if resp.StatusCode >= 300 {
		fmt.Fprintf(stderr, "harnesscli: list-profiles: %v\n", formatAPIError(resp.StatusCode, body))
		return 1
	}

	var plr profileListResponse
	if err := json.Unmarshal(body, &plr); err != nil {
		fmt.Fprintf(stderr, "harnesscli: list-profiles: decode response: %v\n", err)
		return 1
	}

	if len(plr.Profiles) == 0 {
		fmt.Fprintln(stdout, "No profiles available")
		return 0
	}

	// Sort profiles by name for deterministic output.
	profiles := make([]profileSummary, len(plr.Profiles))
	copy(profiles, plr.Profiles)
	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].Name < profiles[j].Name
	})

	for _, p := range profiles {
		desc := p.Description
		if desc == "" {
			desc = "(no description)"
		}
		model := p.Model
		if model == "" {
			model = "(default)"
		}
		fmt.Fprintf(stdout, "Name: %-30s | Description: %-40s | Model: %s\n", p.Name, desc, model)
	}
	return 0
}

func formatAPIError(statusCode int, responseBody []byte) error {
	var payload apiErrorResponse
	if err := json.Unmarshal(responseBody, &payload); err == nil && payload.Error.Message != "" {
		if payload.Error.Code != "" {
			return fmt.Errorf("status %d (%s): %s", statusCode, payload.Error.Code, payload.Error.Message)
		}
		return fmt.Errorf("status %d: %s", statusCode, payload.Error.Message)
	}
	trimmed := strings.TrimSpace(string(responseBody))
	if trimmed == "" {
		trimmed = http.StatusText(statusCode)
	}
	return fmt.Errorf("status %d: %s", statusCode, trimmed)
}
