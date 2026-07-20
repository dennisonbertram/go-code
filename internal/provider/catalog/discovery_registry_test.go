package catalog

import (
	"context"
	"testing"
)

type stubOpenRouterDiscovery struct {
	models []DiscoveredModel
	err    error
}

func (s stubOpenRouterDiscovery) Models(context.Context) ([]DiscoveredModel, error) {
	out := make([]DiscoveredModel, len(s.models))
	copy(out, s.models)
	return out, s.err
}

func TestProviderRegistryResolveProviderUsesOpenRouterDiscovery(t *testing.T) {
	t.Parallel()

	cat := &Catalog{
		CatalogVersion: "1.0",
		Providers: map[string]ProviderEntry{
			"openrouter": {
				DisplayName: "OpenRouter",
				BaseURL:     "https://openrouter.ai/api/v1",
				APIKeyEnv:   "OPENROUTER_API_KEY",
				Protocol:    "openai",
				Models: map[string]Model{
					"openai/gpt-4.1-mini": {DisplayName: "GPT-4.1 Mini", ContextWindow: 128000},
				},
			},
		},
	}

	reg := NewProviderRegistry(cat)
	reg.SetDiscovery("openrouter", stubOpenRouterDiscovery{
		models: []DiscoveredModel{
			{ID: "moonshotai/kimi-k2.5", Name: "Kimi K2.5", ContextWindow: 262144},
		},
	})

	providerName, found := reg.ResolveProvider("moonshotai/kimi-k2.5")
	if !found {
		t.Fatal("expected dynamic openrouter model to resolve")
	}
	if providerName != "openrouter" {
		t.Fatalf("expected openrouter, got %q", providerName)
	}
}

func TestProviderRegistryListModelsMergesStaticAndOpenRouterDiscovery(t *testing.T) {
	t.Parallel()

	cat := &Catalog{
		CatalogVersion: "1.0",
		Providers: map[string]ProviderEntry{
			"openrouter": {
				DisplayName: "OpenRouter",
				BaseURL:     "https://openrouter.ai/api/v1",
				APIKeyEnv:   "OPENROUTER_API_KEY",
				Protocol:    "openai",
				Models: map[string]Model{
					"openai/gpt-4.1-mini": {
						DisplayName:   "Catalog GPT-4.1 Mini",
						ContextWindow: 128000,
						Pricing: &ModelPricing{
							InputPer1MTokensUSD:  0.4,
							OutputPer1MTokensUSD: 1.6,
						},
					},
				},
				Aliases: map[string]string{
					"gpt4-mini": "openai/gpt-4.1-mini",
				},
			},
			"openai": {
				DisplayName: "OpenAI",
				BaseURL:     "https://api.openai.com/v1",
				APIKeyEnv:   "OPENAI_API_KEY",
				Protocol:    "openai",
				Models: map[string]Model{
					"gpt-4.1": {DisplayName: "GPT-4.1", ContextWindow: 128000},
				},
			},
		},
	}

	reg := NewProviderRegistry(cat)
	reg.SetDiscovery("openrouter", stubOpenRouterDiscovery{
		models: []DiscoveredModel{
			{ID: "openai/gpt-4.1-mini", Name: "Live GPT-4.1 Mini", ContextWindow: 999999},
			{ID: "moonshotai/kimi-k2.5", Name: "Kimi K2.5", ContextWindow: 262144},
		},
	})

	models := reg.ListModelsContext(context.Background())
	byProviderAndID := make(map[string]ModelResult, len(models))
	for _, model := range models {
		byProviderAndID[model.Provider+"::"+model.ModelID] = model
	}

	staticOpenAI, ok := byProviderAndID["openai::gpt-4.1"]
	if !ok {
		t.Fatalf("expected static openai model in merged list")
	}
	if staticOpenAI.Model.DisplayName != "GPT-4.1" {
		t.Fatalf("expected static openai display name, got %q", staticOpenAI.Model.DisplayName)
	}

	openRouterStatic, ok := byProviderAndID["openrouter::openai/gpt-4.1-mini"]
	if !ok {
		t.Fatalf("expected openrouter catalog model in merged list")
	}
	if openRouterStatic.Model.DisplayName != "Catalog GPT-4.1 Mini" {
		t.Fatalf("expected static overlay display name, got %q", openRouterStatic.Model.DisplayName)
	}
	if openRouterStatic.Model.ContextWindow != 128000 {
		t.Fatalf("expected static context window override, got %d", openRouterStatic.Model.ContextWindow)
	}
	if openRouterStatic.Model.Pricing == nil || openRouterStatic.Model.Pricing.InputPer1MTokensUSD != 0.4 {
		t.Fatalf("expected static pricing overlay, got %+v", openRouterStatic.Model.Pricing)
	}

	discoveredOnly, ok := byProviderAndID["openrouter::moonshotai/kimi-k2.5"]
	if !ok {
		t.Fatalf("expected discovered-only openrouter model in merged list")
	}
	if discoveredOnly.Model.DisplayName != "Kimi K2.5" {
		t.Fatalf("expected discovered display name, got %q", discoveredOnly.Model.DisplayName)
	}
	if discoveredOnly.Model.ContextWindow != 262144 {
		t.Fatalf("expected discovered context window, got %d", discoveredOnly.Model.ContextWindow)
	}
}

