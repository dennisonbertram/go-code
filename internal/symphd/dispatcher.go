package symphd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go-agent-harness/internal/workspace"
)

// HarnessClient is the interface for interacting with a harnessd instance.
// Implementations may use HTTP or be mocked for testing.
type HarnessClient interface {
	// StartRun posts a prompt to harnessd and returns a run ID.
	StartRun(ctx context.Context, prompt string, workspacePath string) (string, error)
	// RunStatus returns the current status of a run: "running", "completed", "failed", or "queued".
	RunStatus(ctx context.Context, runID string) (string, error)
}

// WorkspaceFactory creates a new, unprovisioned Workspace for each dispatch.
type WorkspaceFactory func() workspace.Workspace

// HarnessClientFactory creates a HarnessClient pointed at the given URL.
type HarnessClientFactory func(harnessURL string) HarnessClient

// DispatchConfig holds dispatcher settings.
type DispatchConfig struct {
	// MaxConcurrent is the maximum number of parallel agent runs.
	MaxConcurrent int
	// StallTimeout is the time with no status change before a run is declared stalled.
	// Defaults to 5 minutes if zero.
	StallTimeout time.Duration
	// HarnessURL is the base URL of the harnessd instance.
	// Used only for "local" and "worktree" workspace types where the URL is static.
	// Ignored for "container" and "vm" types — URL is derived from the provisioned workspace.
	HarnessURL string
	// PollInterval controls how often RunStatus is polled.
	// Defaults to 5 seconds if zero.
	PollInterval time.Duration
	// BaseDir is the base directory passed to workspace.Options when provisioning.
	BaseDir string
	// SubagentConfigTOML is an optional TOML config string written to harness.toml
	// in each provisioned workspace. It propagates non-secret RunnerConfig settings
	// (feature flags, model, cost ceiling) to subagent harness instances.
	// NEVER include secrets (API keys) in this field — use SubagentEnv instead.
	SubagentConfigTOML string
	// SubagentEnv holds environment variables to inject into each workspace.
	// Use this field for secrets (e.g. OPENAI_API_KEY) that must not be written
	// to disk. For container workspaces these are passed directly to the container env.
	SubagentEnv map[string]string
}

// RunResult holds the outcome of a dispatched run.
type RunResult struct {
	IssueNumber int
	Success     bool
	Error       error
	Duration    time.Duration
}

// Dispatcher orchestrates workspace provisioning and harness dispatch.
// It claims issues from the tracker, provisions a workspace per issue, starts
// a harness run, monitors progress, detects stalls, and marks issues complete
// or failed.
type Dispatcher struct {
	config           DispatchConfig
	workspaceFactory WorkspaceFactory
	tracker          Tracker
	clientFactory    HarnessClientFactory

	sem     chan struct{} // semaphore limiting MaxConcurrent concurrent runs
	results chan RunResult

	mu      sync.Mutex
	running map[int]context.CancelFunc // issue number → cancel func
}

// NewDispatcher creates a new Dispatcher with injectable workspace and client factories.
func NewDispatcher(cfg DispatchConfig, wsFactory WorkspaceFactory, tracker Tracker, clientFactory HarnessClientFactory) *Dispatcher {
	if cfg.StallTimeout <= 0 {
		cfg.StallTimeout = 5 * time.Minute
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second
	}
	maxConcurrent := cfg.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	return &Dispatcher{
		config:           cfg,
		workspaceFactory: wsFactory,
		tracker:          tracker,
		clientFactory:    clientFactory,
		sem:              make(chan struct{}, maxConcurrent),
		results:          make(chan RunResult, 64),
		running:          make(map[int]context.CancelFunc),
	}
}

// NewDispatcherSimple creates a Dispatcher using the default HTTPHarnessClient factory.
// Use this when you don't need to inject a custom harness client.
func NewDispatcherSimple(cfg DispatchConfig, wsFactory WorkspaceFactory, tracker Tracker) *Dispatcher {
	return NewDispatcher(cfg, wsFactory, tracker, func(url string) HarnessClient {
		return NewHTTPHarnessClient(url)
	})
}

