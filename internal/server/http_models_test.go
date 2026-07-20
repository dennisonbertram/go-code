package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/provider/catalog"
)

type testOpenRouterDiscovery struct {
	models []catalog.DiscoveredModel
	err    error
}

func (d testOpenRouterDiscovery) Models(context.Context) ([]catalog.DiscoveredModel, error) {
	out := make([]catalog.DiscoveredModel, len(d.models))
	copy(out, d.models)
	return out, d.err
}

// testRunner builds a minimal runner suitable for HTTP handler tests.
func testRunnerForModels(t *testing.T) *harness.Runner {
	t.Helper()
	registry := harness.NewRegistry()
	return harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "done"}},
		registry,
		harness.RunnerConfig{
			DefaultModel:        "gpt-4.1-mini",
			DefaultSystemPrompt: "You are helpful.",
			MaxSteps:            1,
		},
	)
}

// testCatalog returns a small in-memory catalog for testing.
func testCatalog() *catalog.Catalog {
	return &catalog.Catalog{
		CatalogVersion: "1.0.0",
		Providers: map[string]catalog.ProviderEntry{
			"openai": {
				DisplayName: "OpenAI",
				Models: map[string]catalog.Model{
					"gpt-4.1-mini": {
						DisplayName: "GPT-4.1 Mini",
						Pricing: &catalog.ModelPricing{
							InputPer1MTokensUSD:  0.40,
							OutputPer1MTokensUSD: 1.60,
						},
					},
					"gpt-4.1": {
						DisplayName: "GPT-4.1",
						Pricing: &catalog.ModelPricing{
							InputPer1MTokensUSD:  2.00,
							OutputPer1MTokensUSD: 8.00,
						},
					},
				},
				Aliases: map[string]string{
					"gpt4-mini": "gpt-4.1-mini",
					"gpt4":      "gpt-4.1",
				},
			},
		},
	}
}

func TestModelsEndpointReturnsList(t *testing.T) {
	t.Parallel()

	runner := testRunnerForModels(t)
	handler := NewWithCatalog(runner, testCatalog())
	ts := httptest.NewServer(handler)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
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
		Models []ModelResponse `json:"models"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(resp.Models) == 0 {
		t.Fatal("expected non-empty models list")
	}
}

func TestModelsEndpointMethodNotAllowed(t *testing.T) {
	t.Parallel()

	runner := testRunnerForModels(t)
	handler := NewWithCatalog(runner, testCatalog())
	ts := httptest.NewServer(handler)
	defer ts.Close()

	res, err := http.Post(ts.URL+"/v1/models", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /v1/models: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", res.StatusCode)
	}
}

func TestModelsEndpointContainsExpectedModels(t *testing.T) {
	t.Parallel()

	runner := testRunnerForModels(t)
	handler := NewWithCatalog(runner, testCatalog())
	ts := httptest.NewServer(handler)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer res.Body.Close()

	var resp struct {
		Models []ModelResponse `json:"models"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	byID := make(map[string]ModelResponse, len(resp.Models))
	for _, m := range resp.Models {
		byID[m.ID] = m
	}

	// gpt-4.1-mini checks
	mini, ok := byID["gpt-4.1-mini"]
	if !ok {
		t.Fatalf("expected gpt-4.1-mini in response; got %v", resp.Models)
	}
	if mini.Provider != "openai" {
		t.Errorf("gpt-4.1-mini: expected provider openai, got %q", mini.Provider)
	}
	if mini.InputCostPerMTok != 0.40 {
		t.Errorf("gpt-4.1-mini: expected input cost 0.40, got %v", mini.InputCostPerMTok)
	}
	if mini.OutputCostPerMTok != 1.60 {
		t.Errorf("gpt-4.1-mini: expected output cost 1.60, got %v", mini.OutputCostPerMTok)
	}
	// alias "gpt4-mini" -> "gpt-4.1-mini"
	foundAlias := false
	for _, a := range mini.Aliases {
		if a == "gpt4-mini" {
			foundAlias = true
			break
		}
	}
	if !foundAlias {
		t.Errorf("gpt-4.1-mini: expected alias gpt4-mini, got %v", mini.Aliases)
	}

	// gpt-4.1 checks
	full, ok := byID["gpt-4.1"]
	if !ok {
		t.Fatalf("expected gpt-4.1 in response; got %v", resp.Models)
	}
	if full.InputCostPerMTok != 2.00 {
		t.Errorf("gpt-4.1: expected input cost 2.00, got %v", full.InputCostPerMTok)
	}
	if full.OutputCostPerMTok != 8.00 {
		t.Errorf("gpt-4.1: expected output cost 8.00, got %v", full.OutputCostPerMTok)
	}
}

