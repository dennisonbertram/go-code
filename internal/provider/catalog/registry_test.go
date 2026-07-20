package catalog

import (
	"fmt"
	"sync"
	"testing"

	"go-agent-harness/internal/provider"
)

func registryTestCatalog() *Catalog {
	return &Catalog{
		CatalogVersion: "v1-test",
		Providers: map[string]ProviderEntry{
			"openai": {
				DisplayName: "OpenAI",
				BaseURL:     "https://api.openai.com",
				APIKeyEnv:   "OPENAI_API_KEY",
				Protocol:    "openai",
				Models: map[string]Model{
					"gpt-4.1-mini": {
						DisplayName:   "GPT-4.1 Mini",
						ContextWindow: 128000,
						ToolCalling:   true,
						Streaming:     true,
					},
				},
			},
			"deepseek": {
				DisplayName: "DeepSeek",
				BaseURL:     "https://api.deepseek.com",
				APIKeyEnv:   "DEEPSEEK_API_KEY",
				Protocol:    "openai",
				Models: map[string]Model{
					"deepseek-chat": {
						DisplayName:   "DeepSeek Chat",
						ContextWindow: 64000,
						ToolCalling:   true,
						Streaming:     true,
					},
					"deepseek-reasoner": {
						DisplayName:   "DeepSeek Reasoner",
						ContextWindow: 64000,
						ToolCalling:   true,
						Streaming:     true,
					},
				},
				Aliases: map[string]string{
					"deepseek": "deepseek-chat",
				},
			},
		},
	}
}

func fakeGetenv(vals map[string]string) func(string) string {
	return func(key string) string {
		return vals[key]
	}
}

// stubClient is a test double that implements ProviderClient.
type stubClient struct {
	providerName string
}

func stubFactory(apiKey, baseURL, providerName string) (ProviderClient, error) {
	return &stubClient{providerName: providerName}, nil
}