func TestProviderRegistryListModelsFallsBackToStaticWhenDiscoveryFails(t *testing.T) {
	t.Parallel()

	cat := &Catalog{
		CatalogVersion: "1.0",
		Providers: map[string]ProviderEntry{
			"openrouter": {
				DisplayName: "OpenRouter",
				BaseURL:     "https://openrouter.ai/api/v1",
				APIKeyEnv:   "OPENROUTER_API_KEY",
				Protocol:    "openai",
				Models: map[string]Model{
					"openai/gpt-4.1-mini": {DisplayName: "Catalog GPT-4.1 Mini", ContextWindow: 128000},
				},
			},
		},
	}

	reg := NewProviderRegistry(cat)
	reg.SetDiscovery("openrouter", stubOpenRouterDiscovery{err: context.DeadlineExceeded})

	models := reg.ListModelsContext(context.Background())
	if len(models) != 1 {
		t.Fatalf("expected static fallback list of 1 model, got %d", len(models))
	}
	if models[0].ModelID != "openai/gpt-4.1-mini" {
		t.Fatalf("expected static openrouter model, got %q", models[0].ModelID)
	}
}

func TestProviderRegistrySetDiscoveryMergesNonOpenRouterAndFallsBackToStatic(t *testing.T) {
	t.Parallel()

	cat := &Catalog{CatalogVersion: "1.0", Providers: map[string]ProviderEntry{
		"openai": {
			DisplayName: "OpenAI", Models: map[string]Model{
				"gpt-curated": {
					DisplayName: "Curated GPT", ContextWindow: 128000, CostTier: "premium",
					BestFor: []string{"coding"}, Pricing: &ModelPricing{InputPer1MTokensUSD: 1},
				},
			},
		},
	}}

	reg := NewProviderRegistry(cat)
	reg.SetDiscovery("openai", stubOpenRouterDiscovery{models: []DiscoveredModel{
		{ID: "gpt-curated", Name: "Live GPT", ContextWindow: 1},
		{ID: "gpt-live", Name: "Live GPT", ContextWindow: 256000},
	}})
	reg.SetDiscovery("missing", stubOpenRouterDiscovery{models: []DiscoveredModel{{ID: "ignored"}}})

	byID := make(map[string]ModelResult)
	for _, result := range reg.ListModelsContext(context.Background()) {
		byID[result.ModelID] = result
	}
	curated := byID["gpt-curated"].Model
	if curated.DisplayName != "Curated GPT" || curated.ContextWindow != 128000 || curated.Pricing == nil || curated.CostTier != "premium" || len(curated.BestFor) != 1 {
		t.Fatalf("expected curated metadata to win, got %+v", curated)
	}
	if got := byID["gpt-live"].Model.ContextWindow; got != 256000 {
		t.Fatalf("expected live-only model to be merged, got context %d", got)
	}

	reg.SetDiscovery("openai", stubOpenRouterDiscovery{err: context.DeadlineExceeded})
	models := reg.ListModelsContext(context.Background())
	if len(models) != 1 || models[0].ModelID != "gpt-curated" {
		t.Fatalf("expected first-attempt discovery failure to retain static models, got %+v", models)
	}
}
