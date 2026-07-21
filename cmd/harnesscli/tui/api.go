package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"go-agent-harness/cmd/harnesscli/tui/components/modelswitcher"
)

// newHarnessRequest builds an HTTP request targeting the harnessd server and,
// if apiKey is non-empty, attaches it as "Authorization: Bearer <apiKey>".
// EVERY request that targets harnessd (TUIConfig.BaseURL) MUST be built
// through this helper (or StartSSEBridgeWithOptions's SSEBridgeOptions.APIKey
// for the SSE stream) — that is what prevents a future endpoint from being
// added without authentication. Do NOT use this for requests to external
// services (e.g. fetchOpenRouterModelsFromURL's call to openrouter.ai),
// which must keep sending their own provider-specific credentials, never the
// harnessd key — conflating the two would leak the harnessd key to a third
// party.
//
// ctx is typically context.Background(): none of the tea.Cmd closures in
// this file currently carry a request-scoped context (that would require
// threading context.Context through every model.go call site, which is a
// separate, more invasive change) — see the package-level note in
// api_auth_test.go for the full audit of what is and isn't context-aware.
func newHarnessRequest(ctx context.Context, method, url string, body io.Reader, apiKey string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	return req, nil
}

// runAttachment is the wire shape for a typed non-text content block sent
// with a run (epic #818). It mirrors harness.ContentBlock's JSON; the TUI
// package deliberately does not import internal/harness.
type runAttachment struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type runCreateRequest struct {
	Prompt          string `json:"prompt"`
	ConversationID  string `json:"conversation_id,omitempty"`
	Model           string `json:"model,omitempty"`
	ProviderName    string `json:"provider_name,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// Attachments carries base64-encoded image blocks gathered from the input
	// area's attachment chips (epic #818 slice 3). Nil for text-only prompts.
	Attachments []runAttachment `json:"attachments,omitempty"`
	// ProfileName carries the capability profile selected via /profiles. It maps
	// to harness.RunRequest.ProfileName (JSON "profile"), which applies tool
	// restrictions, approval policy, and workspace isolation. This is distinct
	// from "prompt_profile" (prompt/model routing); sending a capability profile
	// name in prompt_profile makes the server reject the run with HTTP 400.
	ProfileName   string `json:"profile,omitempty"`
	WorkspacePath string `json:"workspace_path,omitempty"`
	// ExtraDirs carries session directories added via /add-dir; they map to
	// harness.RunRequest.ExtraDirs so file-tool confinement grants them in
	// addition to the workspace root.
	ExtraDirs []string `json:"extra_dirs,omitempty"`
	// AllowFallback lets the server degrade to its default provider when the
	// requested model's provider can't be resolved, instead of hard-failing
	// the run. Always true from the TUI.
	AllowFallback bool `json:"allow_fallback,omitempty"`
	PlanMode      bool `json:"plan_mode,omitempty"`
}

type runCreateResponse struct {
	RunID string `json:"run_id"`
}

type runContinueResponse struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
}

type tuiRunRecord struct {
	ID             string `json:"id"`
	RunID          string `json:"run_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	Model          string `json:"model,omitempty"`
	Prompt         string `json:"prompt,omitempty"`
	Output         string `json:"output,omitempty"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
}

func (r tuiRunRecord) displayID() string {
	if r.ID != "" {
		return r.ID
	}
	return r.RunID
}

type RemoteSubagent struct {
	ID               string `json:"id"`
	RunID            string `json:"run_id"`
	Status           string `json:"status"`
	Isolation        string `json:"isolation"`
	CleanupPolicy    string `json:"cleanup_policy"`
	WorkspacePath    string `json:"workspace_path,omitempty"`
	WorkspaceCleaned bool   `json:"workspace_cleaned"`
	BranchName       string `json:"branch_name,omitempty"`
	BaseRef          string `json:"base_ref,omitempty"`
	Output           string `json:"output,omitempty"`
	Error            string `json:"error,omitempty"`
}

// RemoteTask is one entry of the GET /v1/tasks union (epic #814): a single
// piece of background work — bash job, subagent, cron job, or delayed
// callback — as rendered by the /tasks overlay.
type RemoteTask struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Status     string    `json:"status"`
	Label      string    `json:"label"`
	StartedAt  time.Time `json:"started_at"`
	AgeSeconds int64     `json:"age_seconds"`
	Actions    []string  `json:"actions"`
}

// startRunCmd returns a tea.Cmd that POSTs a run to the harness and emits
// RunStartedMsg on success or RunFailedMsg on error.
// conversationID may be empty for the first message in a new conversation;
// subsequent messages should pass the run ID returned by the first run so that
// the harness groups them under the same conversation.
// profile is the name of the capability profile to use (may be empty); it is
// sent as the "profile" field so the server applies the profile's tool
// restrictions and isolation.
// attachments carries the base64-encoded image blocks from the input area's
// chips (nil for text-only prompts).
// extraDirs lists additional directory roots the run may read/work in (see
// /add-dir); nil or empty omits the field.
func startRunCmd(baseURL, prompt, conversationID, model, provider, reasoningEffort, profile, workspace, apiKey string, attachments []runAttachment, extraDirs []string, planMode ...bool) tea.Cmd {
	enabled := len(planMode) > 0 && planMode[0]
	return func() tea.Msg {
		body, _ := json.Marshal(runCreateRequest{
			Prompt:          prompt,
			ConversationID:  conversationID,
			Model:           model,
			ProviderName:    provider,
			ReasoningEffort: reasoningEffort,
			Attachments:     attachments,
			ProfileName:     profile,
			WorkspacePath:   workspace,
			ExtraDirs:       extraDirs,
			AllowFallback:   true,
			PlanMode:        enabled,
		})
		url := strings.TrimRight(baseURL, "/") + "/v1/runs"
		req, err := newHarnessRequest(context.Background(), http.MethodPost, url, bytes.NewReader(body), apiKey)
		if err != nil {
			return RunFailedMsg{Error: "start run: build request: " + err.Error()}
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return RunFailedMsg{Error: err.Error()}
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			// Surface the server's error body so actionable rejections (e.g.
			// the slice-3 modality gate's 400) reach the user.
			errBody, _ := io.ReadAll(resp.Body)
			return RunFailedMsg{Error: fmt.Sprintf("start run: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))}
		}
		var created runCreateResponse
		if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
			return RunFailedMsg{Error: fmt.Sprintf("decode run response: %s", err.Error())}
		}
		return RunStartedMsg{RunID: created.RunID}
	}
}

func fetchRunsCmd(baseURL, apiKey string) tea.Cmd {
	return func() tea.Msg {
		req, err := newHarnessRequest(context.Background(), http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/runs", nil, apiKey)
		if err != nil {
			return RunsFetchedMsg{Err: "build request: " + err.Error()}
		}
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return RunsFetchedMsg{Err: "request failed: " + err.Error()}
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return RunsFetchedMsg{Err: "read response: " + err.Error()}
		}
		if resp.StatusCode >= 300 {
			return RunsFetchedMsg{Err: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}
		}
		var payload struct {
			Runs []tuiRunRecord `json:"runs"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return RunsFetchedMsg{Err: "decode response: " + err.Error()}
		}
		return RunsFetchedMsg{Runs: payload.Runs}
	}
}