// Results returns the channel on which completed RunResults are published.
// The caller should drain this channel to avoid blocking dispatched goroutines.
func (d *Dispatcher) Results() <-chan RunResult {
	return d.results
}

// Dispatch provisions a workspace and starts a harness run for the given issue.
// It calls tracker.Start() immediately, then workspace.Provision(), then
// client.StartRun(). Progress is polled at PollInterval; if StallTimeout elapses
// with no terminal status, the run is cancelled and marked failed.
// On completion, tracker.Complete() or tracker.Fail() is called and a RunResult
// is sent to the Results channel.
//
// Dispatch acquires a semaphore slot before launching the goroutine, so callers
// can call Dispatch sequentially and rely on backpressure from MaxConcurrent.
func (d *Dispatcher) Dispatch(ctx context.Context, issue *TrackedIssue) error {
	// Transition tracker state: Claimed → Running.
	if err := d.tracker.Start(issue.Number); err != nil {
		return fmt.Errorf("dispatcher: start issue #%d: %w", issue.Number, err)
	}

	// Acquire a semaphore slot (blocks until a slot is free or ctx is done).
	select {
	case d.sem <- struct{}{}:
	case <-ctx.Done():
		_ = d.tracker.Fail(issue.Number, "context cancelled before semaphore acquired")
		return ctx.Err()
	}

	// Create a per-run cancellable context derived from the parent.
	runCtx, cancel := context.WithCancel(ctx)

	d.mu.Lock()
	d.running[issue.Number] = cancel
	d.mu.Unlock()

	go func() {
		defer func() {
			cancel()
			<-d.sem // release slot
			d.mu.Lock()
			delete(d.running, issue.Number)
			d.mu.Unlock()
		}()

		start := time.Now()
		result := d.runIssue(runCtx, issue)
		result.Duration = time.Since(start)
		d.results <- result
	}()

	return nil
}

// Shutdown cancels all in-flight dispatches and waits for them to drain.
func (d *Dispatcher) Shutdown(ctx context.Context) {
	d.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(d.running))
	for _, cancel := range d.running {
		cancels = append(cancels, cancel)
	}
	d.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}

	// Drain the semaphore: wait until all slots are free.
	// Each running goroutine releases a slot when it exits.
	acquired := 0
	for acquired < cap(d.sem) {
		select {
		case d.sem <- struct{}{}:
			acquired++
		case <-ctx.Done():
			for acquired > 0 {
				<-d.sem
				acquired--
			}
			return
		}
	}
	defer func() {
		for acquired > 0 {
			<-d.sem
			acquired--
		}
	}()

	// Semaphore drain alone is not sufficient: goroutine cleanup can release the
	// slot slightly before it removes the bookkeeping entry from d.running.
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		d.mu.Lock()
		runningCount := len(d.running)
		d.mu.Unlock()
		if runningCount == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// runIssue is the core per-issue dispatch logic executed in a goroutine.
