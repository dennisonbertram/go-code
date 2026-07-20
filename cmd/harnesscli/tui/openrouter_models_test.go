package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-agent-harness/cmd/harnesscli/tui/components/modelswitcher"
)

// openRouterAPIResponse matches the shape returned by https://openrouter.ai/api/v1/models.
type openRouterAPIResponse struct {
	Data []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"data"`
}

// TestFetchOpenRouterModelsCmd_TransformsResponse verifies that the cmd correctly
// transforms an OpenRouter API response into ModelsFetchedMsg with Source "openrouter".
func TestFetchOpenRouterModelsCmd_TransformsResponse(t *testing.T) {
	// Set up a test HTTP server that mimics openrouter.ai/api/v1/models.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openRouterAPIResponse{
			Data: []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}{
				{ID: "openai/gpt-4.1", Name: "OpenAI: GPT-4.1"},
				{ID: "anthropic/claude-opus-4-6", Name: "Anthropic: Claude Opus 4.6"},
				{ID: "meta-llama/llama-3.3-70b-instruct", Name: "Meta: Llama 3.3 70B Instruct"},
				{ID: "somevendor/some-model", Name: ""}, // empty name — fallback to ID
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	// Patch the URL by swapping the real implementation with a version pointing at our test server.
	// Since fetchOpenRouterModelsCmd is a closure, we test it by monkey-patching the URL via
	// an alternative approach: run the cmd and capture the message.
	//
	// We need to temporarily override the base URL. The simplest approach is to directly
	// call an inline version of fetchOpenRouterModelsCmd that targets the test server.
	cmd := fetchOpenRouterModelsFromURL(srv.URL+"/api/v1/models", "test-key")
	msg := cmd()

	fetched, ok := msg.(ModelsFetchedMsg)
	if !ok {
		t.Fatalf("expected ModelsFetchedMsg, got %T: %v", msg, msg)
	}
	if fetched.Source != "openrouter" {
		t.Errorf("expected Source=openrouter, got %q", fetched.Source)
	}
	if len(fetched.Models) != 4 {
		t.Errorf("expected 4 models, got %d", len(fetched.Models))
	}

	// Verify transformations.
	byID := make(map[string]modelswitcher.ServerModelEntry, len(fetched.Models))
	for _, m := range fetched.Models {
		byID[m.ID] = m
	}

	openaiModel, ok := byID["openai/gpt-4.1"]
	if !ok {
		t.Fatal("openai/gpt-4.1 not found in results")
	}
	if openaiModel.Provider != "openai" {
		t.Errorf("expected provider=openai for openai/gpt-4.1, got %q", openaiModel.Provider)
	}
	if openaiModel.DisplayName != "OpenAI: GPT-4.1" {
		t.Errorf("expected DisplayName=OpenAI: GPT-4.1, got %q", openaiModel.DisplayName)
	}

	// Model with no name should fall back to ID.
	noNameModel, ok := byID["somevendor/some-model"]
	if !ok {
		t.Fatal("somevendor/some-model not found in results")
	}
	if noNameModel.DisplayName != "somevendor/some-model" {
		t.Errorf("expected DisplayName=somevendor/some-model for no-name entry, got %q", noNameModel.DisplayName)
	}
}

// TestFetchOpenRouterModelsCmd_HttpError verifies that a non-200 response emits ModelsFetchErrorMsg.
func TestFetchOpenRouterModelsCmd_HttpError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cmd := fetchOpenRouterModelsFromURL(srv.URL+"/api/v1/models", "")
	msg := cmd()

	errMsg, ok := msg.(ModelsFetchErrorMsg)
	if !ok {
		t.Fatalf("expected ModelsFetchErrorMsg, got %T", msg)
	}
	if errMsg.Err == "" {
		t.Error("expected non-empty error string")
	}
}

// TestFetchOpenRouterModelsCmd_DisplayNameSet verifies that all fetched models have
// DisplayName populated (either from the API "name" field or falling back to ID).
func TestFetchOpenRouterModelsCmd_DisplayNameSet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openRouterAPIResponse{
			Data: []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}{
				{ID: "x-ai/grok-3-mini", Name: "xAI: Grok 3 Mini"},
				{ID: "deepseek/deepseek-chat", Name: ""},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cmd := fetchOpenRouterModelsFromURL(srv.URL+"/api/v1/models", "")
	msg := cmd()

	fetched, ok := msg.(ModelsFetchedMsg)
	if !ok {
		t.Fatalf("expected ModelsFetchedMsg, got %T", msg)
	}

	for _, m := range fetched.Models {
		if m.DisplayName == "" {
			t.Errorf("model %q has empty DisplayName", m.ID)
		}
	}
}

// TestWithModels_UsesDisplayName verifies that WithModels uses the provided DisplayName
// from ServerModelEntry when it is non-empty, instead of falling back to the local map.
func TestWithModels_UsesDisplayName(t *testing.T) {
	switcher := modelswitcher.New("")

	serverModels := []modelswitcher.ServerModelEntry{
		{ID: "openai/gpt-4.1", Provider: "openai", DisplayName: "OpenAI: GPT-4.1"},
		{ID: "gpt-4.1-mini", Provider: "openai", DisplayName: ""},                // should use local map
		{ID: "unknown-model", Provider: "unknown", DisplayName: ""},              // should fall back to ID
		{ID: "custom/model", Provider: "custom", DisplayName: "My Custom Model"}, // explicit name
	}

	switcher = switcher.WithModels(serverModels)
	models := switcher.Models

	byID := make(map[string]modelswitcher.ModelEntry, len(models))
	for _, m := range models {
		byID[m.ID] = m
	}

	// Explicit display name from OpenRouter.
	if byID["openai/gpt-4.1"].DisplayName != "OpenAI: GPT-4.1" {
		t.Errorf("expected DisplayName=OpenAI: GPT-4.1, got %q", byID["openai/gpt-4.1"].DisplayName)
	}

	// Falls back to local modelDisplayNames map.
	if byID["gpt-4.1-mini"].DisplayName != "GPT-4.1 Mini" {
		t.Errorf("expected DisplayName=GPT-4.1 Mini from local map, got %q", byID["gpt-4.1-mini"].DisplayName)
	}

	// Falls back to raw ID.
	if byID["unknown-model"].DisplayName != "unknown-model" {
		t.Errorf("expected DisplayName=unknown-model (ID fallback), got %q", byID["unknown-model"].DisplayName)
	}

	// Custom model with provided name.
	if byID["custom/model"].DisplayName != "My Custom Model" {
		t.Errorf("expected DisplayName=My Custom Model, got %q", byID["custom/model"].DisplayName)
	}
}

// TestOpenRouterModels_AllAvailableWhenKeyConfigured verifies that when the OpenRouter
// key is configured, all OR models show as available regardless of provider prefix.
func TestOpenRouterModels_AllAvailableWhenKeyConfigured(t *testing.T) {
	switcher := modelswitcher.New("")

	serverModels := []modelswitcher.ServerModelEntry{
		{ID: "openai/gpt-4.1", Provider: "openai", DisplayName: "OpenAI: GPT-4.1"},
		{ID: "anthropic/claude-opus-4-6", Provider: "anthropic", DisplayName: "Anthropic: Claude Opus 4.6"},
		{ID: "meta-llama/llama-3.3-70b-instruct", Provider: "meta-llama", DisplayName: "Meta: Llama 3.3 70B"},
	}

	// Simulate the ModelsFetchedMsg handler: when source is "openrouter", availability
	// is tied to a single OR key check — not per provider.
	orKeySet := true
	switcher = switcher.WithModels(serverModels)
	switcher = switcher.WithAvailability(func(_ string) bool {
		return orKeySet
	})

	for _, m := range switcher.Models {
		if !m.Available {
			t.Errorf("model %q should be available when OR key is configured, but Available=false", m.ID)
		}
	}

	// Now simulate key NOT configured.
	orKeySet = false
	switcher2 := modelswitcher.New("")
	switcher2 = switcher2.WithModels(serverModels)
	switcher2 = switcher2.WithAvailability(func(_ string) bool {
		return orKeySet
	})

	for _, m := range switcher2.Models {
		if m.Available {
			t.Errorf("model %q should be unavailable when OR key is missing, but Available=true", m.ID)
		}
	}
}

// TestFetchOpenRouterModelsCmd_ReturnsCmd verifies that the public wrapper returns a non-nil Cmd.
func TestFetchOpenRouterModelsCmd_ReturnsCmd(t *testing.T) {
	cmd := fetchOpenRouterModelsCmd("")
	if cmd == nil {
		t.Fatal("fetchOpenRouterModelsCmd returned nil")
	}
}