func cancelRunCmd(baseURL, runID, apiKey string) tea.Cmd {
	return func() tea.Msg {
		endpoint := strings.TrimRight(baseURL, "/") + "/v1/runs/" + url.PathEscape(runID) + "/cancel"
		req, err := newHarnessRequest(context.Background(), http.MethodPost, endpoint, nil, apiKey)
		if err != nil {
			return RunControlResultMsg{Kind: "cancel", RunID: runID, Err: "build request: " + err.Error()}
		}
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return RunControlResultMsg{Kind: "cancel", RunID: runID, Err: "request failed: " + err.Error()}
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return RunControlResultMsg{Kind: "cancel", RunID: runID, Err: "read response: " + err.Error()}
		}
		if resp.StatusCode >= 300 {
			return RunControlResultMsg{Kind: "cancel", RunID: runID, Err: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}
		}
		return RunControlResultMsg{Kind: "cancel", RunID: runID}
	}
}

// compactRunCmd POSTs {"mode":"hybrid","instruction":...} to
// /v1/runs/{id}/compact for the active run (server: internal/server/http_runs.go
// handleRunCompact). The instruction is an optional free-text preserve-hint
// for the summarizer (epic #817); mode is always hybrid — strip remains
// available via the raw API only. The response carries the resolved mode, the
// number of messages removed, and (for hybrid) the compaction summary.
func compactRunCmd(baseURL, runID, instruction, apiKey string) tea.Cmd {
	return func() tea.Msg {
		body, err := json.Marshal(map[string]string{"mode": "hybrid", "instruction": instruction})
		if err != nil {
			return CompactResultMsg{RunID: runID, Err: "encode request: " + err.Error()}
		}
		endpoint := strings.TrimRight(baseURL, "/") + "/v1/runs/" + url.PathEscape(runID) + "/compact"
		req, err := newHarnessRequest(context.Background(), http.MethodPost, endpoint, bytes.NewReader(body), apiKey)
		if err != nil {
			return CompactResultMsg{RunID: runID, Err: "build request: " + err.Error()}
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return CompactResultMsg{RunID: runID, Err: "request failed: " + err.Error()}
		}
		defer resp.Body.Close()
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return CompactResultMsg{RunID: runID, Err: "read response: " + err.Error()}
		}
		if resp.StatusCode >= 300 {
			return CompactResultMsg{RunID: runID, Err: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))}
		}
		var payload struct {
			OK              bool   `json:"ok"`
			MessagesRemoved int    `json:"messages_removed"`
			Mode            string `json:"mode"`
			Summary         string `json:"summary"`
		}
		if err := json.Unmarshal(respBody, &payload); err != nil {
			return CompactResultMsg{RunID: runID, Err: "decode response: " + err.Error()}
		}
		return CompactResultMsg{
			RunID:           runID,
			Mode:            payload.Mode,
			Summary:         payload.Summary,
			MessagesRemoved: payload.MessagesRemoved,
		}
	}
}