func (d *Dispatcher) runIssue(ctx context.Context, issue *TrackedIssue) RunResult {
	result := RunResult{IssueNumber: issue.Number}

	// Create a fresh workspace for this issue.
	ws := d.workspaceFactory()

	// Build env map: merge SubagentEnv into a fresh map so each dispatch
	// gets its own copy and callers cannot mutate the shared SubagentEnv.
	var env map[string]string
	if len(d.config.SubagentEnv) > 0 {
		env = make(map[string]string, len(d.config.SubagentEnv))
		for k, v := range d.config.SubagentEnv {
			env[k] = v
		}
	}

	opts := workspace.Options{
		ID:         fmt.Sprintf("issue-%d", issue.Number),
		BaseDir:    d.config.BaseDir,
		ConfigTOML: d.config.SubagentConfigTOML,
		Env:        env,
	}
	if err := ws.Provision(ctx, opts); err != nil {
		reason := fmt.Sprintf("workspace provision failed: %v", err)
		_ = d.tracker.Fail(issue.Number, reason)
		result.Error = fmt.Errorf("dispatcher: %s", reason)
		return result
	}

	defer func() {
		destroyCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = ws.Destroy(destroyCtx)
	}()

	harnessURL := ws.HarnessURL()
	workspacePath := ws.WorkspacePath()

	// Create a per-dispatch client pointed at the workspace's harness URL.
	client := d.clientFactory(harnessURL)

	// Wait for harnessd to become ready.
	if err := waitForHarnessReady(ctx, harnessURL, 60*time.Second); err != nil {
		reason := fmt.Sprintf("harness not ready: %v", err)
		_ = d.tracker.Fail(issue.Number, reason)
		result.Error = fmt.Errorf("dispatcher: %s", reason)
		return result
	}

	// Build prompt from issue content.
	prompt := buildPrompt(issue)

	// Start run on harnessd.
	runID, err := client.StartRun(ctx, prompt, workspacePath)
	if err != nil {
		reason := fmt.Sprintf("harness start failed: %v", err)
		_ = d.tracker.Fail(issue.Number, reason)
		result.Error = fmt.Errorf("dispatcher: %s", reason)
		return result
	}

	// Poll for completion with stall detection.
	// The stall deadline is set once at dispatch time and NOT reset while the
	// run keeps returning "running" or "queued". A constant non-terminal status
	// IS the stall condition — the deadline only resets on a genuine status
	// transition (e.g. queued → running), not on repeated identical statuses.
	ticker := time.NewTicker(d.config.PollInterval)
	defer ticker.Stop()

	stallTimer := time.NewTimer(d.config.StallTimeout)
	defer stallTimer.Stop()

	lastStatus := ""

	for {
		select {
		case <-ctx.Done():
			reason := "context cancelled"
			_ = d.tracker.Fail(issue.Number, reason)
			result.Error = fmt.Errorf("dispatcher: %s", reason)
			return result

		case <-stallTimer.C:
			reason := fmt.Sprintf("stall timeout (%v) exceeded for run %s", d.config.StallTimeout, runID)
			_ = d.tracker.Fail(issue.Number, reason)
			result.Error = fmt.Errorf("dispatcher: %s", reason)
			return result

		case <-ticker.C:
			status, err := client.RunStatus(ctx, runID)
			if err != nil {
				// Transient error — keep polling.
				continue
			}

			// Reset the stall timer when we see a genuine status transition.
			if status != lastStatus {
				lastStatus = status
				if !stallTimer.Stop() {
					select {
					case <-stallTimer.C:
					default:
					}
				}
				stallTimer.Reset(d.config.StallTimeout)
			}

			switch status {
			case "completed":
				if err := d.tracker.Complete(issue.Number); err != nil {
					result.Error = fmt.Errorf("dispatcher: complete tracker: %w", err)
					return result
				}
				result.Success = true
				return result

			case "failed":
				reason := fmt.Sprintf("harness run %s reported failed", runID)
				_ = d.tracker.Fail(issue.Number, reason)
				result.Error = fmt.Errorf("dispatcher: %s", reason)
				return result

			case "running", "queued":
				// Still in progress — stall timer handles timeout.

			default:
				// Unknown status — keep polling until stall timeout.
			}
		}
	}
}

