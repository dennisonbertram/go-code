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
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	harnessconfig "go-agent-harness/cmd/harnesscli/config"
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

	errInvalidSSEData   = errors.New("invalid sse data")
	errIgnoredSSEBlock  = errors.New("ignored sse block")
	errSSELineTruncated = errors.New("sse line truncated: exceeded max size")

	// maxSSELineSize bounds a single SSE line kept in memory. Lines longer
	// than this are fully drained from the stream (to keep it aligned) but
	// only the first maxSSELineSize bytes are retained; the event they
	// belong to is skipped with a warning instead of aborting the stream.
	// It is a var (not a const) so tests can shrink it temporarily.
	maxSSELineSize = 16 * 1024 * 1024

	// maxResponseBodyBytes bounds how much of any single HTTP response body
	// is read into memory. A hostile or misbehaving server cannot use an
	// oversized body to exhaust client memory.
	maxResponseBodyBytes int64 = 8 * 1024 * 1024
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
	PlanMode         bool                     `json:"plan_mode,omitempty"`
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
	planMode := flags.Bool("plan-mode", false, "start the run in enforced read-only plan mode")
	enableTUI := flags.Bool("tui", false, "launch interactive BubbleTea TUI (experimental)")
	resume := flags.String("resume", "", "resume an existing conversation by ID in the TUI (implies --tui)")
	listProfiles := flags.Bool("list-profiles", false, "list available profiles and exit")
	var behaviorFlags csvListFlag
	var talentFlags csvListFlag
	flags.Var(&behaviorFlags, "prompt-behavior", "behavior extension ids (repeatable or comma-separated)")
	flags.Var(&talentFlags, "prompt-talent", "talent extension ids (repeatable or comma-separated)")

	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(stderr, "harnesscli: parse failed: %v\n", err)
		return exitClientError
	}

	if *listProfiles {
		return listProfilesCmd(requestHTTPClient, *baseURL)
	}

	workspacePath := resolveWorkspacePath(*workspace)

	if *enableTUI {
		if err := runTUI(*baseURL, workspacePath, *resume); err != nil {
			fmt.Fprintf(stderr, "harnesscli: tui: %v\n", err)
			return exitClientError
		}
		return exitSuccess
	}

	if strings.TrimSpace(*prompt) == "" {
		fmt.Fprintln(stderr, "harnesscli: prompt is required")
		return exitClientError
	}

	var extensions *runCreatePromptSettings
	if len(behaviorFlags.values) > 0 || len(talentFlags.values) > 0 || strings.TrimSpace(*promptCustom) != "" {
		extensions = &runCreatePromptSettings{
			Behaviors: append([]string(nil), behaviorFlags.values...),
			Talents:   append([]string(nil), talentFlags.values...),
			Custom:    *promptCustom,
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runID, err := startRun(ctx, requestHTTPClient, *baseURL, runCreateRequest{
		Prompt:           *prompt,
		Model:            *model,
		SystemPrompt:     *systemPrompt,
		AgentIntent:      *agentIntent,
		TaskContext:      *taskContext,
		PromptProfile:    *promptProfile,
		PromptExtensions: extensions,
		WorkspacePath:    workspacePath,
		PlanMode:         *planMode,
	})
	if err != nil {
		fmt.Fprintf(stderr, "harnesscli: start run: %v\n", err)
		return exitClientError
	}

	fmt.Fprintf(stdout, "run_id=%s\n", runID)
	terminalEvent, err := streamRunEvents(ctx, streamHTTPClient, *baseURL, runID, stdout)
	if err != nil {
		var blocked *runBlockedError
		if errors.As(err, &blocked) {
			reportRunBlocked(runID, blocked.eventType)
			return exitBlocked
		}
		return handleStreamError(ctx, requestHTTPClient, *baseURL, runID, err)
	}
	fmt.Fprintf(stdout, "terminal_event=%s\n", terminalEvent)
	return exitCodeForTerminalEvent(harness.EventType(terminalEvent))
}

// runBlockedError is returned by the streaming loop when a blocked signal
// (run.waiting_for_user, tool.approval_required, or plan.approval_required)
// arrives while stdin is non-interactive. It is not a stream failure: the
// server-side run is left intact so an operator can resume it later.
type runBlockedError struct {
	eventType harness.EventType
}

func (e *runBlockedError) Error() string {
	return fmt.Sprintf("run blocked: %s", e.eventType)
}

// reportRunBlocked prints the blocked diagnosis for a headless caller: why
// the run stopped, that the server-side run is untouched, and how to resume
// it interactively.
func reportRunBlocked(runID string, eventType harness.EventType) {
	fmt.Fprintf(stderr, "harnesscli: run %s blocked: %s (%s); stdin is non-interactive, exiting; server-side run left intact\n", runID, blockedEventReason(eventType), eventType)
	fmt.Fprintf(stderr, "harnesscli: resume with: harnesscli continue %s <prompt>\n", runID)
}

// handleStreamError decides how to report a streamRunEvents failure. If ctx
// was cancelled (SIGINT/SIGTERM during the run), it best-effort cancels the
// still-executing server-side run so it stops burning tokens, prints a clear
// message, and returns the conventional interrupted exit code. Otherwise it
// reports the streaming error as-is.
func handleStreamError(ctx context.Context, client *http.Client, baseURL, runID string, streamErr error) int {
	if ctx.Err() != nil {
		fmt.Fprintln(stderr, "harnesscli: interrupted, cancelling run...")
		cancelRunOnInterrupt(client, baseURL, runID)
		fmt.Fprintf(stdout, "run %s cancelled\n", runID)
		return exitInterrupted
	}
	fmt.Fprintf(stderr, "harnesscli: stream events: %v\n", streamErr)
	return exitClientError
}

// cancelRunOnInterrupt best-effort POSTs /v1/runs/{id}/cancel using a fresh,
// short-lived context (the caller's own context is already cancelled). Errors
// are intentionally swallowed: this runs during process shutdown and must not
// block or fail the exit path.
func cancelRunOnInterrupt(client *http.Client, baseURL, runID string) {
	cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	endpoint := strings.TrimRight(baseURL, "/") + "/v1/runs/" + url.PathEscape(runID) + "/cancel"
	req, err := http.NewRequestWithContext(cancelCtx, http.MethodPost, endpoint, nil)
	if err != nil {
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBodyBytes))
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

	responseBody, err := io.ReadAll(io.LimitReader(httpRes.Body, maxResponseBodyBytes))
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
		responseBody, readErr := io.ReadAll(io.LimitReader(httpRes.Body, maxResponseBodyBytes))
		if readErr != nil {
			return "", fmt.Errorf("read event stream error body: %w", readErr)
		}
		return "", formatAPIError(httpRes.StatusCode, responseBody)
	}

	reader := bufio.NewReaderSize(httpRes.Body, 64*1024)

	lines := make([]string, 0, 8)
	for {
		line, readErr := readSSELine(reader, maxSSELineSize)
		if readErr != nil && !errors.Is(readErr, errSSELineTruncated) {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return "", fmt.Errorf("scan event stream: %w", readErr)
		}
		if errors.Is(readErr, errSSELineTruncated) {
			fmt.Fprintf(stderr, "harnesscli: warning: SSE event line exceeded %d bytes; event skipped\n", maxSSELineSize)
			lines = lines[:0]
			continue
		}

		line = strings.TrimRight(line, "\r")
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

// readSSELine reads a single line (delimited by '\n') from r, capping the
// returned line at maxLine bytes. A line longer than maxLine is still fully
// consumed from r so the stream stays aligned on line boundaries, but only
// the first maxLine bytes are returned along with errSSELineTruncated so the
// caller can warn and skip the corresponding SSE event instead of aborting
// the whole stream (as bufio.Scanner's hard ErrTooLong would).
func readSSELine(r *bufio.Reader, maxLine int) (string, error) {
	var buf []byte
	truncated := false
	for {
		chunk, err := r.ReadSlice('\n')
		if len(chunk) > 0 {
			if !truncated {
				if len(buf)+len(chunk) > maxLine {
					if room := maxLine - len(buf); room > 0 {
						buf = append(buf, chunk[:room]...)
					}
					truncated = true
				} else {
					buf = append(buf, chunk...)
				}
			}
		}
		switch {
		case err == nil:
			// Found the delimiter.
			line := strings.TrimSuffix(string(buf), "\n")
			if truncated {
				return line, errSSELineTruncated
			}
			return line, nil
		case errors.Is(err, bufio.ErrBufferFull):
			// No delimiter yet within the read buffer; keep reading.
			continue
		case errors.Is(err, io.EOF):
			if len(buf) == 0 {
				return "", io.EOF
			}
			if truncated {
				return string(buf), errSSELineTruncated
			}
			return string(buf), nil
		default:
			return "", err
		}
	}
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
	// A blocked signal with non-interactive stdin aborts the stream: the
	// run is waiting on input a headless caller will never provide, so
	// streaming further would block forever. With an interactive stdin the
	// stream stays open (unchanged behavior); interactive answer wiring is
	// the ask-user epic's scope.
	if isBlockedEvent(event.Type) && !stdinIsTerminal() {
		return "", false, &runBlockedError{eventType: event.Type}
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

func newTUIConfig(baseURL, workspace, resumeConversationID string) tui.TUIConfig {
	// Default to auto-detection; HARNESS_COLOR_PROFILE overrides (truecolor, 256,
	// ansi, none). Resolved and applied in runTUI before the program starts.
	colorProfile := strings.TrimSpace(os.Getenv("HARNESS_COLOR_PROFILE"))
	if colorProfile == "" {
		colorProfile = "auto"
	}
	// Reuse the exact same harnessd auth key the rest of the CLI already
	// uses (see auth.go's newAuthedRequest / loadConfig, populated by
	// "harnesscli auth login"), so the TUI's requests — including the SSE
	// event stream — authenticate the same way as every other harnesscli
	// command. A missing or unreadable config file just means "no key",
	// preserving today's unauthenticated-local default.
	var apiKey string
	if cfg, err := loadConfig(); err == nil && cfg != nil {
		apiKey = cfg.APIKey
	}
	// Load the persisted theme selection (epic #810 slice 4); the TUI model
	// resolves it through the theme loader and falls back to the default
	// theme on any error. Missing/unreadable config just means "no theme".
	var themeName string
	if hcfg, err := harnessconfig.Load(); err == nil && hcfg != nil {
		themeName = hcfg.Theme
	}
	return tui.TUIConfig{
		BaseURL:              baseURL,
		Workspace:            workspace,
		EnableTUI:            true,
		ColorProfile:         colorProfile,
		AltScreen:            true,
		ResumeConversationID: resumeConversationID,
		APIKey:               apiKey,
		Theme:                themeName,
	}
}

func runTUI(baseURL, workspace, resumeConversationID string) error {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("--tui requires a terminal; pipe output or use without --tui for streaming mode")
	}
	tuiCfg := newTUIConfig(baseURL, workspace, resumeConversationID)
	// Resolve and apply the color profile to the renderer before building the
	// model, and store the effective profile back for accurate display.
	tuiCfg.ColorProfile = tui.ApplyColorProfile(tuiCfg.ColorProfile)
	p := tea.NewProgram(
		tui.New(tuiCfg),
		tea.WithAltScreen(),
	)
	finalModel, err := p.Run()
	if err != nil {
		return err
	}
	if m, ok := finalModel.(tui.Model); ok {
		if convID := m.ConversationID(); convID != "" {
			fmt.Fprintln(stdout)
			fmt.Fprintln(stdout, "Resume this conversation:")
			fmt.Fprintf(stdout, "  go-code --resume %s\n", convID)
		}
	}
	return nil
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
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