// steerRunCmd POSTs a steering message to /v1/runs/{id}/steer for the active
// run (server: internal/server/http_runs.go handleRunSteer). The server queues
// the message and the harness injects it as a user message at the next step
// boundary — the run is neither cancelled nor restarted. Empty/whitespace
// prompts are rejected client-side (SteerErrorMsg Kind "invalid_prompt")
// without issuing a request; 202 maps to SteerAcceptedMsg and the documented
// failure statuses map to SteerErrorMsg kinds (see messages.go).
func steerRunCmd(baseURL, runID, prompt, apiKey string) tea.Cmd {
	return func() tea.Msg {
		if strings.TrimSpace(prompt) == "" {
			return SteerErrorMsg{RunID: runID, Kind: "invalid_prompt", Err: "prompt is required"}
		}
		body, err := json.Marshal(map[string]string{"prompt": prompt})
		if err != nil {
			return SteerErrorMsg{RunID: runID, Kind: "transport", Err: "encode request: " + err.Error()}
		}
		endpoint := strings.TrimRight(baseURL, "/") + "/v1/runs/" + url.PathEscape(runID) + "/steer"
		req, err := newHarnessRequest(context.Background(), http.MethodPost, endpoint, bytes.NewReader(body), apiKey)
		if err != nil {
			return SteerErrorMsg{RunID: runID, Kind: "transport", Err: "build request: " + err.Error()}
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return SteerErrorMsg{RunID: runID, Kind: "transport", Err: "request failed: " + err.Error()}
		}
		defer resp.Body.Close()
		responseBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return SteerErrorMsg{RunID: runID, Kind: "transport", Err: "read response: " + err.Error()}
		}
		if resp.StatusCode >= 300 {
			kind := "http"
			switch resp.StatusCode {
			case http.StatusNotFound:
				kind = "not_found"
			case http.StatusConflict:
				kind = "run_not_active"
			case http.StatusTooManyRequests:
				kind = "steering_buffer_full"
			}
			return SteerErrorMsg{RunID: runID, Kind: kind, Err: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))}
		}
		return SteerAcceptedMsg{RunID: runID}
	}
}

func replayRunCmd(baseURL, target, apiKey string) tea.Cmd {
	return func() tea.Msg {
		body, err := json.Marshal(map[string]any{
			"rollout_path": target,
			"mode":         "simulate",
		})
		if err != nil {
			return RunControlResultMsg{Kind: "replay", RunID: target, Err: "encode request: " + err.Error()}
		}
		endpoint := strings.TrimRight(baseURL, "/") + "/v1/runs/replay"
		req, err := newHarnessRequest(context.Background(), http.MethodPost, endpoint, bytes.NewReader(body), apiKey)
		if err != nil {
			return RunControlResultMsg{Kind: "replay", RunID: target, Err: "build request: " + err.Error()}
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return RunControlResultMsg{Kind: "replay", RunID: target, Err: "request failed: " + err.Error()}
		}
		defer resp.Body.Close()
		responseBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return RunControlResultMsg{Kind: "replay", RunID: target, Err: "read response: " + err.Error()}
		}
		if resp.StatusCode >= 300 {
			return RunControlResultMsg{Kind: "replay", RunID: target, Err: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))}
		}
		var pretty bytes.Buffer
		output := strings.TrimSpace(string(responseBody))
		if err := json.Indent(&pretty, responseBody, "", "  "); err == nil {
			output = strings.TrimSpace(pretty.String())
		}
		return RunControlResultMsg{Kind: "replay", RunID: target, Output: output}
	}
}