func TestGetClient_KnownProvider(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistryWithEnv(cat, fakeGetenv(map[string]string{
		"OPENAI_API_KEY": "sk-test-key",
	}))
	reg.SetClientFactory(stubFactory)

	client, err := reg.GetClient("openai")
	if err != nil {
		t.Fatalf("GetClient(openai) error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestGetClient_UnknownProvider(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistryWithEnv(cat, fakeGetenv(map[string]string{}))
	reg.SetClientFactory(stubFactory)

	_, err := reg.GetClient("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestGetClient_MissingAPIKey(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistryWithEnv(cat, fakeGetenv(map[string]string{}))
	reg.SetClientFactory(stubFactory)

	_, err := reg.GetClient("openai")
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
}

func TestGetClient_NoFactory(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistryWithEnv(cat, fakeGetenv(map[string]string{
		"OPENAI_API_KEY": "sk-test-key",
	}))

	_, err := reg.GetClient("openai")
	if err == nil {
		t.Fatal("expected error when no factory is configured")
	}
}

func TestGetClientForModel_FindsCorrectProvider(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistryWithEnv(cat, fakeGetenv(map[string]string{
		"DEEPSEEK_API_KEY": "ds-test-key",
	}))
	reg.SetClientFactory(stubFactory)

	client, providerName, err := reg.GetClientForModel("deepseek-chat")
	if err != nil {
		t.Fatalf("GetClientForModel error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if providerName != "deepseek" {
		t.Fatalf("expected provider deepseek, got %q", providerName)
	}
}

func TestGetClientForModel_UnknownModel(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistryWithEnv(cat, fakeGetenv(map[string]string{}))
	reg.SetClientFactory(stubFactory)

	_, _, err := reg.GetClientForModel("unknown-model")
	if err == nil {
		t.Fatal("expected error for unknown model")
	}
}

func TestGetClientForModel_DynamicOpenRouterSlug(t *testing.T) {
	t.Parallel()

	cat := registryTestCatalog()
	cat.Providers["openrouter"] = ProviderEntry{
		DisplayName: "OpenRouter",
		APIKeyEnv:   "OPENROUTER_API_KEY",
		BaseURL:     "https://openrouter.ai/api/v1",
		Models: map[string]Model{
			"openai/gpt-4.1-mini": {ContextWindow: 128000},
		},
	}
	reg := NewProviderRegistryWithEnv(cat, fakeGetenv(map[string]string{
		"OPENROUTER_API_KEY": "or-test-key",
	}))
	reg.SetClientFactory(stubFactory)

	client, providerName, err := reg.GetClientForModel("moonshotai/kimi-k2.5")
	if err != nil {
		t.Fatalf("GetClientForModel error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if providerName != "openrouter" {
		t.Fatalf("expected provider openrouter, got %q", providerName)
	}
}

func TestResolveProvider_FindsCorrectProvider(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistry(cat)

	name, found := reg.ResolveProvider("gpt-4.1-mini")
	if !found {
		t.Fatal("expected to find provider for gpt-4.1-mini")
	}
	if name != "openai" {
		t.Fatalf("expected openai, got %q", name)
	}
}

func TestResolveProvider_FindsViaAlias(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistry(cat)

	name, found := reg.ResolveProvider("deepseek")
	if !found {
		t.Fatal("expected to find provider for deepseek alias")
	}
	if name != "deepseek" {
		t.Fatalf("expected deepseek, got %q", name)
	}
}

func TestResolveProvider_UnknownModel(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistry(cat)

	_, found := reg.ResolveProvider("nonexistent-model")
	if found {
		t.Fatal("expected not found for nonexistent model")
	}
}

func TestResolveProvider_DynamicOpenRouterSlug(t *testing.T) {
	t.Parallel()

	cat := registryTestCatalog()
	cat.Providers["openrouter"] = ProviderEntry{
		DisplayName: "OpenRouter",
		APIKeyEnv:   "OPENROUTER_API_KEY",
		BaseURL:     "https://openrouter.ai/api/v1",
		Models: map[string]Model{
			"openai/gpt-4.1-mini": {ContextWindow: 128000},
		},
	}
	reg := NewProviderRegistry(cat)

	name, found := reg.ResolveProvider("moonshotai/kimi-k2.5")
	if !found {
		t.Fatal("expected to find provider for dynamic openrouter slug")
	}
	if name != "openrouter" {
		t.Fatalf("expected openrouter, got %q", name)
	}
}

func TestGetClient_ThreadSafety(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistryWithEnv(cat, fakeGetenv(map[string]string{
		"OPENAI_API_KEY":   "sk-test-key",
		"DEEPSEEK_API_KEY": "ds-test-key",
	}))
	reg.SetClientFactory(stubFactory)

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := reg.GetClient("openai")
			if err != nil {
				errs <- err
			}
		}()
		go func() {
			defer wg.Done()
			_, err := reg.GetClient("deepseek")
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent GetClient error: %v", err)
	}
}

func TestGetClient_LazyInitialization(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistryWithEnv(cat, fakeGetenv(map[string]string{
		"OPENAI_API_KEY": "sk-test-key",
	}))
	reg.SetClientFactory(stubFactory)

	client1, err := reg.GetClient("openai")
	if err != nil {
		t.Fatalf("first GetClient error: %v", err)
	}
	client2, err := reg.GetClient("openai")
	if err != nil {
		t.Fatalf("second GetClient error: %v", err)
	}
	if client1 != client2 {
		t.Fatal("expected same client instance on second call (lazy init)")
	}
}

func TestCatalogAccessor(t *testing.T) {
	t.Parallel()

	cat := registryTestCatalog()
	reg := NewProviderRegistry(cat)

	got := reg.Catalog()
	if got != cat {
		t.Fatal("expected Catalog() to return same pointer")
	}
	if got.CatalogVersion != "v1-test" {
		t.Fatalf("expected v1-test, got %q", got.CatalogVersion)
	}
}

func TestCatalogAccessorNil(t *testing.T) {
	t.Parallel()

	reg := NewProviderRegistry(nil)
	if reg.Catalog() != nil {
		t.Fatal("expected nil catalog")
	}
}

func TestMaxContextTokens_KnownModel(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistry(cat)

	tokens, found := reg.MaxContextTokens("gpt-4.1-mini")
	if !found {
		t.Fatal("expected to find context tokens for gpt-4.1-mini")
	}
	if tokens != 128000 {
		t.Errorf("MaxContextTokens = %d, want 128000", tokens)
	}
}

func TestMaxContextTokens_KnownModelDeepSeek(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistry(cat)

	tokens, found := reg.MaxContextTokens("deepseek-chat")
	if !found {
		t.Fatal("expected to find context tokens for deepseek-chat")
	}
	if tokens != 64000 {
		t.Errorf("MaxContextTokens = %d, want 64000", tokens)
	}
}

func TestMaxContextTokens_ViaAlias(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistry(cat)

	// "deepseek" is an alias for "deepseek-chat" (context_window=64000).
	tokens, found := reg.MaxContextTokens("deepseek")
	if !found {
		t.Fatal("expected to find context tokens via alias")
	}
	if tokens != 64000 {
		t.Errorf("MaxContextTokens via alias = %d, want 64000", tokens)
	}
}

func TestMaxContextTokens_UnknownModel(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistry(cat)

	_, found := reg.MaxContextTokens("nonexistent-model-xyz")
	if found {
		t.Fatal("expected not found for nonexistent model")
	}
}

func TestMaxContextTokens_NilRegistry(t *testing.T) {
	t.Parallel()

	var reg *ProviderRegistry
	tokens, found := reg.MaxContextTokens("gpt-4.1-mini")
	if found {
		t.Fatal("expected not found for nil registry")
	}
	if tokens != 0 {
		t.Errorf("expected 0 tokens for nil registry, got %d", tokens)
	}
}

func TestMaxContextTokens_NilCatalog(t *testing.T) {
	t.Parallel()

	reg := NewProviderRegistry(nil)
	tokens, found := reg.MaxContextTokens("gpt-4.1-mini")
	if found {
		t.Fatal("expected not found for nil catalog")
	}
	if tokens != 0 {
		t.Errorf("expected 0 tokens for nil catalog, got %d", tokens)
	}
}

func TestSetTokenSourceConfiguresProviderPropagatesToFactoryAndEvictsClient(t *testing.T) {
	t.Parallel()

	reg := NewProviderRegistryWithEnv(registryTestCatalog(), fakeGetenv(map[string]string{}))
	var seen []provider.TokenSource
	reg.SetClientFactory(func(_ string, _ string, providerName string, tokenSource provider.TokenSource) (ProviderClient, error) {
		seen = append(seen, tokenSource)
		return &stubClient{providerName: providerName}, nil
	})

	firstSource := provider.StaticToken("fake-first-source")
	reg.SetTokenSource("openai", firstSource)
	if !reg.IsConfigured("openai") {
		t.Fatal("provider with token source is not configured")
	}
	firstClient, err := reg.GetClient("openai")
	if err != nil {
		t.Fatalf("GetClient() error: %v", err)
	}
	if len(seen) != 1 || seen[0] != firstSource {
		t.Fatal("client factory did not receive the registered token source")
	}

	secondSource := provider.StaticToken("fake-second-source")
	reg.SetTokenSource("openai", secondSource)
	secondClient, err := reg.GetClient("openai")
	if err != nil {
		t.Fatalf("GetClient() after source replacement error: %v", err)
	}
	if firstClient == secondClient {
		t.Fatal("SetTokenSource did not evict the cached client")
	}
	if len(seen) != 2 || seen[1] != secondSource {
		t.Fatal("client factory did not receive the replacement token source")
	}
}

func TestSetAPIKey_OverridesEnv(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	// Environment has no keys set.
	reg := NewProviderRegistryWithEnv(cat, fakeGetenv(map[string]string{}))
	reg.SetClientFactory(stubFactory)

	// Without override, IsConfigured returns false.
	if reg.IsConfigured("openai") {
		t.Fatal("expected openai not configured before override")
	}

	// Set override key.
	reg.SetAPIKey("openai", "sk-override")

	// Now IsConfigured returns true.
	if !reg.IsConfigured("openai") {
		t.Fatal("expected openai configured after override")
	}

	// GetClient should succeed using the override key.
	client, err := reg.GetClient("openai")
	if err != nil {
		t.Fatalf("GetClient with override: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client with override key")
	}
}

func TestSetAPIKey_EvictsCachedClient(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistryWithEnv(cat, fakeGetenv(map[string]string{
		"OPENAI_API_KEY": "sk-env-key",
	}))
	reg.SetClientFactory(stubFactory)

	client1, err := reg.GetClient("openai")
	if err != nil {
		t.Fatalf("first GetClient: %v", err)
	}

	// Override key evicts cached client.
	reg.SetAPIKey("openai", "sk-new-key")

	client2, err := reg.GetClient("openai")
	if err != nil {
		t.Fatalf("second GetClient: %v", err)
	}

	// Should be a different client instance since cache was evicted.
	if client1 == client2 {
		t.Fatal("expected different client after SetAPIKey (cache should be evicted)")
	}
}

func TestSetAPIKey_Concurrent(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistryWithEnv(cat, fakeGetenv(map[string]string{}))
	reg.SetClientFactory(stubFactory)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			reg.SetAPIKey("openai", fmt.Sprintf("key-%d", i))
		}(i)
		go func() {
			defer wg.Done()
			reg.IsConfigured("openai")
		}()
	}
	wg.Wait()
}

func TestIsConfigured_EnvFallback(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistryWithEnv(cat, fakeGetenv(map[string]string{
		"OPENAI_API_KEY": "sk-from-env",
	}))

	if !reg.IsConfigured("openai") {
		t.Fatal("expected openai configured via env")
	}
	if reg.IsConfigured("deepseek") {
		t.Fatal("expected deepseek not configured (no env key)")
	}
}

func TestCanonicalModelForProvider_StripsMatchingPrefix(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistry(cat)

	// An OpenRouter-qualified slug whose prefix matches the target provider.
	got := reg.CanonicalModelForProvider("deepseek/deepseek-v4-flash", "deepseek")
	if got != "deepseek-v4-flash" {
		t.Errorf("CanonicalModelForProvider = %q, want deepseek-v4-flash", got)
	}
}

func TestCanonicalModelForProvider_NonMatchingPrefix(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistry(cat)

	// An OpenRouter-qualified slug whose prefix does NOT match the target provider.
	got := reg.CanonicalModelForProvider("openai/gpt-4.1", "deepseek")
	if got != "openai/gpt-4.1" {
		t.Errorf("CanonicalModelForProvider = %q, want unchanged for non-matching prefix", got)
	}
}

func TestCanonicalModelForProvider_OpenRouterPreservesSlug(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistry(cat)

	// OpenRouter provider always preserves qualified slugs.
	got := reg.CanonicalModelForProvider("deepseek/deepseek-v4-flash", "openrouter")
	if got != "deepseek/deepseek-v4-flash" {
		t.Errorf("CanonicalModelForProvider for openrouter = %q, want unchanged", got)
	}
}

func TestCanonicalModelForProvider_BareIDPassesThrough(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistry(cat)

	// A bare (non-qualified) model ID is returned unchanged.
	got := reg.CanonicalModelForProvider("gpt-4.1-mini", "openai")
	if got != "gpt-4.1-mini" {
		t.Errorf("CanonicalModelForProvider bare ID = %q, want gpt-4.1-mini", got)
	}
}

func TestCanonicalModelForProvider_AliasResolution(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistry(cat)

	// Bare ID that is an alias in the provider's catalog entry.
	got := reg.CanonicalModelForProvider("deepseek", "deepseek")
	if got != "deepseek-chat" {
		t.Errorf("CanonicalModelForProvider alias = %q, want deepseek-chat", got)
	}
}

func TestCanonicalModelForProvider_EmptyInput(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistry(cat)

	got := reg.CanonicalModelForProvider("", "deepseek")
	if got != "" {
		t.Errorf("CanonicalModelForProvider empty model = %q, want empty", got)
	}
}

func TestCanonicalModelForProvider_XAIPrefixAlias(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	cat.Providers["xai"] = ProviderEntry{
		DisplayName: "xAI",
		Models: map[string]Model{
			"grok-3-mini": {ContextWindow: 131072},
		},
	}
	reg := NewProviderRegistry(cat)

	// x-ai prefix is a known alias for xai provider.
	got := reg.CanonicalModelForProvider("x-ai/grok-3-mini", "xai")
	if got != "grok-3-mini" {
		t.Errorf("CanonicalModelForProvider x-ai prefix = %q, want grok-3-mini", got)
	}
}

func TestCanonicalModelForProvider_GooglePrefixForGemini(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	cat.Providers["gemini"] = ProviderEntry{
		DisplayName: "Google Gemini",
		Models: map[string]Model{
			"gemini-2.5-flash": {ContextWindow: 1048576},
		},
	}
	reg := NewProviderRegistry(cat)

	// google prefix is a known alias for gemini provider.
	got := reg.CanonicalModelForProvider("google/gemini-2.5-flash", "gemini")
	if got != "gemini-2.5-flash" {
		t.Errorf("CanonicalModelForProvider google prefix = %q, want gemini-2.5-flash", got)
	}
}

func TestIsConfigured_UnknownProvider(t *testing.T) {
	t.Parallel()
	cat := registryTestCatalog()
	reg := NewProviderRegistryWithEnv(cat, fakeGetenv(map[string]string{}))

	if reg.IsConfigured("nonexistent") {
		t.Fatal("expected false for unknown provider")
	}
}
