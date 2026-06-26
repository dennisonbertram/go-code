package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"go-agent-harness/cmd/harnesscli/tui/components/modelswitcher"
)

type runCreateRequest struct {
	Prompt          string `json:"prompt"`
	ConversationID  string `json:"conversation_id,omitempty"`
	Model           string `json:"model,omitempty"`
	ProviderName    string `json:"provider_name,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// ProfileName carries the capability profile selected via /profiles. It maps
	// to harness.RunRequest.ProfileName (JSON "profile"), which applies tool
	// restrictions, approval policy, and workspace isolation. This is distinct
	// from "prompt_profile" (prompt/model routing); sending a capability profile
	// name in prompt_profile makes the server reject the run with HTTP 400.
	ProfileName   string `json:"profile,omitempty"`
	WorkspacePath string `json:"workspace_path,omitempty"`
}

type runCreateResponse struct {
	RunID string `json:"run_id"`
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

// startRunCmd returns a tea.Cmd that POSTs a run to the harness and emits
// RunStartedMsg on success or RunFailedMsg on error.
// conversationID may be empty for the first message in a new conversation;
// subsequent messages should pass the run ID returned by the first run so that
// the harness groups them under the same conversation.
// profile is the name of the capability profile to use (may be empty); it is
// sent as the "profile" field so the server applies the profile's tool
// restrictions and isolation.
func startRunCmd(baseURL, prompt, conversationID, model, provider, reasoningEffort, profile, workspace string) tea.Cmd {
	return func() tea.Msg {
		body, _ := json.Marshal(runCreateRequest{
			Prompt:          prompt,
			ConversationID:  conversationID,
			Model:           model,
			ProviderName:    provider,
			ReasoningEffort: reasoningEffort,
			ProfileName:     profile,
			WorkspacePath:   workspace,
		})
		url := strings.TrimRight(baseURL, "/") + "/v1/runs"
		resp, err := http.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			return RunFailedMsg{Error: err.Error()}
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			return RunFailedMsg{Error: fmt.Sprintf("start run: HTTP %d", resp.StatusCode)}
		}
		var created runCreateResponse
		if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
			return RunFailedMsg{Error: fmt.Sprintf("decode run response: %s", err.Error())}
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
func fetchModelsCmd(baseURL string) tea.Cmd {
	return func() tea.Msg {
		url := strings.TrimRight(baseURL, "/") + "/v1/models"
		resp, err := http.Get(url) //nolint:noctx
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

func loadSubagentsCmd(baseURL string) tea.Cmd {
	return func() tea.Msg {
		url := strings.TrimRight(baseURL, "/") + "/v1/subagents"
		resp, err := http.Get(url) //nolint:noctx
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

// providersResponse matches the JSON body returned by GET /v1/providers.
type providersResponse struct {
	Providers []struct {
		Name       string `json:"name"`
		Configured bool   `json:"configured"`
		APIKeyEnv  string `json:"api_key_env"`
	} `json:"providers"`
}

// fetchProvidersCmd fetches the list of providers from the server's /v1/providers endpoint.
// On success it emits ProvidersLoadedMsg; on failure it returns an empty list.
func fetchProvidersCmd(baseURL string) tea.Cmd {
	return func() tea.Msg {
		url := strings.TrimRight(baseURL, "/") + "/v1/providers"
		resp, err := http.Get(url) //nolint:noctx
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
			}
		}
		return ProvidersLoadedMsg{Providers: providers}
	}
}

// setProviderKeyCmd sends a provider API key to the server via PUT /v1/providers/{provider}/key.
// On success (204) it emits APIKeySetMsg; on failure it returns a status message.
func setProviderKeyCmd(baseURL, provider, apiKey string) tea.Cmd {
	return func() tea.Msg {
		url := strings.TrimRight(baseURL, "/") + "/v1/providers/" + provider + "/key"
		body, _ := json.Marshal(map[string]string{"key": apiKey})
		req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
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
			return APIKeySetMsg{Provider: provider, Key: apiKey}
		}
		return ProvidersLoadedMsg{}
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
func loadProfilesCmd(baseURL string) tea.Cmd {
	return func() tea.Msg {
		url := strings.TrimRight(baseURL, "/") + "/v1/profiles"
		resp, err := http.Get(url) //nolint:noctx
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
func fetchSessionRunsCmd(baseURL, conversationID string) tea.Cmd {
	return func() tea.Msg {
		url := strings.TrimRight(baseURL, "/") + "/v1/conversations/" + conversationID + "/runs"
		resp, err := http.Get(url) //nolint:noctx
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

// sseEventsURL builds the SSE endpoint URL for a given run ID.
func sseEventsURL(baseURL, runID string) string {
	return strings.TrimRight(baseURL, "/") + "/v1/runs/" + runID + "/events"
}

// startSSEForRun starts the SSE bridge for the given run and returns the channel
// and cancel func.
func startSSEForRun(baseURL, runID string) (<-chan tea.Msg, func()) {
	url := sseEventsURL(baseURL, runID)
	return StartSSEBridge(context.Background(), url)
}