func continueRunCmd(baseURL, runID, prompt, apiKey string) tea.Cmd {
	return func() tea.Msg {
		body, err := json.Marshal(map[string]string{"prompt": prompt})
		if err != nil {
			return RunFailedMsg{RunID: runID, Error: "continue: encode request: " + err.Error()}
		}
		endpoint := strings.TrimRight(baseURL, "/") + "/v1/runs/" + url.PathEscape(runID) + "/continue"
		req, err := newHarnessRequest(context.Background(), http.MethodPost, endpoint, bytes.NewReader(body), apiKey)
		if err != nil {
			return RunFailedMsg{RunID: runID, Error: "continue: build request: " + err.Error()}
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return RunFailedMsg{RunID: runID, Error: "continue: request failed: " + err.Error()}
		}
		defer resp.Body.Close()
		responseBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return RunFailedMsg{RunID: runID, Error: "continue: read response: " + err.Error()}
		}
		if resp.StatusCode >= 300 {
			return RunFailedMsg{RunID: runID, Error: fmt.Sprintf("continue: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))}
		}
		var created runContinueResponse
		if err := json.Unmarshal(responseBody, &created); err != nil {
			return RunFailedMsg{RunID: runID, Error: "continue: decode response: " + err.Error()}
		}
		if created.RunID == "" {
			return RunFailedMsg{RunID: runID, Error: "continue: response missing run_id"}
		}
		return RunStartedMsg{RunID: created.RunID}
	}
}

// modelsResponse matches the JSON body returned by GET /v1/models.
type modelsResponse struct {
	Models []modelswitcher.ServerModelEntry `json:"models"`
}

// fetchModelsCmd fetches the model list from the server's /v1/models endpoint.
// On success it emits ModelsFetchedMsg; on failure it emits ModelsFetchErrorMsg.
func fetchModelsCmd(baseURL, apiKey string) tea.Cmd {
	return func() tea.Msg {
		url := strings.TrimRight(baseURL, "/") + "/v1/models"
		req, err := newHarnessRequest(context.Background(), http.MethodGet, url, nil, apiKey)
		if err != nil {
			return ModelsFetchErrorMsg{Err: err.Error()}
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return ModelsFetchErrorMsg{Err: err.Error()}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return ModelsFetchErrorMsg{Err: fmt.Sprintf("server returned %d", resp.StatusCode)}
		}
		var mr modelsResponse
		if err := json.NewDecoder(resp.Body).Decode(&mr); err != nil {
			return ModelsFetchErrorMsg{Err: err.Error()}
		}
		return ModelsFetchedMsg{Models: mr.Models}
	}
}

// fetchOpenRouterModelsCmd fetches the live model catalog from the public OpenRouter API.
// This is called when the user has OpenRouter selected as their gateway.
// Requires no authentication — the OpenRouter /models endpoint is public.
// If apiKey is non-empty, the Authorization header is included for higher rate limits.
//
// IMPORTANT: apiKey here is the OpenRouter PROVIDER key (from
// m.pendingAPIKeys["openrouter"]), never the harnessd auth key
// (TUIConfig.APIKey). This call targets the external openrouter.ai API, not
// harnessd — do NOT route it through newHarnessRequest or pass
// TUIConfig.APIKey to it; that would leak the harnessd credential to a third
// party.
func fetchOpenRouterModelsCmd(apiKey string) tea.Cmd {
	return fetchOpenRouterModelsFromURL("https://openrouter.ai/api/v1/models", apiKey)
}

// fetchOpenRouterModelsFromURL fetches OpenRouter models from the given URL.
// Extracted from fetchOpenRouterModelsCmd to allow tests to inject a custom server URL.
func fetchOpenRouterModelsFromURL(url, apiKey string) tea.Cmd {
	return func() tea.Msg {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return ModelsFetchErrorMsg{Err: "openrouter request: " + err.Error()}
		}
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return ModelsFetchErrorMsg{Err: "openrouter fetch: " + err.Error()}
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return ModelsFetchErrorMsg{Err: fmt.Sprintf("openrouter: status %d", resp.StatusCode)}
		}

		var orResp struct {
			Data []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&orResp); err != nil {
			return ModelsFetchErrorMsg{Err: "openrouter decode: " + err.Error()}
		}

		models := make([]modelswitcher.ServerModelEntry, 0, len(orResp.Data))
		for _, entry := range orResp.Data {
			// OpenRouter IDs look like "openai/gpt-4.1" or "anthropic/claude-opus-4-6".
			// Extract the native provider from the prefix.
			provider := "openrouter"
			if idx := strings.Index(entry.ID, "/"); idx > 0 {
				provider = entry.ID[:idx]
			}
			// Use the OpenRouter-supplied name; fall back to the raw ID.
			displayName := entry.Name
			if displayName == "" {
				displayName = entry.ID
			}
			models = append(models, modelswitcher.ServerModelEntry{
				ID:          entry.ID,
				Provider:    provider,
				DisplayName: displayName,
			})
		}

		return ModelsFetchedMsg{Models: models, Source: "openrouter"}
	}
}

func loadSubagentsCmd(baseURL, apiKey string) tea.Cmd {
	return func() tea.Msg {
		url := strings.TrimRight(baseURL, "/") + "/v1/subagents"
		req, err := newHarnessRequest(context.Background(), http.MethodGet, url, nil, apiKey)
		if err != nil {
			return SubagentsLoadFailedMsg{Err: err.Error()}
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return SubagentsLoadFailedMsg{Err: err.Error()}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return SubagentsLoadFailedMsg{Err: fmt.Sprintf("server returned %d", resp.StatusCode)}
		}
		var payload struct {
			Subagents []RemoteSubagent `json:"subagents"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return SubagentsLoadFailedMsg{Err: err.Error()}
		}
		return SubagentsLoadedMsg{Subagents: payload.Subagents}
	}
}

// loadTasksCmd fetches GET /v1/tasks for the /tasks overlay (epic #814),
// returning the unified background-work union as TasksLoadedMsg or a
// TasksLoadFailedMsg describing the failure.
func loadTasksCmd(baseURL, apiKey string) tea.Cmd {
	return func() tea.Msg {
		url := strings.TrimRight(baseURL, "/") + "/v1/tasks"
		req, err := newHarnessRequest(context.Background(), http.MethodGet, url, nil, apiKey)
		if err != nil {
			return TasksLoadFailedMsg{Err: err.Error()}
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return TasksLoadFailedMsg{Err: err.Error()}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return TasksLoadFailedMsg{Err: fmt.Sprintf("server returned %d", resp.StatusCode)}
		}
		var payload struct {
			Tasks []RemoteTask `json:"tasks"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return TasksLoadFailedMsg{Err: err.Error()}
		}
		return TasksLoadedMsg{Tasks: payload.Tasks}
	}
}

