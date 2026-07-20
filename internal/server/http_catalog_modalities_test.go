package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-agent-harness/internal/provider/catalog"
)

// modalitiesTestCatalog builds a two-model catalog: one image-capable, one
// text-only.
func modalitiesTestCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat, err := catalog.LoadCatalogFromBytes([]byte(`{
		"catalog_version": "1",
		"providers": {
			"openai": {
				"display_name": "OpenAI",
				"base_url": "https://api.openai.com/v1",
				"api_key_env": "OPENAI_API_KEY",
				"protocol": "openai",
				"models": {
					"gpt-4.1": {"display_name": "GPT-4.1", "description": "vision", "context_window": 128000, "modalities": ["text", "image"], "tool_calling": true, "streaming": true}
				}
			},
			"anthropic": {
				"display_name": "Anthropic",
				"base_url": "https://api.anthropic.com",
				"api_key_env": "ANTHROPIC_API_KEY",
				"protocol": "anthropic",
				"models": {
					"claude-sonnet-4-6": {"display_name": "Claude Sonnet", "description": "text", "context_window": 200000, "modalities": ["text"], "tool_calling": true, "streaming": true}
				}
			}
		}
	}`))
	if err != nil {
		t.Fatalf("LoadCatalogFromBytes: %v", err)
	}
	return cat
}

func fetchModelsModalities(t *testing.T, handler http.Handler) map[string][]string {
	t.Helper()
	ts := httptest.NewServer(handler)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/models: status %d, want 200", res.StatusCode)
	}

	var resp struct {
		Models []ModelResponse `json:"models"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	out := make(map[string][]string, len(resp.Models))
	for _, m := range resp.Models {
		out[m.Provider+"/"+m.ID] = m.Modalities
	}
	return out
}

// TestHandleModels_IncludesModalities verifies /v1/models carries the
// catalog's modalities array so clients can pre-flight image input (epic
// #818 slice 2), for both the raw-catalog and provider-registry branches.
func TestHandleModels_IncludesModalities(t *testing.T) {
	assertModalities := func(t *testing.T, byModel map[string][]string) {
		t.Helper()
		got, ok := byModel["openai/gpt-4.1"]
		if !ok {
			t.Fatalf("gpt-4.1 missing from response: %v", byModel)
		}
		if len(got) != 2 || got[0] != "text" || got[1] != "image" {
			t.Errorf("gpt-4.1 modalities = %v, want [text image]", got)
		}
		got, ok = byModel["anthropic/claude-sonnet-4-6"]
		if !ok {
			t.Fatalf("claude-sonnet-4-6 missing from response: %v", byModel)
		}
		if len(got) != 1 || got[0] != "text" {
			t.Errorf("claude-sonnet-4-6 modalities = %v, want [text]", got)
		}
	}

	t.Run("catalog branch", func(t *testing.T) {
		handler := NewWithCatalog(testRunnerForModels(t), modalitiesTestCatalog(t))
		assertModalities(t, fetchModelsModalities(t, handler))
	})

	t.Run("provider registry branch", func(t *testing.T) {
		cat := modalitiesTestCatalog(t)
		handler := NewWithOptions(ServerOptions{
			Runner:           testRunnerForModels(t),
			Catalog:          cat,
			ProviderRegistry: catalog.NewProviderRegistry(cat),
		})
		assertModalities(t, fetchModelsModalities(t, handler))
	})
}
