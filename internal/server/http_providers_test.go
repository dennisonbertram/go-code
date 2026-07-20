package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/provider/catalog"
)

// testCatalogForProviders returns a catalog with two providers for provider endpoint tests.
// The "openai" provider uses env var TEST_OPENAI_KEY_149 and "anthropic" uses TEST_ANTHROPIC_KEY_149
// so tests can control configured status without interfering with real env vars.
func testCatalogForProviders() *catalog.Catalog {
	return &catalog.Catalog{
		CatalogVersion: "1.0.0",
		Providers: map[string]catalog.ProviderEntry{
			"openai": {
				DisplayName: "OpenAI",
				APIKeyEnv:   "TEST_OPENAI_KEY_149",
				BaseURL:     "https://api.openai.com/v1",
				Models: map[string]catalog.Model{
					"gpt-4.1-mini": {DisplayName: "GPT-4.1 Mini"},
					"gpt-4.1":      {DisplayName: "GPT-4.1"},
					"o3-mini":      {DisplayName: "o3 Mini"},
				},
			},
			"anthropic": {
				DisplayName: "Anthropic",
				APIKeyEnv:   "TEST_ANTHROPIC_KEY_149",
				BaseURL:     "https://api.anthropic.com",
				Models: map[string]catalog.Model{
					"claude-opus-4": {DisplayName: "Claude Opus 4"},
				},
			},
		},
	}
}

func testRunnerForProviders(t *testing.T) *harness.Runner {
	t.Helper()
	return harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "done"}},
		harness.NewRegistry(),
		harness.RunnerConfig{DefaultModel: "gpt-4.1-mini", MaxSteps: 1},
	)
}

func TestProvidersEndpointConfiguredAndUnconfigured(t *testing.T) {
	// t.Setenv cannot be used with t.Parallel().

	// Set only the openai test key.
	t.Setenv("TEST_OPENAI_KEY_149", "sk-test-key")
	// TEST_ANTHROPIC_KEY_149 is deliberately NOT set.

	runner := testRunnerForProviders(t)
	handler := NewWithCatalog(runner, testCatalogForProviders())
	ts := httptest.NewServer(handler)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/providers")
	if err != nil {
		t.Fatalf("GET /v1/providers: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, string(body))
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var resp struct {
		Providers []ProviderResponse `json:"providers"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(resp.Providers) != 2 {
		t.Fatalf("expected 2 providers, got %d: %+v", len(resp.Providers), resp.Providers)
	}

	// Providers should be sorted alphabetically: anthropic, openai.
	if resp.Providers[0].Name != "anthropic" {
		t.Errorf("expected first provider to be anthropic (sorted), got %q", resp.Providers[0].Name)
	}
	if resp.Providers[1].Name != "openai" {
		t.Errorf("expected second provider to be openai (sorted), got %q", resp.Providers[1].Name)
	}

	// Build a map for easier assertion.
	byName := make(map[string]ProviderResponse, len(resp.Providers))
	for _, p := range resp.Providers {
		byName[p.Name] = p
	}

	openai := byName["openai"]
	if !openai.Configured {
		t.Error("openai: expected configured=true (env var is set)")
	}
	if openai.APIKeyEnv != "TEST_OPENAI_KEY_149" {
		t.Errorf("openai: expected api_key_env=TEST_OPENAI_KEY_149, got %q", openai.APIKeyEnv)
	}
	if openai.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("openai: expected base_url=https://api.openai.com/v1, got %q", openai.BaseURL)
	}
	if openai.ModelCount != 3 {
		t.Errorf("openai: expected model_count=3, got %d", openai.ModelCount)
	}

	anthropic := byName["anthropic"]
	if anthropic.Configured {
		t.Error("anthropic: expected configured=false (env var is not set)")
	}
	if anthropic.APIKeyEnv != "TEST_ANTHROPIC_KEY_149" {
		t.Errorf("anthropic: expected api_key_env=TEST_ANTHROPIC_KEY_149, got %q", anthropic.APIKeyEnv)
	}
	if anthropic.BaseURL != "https://api.anthropic.com" {
		t.Errorf("anthropic: expected base_url=https://api.anthropic.com, got %q", anthropic.BaseURL)
	}
	if anthropic.ModelCount != 1 {
		t.Errorf("anthropic: expected model_count=1, got %d", anthropic.ModelCount)
	}
}

func TestProvidersEndpointMarksCodexSubscriptionAuth(t *testing.T) {
	t.Parallel()
	cat := testCatalogForProviders()
	cat.Providers["codex-subscription"] = catalog.ProviderEntry{
		BaseURL: "https://chatgpt.com/backend-api/codex", APIKeyOptional: true, TokenSourceRequired: true,
		Models: map[string]catalog.Model{"gpt": {ContextWindow: 1}},
	}
	ts := httptest.NewServer(NewWithCatalog(testRunnerForProviders(t), cat))
	defer ts.Close()
	res, err := http.Get(ts.URL + "/v1/providers")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body struct {
		Providers []ProviderResponse `json:"providers"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	for _, provider := range body.Providers {
		if provider.Name == "codex-subscription" {
			if provider.AuthType != "subscription" || provider.Configured {
				t.Fatalf("Codex response = %#v, want unconfigured subscription", provider)
			}
			return
		}
	}
	t.Fatal("Codex subscription missing from provider response")
}

func TestProvidersEndpointNilCatalog(t *testing.T) {
	t.Parallel()

	runner := testRunnerForProviders(t)
	handler := New(runner) // no catalog
	ts := httptest.NewServer(handler)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/providers")
	if err != nil {
		t.Fatalf("GET /v1/providers: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, string(body))
	}

	var resp struct {
		Providers []ProviderResponse `json:"providers"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Providers == nil {
		t.Error("expected non-nil providers array (should be empty slice, not null)")
	}
	if len(resp.Providers) != 0 {
		t.Errorf("expected empty providers list when no catalog, got %d providers", len(resp.Providers))
	}
}

func TestProvidersEndpointMethodNotAllowed(t *testing.T) {
	t.Parallel()

	runner := testRunnerForProviders(t)
	handler := NewWithCatalog(runner, testCatalogForProviders())
	ts := httptest.NewServer(handler)
	defer ts.Close()

	res, err := http.Post(ts.URL+"/v1/providers", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /v1/providers: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", res.StatusCode)
	}
	if got := res.Header.Get("Allow"); got != http.MethodGet {
		t.Errorf("expected Allow: GET, got %q", got)
	}
}