// loadHooksCmd fetches GET /v1/hooks for the /hooks command. The TUI renders
// server truth only — it never reads hook files from disk.
func loadHooksCmd(baseURL, apiKey string) tea.Cmd {
	return func() tea.Msg {
		url := strings.TrimRight(baseURL, "/") + "/v1/hooks"
		req, err := newHarnessRequest(context.Background(), http.MethodGet, url, nil, apiKey)
		if err != nil {
			return HooksLoadFailedMsg{Err: err.Error()}
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return HooksLoadFailedMsg{Err: err.Error()}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return HooksLoadFailedMsg{Err: fmt.Sprintf("server returned %d", resp.StatusCode)}
		}
		var payload HooksLoadedMsg
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return HooksLoadFailedMsg{Err: err.Error()}
		}
		return payload
	}
}

// providersResponse matches the JSON body returned by GET /v1/providers.
type providersResponse struct {
	Providers []struct {
		Name       string `json:"name"`
		Configured bool   `json:"configured"`
		APIKeyEnv  string `json:"api_key_env"`
		AuthType   string `json:"auth_type"`
	} `json:"providers"`
}

// fetchProvidersCmd fetches the list of providers from the server's /v1/providers endpoint.
// On success it emits ProvidersLoadedMsg; on failure it returns an empty list.
func fetchProvidersCmd(baseURL, apiKey string) tea.Cmd {
	return func() tea.Msg {
		url := strings.TrimRight(baseURL, "/") + "/v1/providers"
		req, err := newHarnessRequest(context.Background(), http.MethodGet, url, nil, apiKey)
		if err != nil {
			return ProvidersLoadedMsg{}
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return ProvidersLoadedMsg{}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return ProvidersLoadedMsg{}
		}
		var pr providersResponse
		if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
			return ProvidersLoadedMsg{}
		}
		providers := make([]ProviderInfo, len(pr.Providers))
		for i, p := range pr.Providers {
			providers[i] = ProviderInfo{
				Name:       p.Name,
				Configured: p.Configured,
				APIKeyEnv:  p.APIKeyEnv,
				AuthType:   p.AuthType,
			}
		}
		return ProvidersLoadedMsg{Providers: providers}
	}
}

// setProviderKeyCmd sends a provider API key to the server via PUT /v1/providers/{provider}/key.
// On success (204) it emits APIKeySetMsg; on failure it returns a status message.
//
// providerKey is the key being configured for provider (e.g. an OpenAI or
// Anthropic key) — it goes in the request BODY. harnessAPIKey is the
// harnessd auth key (TUIConfig.APIKey) — it goes in the Authorization
// header via newHarnessRequest. These are two distinct credentials; do not
// conflate them.
func setProviderKeyCmd(baseURL, provider, providerKey, harnessAPIKey string) tea.Cmd {
	return func() tea.Msg {
		url := strings.TrimRight(baseURL, "/") + "/v1/providers/" + provider + "/key"
		body, _ := json.Marshal(map[string]string{"key": providerKey})
		req, err := newHarnessRequest(context.Background(), http.MethodPut, url, bytes.NewReader(body), harnessAPIKey)
		if err != nil {
			return ProvidersLoadedMsg{}
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return ProvidersLoadedMsg{}
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
			return APIKeySetMsg{Provider: provider, Key: providerKey}
		}
		return ProvidersLoadedMsg{}
	}
}

// importSubscriptionCmd triggers an import from vendor files on the harnessd
// host. It deliberately sends no request body and never handles a token value.
func importSubscriptionCmd(baseURL, provider, harnessAPIKey string) tea.Cmd {
	return func() tea.Msg {
		url := strings.TrimRight(baseURL, "/") + "/v1/providers/" + provider + "/import-subscription"
		req, err := newHarnessRequest(context.Background(), http.MethodPost, url, nil, harnessAPIKey)
		if err != nil {
			return SubscriptionImportMsg{Provider: provider, Err: "Could not start subscription import."}
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return SubscriptionImportMsg{Provider: provider, Err: "Could not reach harnessd to import the subscription."}
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
			return SubscriptionImportMsg{Provider: provider}
		}
		var body struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err == nil && body.Error.Message != "" {
			return SubscriptionImportMsg{Provider: provider, Err: body.Error.Message}
		}
		return SubscriptionImportMsg{Provider: provider, Err: "Subscription import failed."}
	}
}

// profilesListResponse matches the JSON body returned by GET /v1/profiles.
type profilesListResponse struct {
	Profiles []struct {
		Name             string `json:"name"`
		Description      string `json:"description"`
		Model            string `json:"model"`
		AllowedToolCount int    `json:"allowed_tool_count"`
		SourceTier       string `json:"source_tier"`
	} `json:"profiles"`
	Count int `json:"count"`
}

