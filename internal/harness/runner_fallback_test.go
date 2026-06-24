package harness

// Fallback tests for the runtime provider fallback mechanism.
//
// NOTE: We cannot import fakeprovider here because that package imports
// harness, which would create an import cycle (harness ← fakeprovider ← harness).
// Instead, we define minimal inline stubs that replicate the behaviour needed
// for these specific test scenarios.

import (
	"context"
	"sync"
	"testing"

	"go-agent-harness/internal/provider/catalog"

	"github.com/stretchr/testify/require"
)

// scriptedFallbackProvider is a minimal harness.Provider stub that serves a
// fixed sequence of (result, error) pairs.  It counts calls and satisfies
// catalog.ProviderClient (interface{}).
type scriptedFallbackProvider struct {
	mu    sync.Mutex
	turns []fallbackTurn
	idx   int
}

type fallbackTurn struct {
	content string
	deltas  []CompletionDelta // emitted via req.Stream before returning
	err     error
}

func (p *scriptedFallbackProvider) Complete(_ context.Context, req CompletionRequest) (CompletionResult, error) {
	p.mu.Lock()
	i := p.idx
	if i < len(p.turns) {
		p.idx++
	}
	p.mu.Unlock()

	if i >= len(p.turns) {
		return CompletionResult{}, nil
	}
	turn := p.turns[i]

	// Emit deltas before returning.
	if req.Stream != nil {
		for _, d := range turn.deltas {
			req.Stream(d)
		}
	}

	if turn.err != nil {
		return CompletionResult{}, turn.err
	}
	return CompletionResult{Content: turn.content}, nil
}

func (p *scriptedFallbackProvider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.idx
}

// newFallbackTwoProviderRegistry builds a ProviderRegistry with two named
// providers ("primary" and "secondary") each backed by the given stubs.
func newFallbackTwoProviderRegistry(
	primaryStub, secondaryStub *scriptedFallbackProvider,
) *catalog.ProviderRegistry {
	cat := &catalog.Catalog{
		CatalogVersion: "1.0",
		Providers: map[string]catalog.ProviderEntry{
			"primary": {
				DisplayName: "primary",
				BaseURL:     "https://api.primary.example.com",
				APIKeyEnv:   "PRIMARY_API_KEY",
				Protocol:    "openai",
				Models: map[string]catalog.Model{
					"primary-model": {
						DisplayName:   "primary-model",
						ContextWindow: 128000,
						ToolCalling:   true,
						Streaming:     true,
					},
				},
			},
			"secondary": {
				DisplayName: "secondary",
				BaseURL:     "https://api.secondary.example.com",
				APIKeyEnv:   "SECONDARY_API_KEY",
				Protocol:    "openai",
				Models: map[string]catalog.Model{
					"secondary-model": {
						DisplayName:   "secondary-model",
						ContextWindow: 128000,
						ToolCalling:   true,
						Streaming:     true,
					},
				},
			},
		},
	}

	reg := catalog.NewProviderRegistryWithEnv(cat, func(key string) string {
		switch key {
		case "PRIMARY_API_KEY":
			return "sk-primary-fake"
		case "SECONDARY_API_KEY":
			return "sk-secondary-fake"
		}
		return ""
	})

	reg.SetClientFactory(func(_, _, providerName string) (catalog.ProviderClient, error) {
		switch providerName {
		case "primary":
			return primaryStub, nil
		case "secondary":
			return secondaryStub, nil
		}
		return nil, nil
	})

	return reg
}

// TestFallback_PrimaryRateLimited_SecondarySucceeds verifies case (1):
// primary returns 429, AllowFallback=true, FallbackProviders=["secondary"] →
// prompt.warning code=provider_fallback emitted, then run.completed (NOT
// run.failed); primary.Calls()==1, secondary.Calls()==1.
func TestFallback_PrimaryRateLimited_SecondarySucceeds(t *testing.T) {
	t.Parallel()

	primaryStub := &scriptedFallbackProvider{turns: []fallbackTurn{
		{err: &ProviderHTTPError{Provider: "primary", StatusCode: 429, Body: "rate limited"}},
	}}
	secondaryStub := &scriptedFallbackProvider{turns: []fallbackTurn{
		{content: "secondary response"},
	}}

	reg := newFallbackTwoProviderRegistry(primaryStub, secondaryStub)

	runner := NewRunner(primaryStub, NewRegistry(), RunnerConfig{
		DefaultModel:     "primary-model",
		MaxSteps:         2,
		ProviderRegistry: reg,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:            "hello",
		Model:             "primary-model",
		ProviderName:      "primary",
		AllowFallback:     true,
		FallbackProviders: []string{"secondary"},
	})
	require.NoError(t, err)

	events, err := collectRunEvents(t, runner, run.ID)
	require.NoError(t, err)

	// Run must complete (not fail).
	state, ok := runner.GetRun(run.ID)
	require.True(t, ok)
	require.Equal(t, RunStatusCompleted, state.Status, "run must complete when fallback succeeds; got events: %v", eventTypes(events))
	require.Equal(t, "secondary response", state.Output)

	// A prompt.warning with code=provider_fallback must appear.
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

	// provider_fallback warning must appear before run.completed.
	requireEventOrder(t, events, "prompt.warning", "run.completed")

	// Each provider called exactly once.
	require.Equal(t, 1, primaryStub.Calls(), "primary must be called exactly once")
	require.Equal(t, 1, secondaryStub.Calls(), "secondary must be called exactly once")
}

