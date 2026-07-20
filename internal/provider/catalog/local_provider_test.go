package catalog_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/provider"
	"go-agent-harness/internal/provider/catalog"
	"go-agent-harness/internal/provider/openai"
)

func fakeGetenv(vals map[string]string) func(string) string {
	return func(key string) string {
		return vals[key]
	}
}

func localProvidersCatalog(baseURL string) *catalog.Catalog {
	return &catalog.Catalog{
		CatalogVersion: "v1-test",
		Providers: map[string]catalog.ProviderEntry{
			"ollama": {
				DisplayName:    "Ollama",
				BaseURL:        baseURL,
				APIKeyEnv:      "",
				APIKeyOptional: true,
				Protocol:       "openai_compat",
				Models: map[string]catalog.Model{
					"llama3.1:8b": {
						DisplayName:   "Llama 3.1 8B",
						ContextWindow: 128000,
						ToolCalling:   true,
						Streaming:     true,
						Modalities:    []string{"text"},
					},
				},
				Aliases: map[string]string{
					"llama": "llama3.1:8b",
				},
			},
			"lmstudio": {
				DisplayName:    "LM Studio",
				BaseURL:        baseURL,
				APIKeyEnv:      "",
				APIKeyOptional: true,
				Protocol:       "openai_compat",
				Models: map[string]catalog.Model{
					"llama-3.1-8b-instruct": {
						DisplayName:   "Llama 3.1 8B Instruct",
						ContextWindow: 128000,
						ToolCalling:   true,
						Streaming:     true,
						Modalities:    []string{"text"},
					},
				},
			},
		},
	}
}

type stubClient struct {
	providerName string
}

func stubFactory(apiKey, baseURL, providerName string) (catalog.ProviderClient, error) {
	return &stubClient{providerName: providerName}, nil
}

func openAICompatLocalHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "llama3.1:8b", "object": "model"},
				},
			})
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{"message": map[string]any{"content": "Hello from local model"}},
				},
				"usage": map[string]any{
					"prompt_tokens":     10,
					"completion_tokens": 5,
					"total_tokens":      15,
				},
			})
		default:
			t.Logf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
}

func newOpenAIClientFactory(t *testing.T) catalog.ClientFactory {
	t.Helper()
	return func(apiKey, baseURL, providerName string, tokenSource provider.TokenSource) (catalog.ProviderClient, error) {
		if apiKey == "" {
			t.Errorf("expected non-empty API key for optional local provider, got empty")
		}
		client, err := openai.NewClient(openai.Config{
			APIKey:       apiKey,
			TokenSource:  tokenSource,
			BaseURL:      baseURL,
			ProviderName: providerName,
		})
		return client, err
	}
}

func TestLocalProvider_ResolvesWithoutAPIKey(t *testing.T) {
	t.Parallel()

	cat := localProvidersCatalog("http://localhost:11434/v1")
	reg := catalog.NewProviderRegistryWithEnv(cat, fakeGetenv(map[string]string{}))

	tests := []struct {
		modelID      string
		wantProvider string
	}{
		{"llama3.1:8b", "ollama"},
		{"llama", "ollama"},
		{"llama-3.1-8b-instruct", "lmstudio"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.modelID, func(t *testing.T) {
			t.Parallel()
			provider, found := reg.ResolveProvider(tc.modelID)
			if !found {
				t.Fatalf("ResolveProvider(%q) not found", tc.modelID)
			}
			if provider != tc.wantProvider {
				t.Fatalf("ResolveProvider(%q) = %q, want %q", tc.modelID, provider, tc.wantProvider)
			}
			if !reg.IsConfigured(provider) {
				t.Fatalf("IsConfigured(%q) = false, want true", provider)
			}
		})
	}
}

func TestLocalProvider_RequestSucceedsAgainstFakeServer(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(openAICompatLocalHandler(t))
	defer server.Close()

	cat := localProvidersCatalog(server.URL + "/v1")
	reg := catalog.NewProviderRegistryWithEnv(cat, fakeGetenv(map[string]string{}))
	reg.SetClientFactory(newOpenAIClientFactory(t))

	client, err := reg.GetClient("ollama")
	if err != nil {
		t.Fatalf("GetClient(ollama): %v", err)
	}

	openaiClient, ok := client.(*openai.Client)
	if !ok {
		t.Fatalf("expected *openai.Client, got %T", client)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := openaiClient.Complete(ctx, harness.CompletionRequest{
		Model:    "llama3.1:8b",
		Messages: []harness.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Content == "" {
		t.Fatal("expected non-empty completion content")
	}
}

func TestLocalProvider_UnreachableServerYieldsActionableError(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	listener.Close()

	baseURL := "http://" + addr + "/v1"
	cat := localProvidersCatalog(baseURL)
	reg := catalog.NewProviderRegistryWithEnv(cat, fakeGetenv(map[string]string{}))
	reg.SetClientFactory(stubFactory)

	_, err = reg.GetClient("ollama")
	if err == nil {
		t.Fatal("expected error for unreachable Ollama server")
	}
	if !strings.Contains(err.Error(), "no Ollama server reachable") {
		t.Fatalf("expected actionable unreachable error, got %v", err)
	}
	if !strings.Contains(err.Error(), "`ollama serve`") {
		t.Fatalf("expected error to mention `ollama serve`, got %v", err)
	}
}

func TestLocalProvider_LMStudioRequestSucceedsAgainstFakeServer(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(openAICompatLocalHandler(t))
	defer server.Close()

	cat := localProvidersCatalog(server.URL + "/v1")
	reg := catalog.NewProviderRegistryWithEnv(cat, fakeGetenv(map[string]string{}))
	reg.SetClientFactory(newOpenAIClientFactory(t))

	client, err := reg.GetClient("lmstudio")
	if err != nil {
		t.Fatalf("GetClient(lmstudio): %v", err)
	}

	openaiClient, ok := client.(*openai.Client)
	if !ok {
		t.Fatalf("expected *openai.Client, got %T", client)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := openaiClient.Complete(ctx, harness.CompletionRequest{
		Model:    "llama-3.1-8b-instruct",
		Messages: []harness.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Content == "" {
		t.Fatal("expected non-empty completion content")
	}
}

func TestLocalProvider_LMStudioUnreachableYieldsActionableError(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	listener.Close()

	baseURL := "http://" + addr + "/v1"
	cat := localProvidersCatalog(baseURL)
	reg := catalog.NewProviderRegistryWithEnv(cat, fakeGetenv(map[string]string{}))
	reg.SetClientFactory(stubFactory)

	_, err = reg.GetClient("lmstudio")
	if err == nil {
		t.Fatal("expected error for unreachable LM Studio server")
	}
	if !strings.Contains(err.Error(), "no LM Studio server reachable") {
		t.Fatalf("expected actionable unreachable error, got %v", err)
	}
	if !strings.Contains(err.Error(), "is LM Studio running") {
		t.Fatalf("expected error to mention LM Studio, got %v", err)
	}
}