// loadProfilesCmd fetches profile list from GET /v1/profiles.
// On success it emits ProfilesLoadedMsg; on failure it emits ProfilesLoadedMsg with Err set.
func loadProfilesCmd(baseURL, apiKey string) tea.Cmd {
	return func() tea.Msg {
		url := strings.TrimRight(baseURL, "/") + "/v1/profiles"
		req, err := newHarnessRequest(context.Background(), http.MethodGet, url, nil, apiKey)
		if err != nil {
			return ProfilesLoadedMsg{Err: err}
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return ProfilesLoadedMsg{Err: err}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return ProfilesLoadedMsg{Err: fmt.Errorf("server returned %d", resp.StatusCode)}
		}
		var plr profilesListResponse
		if err := json.NewDecoder(resp.Body).Decode(&plr); err != nil {
			return ProfilesLoadedMsg{Err: err}
		}
		entries := make([]ProfileEntry, len(plr.Profiles))
		for i, p := range plr.Profiles {
			entries[i] = ProfileEntry{
				Name:        p.Name,
				Description: p.Description,
				Model:       p.Model,
				ToolCount:   p.AllowedToolCount,
				SourceTier:  p.SourceTier,
			}
		}
		return ProfilesLoadedMsg{Entries: entries}
	}
}

// pollSSECmd reads one message from the SSE channel and returns it as a tea.Msg.
// It blocks until a message is available or the channel is closed.
// Call this again after every SSEEventMsg/SSEDropMsg to continue polling.
func pollSSECmd(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return SSEDoneMsg{EventType: "bridge.closed"}
		}
		return msg
	}
}

// formatRunError formats a run.failed error string for the viewport.
// The harness error looks like:
//
//	"provider completion failed: openai request failed (429): {\"error\":{...}}"
//
// We split at the first '{' to separate the prose prefix from any embedded JSON,
// then render the JSON fields as human-readable key: value lines.
func formatRunError(errStr string) []string {
	if errStr == "" {
		return []string{"✗ run failed"}
	}

	// Split prose prefix from embedded JSON object/array.
	prefix := errStr
	jsonPart := ""
	if idx := strings.Index(errStr, "{"); idx >= 0 {
		prefix = strings.TrimRight(errStr[:idx], ": ")
		jsonPart = errStr[idx:]
	}

	lines := []string{"✗ " + prefix}

	if jsonPart != "" {
		var obj map[string]any
		if err := json.Unmarshal([]byte(jsonPart), &obj); err == nil {
			for _, line := range flattenJSON(obj, "  ") {
				lines = append(lines, line)
			}
		} else {
			// Not valid JSON — just append as-is.
			lines = append(lines, "  "+jsonPart)
		}
	}

	return lines
}

// flattenJSON renders a JSON object as indented "key: value" lines.
// Nested objects are indented further. Arrays are shown as comma-joined values.
func flattenJSON(obj map[string]any, indent string) []string {
	var lines []string
	for k, v := range obj {
		switch val := v.(type) {
		case map[string]any:
			lines = append(lines, indent+k+":")
			lines = append(lines, flattenJSON(val, indent+"  ")...)
		case nil:
			// skip null fields
		default:
			lines = append(lines, fmt.Sprintf("%s%s: %v", indent, k, val))
		}
	}
	return lines
}