// waitForHarnessReady polls the harness /healthz endpoint until it returns 200
// or the timeout is reached.
func waitForHarnessReady(ctx context.Context, harnessURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 3 * time.Second}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, harnessURL+"/healthz", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("harness at %s not ready after %s", harnessURL, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// synthesisDoctrine is prepended to every dispatch prompt to prevent the
// coordinator agent from producing ungrounded synthesis. It requires concrete
// file paths, line numbers, and cited evidence in every delegation and finding.
const synthesisDoctrine = `COORDINATOR SYNTHESIS DOCTRINE

You are a coordinator agent. When you delegate research or report findings to
workers, you MUST follow these rules:

1. NEVER write "based on your findings" or "according to my research" without
   citing exact file paths and line numbers.
2. Worker delegation prompts MUST include the specific files and line ranges
   to investigate (e.g., "inspect /path/to/file.go:42-58 for the retry logic").
3. Before dispatching a subagent, confirm your research is grounded in
   concrete code locations — do not rely on memory or assumptions.

WORKED EXAMPLES — WRONG vs RIGHT delegation patterns:

WRONG: "Based on my findings, the bug is in the retry logic."
RIGHT: "The regression originates in internal/harness/runner.go:4304 where
       RetryWithBackoff is called without a context deadline."

WRONG: "Please investigate the authentication flow for potential issues."
RIGHT: "Inspect internal/auth/handler.go:89-120 and internal/auth/middleware.go:34-67
       — check whether the token validation is bypassed when ctx is cancelled."

WRONG: "According to my research, the config loading has a race condition."
RIGHT: "The race condition is at internal/config/loader.go:156 — the mutex is
       acquired after the map read at line 152, not before."

WRONG: "The error handling seems incomplete — please review and fix."
RIGHT: "At internal/server/handler.go:203-218, errors from doWork() are logged
       but not returned to the caller. Verify the caller at cmd/main.go:88
       handles nil responses correctly."

SYNTHESIS VERIFICATION — Before reporting any conclusion, confirm:
- Every claim references at least one concrete file path and line number.
- If you cannot find the exact location, state "I could not locate this in the
  codebase" rather than fabricating a citation.
- Worker prompts always include the file paths to start from.

`

// buildPrompt constructs the agent prompt for an issue.
func buildPrompt(issue *TrackedIssue) string {
	return fmt.Sprintf("%sImplement GitHub issue #%d: %s\n\n%s",
		synthesisDoctrine, issue.Number, issue.Title, issue.Body)
}

// HTTPHarnessClient implements HarnessClient using real HTTP calls to harnessd.
type HTTPHarnessClient struct {
	baseURL string
	client  *http.Client
}

// NewHTTPHarnessClient creates an HTTPHarnessClient pointing at the given base URL.
func NewHTTPHarnessClient(baseURL string) *HTTPHarnessClient {
	return &HTTPHarnessClient{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// startRunRequest is the JSON body for POST /v1/runs.
type startRunRequest struct {
	Prompt    string `json:"prompt"`
	Workspace string `json:"workspace,omitempty"`
}

// startRunResponse is the JSON body returned by POST /v1/runs.
type startRunResponse struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
}

// runStatusResponse is the JSON body returned by GET /v1/runs/{id}.
type runStatusResponse struct {
	Status string `json:"status"`
}

// StartRun posts a new run to POST /v1/runs and returns the run ID.
func (c *HTTPHarnessClient) StartRun(ctx context.Context, prompt string, workspacePath string) (string, error) {
	body := startRunRequest{Prompt: prompt, Workspace: workspacePath}
	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("harness client: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/runs", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("harness client: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("harness client: post /v1/runs: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("harness client: /v1/runs returned status %d", resp.StatusCode)
	}

	var out startRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("harness client: decode response: %w", err)
	}
	if out.RunID == "" {
		return "", fmt.Errorf("harness client: empty run_id in response")
	}
	return out.RunID, nil
}

// RunStatus queries GET /v1/runs/{id} and returns the status string.
func (c *HTTPHarnessClient) RunStatus(ctx context.Context, runID string) (string, error) {
	url := fmt.Sprintf("%s/v1/runs/%s", c.baseURL, runID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("harness client: build request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("harness client: get /v1/runs/%s: %w", runID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("harness client: /v1/runs/%s returned status %d", runID, resp.StatusCode)
	}

	var out runStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("harness client: decode response: %w", err)
	}
	return out.Status, nil
}