func TestModelsEndpointNilCatalog(t *testing.T) {
	t.Parallel()

	runner := testRunnerForModels(t)
	// Use New (no catalog) — should return empty list, not 500.
	handler := New(runner)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected 200, got %d: %s", res.StatusCode, string(body))
	}

	var resp struct {
		Models []ModelResponse `json:"models"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Models == nil {
		t.Error("expected non-nil models array (should be empty slice, not null)")
	}
	if len(resp.Models) != 0 {
		t.Errorf("expected empty models list when no catalog, got %d models", len(resp.Models))
	}
}

func TestModelsEndpointIncludesDiscoveredOpenRouterModels(t *testing.T) {
	t.Parallel()

	runner := testRunnerForModels(t)
	cat := testCatalog()
	cat.Providers["openrouter"] = catalog.ProviderEntry{
		DisplayName: "OpenRouter",
		BaseURL:     "https://openrouter.ai/api/v1",
		APIKeyEnv:   "OPENROUTER_API_KEY",
		Protocol:    "openai",
		Models: map[string]catalog.Model{
			"openai/gpt-4.1-mini": {
				DisplayName: "Catalog GPT-4.1 Mini",
				Pricing: &catalog.ModelPricing{
					InputPer1MTokensUSD:  0.40,
					OutputPer1MTokensUSD: 1.60,
				},
				ContextWindow: 128000,
			},
		},
		Aliases: map[string]string{
			"gpt4-mini": "openai/gpt-4.1-mini",
		},
	}
	reg := catalog.NewProviderRegistry(cat)
	reg.SetOpenRouterDiscovery(testOpenRouterDiscovery{
		models: []catalog.DiscoveredModel{
			{ID: "openai/gpt-4.1-mini", Name: "Live GPT-4.1 Mini", ContextWindow: 999999},
			{ID: "moonshotai/kimi-k2.5", Name: "Kimi K2.5", ContextWindow: 262144},
		},
	})

	handler := NewWithOptions(ServerOptions{
		Runner:           runner,
		Catalog:          cat,
		ProviderRegistry: reg,
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer res.Body.Close()

	var resp struct {
		Models []ModelResponse `json:"models"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	byID := make(map[string]ModelResponse, len(resp.Models))
	for _, model := range resp.Models {
		byID[model.ID] = model
	}

	kimi, ok := byID["moonshotai/kimi-k2.5"]
	if !ok {
		t.Fatalf("expected discovered Kimi model in response, got %v", resp.Models)
	}
	if kimi.Provider != "openrouter" {
		t.Fatalf("expected kimi provider openrouter, got %q", kimi.Provider)
	}

	openRouterMini, ok := byID["openai/gpt-4.1-mini"]
	if !ok {
		t.Fatalf("expected openrouter gpt-4.1-mini in response")
	}
	if openRouterMini.InputCostPerMTok != 0.40 || openRouterMini.OutputCostPerMTok != 1.60 {
		t.Fatalf("expected static pricing overlay, got input=%v output=%v", openRouterMini.InputCostPerMTok, openRouterMini.OutputCostPerMTok)
	}
	foundAlias := false
	for _, alias := range openRouterMini.Aliases {
		if alias == "gpt4-mini" {
			foundAlias = true
			break
		}
	}
	if !foundAlias {
		t.Fatalf("expected static alias on openrouter model, got %v", openRouterMini.Aliases)
	}
}