// fetchSessionRunsCmd fetches the run history for a conversation from
// GET /v1/conversations/{id}/runs.  On success it emits a SessionRunsFetchedMsg;
// on failure (including 501 Not Implemented) it emits a zero SessionRunsFetchedMsg
// so callers can handle the empty case gracefully.
func fetchSessionRunsCmd(baseURL, conversationID, apiKey string) tea.Cmd {
	return func() tea.Msg {
		url := strings.TrimRight(baseURL, "/") + "/v1/conversations/" + conversationID + "/runs"
		req, err := newHarnessRequest(context.Background(), http.MethodGet, url, nil, apiKey)
		if err != nil {
			return SessionRunsFetchedMsg{}
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return SessionRunsFetchedMsg{}
		}
		defer resp.Body.Close()
		// 501 means the server has no run store — treat as empty.
		if resp.StatusCode == http.StatusNotImplemented || resp.StatusCode != http.StatusOK {
			return SessionRunsFetchedMsg{}
		}
		var payload struct {
			Runs []struct {
				RunID string `json:"run_id"`
			} `json:"runs"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return SessionRunsFetchedMsg{}
		}
		ids := make([]string, len(payload.Runs))
		for i, r := range payload.Runs {
			ids[i] = r.RunID
		}
		return SessionRunsFetchedMsg{ConversationID: conversationID, RunIDs: ids}
	}
}

// fetchConversationMessagesCmd fetches the message history for a resumed
// conversation from GET /v1/conversations/{id}/messages. On success it emits
// ConversationHistoryMsg; on failure it emits ConversationHistoryErrorMsg.
func fetchConversationMessagesCmd(baseURL, conversationID, apiKey string) tea.Cmd {
	return func() tea.Msg {
		endpoint := strings.TrimRight(baseURL, "/") + "/v1/conversations/" + url.PathEscape(conversationID) + "/messages"
		req, err := newHarnessRequest(context.Background(), http.MethodGet, endpoint, nil, apiKey)
		if err != nil {
			return ConversationHistoryErrorMsg{ConversationID: conversationID, Err: err.Error()}
		}
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return ConversationHistoryErrorMsg{ConversationID: conversationID, Err: err.Error()}
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return ConversationHistoryErrorMsg{ConversationID: conversationID, Err: "read response: " + err.Error()}
		}
		if resp.StatusCode != http.StatusOK {
			return ConversationHistoryErrorMsg{ConversationID: conversationID, Err: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}
		}
		var payload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return ConversationHistoryErrorMsg{ConversationID: conversationID, Err: "decode response: " + err.Error()}
		}
		messages := make([]ConversationMessage, len(payload.Messages))
		for i, msg := range payload.Messages {
			messages[i] = ConversationMessage{Role: msg.Role, Content: msg.Content}
		}
		return ConversationHistoryMsg{ConversationID: conversationID, Messages: messages}
	}
}

func fetchRewindPointsCmd(baseURL, conversationID, apiKey string) tea.Cmd {
	return func() tea.Msg {
		endpoint := strings.TrimRight(baseURL, "/") + "/v1/conversations/" + url.PathEscape(conversationID) + "/rewind-points"
		req, err := newHarnessRequest(context.Background(), http.MethodGet, endpoint, nil, apiKey)
		if err != nil {
			return RewindResultMsg{Err: err.Error()}
		}
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return RewindResultMsg{Err: err.Error()}
		}
		defer resp.Body.Close()
		var payload struct {
			Points []RewindPoint `json:"points"`
		}
		if resp.StatusCode != http.StatusOK {
			return RewindResultMsg{Err: fmt.Sprintf("HTTP %d", resp.StatusCode)}
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return RewindResultMsg{Err: err.Error()}
		}
		return RewindPointsLoadedMsg{Points: payload.Points}
	}
}
func restoreRewindCmd(baseURL, conversationID, pointID, apiKey string) tea.Cmd {
	return func() tea.Msg {
		b, _ := json.Marshal(map[string]string{"point_id": pointID})
		endpoint := strings.TrimRight(baseURL, "/") + "/v1/conversations/" + url.PathEscape(conversationID) + "/rewind"
		req, err := newHarnessRequest(context.Background(), http.MethodPost, endpoint, bytes.NewReader(b), apiKey)
		if err != nil {
			return RewindResultMsg{Err: err.Error()}
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return RewindResultMsg{Err: err.Error()}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return RewindResultMsg{Err: fmt.Sprintf("HTTP %d", resp.StatusCode)}
		}
		var out RewindResultMsg
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			out.Err = err.Error()
		}
		return out
	}
}

// undoConversationCmd POSTs {"count": n} to /v1/conversations/{id}/undo
// (Issue #805). On success it also refetches the trimmed history so the
// caller can rebuild the viewport from the server's authoritative state; on
// 409 it returns Conflict with the server's compaction-boundary explanation.
func undoConversationCmd(baseURL, conversationID string, count int, apiKey string) tea.Cmd {
	return func() tea.Msg {
		b, _ := json.Marshal(map[string]int{"count": count})
		endpoint := strings.TrimRight(baseURL, "/") + "/v1/conversations/" + url.PathEscape(conversationID) + "/undo"
		req, err := newHarnessRequest(context.Background(), http.MethodPost, endpoint, bytes.NewReader(b), apiKey)
		if err != nil {
			return UndoResultMsg{Err: err.Error()}
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return UndoResultMsg{Err: err.Error()}
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return UndoResultMsg{Err: "read response: " + err.Error()}
		}
		if resp.StatusCode == http.StatusConflict {
			var errPayload struct {
				ErrBody struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			msg := strings.TrimSpace(string(body))
			if json.Unmarshal(body, &errPayload) == nil && errPayload.ErrBody.Message != "" {
				msg = errPayload.ErrBody.Message
			}
			return UndoResultMsg{Conflict: true, Err: msg}
		}
		if resp.StatusCode != http.StatusOK {
			return UndoResultMsg{Err: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}
		}
		var payload struct {
			RemovedFromStep   int `json:"removed_from_step"`
			RemainingMessages int `json:"remaining_messages"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return UndoResultMsg{Err: "decode response: " + err.Error()}
		}
		out := UndoResultMsg{RemovedFromStep: payload.RemovedFromStep, RemainingMessages: payload.RemainingMessages}

		// Refetch the trimmed history for the viewport rebuild. A refetch
		// failure is surfaced as Err so the user knows to refresh manually.
		messagesEndpoint := strings.TrimRight(baseURL, "/") + "/v1/conversations/" + url.PathEscape(conversationID) + "/messages"
		messagesReq, err := newHarnessRequest(context.Background(), http.MethodGet, messagesEndpoint, nil, apiKey)
		if err != nil {
			out.Err = err.Error()
			return out
		}
		messagesResp, err := (&http.Client{Timeout: 10 * time.Second}).Do(messagesReq)
		if err != nil {
			out.Err = err.Error()
			return out
		}
		defer messagesResp.Body.Close()
		messagesBody, err := io.ReadAll(messagesResp.Body)
		if err != nil {
			out.Err = "read messages: " + err.Error()
			return out
		}
		if messagesResp.StatusCode != http.StatusOK {
			out.Err = fmt.Sprintf("refetch messages: HTTP %d", messagesResp.StatusCode)
			return out
		}
		var messagesPayload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(messagesBody, &messagesPayload); err != nil {
			out.Err = "decode messages: " + err.Error()
			return out
		}
		out.Messages = make([]ConversationMessage, len(messagesPayload.Messages))
		for i, m := range messagesPayload.Messages {
			out.Messages[i] = ConversationMessage{Role: m.Role, Content: m.Content}
		}
		return out
	}
}

