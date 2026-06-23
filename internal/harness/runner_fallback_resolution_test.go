package harness

// Resolution-time fallback tests for provider selection.
//
// This file guards the EXISTING resolution-time fallback behavior (it must be
// unchanged by the new runtime-fallback work). The runtime-fallback cases
// already live in runner_fallback_test.go.
//
// NOTE: We cannot import fakeprovider here because that package imports
// harness, which would create an import cycle. Instead, we define minimal
// inline stubs that replicate the behaviour needed for these specific test
// scenarios.

import (
	"context"
	"sync"
	"testing"

	"go-agent-harness/internal/provider/catalog"

	"github.com/stretchr/testify/require"
)

// resolutionTestProvider is a minimal harness.Provider stub for resolution tests.
// It tracks how many times it was called.
type resolutionTestProvider struct {
	mu        sync.Mutex
	callCount int
}

func (p *resolutionTestProvider) Complete(_ context.Context, req CompletionRequest) (CompletionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callCount++
	return CompletionResult{Content: "test response"}, nil
}

func (p *resolutionTestProvider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callCount
}

// TestResolution_PreferredUnavailable_AllowFallbackTrue verifies:
// Preferred provider unavailable (provider_name set to a name not in the registry / not
// implementing Provider) + AllowFallback:true -> the run resolves to a fallback
// and emits a prompt.warning with code "provider_fallback", and the run still completes.
// When no model is available in the registry, it falls back to the default provider.
func TestResolution_PreferredUnavailable_AllowFallbackTrue(t *testing.T) {
	t.Parallel()

	defaultProvider := &resolutionTestProvider{}

	// Create a catalog with a provider that has a DIFFERENT model,
	// so when preferred provider is unavailable, GetClientForModel("test-model")
	// will fail and trigger fallback to default provider.
	cat := &catalog.Catalog{
		CatalogVersion: "1.0",
		Providers: map[string]catalog.ProviderEntry{
			"available-provider": {
				DisplayName: "available-provider",
				BaseURL:     "https://api.example.com",
				APIKeyEnv:   "TEST_API_KEY",
				Protocol:    "openai",
				Models: map[string]catalog.Model{
					"other-model": {
						DisplayName:   "other-model",
						ContextWindow: 128000,
						ToolCalling:   true,
						Streaming:     true,
					},
				},
			},
		},
	}

	reg := catalog.NewProviderRegistryWithEnv(cat, func(key string) string {
		if key == "TEST_API_KEY" {
			return "sk-test-fake"
		}
		return ""
	})

	reg.SetClientFactory(func(_, _, providerName string) (catalog.ProviderClient, error) {
		if providerName == "available-provider" {
			return &resolutionTestProvider{}, nil
		}
		return nil, nil
	})

	runner := NewRunner(defaultProvider, NewRegistry(), RunnerConfig{
		DefaultModel:     "test-model",
		MaxSteps:         2,
		ProviderRegistry: reg,
	})

	// Request a provider that does not exist in the registry, with a model not in the registry
	run, err := runner.StartRun(RunRequest{
		Prompt:        "hello",
		Model:         "test-model", // This model is not in the registry
		ProviderName:  "nonexistent-provider",
		AllowFallback: true,
	})
	require.NoError(t, err)

	events, err := collectRunEvents(t, runner, run.ID)
	require.NoError(t, err)

	// Run must complete (not fail).
	state, ok := runner.GetRun(run.ID)
	require.True(t, ok)
	require.Equal(t, RunStatusCompleted, state.Status,
		"run must complete when preferred provider unavailable + AllowFallback:true; got events: %v", eventTypes(events))

	// At least one prompt.warning with code=provider_fallback must appear
	// (may be two: one for preferred provider, one for model).
	var foundFallbackWarning bool
	for _, ev := range events {
		if ev.Type == EventPromptWarning {
			if code, _ := ev.Payload["code"].(string); code == "provider_fallback" {
				foundFallbackWarning = true
			}
		}
	}
	require.True(t, foundFallbackWarning,
		"expected prompt.warning with code=provider_fallback; got events: %v", eventTypes(events))

	// The default provider should have been used.
	require.Equal(t, 1, defaultProvider.Calls(), "default provider must be used")
}

