package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func validCatalogJSON() string {
	return `{
		"catalog_version": "1.0.0",
		"providers": {
			"openai": {
				"display_name": "OpenAI",
				"base_url": "https://api.openai.com/v1",
				"api_key_env": "OPENAI_API_KEY",
				"protocol": "openai_compat",
				"models": {
					"gpt-4.1-mini": {
						"display_name": "GPT-4.1 Mini",
						"description": "Fast and affordable",
						"context_window": 1000000,
						"modalities": ["text"],
						"tool_calling": true,
						"streaming": true,
						"speed_tier": "fast",
						"cost_tier": "budget",
						"pricing": {
							"input_per_1m_tokens_usd": 0.40,
							"output_per_1m_tokens_usd": 1.60
						}
					}
				},
				"aliases": {
					"gpt4-mini": "gpt-4.1-mini"
				}
			}
		}
	}`
}

func TestLoadCatalogFromBytes_Valid(t *testing.T) {
	cat, err := LoadCatalogFromBytes([]byte(validCatalogJSON()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cat.CatalogVersion != "1.0.0" {
		t.Errorf("catalog_version = %q, want %q", cat.CatalogVersion, "1.0.0")
	}
	if len(cat.Providers) != 1 {
		t.Fatalf("providers count = %d, want 1", len(cat.Providers))
	}
	p, ok := cat.Providers["openai"]
	if !ok {
		t.Fatal("provider 'openai' not found")
	}
	if p.DisplayName != "OpenAI" {
		t.Errorf("display_name = %q, want %q", p.DisplayName, "OpenAI")
	}
	if p.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("base_url = %q, want %q", p.BaseURL, "https://api.openai.com/v1")
	}
	if p.APIKeyEnv != "OPENAI_API_KEY" {
		t.Errorf("api_key_env = %q, want %q", p.APIKeyEnv, "OPENAI_API_KEY")
	}
	if p.Protocol != "openai_compat" {
		t.Errorf("protocol = %q, want %q", p.Protocol, "openai_compat")
	}

	m, ok := p.Models["gpt-4.1-mini"]
	if !ok {
		t.Fatal("model 'gpt-4.1-mini' not found")
	}
	if m.ContextWindow != 1000000 {
		t.Errorf("context_window = %d, want 1000000", m.ContextWindow)
	}
	if !m.ToolCalling {
		t.Error("tool_calling should be true")
	}
	if !m.Streaming {
		t.Error("streaming should be true")
	}
	if m.Pricing == nil {
		t.Fatal("pricing should not be nil")
	}
	if m.Pricing.InputPer1MTokensUSD != 0.40 {
		t.Errorf("input pricing = %f, want 0.40", m.Pricing.InputPer1MTokensUSD)
	}
	if m.Pricing.OutputPer1MTokensUSD != 1.60 {
		t.Errorf("output pricing = %f, want 1.60", m.Pricing.OutputPer1MTokensUSD)
	}

	alias, ok := p.Aliases["gpt4-mini"]
	if !ok {
		t.Fatal("alias 'gpt4-mini' not found")
	}
	if alias != "gpt-4.1-mini" {
		t.Errorf("alias = %q, want %q", alias, "gpt-4.1-mini")
	}
}

func TestLoadCatalogFromBytes_DerivesKimiSubscriptionModels(t *testing.T) {
	cat, err := LoadCatalogFromBytes([]byte(`{"catalog_version":"1","providers":{"kimi":{"base_url":"https://metered","api_key_env":"MOONSHOT_API_KEY","models":{"kimi-k2.5":{"context_window":1}}},"kimi-subscription":{"base_url":"https://api.kimi.com/coding/v1","api_key_optional":true,"models_from":"kimi"}}}`))
	if err != nil {
		t.Fatalf("LoadCatalogFromBytes: %v", err)
	}
	if got := cat.Providers["kimi-subscription"].Models["kimi-k2.5"].ContextWindow; got != 1 {
		t.Fatalf("derived model context = %d", got)
	}
}

func TestLoadCatalogFromBytes_EmptyVersion(t *testing.T) {
	data := `{"catalog_version": "", "providers": {"p": {"base_url": "http://x", "api_key_env": "K", "models": {"m": {"context_window": 1}}}}}`
	_, err := LoadCatalogFromBytes([]byte(data))
	if err == nil {
		t.Fatal("expected error for empty catalog_version")
	}
}

func TestLoadCatalogFromBytes_NoProviders(t *testing.T) {
	data := `{"catalog_version": "1.0", "providers": {}}`
	_, err := LoadCatalogFromBytes([]byte(data))
	if err == nil {
		t.Fatal("expected error for no providers")
	}
}

func TestLoadCatalogFromBytes_MissingBaseURL(t *testing.T) {
	data := `{"catalog_version": "1.0", "providers": {"p": {"base_url": "", "api_key_env": "K", "models": {"m": {"context_window": 1}}}}}`
	_, err := LoadCatalogFromBytes([]byte(data))
	if err == nil {
		t.Fatal("expected error for missing base_url")
	}
}

func TestLoadCatalogFromBytes_MissingAPIKeyEnv(t *testing.T) {
	data := `{"catalog_version": "1.0", "providers": {"p": {"base_url": "http://x", "api_key_env": "", "models": {"m": {"context_window": 1}}}}}`
	_, err := LoadCatalogFromBytes([]byte(data))
	if err == nil {
		t.Fatal("expected error for missing api_key_env")
	}
}

func TestLoadCatalogFromBytes_NoModels(t *testing.T) {
	data := `{"catalog_version": "1.0", "providers": {"p": {"base_url": "http://x", "api_key_env": "K", "models": {}}}}`
	_, err := LoadCatalogFromBytes([]byte(data))
	if err == nil {
		t.Fatal("expected error for no models")
	}
}

func TestLoadCatalogFromBytes_ContextWindowZero(t *testing.T) {
	data := `{"catalog_version": "1.0", "providers": {"p": {"base_url": "http://x", "api_key_env": "K", "models": {"m": {"context_window": 0}}}}}`
	_, err := LoadCatalogFromBytes([]byte(data))
	if err == nil {
		t.Fatal("expected error for context_window <= 0")
	}
}

func TestLoadCatalogFromBytes_ContextWindowNegative(t *testing.T) {
	data := `{"catalog_version": "1.0", "providers": {"p": {"base_url": "http://x", "api_key_env": "K", "models": {"m": {"context_window": -1}}}}}`
	_, err := LoadCatalogFromBytes([]byte(data))
	if err == nil {
		t.Fatal("expected error for negative context_window")
	}
}

func TestLoadCatalogFromBytes_InvalidJSON(t *testing.T) {
	_, err := LoadCatalogFromBytes([]byte(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadCatalog_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	if err := os.WriteFile(path, []byte(validCatalogJSON()), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	cat, err := LoadCatalog(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cat.CatalogVersion != "1.0.0" {
		t.Errorf("catalog_version = %q, want %q", cat.CatalogVersion, "1.0.0")
	}
}

func TestLoadCatalog_EmptyPath(t *testing.T) {
	_, err := LoadCatalog("")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestLoadCatalog_MissingFile(t *testing.T) {
	_, err := LoadCatalog("/nonexistent/path/catalog.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadCatalogFromBytes_AllFieldsDeserialize(t *testing.T) {
	data := `{
		"catalog_version": "2.0",
		"providers": {
			"test": {
				"display_name": "Test Provider",
				"base_url": "https://test.api/v1",
				"api_key_env": "TEST_KEY",
				"protocol": "openai_compat",
				"quirks": ["quirk1", "quirk2"],
				"models": {
					"model-1": {
						"display_name": "Model One",
						"description": "A test model",
						"context_window": 128000,
						"max_output_tokens": 4096,
						"modalities": ["text", "image"],
						"tool_calling": true,
						"parallel_tool_calls": true,
						"streaming": true,
						"reasoning_mode": true,
						"strengths": ["fast", "accurate"],
						"weaknesses": ["expensive"],
						"best_for": ["coding"],
						"speed_tier": "fast",
						"cost_tier": "premium",
						"pricing": {
							"input_per_1m_tokens_usd": 1.50,
							"output_per_1m_tokens_usd": 5.00,
							"cache_read_per_1m_tokens_usd": 0.75,
							"cache_write_per_1m_tokens_usd": 1.50
						}
					}
				}
			}
		}
	}`

	cat, err := LoadCatalogFromBytes([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	p := cat.Providers["test"]
	if len(p.Quirks) != 2 || p.Quirks[0] != "quirk1" {
		t.Errorf("quirks = %v, want [quirk1 quirk2]", p.Quirks)
	}

	m := p.Models["model-1"]
	if m.MaxOutputTokens != 4096 {
		t.Errorf("max_output_tokens = %d, want 4096", m.MaxOutputTokens)
	}
	if len(m.Modalities) != 2 {
		t.Errorf("modalities count = %d, want 2", len(m.Modalities))
	}
	if !m.ParallelToolCalls {
		t.Error("parallel_tool_calls should be true")
	}
	if !m.ReasoningMode {
		t.Error("reasoning_mode should be true")
	}
	if len(m.Strengths) != 2 {
		t.Errorf("strengths = %v, want [fast accurate]", m.Strengths)
	}
	if len(m.Weaknesses) != 1 {
		t.Errorf("weaknesses = %v, want [expensive]", m.Weaknesses)
	}
	if len(m.BestFor) != 1 {
		t.Errorf("best_for = %v, want [coding]", m.BestFor)
	}
	if m.Pricing.CacheReadPer1MTokensUSD != 0.75 {
		t.Errorf("cache_read = %f, want 0.75", m.Pricing.CacheReadPer1MTokensUSD)
	}
	if m.Pricing.CacheWritePer1MTokensUSD != 1.50 {
		t.Errorf("cache_write = %f, want 1.50", m.Pricing.CacheWritePer1MTokensUSD)
	}
}

// TestLoadCatalogFromBytes_APIFieldParsed verifies that the api field is correctly
// deserialized for models that require the Responses API.
func TestLoadCatalogFromBytes_APIFieldParsed(t *testing.T) {
	data := `{
		"catalog_version": "1.0",
		"providers": {
			"openai": {
				"display_name": "OpenAI",
				"base_url": "https://api.openai.com/v1",
				"api_key_env": "OPENAI_API_KEY",
				"protocol": "openai_compat",
				"models": {
					"gpt-4.1-mini": {
						"display_name": "GPT-4.1 Mini",
						"description": "Standard chat model",
						"context_window": 1000000,
						"modalities": ["text"],
						"tool_calling": true,
						"streaming": true
					},
					"gpt-5.1-codex-mini": {
						"display_name": "GPT-5.1 Codex Mini",
						"description": "Codex model requiring Responses API",
						"context_window": 128000,
						"modalities": ["text"],
						"tool_calling": true,
						"streaming": true,
						"api": "responses"
					}
				}
			}
		}
	}`

	cat, err := LoadCatalogFromBytes([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	chatModel := cat.Providers["openai"].Models["gpt-4.1-mini"]
	if chatModel.API != "" {
		t.Errorf("gpt-4.1-mini: expected empty api field, got %q", chatModel.API)
	}

	codexModel := cat.Providers["openai"].Models["gpt-5.1-codex-mini"]
	if codexModel.API != "responses" {
		t.Errorf("gpt-5.1-codex-mini: expected api=%q, got %q", "responses", codexModel.API)
	}
}