// forkConversationCmd forks the given conversation via
// POST /v1/conversations/{id}/fork (epic #816). On success it emits
// ForkResultMsg with the server-minted new conversation ID; on failure it
// emits ForkResultMsg with Err set.
func forkConversationCmd(baseURL, conversationID, apiKey string) tea.Cmd {
	return func() tea.Msg {
		endpoint := strings.TrimRight(baseURL, "/") + "/v1/conversations/" + url.PathEscape(conversationID) + "/fork"
		req, err := newHarnessRequest(context.Background(), http.MethodPost, endpoint, nil, apiKey)
		if err != nil {
			return ForkResultMsg{SrcID: conversationID, Err: err.Error()}
		}
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return ForkResultMsg{SrcID: conversationID, Err: err.Error()}
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return ForkResultMsg{SrcID: conversationID, Err: "read response: " + err.Error()}
		}
		if resp.StatusCode != http.StatusOK {
			return ForkResultMsg{SrcID: conversationID, Err: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}
		}
		var payload struct {
			ConversationID string `json:"conversation_id"`
			ForkedFrom     string `json:"forked_from"`
			MessageCount   int    `json:"message_count"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return ForkResultMsg{SrcID: conversationID, Err: "decode response: " + err.Error()}
		}
		return ForkResultMsg{SrcID: conversationID, NewID: payload.ConversationID, MessageCount: payload.MessageCount}
	}
}

// sseEventsURL builds the SSE endpoint URL for a given run ID.
func sseEventsURL(baseURL, runID string) string {
	return strings.TrimRight(baseURL, "/") + "/v1/runs/" + runID + "/events"
}

// startSSEForRun starts the SSE bridge for the given run and returns the
// channel and cancel func. apiKey, if non-empty, is sent as
// "Authorization: Bearer <apiKey>" (see SSEBridgeOptions) — the same
// harnessd auth key sourced from ~/.harness/config.json via
// cmd/harnesscli/auth.go's newAuthedRequest pattern and threaded into
// TUIConfig.APIKey by cmd/harnesscli/main.go's newTUIConfig.
func startSSEForRun(baseURL, runID, apiKey string) (<-chan tea.Msg, func()) {
	url := sseEventsURL(baseURL, runID)
	return StartSSEBridgeWithOptions(context.Background(), url, SSEBridgeOptions{APIKey: apiKey})
}

// startSSEForRunFrom starts the SSE bridge for the given run, resuming from
// lastEventID via the Last-Event-ID header and authenticating with apiKey
// (see SSEBridgeOptions). Used to reconnect after the stream drops mid-run
// without losing or duplicating events, and without dropping authentication
// on the resumed connection.
func startSSEForRunFrom(baseURL, runID, lastEventID, apiKey string) (<-chan tea.Msg, func()) {
	url := sseEventsURL(baseURL, runID)
	return StartSSEBridgeWithOptions(context.Background(), url, SSEBridgeOptions{LastEventID: lastEventID, APIKey: apiKey})
}

// maxSSEReconnectAttempts bounds how many times the TUI will automatically
// reconnect a dropped SSE stream for a single run before giving up and
// surfacing a clear "connection lost" message to the user.
const maxSSEReconnectAttempts = 5

// sseReconnectBackoff returns the delay before reconnect attempt N
// (1-indexed), growing exponentially from a short base and capped so the
// TUI recovers quickly from a transient drop instead of leaving the user
// staring at a dead stream for long.
func sseReconnectBackoff(attempt int) time.Duration {
	const base = 200 * time.Millisecond
	const capDelay = 3 * time.Second
	d := base
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= capDelay {
			return capDelay
		}
	}
	return d
}

// reconnectSSECmd schedules a bounded, backed-off SSE reconnect attempt for
// the given run that resumes exactly where the previous connection left off
// via lastEventID and re-authenticates with apiKey (see startSSEForRunFrom
// and internal/server/http_runs.go's Last-Event-ID handling). It always
// yields SSEReconnectedMsg so Update() can decide whether the reconnect is
// still wanted — the run may have been cancelled or completed while the
// backoff was pending.
func reconnectSSECmd(baseURL, runID, lastEventID, apiKey string, attempt int) tea.Cmd {
	delay := sseReconnectBackoff(attempt)
	return tea.Tick(delay, func(time.Time) tea.Msg {
		ch, cancel := startSSEForRunFrom(baseURL, runID, lastEventID, apiKey)
		return SSEReconnectedMsg{Ch: ch, Cancel: cancel}
	})
}