// TestResolution_PreferredUnavailable_AllowFallbackFalse verifies:
// Preferred provider unavailable + AllowFallback:false -> the run errors/fails at resolution
// (unchanged behavior).
func TestResolution_PreferredUnavailable_AllowFallbackFalse(t *testing.T) {
	t.Parallel()

	defaultProvider := &resolutionTestProvider{}
	registryProvider := &resolutionTestProvider{}

	cat := &catalog.Catalog{
		CatalogVersion: "1.0",
		Providers: map[string]catalog.ProviderEntry{
			"available-provider": {
				DisplayName: "available-provider",
				BaseURL:     "https://api.example.com",
				APIKeyEnv:   "TEST_API_KEY",
				Protocol:    "openai",
				Models: map[string]catalog.Model{
					"test-model": {
						DisplayName:   "test-model",
						ContextWindow: 128000,
						ToolCalling:   true,
						Streaming:     true,
					},
				},
			},
		},
	}

	reg := catalog.NewProviderRegistryWithEnv(cat, func(key string) string {
		if key == "TEST_API_KEY" {
			return "sk-test-fake"
		}
		return ""
	})

	reg.SetClientFactory(func(_, _, providerName string) (catalog.ProviderClient, error) {
		if providerName == "available-provider" {
			return registryProvider, nil
		}
		return nil, nil
	})

	runner := NewRunner(defaultProvider, NewRegistry(), RunnerConfig{
		DefaultModel:     "test-model",
		MaxSteps:         2,
		ProviderRegistry: reg,
	})

	// Request a provider that does not exist in the registry, with AllowFallback:false
	run, err := runner.StartRun(RunRequest{
		Prompt:        "hello",
		Model:         "test-model",
		ProviderName:  "nonexistent-provider",
		AllowFallback: false,
	})
	require.NoError(t, err)

	events, err := collectRunEvents(t, runner, run.ID)
	require.NoError(t, err)

	// Run must fail at resolution.
	state, ok := runner.GetRun(run.ID)
	require.True(t, ok)
	require.Equal(t, RunStatusFailed, state.Status,
		"run must fail when preferred provider unavailable + AllowFallback:false; got events: %v", eventTypes(events))

	requireEventOrder(t, events, "run.failed")

	// Neither provider should have been called (run failed at resolution).
	require.Equal(t, 0, defaultProvider.Calls())
	require.Equal(t, 0, registryProvider.Calls())
}

// TestResolution_Consistency verifies that resolveProviderCandidates index 0
// matches resolveProvider for the same inputs. Tests three cases:
// (a) preferred set + available
// (b) preferred unavailable + fallback enabled
// (c) no preferred provider
func TestResolution_Consistency(t *testing.T) {
	t.Parallel()

	defaultProvider := &resolutionTestProvider{}
	registryProvider := &resolutionTestProvider{}

	cat := &catalog.Catalog{
		CatalogVersion: "1.0",
		Providers: map[string]catalog.ProviderEntry{
			"registry-provider": {
				DisplayName: "registry-provider",
				BaseURL:     "https://api.example.com",
				APIKeyEnv:   "TEST_API_KEY",
				Protocol:    "openai",
				Models: map[string]catalog.Model{
					"test-model": {
						DisplayName:   "test-model",
						ContextWindow: 128000,
						ToolCalling:   true,
						Streaming:     true,
					},
				},
			},
		},
	}

	reg := catalog.NewProviderRegistryWithEnv(cat, func(key string) string {
		if key == "TEST_API_KEY" {
			return "sk-test-fake"
		}
		return ""
	})

	reg.SetClientFactory(func(_, _, providerName string) (catalog.ProviderClient, error) {
		if providerName == "registry-provider" {
			return registryProvider, nil
		}
		return nil, nil
	})

	runner := NewRunner(defaultProvider, NewRegistry(), RunnerConfig{
		DefaultModel:     "test-model",
		MaxSteps:         2,
		ProviderRegistry: reg,
	})

	// Case (a): preferred provider set and available
	t.Run("preferred_available", func(t *testing.T) {
		p, name, err := runner.resolveProvider("test-run-a", "test-model", "registry-provider", true)
		require.NoError(t, err)
		require.Equal(t, "registry-provider", name)
		require.Equal(t, registryProvider, p)

		// resolveProviderCandidates should have the same provider at index 0
		candidates, err := runner.resolveProviderCandidates("test-run-a", "test-model", "registry-provider", true, nil)
		require.NoError(t, err)
		require.NotEmpty(t, candidates)
		require.Equal(t, name, candidates[0].Name)
		require.Equal(t, p, candidates[0].Provider)
	})

	// Case (b): preferred unavailable but fallback enabled
	// To test fallback to default, we need a model that doesn't exist in registry
	t.Run("preferred_unavailable_fallback", func(t *testing.T) {
		p, name, err := runner.resolveProvider("test-run-b", "nonexistent-model", "nonexistent", true)
		require.NoError(t, err)
		require.Equal(t, "default", name)
		require.Equal(t, defaultProvider, p)

		// resolveProviderCandidates should have the same provider at index 0
		candidates, err := runner.resolveProviderCandidates("test-run-b", "nonexistent-model", "nonexistent", true, nil)
		require.NoError(t, err)
		require.NotEmpty(t, candidates)
		require.Equal(t, name, candidates[0].Name)
		require.Equal(t, p, candidates[0].Provider)
	})

	// Case (c): no preferred provider specified
	t.Run("no_preferred", func(t *testing.T) {
		p, name, err := runner.resolveProvider("test-run-c", "test-model", "", true)
		require.NoError(t, err)
		require.Equal(t, "registry-provider", name)
		require.Equal(t, registryProvider, p)

		// resolveProviderCandidates should have the same provider at index 0
		candidates, err := runner.resolveProviderCandidates("test-run-c", "test-model", "", true, nil)
		require.NoError(t, err)
		require.NotEmpty(t, candidates)
		require.Equal(t, name, candidates[0].Name)
		require.Equal(t, p, candidates[0].Provider)
	})
}