// TestFallback_AllowFallbackFalse_RunFails verifies case (2):
// AllowFallback=false → run.failed; secondary never called.
func TestFallback_AllowFallbackFalse_RunFails(t *testing.T) {
	t.Parallel()

	primaryStub := &scriptedFallbackProvider{turns: []fallbackTurn{
		{err: &ProviderHTTPError{Provider: "primary", StatusCode: 429, Body: "rate limited"}},
	}}
	secondaryStub := &scriptedFallbackProvider{turns: []fallbackTurn{
		{content: "secondary response"},
	}}

	reg := newFallbackTwoProviderRegistry(primaryStub, secondaryStub)

	runner := NewRunner(primaryStub, NewRegistry(), RunnerConfig{
		DefaultModel:     "primary-model",
		MaxSteps:         2,
		ProviderRegistry: reg,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:            "hello",
		Model:             "primary-model",
		ProviderName:      "primary",
		AllowFallback:     false,
		FallbackProviders: []string{"secondary"},
	})
	require.NoError(t, err)

	events, err := collectRunEvents(t, runner, run.ID)
	require.NoError(t, err)

	state, ok := runner.GetRun(run.ID)
	require.True(t, ok)
	require.Equal(t, RunStatusFailed, state.Status,
		"run must fail when AllowFallback=false; got events: %v", eventTypes(events))

	requireEventOrder(t, events, "run.failed")

	// Secondary must never be called.
	require.Equal(t, 0, secondaryStub.Calls(),
		"secondary must NOT be called when AllowFallback=false")
}

// TestFallback_MidStream_NoFallback verifies case (3):
// Primary emits a delta then errors → NO fallback (streaming-safety rule); run.failed.
func TestFallback_MidStream_NoFallback(t *testing.T) {
	t.Parallel()

	// Primary emits a content delta then returns a retryable error.
	primaryStub := &scriptedFallbackProvider{turns: []fallbackTurn{
		{
			deltas: []CompletionDelta{{Content: "partial content"}},
			err:    &ProviderHTTPError{Provider: "primary", StatusCode: 429, Body: "rate limited mid-stream"},
		},
	}}
	secondaryStub := &scriptedFallbackProvider{turns: []fallbackTurn{
		{content: "secondary response"},
	}}

	reg := newFallbackTwoProviderRegistry(primaryStub, secondaryStub)

	runner := NewRunner(primaryStub, NewRegistry(), RunnerConfig{
		DefaultModel:     "primary-model",
		MaxSteps:         2,
		ProviderRegistry: reg,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:            "hello",
		Model:             "primary-model",
		ProviderName:      "primary",
		AllowFallback:     true,
		FallbackProviders: []string{"secondary"},
	})
	require.NoError(t, err)

	events, err := collectRunEvents(t, runner, run.ID)
	require.NoError(t, err)

	state, ok := runner.GetRun(run.ID)
	require.True(t, ok)
	require.Equal(t, RunStatusFailed, state.Status,
		"run must fail when primary emits a delta before the error (streaming-safety); got events: %v", eventTypes(events))

	requireEventOrder(t, events, "run.failed")

	// Secondary must NOT be called.
	require.Equal(t, 0, secondaryStub.Calls(),
		"secondary must NOT be called when primary already emitted a streaming delta")
}

// TestFallback_NonEligibleStatus_NoFallback verifies case (4):
// Non-eligible status 400 + AllowFallback=true → run.failed (not masked).
func TestFallback_NonEligibleStatus_NoFallback(t *testing.T) {
	t.Parallel()

	primaryStub := &scriptedFallbackProvider{turns: []fallbackTurn{
		{err: &ProviderHTTPError{Provider: "primary", StatusCode: 400, Body: "bad request"}},
	}}
	secondaryStub := &scriptedFallbackProvider{turns: []fallbackTurn{
		{content: "secondary response"},
	}}

	reg := newFallbackTwoProviderRegistry(primaryStub, secondaryStub)

	runner := NewRunner(primaryStub, NewRegistry(), RunnerConfig{
		DefaultModel:     "primary-model",
		MaxSteps:         2,
		ProviderRegistry: reg,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:            "hello",
		Model:             "primary-model",
		ProviderName:      "primary",
		AllowFallback:     true,
		FallbackProviders: []string{"secondary"},
	})
	require.NoError(t, err)

	events, err := collectRunEvents(t, runner, run.ID)
	require.NoError(t, err)

	state, ok := runner.GetRun(run.ID)
	require.True(t, ok)
	require.Equal(t, RunStatusFailed, state.Status,
		"run must fail for non-eligible status 400 even with AllowFallback=true; got events: %v", eventTypes(events))

	requireEventOrder(t, events, "run.failed")

	// Secondary must NOT be called.
	require.Equal(t, 0, secondaryStub.Calls(),
		"secondary must NOT be called for non-eligible status 400")
}
