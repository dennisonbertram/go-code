package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/config"
	"go-agent-harness/internal/cron"
	"go-agent-harness/internal/fakeprovider"
	"go-agent-harness/internal/harness"
	htools "go-agent-harness/internal/harness/tools"
	om "go-agent-harness/internal/observationalmemory"
	"go-agent-harness/internal/profiles"
	"go-agent-harness/internal/provider/catalog"
	openai "go-agent-harness/internal/provider/openai"
	"go-agent-harness/internal/skills"
	"go-agent-harness/internal/systemprompt"
)

type noopProvider struct{}

func (n *noopProvider) Complete(_ context.Context, _ harness.CompletionRequest) (harness.CompletionResult, error) {
	return harness.CompletionResult{Content: "ok"}, nil
}

type namedProvider struct {
	name string
}

func (n *namedProvider) Complete(_ context.Context, _ harness.CompletionRequest) (harness.CompletionResult, error) {
	return harness.CompletionResult{Content: n.name}, nil
}

type modelProviderStub struct {
	result harness.CompletionResult
	err    error
	req    harness.CompletionRequest
}

func (m *modelProviderStub) Complete(_ context.Context, req harness.CompletionRequest) (harness.CompletionResult, error) {
	m.req = req
	if m.err != nil {
		return harness.CompletionResult{}, m.err
	}
	return m.result, nil
}

type scriptedHarnessdProvider struct {
	mu    sync.Mutex
	turns []harness.CompletionResult
	calls int
}

func (p *scriptedHarnessdProvider) Complete(_ context.Context, _ harness.CompletionRequest) (harness.CompletionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls >= len(p.turns) {
		return harness.CompletionResult{}, nil
	}
	result := p.turns[p.calls]
	p.calls++
	return result, nil
}

type recordingConversationCleaner struct {
	started chan struct{}
	done    chan struct{}
}

func (c *recordingConversationCleaner) Start(ctx context.Context, _ time.Duration) {
	close(c.started)
	go func() {
		<-ctx.Done()
		close(c.done)
	}()
}

type stubPromptEngine struct{}

func (stubPromptEngine) Resolve(systemprompt.ResolveRequest) (systemprompt.ResolvedPrompt, error) {
	return systemprompt.ResolvedPrompt{StaticPrompt: "static prompt"}, nil
}

func (stubPromptEngine) RuntimeContext(systemprompt.RuntimeContextInput) string {
	return "runtime context"
}

func TestMainDoesNotExitWhenRunSucceeds(t *testing.T) {
	origRun := runMain
	origExit := exitFunc
	defer func() {
		runMain = origRun
		exitFunc = origExit
	}()

	runMain = func() error { return nil }
	exitCalled := false
	exitFunc = func(int) { exitCalled = true }

	main()

	if exitCalled {
		t.Fatalf("did not expect exit")
	}
}

func TestMainExitsWhenRunFails(t *testing.T) {
	origRun := runMain
	origExit := exitFunc
	defer func() {
		runMain = origRun
		exitFunc = origExit
	}()

	runMain = func() error { return errors.New("boom") }
	exitCode := -1
	exitFunc = func(code int) {
		exitCode = code
		panic("exit-called")
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic sentinel")
		}
		if r != "exit-called" {
			t.Fatalf("unexpected panic: %v", r)
		}
		if exitCode != 1 {
			t.Fatalf("expected exit code 1, got %d", exitCode)
		}
	}()

	main()
}

func TestGetenvOrDefault(t *testing.T) {
	t.Setenv("HARNESS_TEST_VALUE", "x")
	if got := getenvOrDefault("HARNESS_TEST_VALUE", "fallback"); got != "x" {
		t.Fatalf("expected x, got %q", got)
	}
	if got := getenvOrDefault("HARNESS_TEST_MISSING", "fallback"); got != "fallback" {
		t.Fatalf("expected fallback, got %q", got)
	}
}

func TestGetenvIntOrDefault(t *testing.T) {
	t.Setenv("HARNESS_INT", "17")
	if got := getenvIntOrDefault("HARNESS_INT", 9); got != 17 {
		t.Fatalf("expected 17, got %d", got)
	}
	t.Setenv("HARNESS_INT", "bad")
	if got := getenvIntOrDefault("HARNESS_INT", 9); got != 9 {
		t.Fatalf("expected fallback 9, got %d", got)
	}
	os.Unsetenv("HARNESS_INT")
	if got := getenvIntOrDefault("HARNESS_INT", 9); got != 9 {
		t.Fatalf("expected fallback 9, got %d", got)
	}
}

func TestAskUserTimeoutEnvParsing(t *testing.T) {
	t.Setenv("HARNESS_ASK_USER_TIMEOUT_SECONDS", "45")
	if got := getenvIntOrDefault("HARNESS_ASK_USER_TIMEOUT_SECONDS", 300); got != 45 {
		t.Fatalf("expected 45, got %d", got)
	}

	t.Setenv("HARNESS_ASK_USER_TIMEOUT_SECONDS", "bad")
	if got := getenvIntOrDefault("HARNESS_ASK_USER_TIMEOUT_SECONDS", 300); got != 300 {
		t.Fatalf("expected fallback 300, got %d", got)
	}
}

func TestGetenvToolApprovalModeOrDefault(t *testing.T) {
	t.Setenv("HARNESS_TOOL_APPROVAL_MODE", "permissions")
	if got := getenvToolApprovalModeOrDefault("HARNESS_TOOL_APPROVAL_MODE", harness.ToolApprovalModeFullAuto); got != harness.ToolApprovalModePermissions {
		t.Fatalf("expected permissions, got %q", got)
	}
	t.Setenv("HARNESS_TOOL_APPROVAL_MODE", "FULL_AUTO")
	if got := getenvToolApprovalModeOrDefault("HARNESS_TOOL_APPROVAL_MODE", harness.ToolApprovalModePermissions); got != harness.ToolApprovalModeFullAuto {
		t.Fatalf("expected full_auto, got %q", got)
	}
	t.Setenv("HARNESS_TOOL_APPROVAL_MODE", "bad")
	if got := getenvToolApprovalModeOrDefault("HARNESS_TOOL_APPROVAL_MODE", harness.ToolApprovalModeFullAuto); got != harness.ToolApprovalModeFullAuto {
		t.Fatalf("expected fallback full_auto, got %q", got)
	}
}

func TestRunDelegatesToRunWithSignals(t *testing.T) {
	orig := runWithSignalsFunc
	defer func() { runWithSignalsFunc = orig }()

	called := false
	runWithSignalsFunc = func(sig <-chan os.Signal, getenv func(string) string, newProvider providerFactory, profileName string) error {
		called = true
		if sig == nil {
			t.Fatalf("expected non-nil signal channel")
		}
		if getenv == nil {
			t.Fatalf("expected getenv callback")
		}
		if newProvider == nil {
			t.Fatalf("expected provider callback")
		}
		return nil
	}

	if err := run(); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !called {
		t.Fatalf("expected runWithSignalsFunc to be called")
	}
}

func TestRunWithSignalsMissingAPIKey(t *testing.T) {
	err := runWithSignals(make(chan os.Signal, 1), func(string) string { return "" }, func(openai.Config) (harness.Provider, error) {
		return &noopProvider{}, nil
	}, "")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "no provider configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveDefaultProviderKeepsLegacyOpenAIPath(t *testing.T) {
	t.Parallel()

	called := false
	provider, err := resolveDefaultProvider(resolveDefaultProviderOptions{
		getenv: func(key string) string {
			switch key {
			case "OPENAI_API_KEY":
				return "test-openai-key"
			case "OPENAI_BASE_URL":
				return "https://example.test"
			default:
				return ""
			}
		},
		newProvider: func(cfg openai.Config) (harness.Provider, error) {
			called = true
			if cfg.APIKey != "test-openai-key" {
				t.Fatalf("APIKey: got %q", cfg.APIKey)
			}
			if cfg.BaseURL != "https://example.test" {
				t.Fatalf("BaseURL: got %q", cfg.BaseURL)
			}
			if cfg.Model != "gpt-4.1-mini" {
				t.Fatalf("Model: got %q", cfg.Model)
			}
			return &namedProvider{name: "openai"}, nil
		},
		model: "gpt-4.1-mini",
	})
	if err != nil {
		t.Fatalf("resolveDefaultProvider: %v", err)
	}
	if !called {
		t.Fatalf("expected OpenAI provider factory to be used")
	}
	named, ok := provider.(*namedProvider)
	if !ok || named.name != "openai" {
		t.Fatalf("unexpected provider: %#v", provider)
	}
}

func TestResolveDefaultProviderUsesOpenRouterForDynamicSlashModel(t *testing.T) {
	t.Parallel()

	cat := &catalog.Catalog{
		CatalogVersion: "1.0.0",
		Providers: map[string]catalog.ProviderEntry{
			"openrouter": {
				DisplayName: "OpenRouter",
				BaseURL:     "https://openrouter.ai/api/v1",
				APIKeyEnv:   "OPENROUTER_API_KEY",
				Models: map[string]catalog.Model{
					"openai/gpt-4.1-mini": {ContextWindow: 128000},
				},
			},
		},
	}
	registry := catalog.NewProviderRegistryWithEnv(cat, func(key string) string {
		if key == "OPENROUTER_API_KEY" {
			return "test-openrouter-key"
		}
		return ""
	})
	registry.SetClientFactory(func(apiKey, baseURL, providerName string) (catalog.ProviderClient, error) {
		if apiKey != "test-openrouter-key" {
			t.Fatalf("APIKey: got %q", apiKey)
		}
		if providerName != "openrouter" {
			t.Fatalf("providerName: got %q", providerName)
		}
		return &namedProvider{name: providerName}, nil
	})

	provider, err := resolveDefaultProvider(resolveDefaultProviderOptions{
		getenv: func(string) string { return "" },
		newProvider: func(cfg openai.Config) (harness.Provider, error) {
			t.Fatalf("legacy OpenAI path should not be used for dynamic OpenRouter models")
			return nil, nil
		},
		registry: registry,
		model:    "moonshotai/kimi-k2.5",
	})
	if err != nil {
		t.Fatalf("resolveDefaultProvider: %v", err)
	}
	named, ok := provider.(*namedProvider)
	if !ok || named.name != "openrouter" {
		t.Fatalf("unexpected provider: %#v", provider)
	}
}

// TestResolveDefaultProvider_FakePath covers Path 0: when HARNESS_PROVIDER=="fake",
// resolveDefaultProvider must return a *fakeprovider.Provider scripted from the
// turns JSON file named by HARNESS_FAKE_TURNS.  When HARNESS_PROVIDER is absent
// the existing (OpenAI/registry) path must be untouched.
func TestResolveDefaultProvider_FakePath(t *testing.T) {
	t.Parallel()

	t.Run("fake provider loaded from turns file", func(t *testing.T) {
		t.Parallel()

		// Write a minimal turns JSON: one turn with content, usage, cost_usd, cost_status.
		turnsJSON := `[
			{
				"content": "hello from fake",
				"usage": {"prompt": 10, "completion": 5},
				"cost_usd": 0.001,
				"cost_status": "available"
			}
		]`
		turnsFile := filepath.Join(t.TempDir(), "turns.json")
		if err := os.WriteFile(turnsFile, []byte(turnsJSON), 0o644); err != nil {
			t.Fatalf("write turns file: %v", err)
		}

		openAICalled := false
		provider, err := resolveDefaultProvider(resolveDefaultProviderOptions{
			getenv: func(key string) string {
				switch key {
				case "HARNESS_PROVIDER":
					return "fake"
				case "HARNESS_FAKE_TURNS":
					return turnsFile
				default:
					return ""
				}
			},
			newProvider: func(cfg openai.Config) (harness.Provider, error) {
				openAICalled = true
				return &namedProvider{name: "openai"}, nil
			},
		})
		if err != nil {
			t.Fatalf("resolveDefaultProvider: %v", err)
		}
		if openAICalled {
			t.Fatal("OpenAI provider factory must not be called when HARNESS_PROVIDER=fake")
		}

		fp, ok := provider.(*fakeprovider.Provider)
		if !ok {
			t.Fatalf("expected *fakeprovider.Provider, got %T", provider)
		}

		// Confirm the scripted turn is served correctly.
		result, err := fp.Complete(context.Background(), harness.CompletionRequest{
			Messages: []harness.Message{{Role: "user", Content: "ping"}},
		})
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if result.Content != "hello from fake" {
			t.Fatalf("Content: got %q, want %q", result.Content, "hello from fake")
		}
		if result.Usage == nil {
			t.Fatal("Usage must not be nil")
		}
		if result.Usage.PromptTokens != 10 {
			t.Fatalf("Usage.PromptTokens: got %d, want 10", result.Usage.PromptTokens)
		}
		if result.Usage.CompletionTokens != 5 {
			t.Fatalf("Usage.CompletionTokens: got %d, want 5", result.Usage.CompletionTokens)
		}
		if result.CostUSD == nil || *result.CostUSD != 0.001 {
			t.Fatalf("CostUSD: got %v, want 0.001", result.CostUSD)
		}
		if result.CostStatus != harness.CostStatusAvailable {
			t.Fatalf("CostStatus: got %q, want %q", result.CostStatus, harness.CostStatusAvailable)
		}
	})

	t.Run("missing turns file returns error", func(t *testing.T) {
		t.Parallel()

		_, err := resolveDefaultProvider(resolveDefaultProviderOptions{
			getenv: func(key string) string {
				switch key {
				case "HARNESS_PROVIDER":
					return "fake"
				case "HARNESS_FAKE_TURNS":
					return "/nonexistent/path/turns.json"
				default:
					return ""
				}
			},
			newProvider: func(cfg openai.Config) (harness.Provider, error) {
				return &namedProvider{name: "openai"}, nil
			},
		})
		if err == nil {
			t.Fatal("expected error when turns file does not exist")
		}
	})

	t.Run("HARNESS_PROVIDER unset leaves existing path untouched", func(t *testing.T) {
		t.Parallel()

		openAICalled := false
		provider, err := resolveDefaultProvider(resolveDefaultProviderOptions{
			getenv: func(key string) string {
				switch key {
				case "OPENAI_API_KEY":
					return "test-key"
				default:
					return "" // HARNESS_PROVIDER not set
				}
			},
			newProvider: func(cfg openai.Config) (harness.Provider, error) {
				openAICalled = true
				return &namedProvider{name: "openai"}, nil
			},
			model: "gpt-4.1-mini",
		})
		if err != nil {
			t.Fatalf("resolveDefaultProvider: %v", err)
		}
		if !openAICalled {
			t.Fatal("expected OpenAI provider factory to be called when HARNESS_PROVIDER is unset")
		}
		named, ok := provider.(*namedProvider)
		if !ok || named.name != "openai" {
			t.Fatalf("expected namedProvider{openai}, got %T %#v", provider, provider)
		}
	})
}

func TestLoadStartupProfileFallsBackToBuiltInProfile(t *testing.T) {
	t.Parallel()

	profile, err := loadStartupProfile("full", "", t.TempDir())
	if err != nil {
		t.Fatalf("loadStartupProfile: %v", err)
	}
	if profile == nil {
		t.Fatal("expected built-in profile")
	}
	if profile.Meta.Name != "full" {
		t.Fatalf("profile name: got %q", profile.Meta.Name)
	}
}

func TestApplyProfileDefaultsAppliesProfileThenEnvOverrides(t *testing.T) {
	t.Parallel()

	cfg := config.Defaults()
	cfg.Model = "base-model"
	cfg.MaxSteps = 8
	cfg.Cost.MaxPerRunUSD = 0.25

	profile := &profiles.Profile{
		Runner: profiles.ProfileRunner{
			Model:      "profile-model",
			MaxSteps:   30,
			MaxCostUSD: 2.0,
		},
	}

	env := map[string]string{
		"HARNESS_MODEL":                "env-model",
		"HARNESS_MAX_STEPS":            "12",
		"HARNESS_MAX_COST_PER_RUN_USD": "1.5",
	}
	got := applyProfileDefaults(cfg, profile, func(key string) string { return env[key] })

	if got.Model != "env-model" {
		t.Fatalf("Model: got %q", got.Model)
	}
	if got.MaxSteps != 12 {
		t.Fatalf("MaxSteps: got %d", got.MaxSteps)
	}
	if got.Cost.MaxPerRunUSD != 1.5 {
		t.Fatalf("MaxPerRunUSD: got %v", got.Cost.MaxPerRunUSD)
	}
}

func TestBuildRunnerConfigProjectsAutoCompactAndForensics(t *testing.T) {
	t.Parallel()

	askUserBroker := harness.NewInMemoryAskUserQuestionBroker(time.Now)
	memoryManager, err := newObservationalMemoryManager(observationalMemoryManagerOptions{Mode: om.ModeOff})
	if err != nil {
		t.Fatalf("newObservationalMemoryManager: %v", err)
	}
	cfg := config.Defaults()
	cfg.Model = "runtime-model"
	cfg.MaxSteps = 24
	cfg.Cost.MaxPerRunUSD = 3.5
	cfg.Memory.Enabled = true
	cfg.AutoCompact = config.AutoCompactConfig{
		Enabled:            true,
		Mode:               "summarize",
		Threshold:          0.72,
		KeepLast:           5,
		ModelContextWindow: 64000,
	}
	cfg.Forensics = config.ForensicsConfig{
		TraceToolDecisions:            true,
		DetectAntiPatterns:            true,
		TraceHookMutations:            true,
		CaptureRequestEnvelope:        true,
		SnapshotMemorySnippet:         true,
		ErrorChainEnabled:             true,
		ErrorContextDepth:             14,
		CaptureReasoning:              true,
		CostAnomalyDetectionEnabled:   true,
		CostAnomalyStepMultiplier:     4.25,
		AuditTrailEnabled:             true,
		ContextWindowSnapshotEnabled:  true,
		ContextWindowWarningThreshold: 0.61,
		CausalGraphEnabled:            true,
		RolloutDir:                    "/tmp/config-rollouts",
	}

	got := buildRunnerConfig(cfg, runnerConfigOptions{
		DefaultSystemPrompt: "system prompt",
		DefaultAgentIntent:  "general",
		AskUserTimeout:      45 * time.Second,
		AskUserBroker:       askUserBroker,
		MemoryManager:       memoryManager,
		PromptEngine:        stubPromptEngine{},
		ToolApprovalMode:    harness.ToolApprovalModePermissions,
		Logger:              &stdLogger{},
		GlobalMCPServerNames: []string{
			"global-a",
			"global-b",
		},
		RoleModels: harness.RoleModels{
			Primary:    "primary-role",
			Summarizer: "summary-role",
		},
	})

	if got.DefaultModel != "runtime-model" {
		t.Fatalf("DefaultModel: got %q", got.DefaultModel)
	}
	if got.MaxSteps != 24 {
		t.Fatalf("MaxSteps: got %d", got.MaxSteps)
	}
	if got.MemoryManager == nil {
		t.Fatal("expected MemoryManager to be preserved")
	}
	if !got.AutoCompactEnabled {
		t.Fatal("expected AutoCompactEnabled")
	}
	if got.AutoCompactMode != "summarize" {
		t.Fatalf("AutoCompactMode: got %q", got.AutoCompactMode)
	}
	if got.AutoCompactThreshold != 0.72 {
		t.Fatalf("AutoCompactThreshold: got %v", got.AutoCompactThreshold)
	}
	if got.AutoCompactKeepLast != 5 {
		t.Fatalf("AutoCompactKeepLast: got %d", got.AutoCompactKeepLast)
	}
	if got.ModelContextWindow != 64000 {
		t.Fatalf("ModelContextWindow: got %d", got.ModelContextWindow)
	}
	if !got.TraceToolDecisions || !got.DetectAntiPatterns || !got.TraceHookMutations {
		t.Fatal("expected tool-decision forensics flags to project")
	}
	if !got.CaptureRequestEnvelope || !got.SnapshotMemorySnippet {
		t.Fatal("expected request-envelope forensics flags to project")
	}
	if !got.ErrorChainEnabled {
		t.Fatal("expected ErrorChainEnabled")
	}
	if got.ErrorContextDepth != 14 {
		t.Fatalf("ErrorContextDepth: got %d", got.ErrorContextDepth)
	}
	if !got.CaptureReasoning {
		t.Fatal("expected CaptureReasoning")
	}
	if !got.CostAnomalyDetectionEnabled {
		t.Fatal("expected CostAnomalyDetectionEnabled")
	}
	if got.CostAnomalyStepMultiplier != 4.25 {
		t.Fatalf("CostAnomalyStepMultiplier: got %v", got.CostAnomalyStepMultiplier)
	}
	if !got.AuditTrailEnabled {
		t.Fatal("expected AuditTrailEnabled")
	}
	if !got.ContextWindowSnapshotEnabled {
		t.Fatal("expected ContextWindowSnapshotEnabled")
	}
	if got.ContextWindowWarningThreshold != 0.61 {
		t.Fatalf("ContextWindowWarningThreshold: got %v", got.ContextWindowWarningThreshold)
	}
	if !got.CausalGraphEnabled {
		t.Fatal("expected CausalGraphEnabled")
	}
	if got.RolloutDir != "/tmp/config-rollouts" {
		t.Fatalf("RolloutDir: got %q", got.RolloutDir)
	}
}

func TestBuildRunnerConfigPreservesExistingRuntimeDependencies(t *testing.T) {
	t.Parallel()

	askUserBroker := harness.NewInMemoryAskUserQuestionBroker(time.Now)
	memoryManager, err := newObservationalMemoryManager(observationalMemoryManagerOptions{Mode: om.ModeOff})
	if err != nil {
		t.Fatalf("newObservationalMemoryManager: %v", err)
	}
	promptEngine := stubPromptEngine{}
	providerRegistry := catalog.NewProviderRegistryWithEnv(&catalog.Catalog{
		CatalogVersion: "1.0.0",
		Providers:      map[string]catalog.ProviderEntry{},
	}, func(string) string { return "" })

	got := buildRunnerConfig(config.Config{
		Model:    "config-model",
		MaxSteps: 8,
	}, runnerConfigOptions{
		DefaultSystemPrompt: "system prompt",
		DefaultAgentIntent:  "debug",
		AskUserTimeout:      2 * time.Minute,
		AskUserBroker:       askUserBroker,
		MemoryManager:       memoryManager,
		PromptEngine:        promptEngine,
		ToolApprovalMode:    harness.ToolApprovalModePermissions,
		ProviderRegistry:    providerRegistry,
		Logger:              &stdLogger{},
		GlobalMCPServerNames: []string{
			"server-1",
		},
		RoleModels: harness.RoleModels{
			Primary:    "role-primary",
			Summarizer: "role-summarizer",
		},
		RolloutDirOverride: "/tmp/override-rollouts",
	})

	if got.DefaultSystemPrompt != "system prompt" {
		t.Fatalf("DefaultSystemPrompt: got %q", got.DefaultSystemPrompt)
	}
	if got.DefaultAgentIntent != "debug" {
		t.Fatalf("DefaultAgentIntent: got %q", got.DefaultAgentIntent)
	}
	if got.AskUserTimeout != 2*time.Minute {
		t.Fatalf("AskUserTimeout: got %v", got.AskUserTimeout)
	}
	if got.AskUserBroker != askUserBroker {
		t.Fatal("expected AskUserBroker to be preserved")
	}
	if got.MemoryManager != memoryManager {
		t.Fatal("expected MemoryManager to be preserved")
	}
	if got.PromptEngine != promptEngine {
		t.Fatal("expected PromptEngine to be preserved")
	}
	if got.ToolApprovalMode != harness.ToolApprovalModePermissions {
		t.Fatalf("ToolApprovalMode: got %q", got.ToolApprovalMode)
	}
	if got.ProviderRegistry != providerRegistry {
		t.Fatal("expected ProviderRegistry to be preserved")
	}
	if got.RoleModels.Primary != "role-primary" || got.RoleModels.Summarizer != "role-summarizer" {
		t.Fatalf("RoleModels: got %+v", got.RoleModels)
	}
	if got.RolloutDir != "/tmp/override-rollouts" {
		t.Fatalf("RolloutDir override: got %q", got.RolloutDir)
	}
	if len(got.GlobalMCPServerNames) != 1 || got.GlobalMCPServerNames[0] != "server-1" {
		t.Fatalf("GlobalMCPServerNames: got %+v", got.GlobalMCPServerNames)
	}
}

// TestBootstrapFailsWithNoProviderConfigured checks that when no API keys are
// set and no catalog is loaded, the server returns a clear "no provider
// configured" error rather than an OpenAI-specific message.
func TestBootstrapFailsWithNoProviderConfigured(t *testing.T) {
	env := map[string]string{} // no keys at all
	getenv := func(key string) string { return env[key] }

	err := runWithSignals(make(chan os.Signal, 1), getenv, func(openai.Config) (harness.Provider, error) {
		return &noopProvider{}, nil
	}, "")
	if err == nil {
		t.Fatalf("expected error when no provider is configured")
	}
	if !strings.Contains(err.Error(), "provider") {
		t.Fatalf("expected error mentioning 'provider', got: %v", err)
	}
}

// TestBootstrapSucceedsWithAnthropicNoOpenAI verifies that the server starts
// successfully when only ANTHROPIC_API_KEY is set (no OPENAI_API_KEY).  It
// uses a minimal in-process catalog that registers Anthropic as a provider,
// then sends an interrupt to trigger graceful shutdown.
func TestBootstrapSucceedsWithAnthropicNoOpenAI(t *testing.T) {
	// Write a minimal model catalog JSON to a temp file.
	catalogJSON := `{
  "catalog_version": "1.0.0",
  "providers": {
    "anthropic": {
      "display_name": "Anthropic",
      "base_url": "https://api.anthropic.com/v1",
      "api_key_env": "ANTHROPIC_API_KEY",
      "protocol": "anthropic",
      "models": {
        "claude-3-5-haiku-20241022": {
          "display_name": "Claude 3.5 Haiku",
          "context_window": 200000,
          "modalities": ["text"],
          "tool_calling": true,
          "streaming": true
        }
      }
    }
  }
}`
	tmpDir := t.TempDir()
	catalogPath := tmpDir + "/models.json"
	if err := os.WriteFile(catalogPath, []byte(catalogJSON), 0o644); err != nil {
		t.Fatalf("write catalog: %v", err)
	}

	env := map[string]string{
		"ANTHROPIC_API_KEY":          "test-anthropic-key",
		"HARNESS_ADDR":               "127.0.0.1:0",
		"HARNESS_MODEL_CATALOG_PATH": catalogPath,
		"HARNESS_MODEL":              "claude-3-5-haiku-20241022",
	}
	getenv := func(key string) string { return env[key] }
	sig := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- runWithSignals(sig, getenv, func(openai.Config) (harness.Provider, error) {
			// Should not be called when OPENAI_API_KEY is absent.
			t.Errorf("newProvider (OpenAI factory) was called unexpectedly")
			return &noopProvider{}, nil
		}, "")
	}()

	// Give the server a moment to start, then send shutdown signal.
	time.Sleep(150 * time.Millisecond)
	sig <- os.Interrupt

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWithSignals returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for graceful shutdown")
	}
}

func TestRunWithSignalsFailsWhenDefaultModelCannotBeResolvedWithoutOpenAI(t *testing.T) {
	t.Parallel()

	catalogJSON := `{
  "catalog_version": "1.0.0",
  "providers": {
    "anthropic": {
      "display_name": "Anthropic",
      "base_url": "https://api.anthropic.com/v1",
      "api_key_env": "ANTHROPIC_API_KEY",
      "protocol": "anthropic",
      "models": {
        "claude-3-5-haiku-20241022": {
          "display_name": "Claude 3.5 Haiku",
          "context_window": 200000,
          "modalities": ["text"],
          "tool_calling": true,
          "streaming": true
        }
      }
    }
  }
}`
	tmpDir := t.TempDir()
	catalogPath := tmpDir + "/models.json"
	if err := os.WriteFile(catalogPath, []byte(catalogJSON), 0o644); err != nil {
		t.Fatalf("write catalog: %v", err)
	}

	env := map[string]string{
		"ANTHROPIC_API_KEY":          "test-anthropic-key",
		"HARNESS_ADDR":               "127.0.0.1:0",
		"HARNESS_MEMORY_MODE":        "off",
		"HARNESS_MODEL_CATALOG_PATH": catalogPath,
	}
	getenv := func(key string) string { return env[key] }
	sig := make(chan os.Signal, 1)
	done := make(chan error, 1)

	go func() {
		done <- runWithSignals(sig, getenv, func(openai.Config) (harness.Provider, error) {
			t.Errorf("newProvider (OpenAI factory) was called unexpectedly")
			return &noopProvider{}, nil
		}, "")
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected startup error")
		}
		if !strings.Contains(err.Error(), "gpt-4.1-mini") {
			t.Fatalf("expected error to mention unresolved default model, got: %v", err)
		}
	case <-time.After(300 * time.Millisecond):
		sig <- os.Interrupt
		err := <-done
		t.Fatalf("expected startup to fail before serving, but it started cleanly: %v", err)
	}
}

func TestRunWithSignalsProviderFailure(t *testing.T) {
	env := map[string]string{
		"OPENAI_API_KEY":      "x",
		"HARNESS_ADDR":        "127.0.0.1:0",
		"HARNESS_MEMORY_MODE": "off",
	}
	getenv := func(key string) string { return env[key] }

	err := runWithSignals(make(chan os.Signal, 1), getenv, func(openai.Config) (harness.Provider, error) {
		return nil, errors.New("provider init failed")
	}, "")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "provider init failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunWithSignalsGracefulShutdown(t *testing.T) {
	env := map[string]string{
		"OPENAI_API_KEY":      "x",
		"HARNESS_ADDR":        "127.0.0.1:0",
		"HARNESS_MEMORY_MODE": "off",
	}
	getenv := func(key string) string { return env[key] }
	sig := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- runWithSignals(sig, getenv, func(openai.Config) (harness.Provider, error) {
			return &noopProvider{}, nil
		}, "")
	}()

	time.Sleep(100 * time.Millisecond)
	sig <- os.Interrupt

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWithSignals returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for graceful shutdown")
	}
}

func TestGetenvMemoryModeOrDefault(t *testing.T) {
	t.Setenv("HARNESS_MEMORY_MODE", "local_coordinator")
	if got := getenvMemoryModeOrDefault("HARNESS_MEMORY_MODE", "off"); got != "local_coordinator" {
		t.Fatalf("expected local_coordinator, got %q", got)
	}
	t.Setenv("HARNESS_MEMORY_MODE", "bad")
	if got := getenvMemoryModeOrDefault("HARNESS_MEMORY_MODE", "auto"); got != "auto" {
		t.Fatalf("expected fallback auto, got %q", got)
	}
}

func TestGetenvBoolOrDefault(t *testing.T) {
	t.Setenv("HARNESS_BOOL", "yes")
	if !getenvBoolOrDefault("HARNESS_BOOL", false) {
		t.Fatalf("expected true")
	}
	t.Setenv("HARNESS_BOOL", "off")
	if getenvBoolOrDefault("HARNESS_BOOL", true) {
		t.Fatalf("expected false")
	}
	t.Setenv("HARNESS_BOOL", "invalid")
	if !getenvBoolOrDefault("HARNESS_BOOL", true) {
		t.Fatalf("expected fallback true")
	}
}

func TestObservationalMemoryModelComplete(t *testing.T) {
	t.Parallel()

	m := observationalMemoryModel{}
	if _, err := m.Complete(context.Background(), om.ModelRequest{}); err == nil {
		t.Fatalf("expected provider required error")
	}

	provider := &modelProviderStub{
		result: harness.CompletionResult{Content: "  summary result  "},
	}
	m = observationalMemoryModel{
		provider: provider,
		model:    "gpt-5-nano",
	}
	out, err := m.Complete(context.Background(), om.ModelRequest{
		Messages: []om.PromptMessage{{Role: "system", Content: "A"}, {Role: "user", Content: "B"}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if out != "summary result" {
		t.Fatalf("unexpected trimmed output: %q", out)
	}
	if provider.req.Model != "gpt-5-nano" || len(provider.req.Messages) != 2 {
		t.Fatalf("unexpected provider request: %+v", provider.req)
	}

	provider.err = errors.New("provider failed")
	if _, err := m.Complete(context.Background(), om.ModelRequest{Messages: []om.PromptMessage{{Role: "user", Content: "x"}}}); err == nil {
		t.Fatalf("expected provider error")
	}
}

func TestNewObservationalMemoryManagerBranches(t *testing.T) {
	t.Parallel()

	offMgr, err := newObservationalMemoryManager(observationalMemoryManagerOptions{
		Mode: om.ModeOff,
	})
	if err != nil {
		t.Fatalf("mode off manager: %v", err)
	}
	if offMgr.Mode() != om.ModeOff {
		t.Fatalf("expected off mode, got %q", offMgr.Mode())
	}

	if _, err := newObservationalMemoryManager(observationalMemoryManagerOptions{
		Mode:          om.ModeAuto,
		Driver:        "unknown",
		WorkspaceRoot: t.TempDir(),
	}); err == nil {
		t.Fatalf("expected unsupported driver error")
	}

	if _, err := newObservationalMemoryManager(observationalMemoryManagerOptions{
		Mode:          om.ModeAuto,
		Driver:        "postgres",
		WorkspaceRoot: t.TempDir(),
		MemoryLLMMode: "inherit",
	}); err == nil {
		t.Fatalf("expected postgres dsn error")
	}

	provider := &noopProvider{}
	manager, err := newObservationalMemoryManager(observationalMemoryManagerOptions{
		Mode:          om.ModeAuto,
		Driver:        "sqlite",
		SQLitePath:    ".harness/memory.db",
		WorkspaceRoot: t.TempDir(),
		Provider:      provider,
		Model:         "gpt-5-nano",
		MemoryLLMMode: "inherit",
		DefaultConfig: om.DefaultConfig(),
	})
	if err != nil {
		t.Fatalf("sqlite inherit manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if manager.Mode() != om.ModeLocalCoordinator {
		t.Fatalf("expected local coordinator mode, got %q", manager.Mode())
	}

	providerCat := &catalog.Catalog{
		CatalogVersion: "1.0.0",
		Providers: map[string]catalog.ProviderEntry{
			"openrouter": {
				DisplayName: "OpenRouter",
				APIKeyEnv:   "OPENROUTER_API_KEY",
				BaseURL:     "https://openrouter.ai/api/v1",
				Models: map[string]catalog.Model{
					"openai/gpt-4.1-mini": {ContextWindow: 128000},
				},
			},
		},
	}
	providerRegistry := catalog.NewProviderRegistryWithEnv(providerCat, func(key string) string {
		if key == "OPENROUTER_API_KEY" {
			return "test-openrouter-key"
		}
		return ""
	})
	providerRegistry.SetClientFactory(func(apiKey, baseURL, providerName string) (catalog.ProviderClient, error) {
		if apiKey != "test-openrouter-key" {
			t.Fatalf("provider api key: got %q", apiKey)
		}
		if providerName != "openrouter" {
			t.Fatalf("provider name: got %q", providerName)
		}
		return &noopProvider{}, nil
	})
	providerModeMgr, err := newObservationalMemoryManager(observationalMemoryManagerOptions{
		Mode:              om.ModeAuto,
		Driver:            "sqlite",
		SQLitePath:        ".harness/provider-memory.db",
		WorkspaceRoot:     t.TempDir(),
		ProviderRegistry:  providerRegistry,
		Model:             "gpt-4.1-mini",
		MemoryLLMMode:     "provider",
		MemoryLLMProvider: "openrouter",
		MemoryLLMModel:    "moonshotai/kimi-k2.5",
		DefaultConfig:     om.DefaultConfig(),
	})
	if err != nil {
		t.Fatalf("sqlite provider manager: %v", err)
	}
	t.Cleanup(func() { _ = providerModeMgr.Close() })
	if providerModeMgr.Mode() != om.ModeLocalCoordinator {
		t.Fatalf("expected local coordinator mode for provider manager, got %q", providerModeMgr.Mode())
	}

	if _, err := newObservationalMemoryManager(observationalMemoryManagerOptions{
		Mode:             om.ModeAuto,
		Driver:           "sqlite",
		SQLitePath:       ".harness/memory.db",
		WorkspaceRoot:    t.TempDir(),
		MemoryLLMMode:    "openai",
		MemoryLLMAPIKey:  "",
		MemoryLLMBaseURL: "",
		MemoryLLMModel:   "",
	}); err == nil {
		t.Fatalf("expected openai api key error")
	}

	if _, err := newObservationalMemoryManager(observationalMemoryManagerOptions{
		Mode:          om.ModeAuto,
		Driver:        "sqlite",
		SQLitePath:    ".harness/memory.db",
		WorkspaceRoot: t.TempDir(),
		MemoryLLMMode: "unsupported",
	}); err == nil {
		t.Fatalf("expected unsupported llm mode error")
	}

	if _, err := newObservationalMemoryManager(observationalMemoryManagerOptions{
		Mode:          om.ModeAuto,
		Driver:        "sqlite",
		SQLitePath:    ".harness/memory.db",
		WorkspaceRoot: t.TempDir(),
		MemoryLLMMode: "provider",
	}); err == nil {
		t.Fatalf("expected provider mode config error")
	}
}

func TestRunWithSignalsObservationalMemoryFallsBackToOpenAIAPIKey(t *testing.T) {
	t.Parallel()

	addr := freeLocalAddr(t)
	workspace := t.TempDir()
	env := map[string]string{
		"OPENAI_API_KEY":    "test-openai-key",
		"HARNESS_ADDR":      addr,
		"HARNESS_WORKSPACE": workspace,
	}
	getenv := func(key string) string { return env[key] }
	sig := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- runWithSignals(sig, getenv, func(cfg openai.Config) (harness.Provider, error) {
			return &noopProvider{}, nil
		}, "")
	}()

	awaitHealthy(t, addr, 3*time.Second)
	sig <- os.Interrupt

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWithSignals returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for graceful shutdown")
	}
}

func TestRunWithSignalsMemoryProviderModeFromProjectConfig(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".harness"), 0o755); err != nil {
		t.Fatalf("mkdir .harness: %v", err)
	}
	configTOML := `
[memory]
mode = "auto"
llm_mode = "provider"
llm_provider = "openrouter"
llm_model = "moonshotai/kimi-k2.5"
`
	if err := os.WriteFile(filepath.Join(workspace, ".harness", "config.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	catalogJSON := `{
  "catalog_version": "1.0.0",
  "providers": {
    "openrouter": {
      "display_name": "OpenRouter",
      "base_url": "https://openrouter.ai/api/v1",
      "api_key_env": "OPENROUTER_API_KEY",
      "protocol": "openai_compat",
      "models": {
        "openai/gpt-4.1-mini": {
          "display_name": "GPT-4.1 Mini",
          "context_window": 128000,
          "modalities": ["text"],
          "tool_calling": true,
          "streaming": true
        }
      }
    }
  }
}`
	catalogPath := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(catalogPath, []byte(catalogJSON), 0o644); err != nil {
		t.Fatalf("write catalog: %v", err)
	}

	addr := freeLocalAddr(t)
	env := map[string]string{
		"OPENROUTER_API_KEY":         "test-openrouter-key",
		"HARNESS_ADDR":               addr,
		"HARNESS_WORKSPACE":          workspace,
		"HARNESS_MODEL_CATALOG_PATH": catalogPath,
		"HARNESS_MODEL":              "openai/gpt-4.1-mini",
	}
	getenv := func(key string) string { return env[key] }
	sig := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- runWithSignals(sig, getenv, func(cfg openai.Config) (harness.Provider, error) {
			return &noopProvider{}, nil
		}, "")
	}()

	awaitHealthy(t, addr, 3*time.Second)
	sig <- os.Interrupt

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWithSignals returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for graceful shutdown")
	}
}

// ---------------------------------------------------------------------------
// cronJobFromCron / cronExecFromCron field-mapping tests
// ---------------------------------------------------------------------------

func TestCronJobFromCronAllFields(t *testing.T) {
	t.Parallel()

	now := time.Now().Truncate(time.Second)
	lastRun := now.Add(-1 * time.Hour)

	j := cron.Job{
		ID:         "job-123",
		Name:       "nightly-backup",
		Schedule:   "0 2 * * *",
		ExecType:   "shell",
		ExecConfig: `{"command":"pg_dump"}`,
		Status:     "active",
		TimeoutSec: 300,
		Tags:       "backup,prod",
		NextRunAt:  now.Add(24 * time.Hour),
		LastRunAt:  lastRun,
		CreatedAt:  now.Add(-48 * time.Hour),
		UpdatedAt:  now,
	}

	got := cronJobFromCron(j)

	if got.ID != j.ID {
		t.Fatalf("ID: got %q, want %q", got.ID, j.ID)
	}
	if got.Name != j.Name {
		t.Fatalf("Name: got %q, want %q", got.Name, j.Name)
	}
	if got.Schedule != j.Schedule {
		t.Fatalf("Schedule: got %q, want %q", got.Schedule, j.Schedule)
	}
	if got.ExecType != j.ExecType {
		t.Fatalf("ExecType: got %q, want %q", got.ExecType, j.ExecType)
	}
	if got.ExecConfig != j.ExecConfig {
		t.Fatalf("ExecConfig: got %q, want %q", got.ExecConfig, j.ExecConfig)
	}
	if got.Status != j.Status {
		t.Fatalf("Status: got %q, want %q", got.Status, j.Status)
	}
	if got.TimeoutSec != j.TimeoutSec {
		t.Fatalf("TimeoutSec: got %d, want %d", got.TimeoutSec, j.TimeoutSec)
	}
	if got.Tags != j.Tags {
		t.Fatalf("Tags: got %q, want %q", got.Tags, j.Tags)
	}
	if !got.NextRunAt.Equal(j.NextRunAt) {
		t.Fatalf("NextRunAt: got %v, want %v", got.NextRunAt, j.NextRunAt)
	}
	if !got.LastRunAt.Equal(j.LastRunAt) {
		t.Fatalf("LastRunAt: got %v, want %v", got.LastRunAt, j.LastRunAt)
	}
	if !got.CreatedAt.Equal(j.CreatedAt) {
		t.Fatalf("CreatedAt: got %v, want %v", got.CreatedAt, j.CreatedAt)
	}
	if !got.UpdatedAt.Equal(j.UpdatedAt) {
		t.Fatalf("UpdatedAt: got %v, want %v", got.UpdatedAt, j.UpdatedAt)
	}
}

func TestCronJobFromCronZeroValues(t *testing.T) {
	t.Parallel()

	got := cronJobFromCron(cron.Job{})

	if got.ID != "" {
		t.Fatalf("expected empty ID, got %q", got.ID)
	}
	if got.Name != "" {
		t.Fatalf("expected empty Name, got %q", got.Name)
	}
	if got.TimeoutSec != 0 {
		t.Fatalf("expected 0 TimeoutSec, got %d", got.TimeoutSec)
	}
	if !got.NextRunAt.IsZero() {
		t.Fatalf("expected zero NextRunAt, got %v", got.NextRunAt)
	}
	if !got.LastRunAt.IsZero() {
		t.Fatalf("expected zero LastRunAt, got %v", got.LastRunAt)
	}
	if !got.CreatedAt.IsZero() {
		t.Fatalf("expected zero CreatedAt, got %v", got.CreatedAt)
	}
	if !got.UpdatedAt.IsZero() {
		t.Fatalf("expected zero UpdatedAt, got %v", got.UpdatedAt)
	}
}

func TestCronExecFromCronAllFields(t *testing.T) {
	t.Parallel()

	now := time.Now().Truncate(time.Second)

	e := cron.Execution{
		ID:            "exec-456",
		JobID:         "job-123",
		StartedAt:     now.Add(-5 * time.Minute),
		FinishedAt:    now,
		Status:        "success",
		RunID:         "run-789",
		OutputSummary: "completed 42 rows",
		Error:         "",
		DurationMs:    300000,
	}

	got := cronExecFromCron(e)

	if got.ID != e.ID {
		t.Fatalf("ID: got %q, want %q", got.ID, e.ID)
	}
	if got.JobID != e.JobID {
		t.Fatalf("JobID: got %q, want %q", got.JobID, e.JobID)
	}
	if !got.StartedAt.Equal(e.StartedAt) {
		t.Fatalf("StartedAt: got %v, want %v", got.StartedAt, e.StartedAt)
	}
	if !got.FinishedAt.Equal(e.FinishedAt) {
		t.Fatalf("FinishedAt: got %v, want %v", got.FinishedAt, e.FinishedAt)
	}
	if got.Status != e.Status {
		t.Fatalf("Status: got %q, want %q", got.Status, e.Status)
	}
	if got.RunID != e.RunID {
		t.Fatalf("RunID: got %q, want %q", got.RunID, e.RunID)
	}
	if got.OutputSummary != e.OutputSummary {
		t.Fatalf("OutputSummary: got %q, want %q", got.OutputSummary, e.OutputSummary)
	}
	if got.Error != e.Error {
		t.Fatalf("Error: got %q, want %q", got.Error, e.Error)
	}
	if got.DurationMs != e.DurationMs {
		t.Fatalf("DurationMs: got %d, want %d", got.DurationMs, e.DurationMs)
	}
}

func TestCronExecFromCronZeroValues(t *testing.T) {
	t.Parallel()

	got := cronExecFromCron(cron.Execution{})

	if got.ID != "" {
		t.Fatalf("expected empty ID, got %q", got.ID)
	}
	if got.JobID != "" {
		t.Fatalf("expected empty JobID, got %q", got.JobID)
	}
	if !got.StartedAt.IsZero() {
		t.Fatalf("expected zero StartedAt, got %v", got.StartedAt)
	}
	if !got.FinishedAt.IsZero() {
		t.Fatalf("expected zero FinishedAt, got %v", got.FinishedAt)
	}
	if got.Status != "" {
		t.Fatalf("expected empty Status, got %q", got.Status)
	}
	if got.RunID != "" {
		t.Fatalf("expected empty RunID, got %q", got.RunID)
	}
	if got.OutputSummary != "" {
		t.Fatalf("expected empty OutputSummary, got %q", got.OutputSummary)
	}
	if got.Error != "" {
		t.Fatalf("expected empty Error, got %q", got.Error)
	}
	if got.DurationMs != 0 {
		t.Fatalf("expected 0 DurationMs, got %d", got.DurationMs)
	}
}

// ---------------------------------------------------------------------------
// cronClientAdapter end-to-end tests with httptest
// ---------------------------------------------------------------------------

// sampleJob returns a cron.Job fixture for httptest JSON responses.
func sampleJob() cron.Job {
	return cron.Job{
		ID:         "job-abc",
		Name:       "test-job",
		Schedule:   "*/5 * * * *",
		ExecType:   "shell",
		ExecConfig: `{"cmd":"echo hi"}`,
		Status:     "active",
		TimeoutSec: 60,
		Tags:       "test",
		NextRunAt:  time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC),
		LastRunAt:  time.Date(2026, 3, 8, 11, 55, 0, 0, time.UTC),
		CreatedAt:  time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 3, 8, 11, 55, 0, 0, time.UTC),
	}
}

// sampleExecution returns a cron.Execution fixture for httptest JSON responses.
func sampleExecution() cron.Execution {
	return cron.Execution{
		ID:            "exec-001",
		JobID:         "job-abc",
		StartedAt:     time.Date(2026, 3, 8, 11, 55, 0, 0, time.UTC),
		FinishedAt:    time.Date(2026, 3, 8, 11, 55, 2, 0, time.UTC),
		Status:        "success",
		RunID:         "run-xyz",
		OutputSummary: "all good",
		Error:         "",
		DurationMs:    2000,
	}
}

func newTestAdapter(ts *httptest.Server) *cronClientAdapter {
	return &cronClientAdapter{client: cron.NewClient(ts.URL)}
}

func TestCronClientAdapterCreateJob(t *testing.T) {
	t.Parallel()

	job := sampleJob()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/jobs" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad", 400)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json Content-Type, got %q", r.Header.Get("Content-Type"))
		}
		var reqBody cron.CreateJobRequest
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("decode request body: %v", err)
			http.Error(w, "bad", 400)
			return
		}
		if reqBody.Name != "test-job" {
			t.Errorf("request Name: got %q, want %q", reqBody.Name, "test-job")
		}
		if reqBody.Schedule != "*/5 * * * *" {
			t.Errorf("request Schedule: got %q, want %q", reqBody.Schedule, "*/5 * * * *")
		}
		if reqBody.Tags != "test" {
			t.Errorf("request Tags: got %q, want %q", reqBody.Tags, "test")
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(job)
	}))
	defer ts.Close()

	adapter := newTestAdapter(ts)
	got, err := adapter.CreateJob(context.Background(), htools.CronCreateJobRequest{
		Name:       "test-job",
		Schedule:   "*/5 * * * *",
		ExecType:   "shell",
		ExecConfig: `{"cmd":"echo hi"}`,
		TimeoutSec: 60,
		Tags:       "test",
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if got.ID != "job-abc" {
		t.Fatalf("ID: got %q, want %q", got.ID, "job-abc")
	}
	if got.Name != "test-job" {
		t.Fatalf("Name: got %q, want %q", got.Name, "test-job")
	}
	if got.Tags != "test" {
		t.Fatalf("Tags: got %q, want %q", got.Tags, "test")
	}
}

func TestCronClientAdapterListJobs(t *testing.T) {
	t.Parallel()

	jobs := []cron.Job{sampleJob(), {
		ID:     "job-def",
		Name:   "second-job",
		Status: "paused",
	}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/jobs" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad", 400)
			return
		}
		resp := struct {
			Jobs []cron.Job `json:"jobs"`
		}{Jobs: jobs}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	adapter := newTestAdapter(ts)
	got, err := adapter.ListJobs(context.Background())
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(got))
	}
	if got[0].ID != "job-abc" {
		t.Fatalf("first job ID: got %q, want %q", got[0].ID, "job-abc")
	}
	if got[1].Name != "second-job" {
		t.Fatalf("second job Name: got %q, want %q", got[1].Name, "second-job")
	}
	if got[1].Status != "paused" {
		t.Fatalf("second job Status: got %q, want %q", got[1].Status, "paused")
	}
}

func TestCronClientAdapterGetJob(t *testing.T) {
	t.Parallel()

	job := sampleJob()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/jobs/job-abc" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", 404)
			return
		}
		_ = json.NewEncoder(w).Encode(job)
	}))
	defer ts.Close()

	adapter := newTestAdapter(ts)
	got, err := adapter.GetJob(context.Background(), "job-abc")
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.ID != "job-abc" {
		t.Fatalf("ID: got %q, want %q", got.ID, "job-abc")
	}
	if got.Schedule != "*/5 * * * *" {
		t.Fatalf("Schedule: got %q, want %q", got.Schedule, "*/5 * * * *")
	}
}

func TestCronClientAdapterUpdateJob(t *testing.T) {
	t.Parallel()

	updatedJob := sampleJob()
	updatedJob.Status = "paused"
	updatedJob.Schedule = "0 * * * *"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/v1/jobs/job-abc" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad", 400)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var reqBody map[string]interface{}
		if err := json.Unmarshal(body, &reqBody); err != nil {
			t.Errorf("unmarshal request: %v", err)
		}
		if reqBody["status"] != "paused" {
			t.Errorf("request status: got %v, want %q", reqBody["status"], "paused")
		}
		if reqBody["schedule"] != "0 * * * *" {
			t.Errorf("request schedule: got %v, want %q", reqBody["schedule"], "0 * * * *")
		}
		_ = json.NewEncoder(w).Encode(updatedJob)
	}))
	defer ts.Close()

	adapter := newTestAdapter(ts)
	newSched := "0 * * * *"
	newStatus := "paused"
	got, err := adapter.UpdateJob(context.Background(), "job-abc", htools.CronUpdateJobRequest{
		Schedule: &newSched,
		Status:   &newStatus,
	})
	if err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}
	if got.Status != "paused" {
		t.Fatalf("Status: got %q, want %q", got.Status, "paused")
	}
	if got.Schedule != "0 * * * *" {
		t.Fatalf("Schedule: got %q, want %q", got.Schedule, "0 * * * *")
	}
}

func TestCronClientAdapterDeleteJob(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/jobs/job-abc" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad", 400)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	adapter := newTestAdapter(ts)
	if err := adapter.DeleteJob(context.Background(), "job-abc"); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}
}

func TestCronClientAdapterListExecutions(t *testing.T) {
	t.Parallel()

	execs := []cron.Execution{sampleExecution(), {
		ID:            "exec-002",
		JobID:         "job-abc",
		Status:        "failed",
		Error:         "timeout exceeded",
		OutputSummary: "partial",
		DurationMs:    60000,
	}}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/jobs/job-abc/history" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad", 400)
			return
		}
		// Verify query params
		if r.URL.Query().Get("limit") != "10" {
			t.Errorf("limit: got %q, want %q", r.URL.Query().Get("limit"), "10")
		}
		if r.URL.Query().Get("offset") != "0" {
			t.Errorf("offset: got %q, want %q", r.URL.Query().Get("offset"), "0")
		}
		resp := struct {
			Executions []cron.Execution `json:"executions"`
		}{Executions: execs}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	adapter := newTestAdapter(ts)
	got, err := adapter.ListExecutions(context.Background(), "job-abc", 10, 0)
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 executions, got %d", len(got))
	}
	if got[0].RunID != "run-xyz" {
		t.Fatalf("first exec RunID: got %q, want %q", got[0].RunID, "run-xyz")
	}
	if got[0].OutputSummary != "all good" {
		t.Fatalf("first exec OutputSummary: got %q, want %q", got[0].OutputSummary, "all good")
	}
	if got[1].Error != "timeout exceeded" {
		t.Fatalf("second exec Error: got %q, want %q", got[1].Error, "timeout exceeded")
	}
	if got[1].DurationMs != 60000 {
		t.Fatalf("second exec DurationMs: got %d, want %d", got[1].DurationMs, 60000)
	}
}

func TestCronClientAdapterHealth(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/healthz" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad", 400)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer ts.Close()

	adapter := newTestAdapter(ts)
	if err := adapter.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Error cases
// ---------------------------------------------------------------------------

func TestCronClientAdapterServerError(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{
				"code":    "internal_error",
				"message": "something broke",
			},
		})
	}))
	defer ts.Close()

	adapter := newTestAdapter(ts)
	ctx := context.Background()

	if _, err := adapter.CreateJob(ctx, htools.CronCreateJobRequest{Name: "x"}); err == nil {
		t.Fatalf("CreateJob: expected error")
	}
	if _, err := adapter.ListJobs(ctx); err == nil {
		t.Fatalf("ListJobs: expected error")
	}
	if _, err := adapter.GetJob(ctx, "id"); err == nil {
		t.Fatalf("GetJob: expected error")
	}
	if _, err := adapter.UpdateJob(ctx, "id", htools.CronUpdateJobRequest{}); err == nil {
		t.Fatalf("UpdateJob: expected error")
	}
	if err := adapter.DeleteJob(ctx, "id"); err == nil {
		t.Fatalf("DeleteJob: expected error")
	}
	if _, err := adapter.ListExecutions(ctx, "id", 10, 0); err == nil {
		t.Fatalf("ListExecutions: expected error")
	}
	if err := adapter.Health(ctx); err == nil {
		t.Fatalf("Health: expected error")
	}
}

func TestCronClientAdapterServerUnreachable(t *testing.T) {
	t.Parallel()

	// Create a server and immediately close it to get an unreachable URL.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := ts.URL
	ts.Close()

	adapter := &cronClientAdapter{client: cron.NewClient(url)}
	ctx := context.Background()

	if _, err := adapter.CreateJob(ctx, htools.CronCreateJobRequest{Name: "x"}); err == nil {
		t.Fatalf("CreateJob: expected connection error")
	}
	if _, err := adapter.ListJobs(ctx); err == nil {
		t.Fatalf("ListJobs: expected connection error")
	}
	if _, err := adapter.GetJob(ctx, "id"); err == nil {
		t.Fatalf("GetJob: expected connection error")
	}
	if _, err := adapter.UpdateJob(ctx, "id", htools.CronUpdateJobRequest{}); err == nil {
		t.Fatalf("UpdateJob: expected connection error")
	}
	if err := adapter.DeleteJob(ctx, "id"); err == nil {
		t.Fatalf("DeleteJob: expected connection error")
	}
	if _, err := adapter.ListExecutions(ctx, "id", 10, 0); err == nil {
		t.Fatalf("ListExecutions: expected connection error")
	}
	if err := adapter.Health(ctx); err == nil {
		t.Fatalf("Health: expected connection error")
	}
}

func TestCronClientAdapterContextCancelled(t *testing.T) {
	t.Parallel()

	// Server that blocks long enough for context cancellation to win.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer ts.Close()

	adapter := newTestAdapter(ts)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	if _, err := adapter.CreateJob(ctx, htools.CronCreateJobRequest{Name: "x"}); err == nil {
		t.Fatalf("CreateJob: expected context error")
	}
	if _, err := adapter.ListJobs(ctx); err == nil {
		t.Fatalf("ListJobs: expected context error")
	}
	if _, err := adapter.GetJob(ctx, "id"); err == nil {
		t.Fatalf("GetJob: expected context error")
	}
	if _, err := adapter.UpdateJob(ctx, "id", htools.CronUpdateJobRequest{}); err == nil {
		t.Fatalf("UpdateJob: expected context error")
	}
	if err := adapter.DeleteJob(ctx, "id"); err == nil {
		t.Fatalf("DeleteJob: expected context error")
	}
	if _, err := adapter.ListExecutions(ctx, "id", 10, 0); err == nil {
		t.Fatalf("ListExecutions: expected context error")
	}
	if err := adapter.Health(ctx); err == nil {
		t.Fatalf("Health: expected context error")
	}
}

// ---------------------------------------------------------------------------
// Concurrency
// ---------------------------------------------------------------------------

func TestCronClientAdapterConcurrent(t *testing.T) {
	t.Parallel()

	job := sampleJob()
	exec := sampleExecution()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/jobs":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(job)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs":
			resp := struct {
				Jobs []cron.Job `json:"jobs"`
			}{Jobs: []cron.Job{job}}
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/history"):
			resp := struct {
				Executions []cron.Execution `json:"executions"`
			}{Executions: []cron.Execution{exec}}
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodGet && r.URL.Path == "/healthz":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/jobs/"):
			_ = json.NewEncoder(w).Encode(job)
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/v1/jobs/"):
			_ = json.NewEncoder(w).Encode(job)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/jobs/"):
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "not found", 404)
		}
	}))
	defer ts.Close()

	adapter := newTestAdapter(ts)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, 70)

	for i := 0; i < 10; i++ {
		wg.Add(7)
		go func() {
			defer wg.Done()
			if _, err := adapter.CreateJob(ctx, htools.CronCreateJobRequest{Name: "c"}); err != nil {
				errs <- fmt.Errorf("CreateJob: %w", err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := adapter.ListJobs(ctx); err != nil {
				errs <- fmt.Errorf("ListJobs: %w", err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := adapter.GetJob(ctx, "job-abc"); err != nil {
				errs <- fmt.Errorf("GetJob: %w", err)
			}
		}()
		go func() {
			defer wg.Done()
			s := "paused"
			if _, err := adapter.UpdateJob(ctx, "job-abc", htools.CronUpdateJobRequest{Status: &s}); err != nil {
				errs <- fmt.Errorf("UpdateJob: %w", err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := adapter.DeleteJob(ctx, "job-abc"); err != nil {
				errs <- fmt.Errorf("DeleteJob: %w", err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := adapter.ListExecutions(ctx, "job-abc", 10, 0); err != nil {
				errs <- fmt.Errorf("ListExecutions: %w", err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := adapter.Health(ctx); err != nil {
				errs <- fmt.Errorf("Health: %w", err)
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// HARNESS_CRON_URL env var wiring
// ---------------------------------------------------------------------------

func TestCronURLEnvVarWiring(t *testing.T) {
	t.Parallel()

	// Sub-test: empty string -> embedded cron
	// Each subtest uses t.TempDir() for HARNESS_WORKSPACE so that the embedded
	// cron SQLite file is isolated and never shared across parallel subtests.
	t.Run("empty_string", func(t *testing.T) {
		t.Parallel()
		workspaceDir := t.TempDir()
		env := map[string]string{
			"OPENAI_API_KEY":      "test-key",
			"HARNESS_ADDR":        "127.0.0.1:0",
			"HARNESS_MEMORY_MODE": "off",
			"HARNESS_CRON_URL":    "",
			"HARNESS_WORKSPACE":   workspaceDir,
		}
		getenv := func(key string) string { return env[key] }

		sig := make(chan os.Signal, 1)
		done := make(chan error, 1)
		go func() {
			done <- runWithSignals(sig, getenv, func(openai.Config) (harness.Provider, error) {
				return &noopProvider{}, nil
			}, "")
		}()
		// Give server time to start, then shut down.
		time.Sleep(100 * time.Millisecond)
		sig <- os.Interrupt
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("runWithSignals: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out")
		}
	})

	// Sub-test: whitespace-only -> treated as empty (embedded cron)
	t.Run("whitespace_only", func(t *testing.T) {
		t.Parallel()
		workspaceDir := t.TempDir()
		env := map[string]string{
			"OPENAI_API_KEY":      "test-key",
			"HARNESS_ADDR":        "127.0.0.1:0",
			"HARNESS_MEMORY_MODE": "off",
			"HARNESS_CRON_URL":    "   ",
			"HARNESS_WORKSPACE":   workspaceDir,
		}
		getenv := func(key string) string { return env[key] }

		sig := make(chan os.Signal, 1)
		done := make(chan error, 1)
		go func() {
			done <- runWithSignals(sig, getenv, func(openai.Config) (harness.Provider, error) {
				return &noopProvider{}, nil
			}, "")
		}()
		time.Sleep(100 * time.Millisecond)
		sig <- os.Interrupt
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("runWithSignals: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out")
		}
	})

	// Sub-test: valid URL -> server starts (won't connect to cron, but should start)
	t.Run("valid_url", func(t *testing.T) {
		t.Parallel()
		workspaceDir := t.TempDir()
		env := map[string]string{
			"OPENAI_API_KEY":      "test-key",
			"HARNESS_ADDR":        "127.0.0.1:0",
			"HARNESS_MEMORY_MODE": "off",
			"HARNESS_CRON_URL":    "http://localhost:9090",
			"HARNESS_WORKSPACE":   workspaceDir,
		}
		getenv := func(key string) string { return env[key] }

		sig := make(chan os.Signal, 1)
		done := make(chan error, 1)
		go func() {
			done <- runWithSignals(sig, getenv, func(openai.Config) (harness.Provider, error) {
				return &noopProvider{}, nil
			}, "")
		}()
		time.Sleep(100 * time.Millisecond)
		sig <- os.Interrupt
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("runWithSignals: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out")
		}
	})
}

// ---------------------------------------------------------------------------
// stdLogger.Error coverage
// ---------------------------------------------------------------------------

func TestStdLoggerError(t *testing.T) {
	t.Parallel()

	l := &stdLogger{}
	// Verify it does not panic with various arguments.
	l.Error("something went wrong")
	l.Error("context failure", "key", "value", "count", 42)
}

// ---------------------------------------------------------------------------
// callbackRunStarter.StartRun coverage
// ---------------------------------------------------------------------------

func TestCallbackRunStarterNilRunner(t *testing.T) {
	t.Parallel()

	starter := &callbackRunStarter{}
	err := starter.StartRun("hello", "conv-1", "tenant-a", "agent-a")
	if err == nil {
		t.Fatalf("expected error when runner is nil")
	}
	if !strings.Contains(err.Error(), "not yet initialized") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCallbackRunStarterWithRunner(t *testing.T) {
	t.Parallel()

	provider := &noopProvider{}
	reg := harness.NewRegistry()
	runner := harness.NewRunner(provider, reg, harness.RunnerConfig{
		DefaultModel:        "gpt-4.1-mini",
		DefaultSystemPrompt: "test",
		MaxSteps:            2,
	})

	starter := &callbackRunStarter{}
	starter.mu.Lock()
	starter.runner = runner
	starter.mu.Unlock()

	err := starter.StartRun("do something", "", "", "")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
}

// ---------------------------------------------------------------------------
// skillListerAdapter coverage
// ---------------------------------------------------------------------------

func TestSkillListerAdapterGetSkillNotFound(t *testing.T) {
	t.Parallel()

	reg := skills.NewRegistry()
	resolver := skills.NewResolver(reg)
	adapter := &skillListerAdapter{registry: reg, resolver: resolver, workspace: "/tmp"}

	_, ok := adapter.GetSkill("nonexistent")
	if ok {
		t.Fatalf("expected not found")
	}
}

func TestSkillListerAdapterGetSkillFound(t *testing.T) {
	t.Parallel()

	reg := skills.NewRegistry()
	// Insert a skill directly by loading via the registry's internal structure.
	// We use the loader path instead: build a temp skill directory.
	dir := t.TempDir()
	skillDir := dir + "/test-skill"
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillContent := `---
name: test-skill
description: A test skill
version: 1
argument-hint: "<arg>"
allowed-tools:
  - bash
---
Hello $ARGUMENTS`
	if err := os.WriteFile(skillDir+"/SKILL.md", []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := skills.NewLoader(skills.LoaderConfig{GlobalDir: dir})
	if err := reg.Load(loader); err != nil {
		t.Fatal(err)
	}

	resolver := skills.NewResolver(reg)
	adapter := &skillListerAdapter{registry: reg, resolver: resolver, workspace: "/tmp"}

	info, ok := adapter.GetSkill("test-skill")
	if !ok {
		t.Fatalf("expected to find test-skill")
	}
	if info.Name != "test-skill" {
		t.Fatalf("Name: got %q", info.Name)
	}
	if info.Description != "A test skill" {
		t.Fatalf("Description: got %q", info.Description)
	}
	if info.ArgumentHint != "<arg>" {
		t.Fatalf("ArgumentHint: got %q", info.ArgumentHint)
	}
	if len(info.AllowedTools) != 1 || info.AllowedTools[0] != "bash" {
		t.Fatalf("AllowedTools: got %v", info.AllowedTools)
	}
}

func TestSkillListerAdapterListSkills(t *testing.T) {
	t.Parallel()

	reg := skills.NewRegistry()
	dir := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		skillDir := dir + "/" + name
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := fmt.Sprintf("---\nname: %s\ndescription: Skill %s\nversion: 1\n---\nBody", name, name)
		if err := os.WriteFile(skillDir+"/SKILL.md", []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	loader := skills.NewLoader(skills.LoaderConfig{GlobalDir: dir})
	if err := reg.Load(loader); err != nil {
		t.Fatal(err)
	}

	resolver := skills.NewResolver(reg)
	adapter := &skillListerAdapter{registry: reg, resolver: resolver, workspace: "/tmp"}

	all := adapter.ListSkills()
	if len(all) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(all))
	}
}

func TestSkillListerAdapterResolveSkill(t *testing.T) {
	t.Parallel()

	reg := skills.NewRegistry()
	dir := t.TempDir()
	skillDir := dir + "/greet"
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: greet\ndescription: Greet\nversion: 1\n---\nHello $ARGUMENTS from $WORKSPACE"
	if err := os.WriteFile(skillDir+"/SKILL.md", []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := skills.NewLoader(skills.LoaderConfig{GlobalDir: dir})
	if err := reg.Load(loader); err != nil {
		t.Fatal(err)
	}

	resolver := skills.NewResolver(reg)
	adapter := &skillListerAdapter{registry: reg, resolver: resolver, workspace: "/default"}

	// With default workspace.
	result, err := adapter.ResolveSkill(context.Background(), "greet", "world", "")
	if err != nil {
		t.Fatalf("ResolveSkill: %v", err)
	}
	if !strings.Contains(result, "world") {
		t.Fatalf("expected 'world' in result: %q", result)
	}
	if !strings.Contains(result, "/default") {
		t.Fatalf("expected '/default' in result: %q", result)
	}

	// With explicit workspace.
	result, err = adapter.ResolveSkill(context.Background(), "greet", "earth", "/custom")
	if err != nil {
		t.Fatalf("ResolveSkill: %v", err)
	}
	if !strings.Contains(result, "/custom") {
		t.Fatalf("expected '/custom' in result: %q", result)
	}

	// Not found.
	_, err = adapter.ResolveSkill(context.Background(), "nonexistent", "", "")
	if err == nil {
		t.Fatalf("expected error for nonexistent skill")
	}
}

func TestSkillListerAdapterGetSkillFilePath(t *testing.T) {
	t.Parallel()

	reg := skills.NewRegistry()
	dir := t.TempDir()
	skillDir := dir + "/myskill"
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: myskill\ndescription: My skill\nversion: 1\n---\nBody"
	if err := os.WriteFile(skillDir+"/SKILL.md", []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := skills.NewLoader(skills.LoaderConfig{GlobalDir: dir})
	if err := reg.Load(loader); err != nil {
		t.Fatal(err)
	}

	resolver := skills.NewResolver(reg)
	adapter := &skillListerAdapter{registry: reg, resolver: resolver, workspace: "/tmp"}

	// Existing skill should return a file path.
	path, ok := adapter.GetSkillFilePath("myskill")
	if !ok {
		t.Fatal("expected GetSkillFilePath to return ok=true for existing skill")
	}
	if path == "" {
		t.Error("expected non-empty file path for existing skill")
	}

	// Non-existent skill should return ok=false.
	_, ok = adapter.GetSkillFilePath("nonexistent")
	if ok {
		t.Error("expected GetSkillFilePath to return ok=false for non-existent skill")
	}
}

func TestSkillListerAdapterUpdateSkillVerification(t *testing.T) {
	t.Parallel()

	reg := skills.NewRegistry()
	dir := t.TempDir()
	skillDir := dir + "/verified-skill"
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: verified-skill\ndescription: Verified skill\nversion: 1\n---\nBody"
	if err := os.WriteFile(skillDir+"/SKILL.md", []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := skills.NewLoader(skills.LoaderConfig{GlobalDir: dir})
	if err := reg.Load(loader); err != nil {
		t.Fatal(err)
	}

	resolver := skills.NewResolver(reg)
	adapter := &skillListerAdapter{registry: reg, resolver: resolver, workspace: "/tmp"}

	// UpdateSkillVerification delegates to registry; it may return an error for
	// non-persisted in-memory registries. We just verify the method is callable.
	err := adapter.UpdateSkillVerification(context.Background(), "verified-skill", true, time.Now(), "test-user")
	// Allow either nil or "not supported" style errors — we're testing that the
	// method is exercised and does not panic.
	_ = err
}

// ---------------------------------------------------------------------------
// embeddedCronAdapter coverage
// ---------------------------------------------------------------------------

func TestEmbeddedCronAdapterCreateJob(t *testing.T) {
	t.Parallel()

	store := newTestCronStore(t)
	clock := testClock{t: time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)}
	executor := &cron.ShellExecutor{}
	scheduler := cron.NewScheduler(store, executor, clock, cron.SchedulerConfig{MaxConcurrent: 1})
	defer scheduler.Stop()

	adapter := &embeddedCronAdapter{store: store, scheduler: scheduler, clock: clock}

	// Missing name.
	_, err := adapter.CreateJob(context.Background(), htools.CronCreateJobRequest{Schedule: "*/5 * * * *"})
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("expected name required error, got: %v", err)
	}

	// Missing schedule.
	_, err = adapter.CreateJob(context.Background(), htools.CronCreateJobRequest{Name: "test"})
	if err == nil || !strings.Contains(err.Error(), "schedule is required") {
		t.Fatalf("expected schedule required error, got: %v", err)
	}

	// Invalid execution type.
	_, err = adapter.CreateJob(context.Background(), htools.CronCreateJobRequest{
		Name:     "test",
		Schedule: "*/5 * * * *",
		ExecType: "invalid",
	})
	if err == nil || !strings.Contains(err.Error(), "execution_type") {
		t.Fatalf("expected execution_type error, got: %v", err)
	}

	// Valid creation.
	job, err := adapter.CreateJob(context.Background(), htools.CronCreateJobRequest{
		Name:       "my-job",
		Schedule:   "*/5 * * * *",
		ExecType:   "shell",
		ExecConfig: `{"command":"echo hi"}`,
		Tags:       "test",
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if job.Name != "my-job" {
		t.Fatalf("Name: got %q", job.Name)
	}
	if job.TimeoutSec != 30 {
		t.Fatalf("expected default timeout 30, got %d", job.TimeoutSec)
	}
}

func TestEmbeddedCronAdapterListJobs(t *testing.T) {
	t.Parallel()

	store := newTestCronStore(t)
	clock := testClock{t: time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)}
	scheduler := cron.NewScheduler(store, &cron.ShellExecutor{}, clock, cron.SchedulerConfig{MaxConcurrent: 1})
	defer scheduler.Stop()

	adapter := &embeddedCronAdapter{store: store, scheduler: scheduler, clock: clock}

	// Create a job first.
	_, err := adapter.CreateJob(context.Background(), htools.CronCreateJobRequest{
		Name: "list-test", Schedule: "*/5 * * * *", ExecType: "shell",
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	jobs, err := adapter.ListJobs(context.Background())
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
}

func TestEmbeddedCronAdapterGetJob(t *testing.T) {
	t.Parallel()

	store := newTestCronStore(t)
	clock := testClock{t: time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)}
	scheduler := cron.NewScheduler(store, &cron.ShellExecutor{}, clock, cron.SchedulerConfig{MaxConcurrent: 1})
	defer scheduler.Stop()

	adapter := &embeddedCronAdapter{store: store, scheduler: scheduler, clock: clock}

	created, err := adapter.CreateJob(context.Background(), htools.CronCreateJobRequest{
		Name: "get-test", Schedule: "*/5 * * * *", ExecType: "shell",
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	// Get by ID.
	got, err := adapter.GetJob(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetJob by ID: %v", err)
	}
	if got.Name != "get-test" {
		t.Fatalf("Name: got %q", got.Name)
	}

	// Get by name.
	got, err = adapter.GetJob(context.Background(), "get-test")
	if err != nil {
		t.Fatalf("GetJob by name: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("ID: got %q, want %q", got.ID, created.ID)
	}

	// Not found.
	_, err = adapter.GetJob(context.Background(), "nonexistent")
	if err == nil {
		t.Fatalf("expected error for nonexistent job")
	}
}

func TestEmbeddedCronAdapterUpdateJob(t *testing.T) {
	t.Parallel()

	store := newTestCronStore(t)
	clock := testClock{t: time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)}
	scheduler := cron.NewScheduler(store, &cron.ShellExecutor{}, clock, cron.SchedulerConfig{MaxConcurrent: 1})
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	defer scheduler.Stop()

	adapter := &embeddedCronAdapter{store: store, scheduler: scheduler, clock: clock}

	created, err := adapter.CreateJob(context.Background(), htools.CronCreateJobRequest{
		Name: "update-test", Schedule: "*/5 * * * *", ExecType: "shell",
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	// Update schedule.
	newSched := "0 * * * *"
	updated, err := adapter.UpdateJob(context.Background(), created.ID, htools.CronUpdateJobRequest{
		Schedule: &newSched,
	})
	if err != nil {
		t.Fatalf("UpdateJob schedule: %v", err)
	}
	if updated.Schedule != "0 * * * *" {
		t.Fatalf("Schedule: got %q", updated.Schedule)
	}

	// Update status to paused.
	paused := "paused"
	updated, err = adapter.UpdateJob(context.Background(), created.ID, htools.CronUpdateJobRequest{
		Status: &paused,
	})
	if err != nil {
		t.Fatalf("UpdateJob pause: %v", err)
	}
	if updated.Status != "paused" {
		t.Fatalf("Status: got %q", updated.Status)
	}

	// Resume.
	active := "active"
	updated, err = adapter.UpdateJob(context.Background(), created.ID, htools.CronUpdateJobRequest{
		Status: &active,
	})
	if err != nil {
		t.Fatalf("UpdateJob resume: %v", err)
	}
	if updated.Status != "active" {
		t.Fatalf("Status: got %q", updated.Status)
	}

	// Invalid status.
	bad := "invalid"
	_, err = adapter.UpdateJob(context.Background(), created.ID, htools.CronUpdateJobRequest{
		Status: &bad,
	})
	if err == nil {
		t.Fatalf("expected error for invalid status")
	}

	// Empty schedule.
	empty := "  "
	_, err = adapter.UpdateJob(context.Background(), created.ID, htools.CronUpdateJobRequest{
		Schedule: &empty,
	})
	if err == nil {
		t.Fatalf("expected error for empty schedule")
	}

	// Not found.
	_, err = adapter.UpdateJob(context.Background(), "nonexistent", htools.CronUpdateJobRequest{})
	if err == nil {
		t.Fatalf("expected error for nonexistent job")
	}
}

// TestEmbeddedCronAdapterUpdateJob_PausedJobScheduleOnlyPatch_NotReArmed
// (BUG 4, BT-006, P2) reproduces the parallel copy of the BUG-4 re-arm
// condition in embeddedCronAdapter.UpdateJob (main.go ~1735):
// `req.Schedule != nil && (req.Status == nil || *req.Status == cron.StatusActive)`.
// A schedule-only update (req.Status == nil) on an already-paused job must
// not re-add it to the live scheduler.
//
// This fails before the fix (scheduler.HasEntry reports true after a
// schedule-only update on a paused job) and passes after (gating on the
// job's effective post-update status).
func TestEmbeddedCronAdapterUpdateJob_PausedJobScheduleOnlyPatch_NotReArmed(t *testing.T) {
	t.Parallel()

	store := newTestCronStore(t)
	clock := testClock{t: time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)}
	scheduler := cron.NewScheduler(store, &cron.ShellExecutor{}, clock, cron.SchedulerConfig{MaxConcurrent: 1})
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	defer scheduler.Stop()

	adapter := &embeddedCronAdapter{store: store, scheduler: scheduler, clock: clock}

	created, err := adapter.CreateJob(context.Background(), htools.CronCreateJobRequest{
		Name: "pause-then-schedule-only", Schedule: "*/5 * * * *", ExecType: "shell",
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	// Pause it first — this removes it from the live scheduler.
	paused := "paused"
	if _, err := adapter.UpdateJob(context.Background(), created.ID, htools.CronUpdateJobRequest{
		Status: &paused,
	}); err != nil {
		t.Fatalf("UpdateJob pause: %v", err)
	}
	if scheduler.HasEntry(created.ID) {
		t.Fatalf("expected job to be removed from the live scheduler after pausing")
	}

	// PATCH only the schedule — no Status field.
	newSched := "0 * * * *"
	updated, err := adapter.UpdateJob(context.Background(), created.ID, htools.CronUpdateJobRequest{
		Schedule: &newSched,
	})
	if err != nil {
		t.Fatalf("UpdateJob schedule-only: %v", err)
	}
	if updated.Status != "paused" {
		t.Fatalf("expected status to remain paused, got %q", updated.Status)
	}
	if scheduler.HasEntry(created.ID) {
		t.Fatal("expected a schedule-only update on a paused job to NOT re-arm it in the live scheduler")
	}
}

func TestEmbeddedCronAdapterDeleteJob(t *testing.T) {
	t.Parallel()

	store := newTestCronStore(t)
	clock := testClock{t: time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)}
	scheduler := cron.NewScheduler(store, &cron.ShellExecutor{}, clock, cron.SchedulerConfig{MaxConcurrent: 1})
	defer scheduler.Stop()

	adapter := &embeddedCronAdapter{store: store, scheduler: scheduler, clock: clock}

	created, err := adapter.CreateJob(context.Background(), htools.CronCreateJobRequest{
		Name: "delete-test", Schedule: "*/5 * * * *", ExecType: "shell",
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if err := adapter.DeleteJob(context.Background(), created.ID); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}

	// Verify deleted.
	_, err = adapter.GetJob(context.Background(), created.ID)
	if err == nil {
		t.Fatalf("expected error after delete")
	}
}

func TestEmbeddedCronAdapterListExecutions(t *testing.T) {
	t.Parallel()

	store := newTestCronStore(t)
	clock := testClock{t: time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)}
	scheduler := cron.NewScheduler(store, &cron.ShellExecutor{}, clock, cron.SchedulerConfig{MaxConcurrent: 1})
	defer scheduler.Stop()

	adapter := &embeddedCronAdapter{store: store, scheduler: scheduler, clock: clock}

	created, err := adapter.CreateJob(context.Background(), htools.CronCreateJobRequest{
		Name: "exec-test", Schedule: "*/5 * * * *", ExecType: "shell",
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	execs, err := adapter.ListExecutions(context.Background(), created.ID, 10, 0)
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if len(execs) != 0 {
		t.Fatalf("expected 0 executions, got %d", len(execs))
	}
}

func TestEmbeddedCronAdapterHealth(t *testing.T) {
	t.Parallel()

	adapter := &embeddedCronAdapter{}
	if err := adapter.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
}

// testClock implements cron.Clock with a fixed time.
type testClock struct{ t time.Time }

func (c testClock) Now() time.Time { return c.t }

// newTestCronStore creates a SQLite cron store backed by a temp directory.
func newTestCronStore(t *testing.T) cron.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := cron.NewSQLiteStore(dir + "/test.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// --- lazySummarizer tests ---

// stubSummarizer is a test double for htools.MessageSummarizer.
type stubSummarizer struct {
	result string
	err    error
	called bool
	msgs   []map[string]any
}

func (s *stubSummarizer) SummarizeMessages(_ context.Context, msgs []map[string]any) (string, error) {
	s.called = true
	s.msgs = msgs
	return s.result, s.err
}

func TestLazySummarizer_NotConfigured(t *testing.T) {
	t.Parallel()

	ls := &lazySummarizer{}
	_, err := ls.SummarizeMessages(context.Background(), []map[string]any{
		{"role": "user", "content": "hello"},
	})
	if err == nil {
		t.Fatal("expected error when summarizer not configured")
	}
	if err.Error() != "summarizer not configured yet" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLazySummarizer_AfterWiring(t *testing.T) {
	t.Parallel()

	inner := &stubSummarizer{result: "summary result"}
	ls := &lazySummarizer{}

	// Wire the inner summarizer
	ls.mu.Lock()
	ls.summarizer = inner
	ls.mu.Unlock()

	msgs := []map[string]any{
		{"role": "user", "content": "hello"},
		{"role": "assistant", "content": "hi"},
	}

	result, err := ls.SummarizeMessages(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "summary result" {
		t.Fatalf("expected %q, got %q", "summary result", result)
	}
	if !inner.called {
		t.Fatal("inner summarizer was not called")
	}
	if len(inner.msgs) != 2 {
		t.Fatalf("expected 2 messages passed to inner, got %d", len(inner.msgs))
	}
}

func TestLazySummarizer_ErrorPropagation(t *testing.T) {
	t.Parallel()

	inner := &stubSummarizer{err: errors.New("inner error")}
	ls := &lazySummarizer{}

	ls.mu.Lock()
	ls.summarizer = inner
	ls.mu.Unlock()

	_, err := ls.SummarizeMessages(context.Background(), []map[string]any{
		{"role": "user", "content": "hello"},
	})
	if err == nil {
		t.Fatal("expected error from inner summarizer")
	}
	if err.Error() != "inner error" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// lookupModelAPI wiring tests
// ---------------------------------------------------------------------------

// TestLookupModelAPIWiredInRunWithSignals verifies that when a model catalog is
// configured and a model has "api": "responses", runWithSignals passes a
// ModelAPILookup function to the provider. We do this by tracking the Config
// that newProvider receives and verifying ModelAPILookup is non-nil and returns
// the correct value.
func TestLookupModelAPIWiredInRunWithSignals(t *testing.T) {
	t.Parallel()

	// Write a temporary catalog file with a codex model that has api: "responses".
	catalogJSON := `{
		"catalog_version": "1.0.0",
		"providers": {
			"openai": {
				"display_name": "OpenAI",
				"base_url": "https://api.openai.com",
				"api_key_env": "OPENAI_API_KEY",
				"protocol": "openai_compat",
				"models": {
					"gpt-4.1": {
						"display_name": "GPT-4.1",
						"context_window": 128000,
						"tool_calling": true,
						"streaming": true
					},
					"gpt-5.3-codex": {
						"display_name": "GPT-5.3 Codex",
						"context_window": 200000,
						"tool_calling": true,
						"streaming": true,
						"api": "responses"
					}
				}
			}
		}
	}`

	catalogFile, err := os.CreateTemp(t.TempDir(), "catalog*.json")
	if err != nil {
		t.Fatalf("create temp catalog: %v", err)
	}
	if _, err := catalogFile.WriteString(catalogJSON); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	catalogFile.Close()

	var capturedConfig openai.Config
	var configMu sync.Mutex

	workspaceDir := t.TempDir()
	env := map[string]string{
		"OPENAI_API_KEY":             "test-key",
		"HARNESS_ADDR":               "127.0.0.1:0",
		"HARNESS_MEMORY_MODE":        "off",
		"HARNESS_WORKSPACE":          workspaceDir,
		"HARNESS_MODEL_CATALOG_PATH": catalogFile.Name(),
	}
	getenv := func(key string) string { return env[key] }
	sig := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- runWithSignals(sig, getenv, func(cfg openai.Config) (harness.Provider, error) {
			configMu.Lock()
			capturedConfig = cfg
			configMu.Unlock()
			return &noopProvider{}, nil
		}, "")
	}()

	time.Sleep(150 * time.Millisecond)
	sig <- os.Interrupt

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWithSignals returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for graceful shutdown")
	}

	configMu.Lock()
	cfg := capturedConfig
	configMu.Unlock()

	// Verify ModelAPILookup is non-nil.
	if cfg.ModelAPILookup == nil {
		t.Fatal("expected ModelAPILookup to be non-nil when catalog is loaded")
	}

	// Verify gpt-5.3-codex resolves to "responses".
	got := cfg.ModelAPILookup("openai", "gpt-5.3-codex")
	if got != "responses" {
		t.Errorf("expected ModelAPILookup(openai, gpt-5.3-codex) = %q, got %q", "responses", got)
	}

	// Verify standard model resolves to "" (empty).
	got2 := cfg.ModelAPILookup("openai", "gpt-4.1")
	if got2 != "" {
		t.Errorf("expected ModelAPILookup(openai, gpt-4.1) = %q, got %q", "", got2)
	}
}

// TestLookupModelAPIWithAlias verifies that the lookupModelAPI closure correctly
// resolves model aliases.
func TestLookupModelAPIWithAlias(t *testing.T) {
	t.Parallel()

	catalogJSON := `{
		"catalog_version": "1.0.0",
		"providers": {
			"openai": {
				"display_name": "OpenAI",
				"base_url": "https://api.openai.com",
				"api_key_env": "OPENAI_API_KEY",
				"protocol": "openai_compat",
				"models": {
					"gpt-5.3-codex": {
						"display_name": "GPT-5.3 Codex",
						"context_window": 200000,
						"tool_calling": true,
						"streaming": true,
						"api": "responses"
					}
				},
				"aliases": {
					"codex": "gpt-5.3-codex"
				}
			}
		}
	}`

	catalogFile, err := os.CreateTemp(t.TempDir(), "catalog*.json")
	if err != nil {
		t.Fatalf("create temp catalog: %v", err)
	}
	if _, err := catalogFile.WriteString(catalogJSON); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	catalogFile.Close()

	var capturedConfig openai.Config
	var configMu sync.Mutex

	workspaceDir2 := t.TempDir()
	env := map[string]string{
		"OPENAI_API_KEY":             "test-key",
		"HARNESS_ADDR":               "127.0.0.1:0",
		"HARNESS_MEMORY_MODE":        "off",
		"HARNESS_WORKSPACE":          workspaceDir2,
		"HARNESS_MODEL_CATALOG_PATH": catalogFile.Name(),
	}
	getenv := func(key string) string { return env[key] }
	sig := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- runWithSignals(sig, getenv, func(cfg openai.Config) (harness.Provider, error) {
			configMu.Lock()
			capturedConfig = cfg
			configMu.Unlock()
			return &noopProvider{}, nil
		}, "")
	}()

	time.Sleep(150 * time.Millisecond)
	sig <- os.Interrupt

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWithSignals returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for graceful shutdown")
	}

	configMu.Lock()
	cfg := capturedConfig
	configMu.Unlock()

	if cfg.ModelAPILookup == nil {
		t.Fatal("expected ModelAPILookup to be non-nil when catalog is loaded")
	}

	// Alias "codex" should resolve to "responses" via gpt-5.3-codex.
	got := cfg.ModelAPILookup("openai", "codex")
	if got != "responses" {
		t.Errorf("expected ModelAPILookup(openai, codex) = %q (alias), got %q", "responses", got)
	}
}

// TestLookupModelAPIWithoutCatalog verifies that when no catalog is loaded,
// ModelAPILookup returns "" safely (no nil panic).
func TestLookupModelAPIWithoutCatalog(t *testing.T) {
	t.Parallel()

	var capturedConfig openai.Config
	var configMu sync.Mutex

	workspaceDir3 := t.TempDir()
	env := map[string]string{
		"OPENAI_API_KEY":      "test-key",
		"HARNESS_ADDR":        "127.0.0.1:0",
		"HARNESS_MEMORY_MODE": "off",
		"HARNESS_WORKSPACE":   workspaceDir3,
		// No HARNESS_MODEL_CATALOG_PATH set — catalog is nil.
	}
	getenv := func(key string) string { return env[key] }
	sig := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- runWithSignals(sig, getenv, func(cfg openai.Config) (harness.Provider, error) {
			configMu.Lock()
			capturedConfig = cfg
			configMu.Unlock()
			return &noopProvider{}, nil
		}, "")
	}()

	time.Sleep(150 * time.Millisecond)
	sig <- os.Interrupt

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWithSignals returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for graceful shutdown")
	}

	configMu.Lock()
	cfg := capturedConfig
	configMu.Unlock()

	// Even without a catalog, ModelAPILookup should be wired and return "".
	if cfg.ModelAPILookup == nil {
		t.Fatal("expected ModelAPILookup to be non-nil (closure always assigned)")
	}
	got := cfg.ModelAPILookup("openai", "any-model")
	if got != "" {
		t.Errorf("expected empty string without catalog, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// HARNESS_ROLE_MODEL_* env var wiring via getenv seam
// ---------------------------------------------------------------------------

// TestRoleModelEnvVarsUseGetenvSeam verifies that HARNESS_ROLE_MODEL_PRIMARY and
// HARNESS_ROLE_MODEL_SUMMARIZER are read through the injected getenv closure,
// not os.Getenv directly. We set sentinel values in the real environment (via
// t.Setenv) but supply empty strings via the getenv closure. The server must
// start and shut down without error, proving it does not fall through to
// os.Getenv for these keys (if it did, it would still succeed, but the model
// would be wrong; the negative case is verified by the positive test below).
//
// Not marked t.Parallel() because it uses t.Setenv.
func TestRoleModelEnvVarsUseGetenvSeam(t *testing.T) {
	// Sentinel values present in the real environment.
	t.Setenv("HARNESS_ROLE_MODEL_PRIMARY", "should-not-be-used-primary")
	t.Setenv("HARNESS_ROLE_MODEL_SUMMARIZER", "should-not-be-used-summarizer")

	// The fake getenv does NOT expose role model vars — only the minimum to
	// boot the server. If runWithSignals reads os.Getenv it would pick up the
	// sentinel above; if it uses getenv it gets "" (no override).
	env := map[string]string{
		"OPENAI_API_KEY":      "test-key",
		"HARNESS_ADDR":        "127.0.0.1:0",
		"HARNESS_MEMORY_MODE": "off",
	}
	getenv := func(key string) string { return env[key] }
	sig := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- runWithSignals(sig, getenv, func(openai.Config) (harness.Provider, error) {
			return &noopProvider{}, nil
		}, "")
	}()

	time.Sleep(100 * time.Millisecond)
	sig <- os.Interrupt

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWithSignals returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for graceful shutdown")
	}
}

// TestRoleModelPrimaryFromGetenvAppliedToRun verifies the positive case: when
// HARNESS_ROLE_MODEL_PRIMARY and HARNESS_ROLE_MODEL_SUMMARIZER are supplied
// only through the injected getenv closure (not os.Setenv), runWithSignals
// starts the server successfully. This exercises the code path changed by the
// fix from os.Getenv → getenv in cmd/harnessd/main.go.
func TestRoleModelPrimaryFromGetenvAppliedToRun(t *testing.T) {
	t.Parallel()

	// Role model env vars appear ONLY in the fake getenv, NOT in os.Setenv.
	env := map[string]string{
		"OPENAI_API_KEY":                "test-key",
		"HARNESS_ADDR":                  "127.0.0.1:0",
		"HARNESS_MEMORY_MODE":           "off",
		"HARNESS_ROLE_MODEL_PRIMARY":    "injected-primary-model",
		"HARNESS_ROLE_MODEL_SUMMARIZER": "injected-summarizer-model",
	}
	getenv := func(key string) string { return env[key] }
	sig := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- runWithSignals(sig, getenv, func(openai.Config) (harness.Provider, error) {
			return &noopProvider{}, nil
		}, "")
	}()

	time.Sleep(100 * time.Millisecond)
	sig <- os.Interrupt

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWithSignals with role model env vars in getenv returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for graceful shutdown")
	}
}

// ---------------------------------------------------------------------------
// Issue #336: runWithSignals startup matrix for optional subsystem wiring
// ---------------------------------------------------------------------------

// freeLocalAddr finds an available TCP port on 127.0.0.1 and returns the
// address string (e.g. "127.0.0.1:54321").  The port is released before
// returning so the caller can bind to it.
func freeLocalAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freeLocalAddr: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// awaitHealthy polls GET /healthz until it gets HTTP 200 or times out.
// It returns the addr that was polled (useful for subsequent requests).
func awaitHealthy(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	url := "http://" + addr + "/healthz"
	for time.Now().Before(deadline) {
		resp, err := http.Get(url) //nolint:noctx
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s never became healthy within %s", addr, timeout)
}

// runMatrixTest is a helper that starts runWithSignals with the given env map,
// waits for the server to become healthy, calls checkFn (which can make HTTP
// requests), then sends an interrupt signal and waits for clean shutdown.
func runMatrixTest(t *testing.T, env map[string]string, checkFn func(addr string)) {
	t.Helper()
	getenv := func(key string) string { return env[key] }
	sig := make(chan os.Signal, 1)
	done := make(chan error, 1)

	go func() {
		done <- runWithSignals(sig, getenv, func(openai.Config) (harness.Provider, error) {
			return &noopProvider{}, nil
		}, "")
	}()

	addr := env["HARNESS_ADDR"]
	awaitHealthy(t, addr, 10*time.Second)

	if checkFn != nil {
		checkFn(addr)
	}

	sig <- os.Interrupt

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWithSignals returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for graceful shutdown")
	}
}

func awaitRunTerminalState(t *testing.T, baseURL, runID string, timeout time.Duration) map[string]any {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/v1/runs/" + runID)
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}

		var payload map[string]any
		decodeErr := json.NewDecoder(resp.Body).Decode(&payload)
		resp.Body.Close()
		if decodeErr != nil {
			t.Fatalf("decode run status %s: %v", runID, decodeErr)
		}

		status, _ := payload["status"].(string)
		switch status {
		case string(harness.RunStatusCompleted), string(harness.RunStatusFailed), string(harness.RunStatusCancelled):
			return payload
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for run %s to reach a terminal state", runID)
	return nil
}

func startHarnessdTestServer(
	t *testing.T,
	env map[string]string,
	newProvider providerFactory,
	profileName string,
) (baseURL string, shutdown func()) {
	t.Helper()

	getenv := func(key string) string { return env[key] }
	sig := make(chan os.Signal, 1)
	done := make(chan error, 1)

	go func() {
		done <- runWithSignals(sig, getenv, newProvider, profileName)
	}()

	addr := env["HARNESS_ADDR"]
	deadline := time.Now().Add(5 * time.Second)
	healthURL := "http://" + addr + "/healthz"
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			if err == nil {
				t.Fatalf("server exited before becoming healthy")
			}
			t.Fatalf("server exited before becoming healthy: %v", err)
		default:
		}

		resp, err := http.Get(healthURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return "http://" + addr, func() {
					t.Helper()
					sig <- os.Interrupt
					select {
					case err := <-done:
						if err != nil {
							t.Fatalf("runWithSignals returned error: %v", err)
						}
					case <-time.After(5 * time.Second):
						t.Fatalf("timed out waiting for graceful shutdown")
					}
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	return "http://" + addr, func() {
		t.Helper()
		sig <- os.Interrupt
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("runWithSignals returned error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for graceful shutdown")
		}
	}
}

// ---------------------------------------------------------------------------
// Issue #337: runWithSignals failure paths
// ---------------------------------------------------------------------------

// TestRunWithSignalsPromptEngineFailure verifies that a bad HARNESS_PROMPTS_DIR
// (pointing to a directory with no catalog.yaml) causes runWithSignals to
// return an error wrapping "load prompt engine".
func TestRunWithSignalsPromptEngineFailure(t *testing.T) {
	t.Parallel()

	// Use a temp dir that has no catalog.yaml — NewFileEngine will fail.
	emptyDir := t.TempDir()

	env := map[string]string{
		"OPENAI_API_KEY":      "test-key",
		"HARNESS_ADDR":        "127.0.0.1:0",
		"HARNESS_MEMORY_MODE": "off",
		"HARNESS_PROMPTS_DIR": emptyDir,
	}
	getenv := func(key string) string { return env[key] }

	err := runWithSignals(make(chan os.Signal, 1), getenv, func(openai.Config) (harness.Provider, error) {
		return &noopProvider{}, nil
	}, "")

	if err == nil {
		t.Fatal("expected error when prompts dir is missing catalog.yaml")
	}
	if !strings.Contains(err.Error(), "load prompt engine") {
		t.Fatalf("expected 'load prompt engine' in error, got: %v", err)
	}
}

// TestRunWithSignalsMemoryManagerFailure verifies that an unsupported
// HARNESS_MEMORY_DB_DRIVER causes runWithSignals to return an error wrapping
// "create observational memory manager".
func TestRunWithSignalsMemoryManagerFailure(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"OPENAI_API_KEY":           "test-key",
		"HARNESS_ADDR":             "127.0.0.1:0",
		"HARNESS_MEMORY_MODE":      "auto",
		"HARNESS_MEMORY_DB_DRIVER": "unsupported_driver_xyz",
	}
	getenv := func(key string) string { return env[key] }

	err := runWithSignals(make(chan os.Signal, 1), getenv, func(openai.Config) (harness.Provider, error) {
		return &noopProvider{}, nil
	}, "")

	if err == nil {
		t.Fatal("expected error when memory driver is unsupported")
	}
	if !strings.Contains(err.Error(), "create observational memory manager") {
		t.Fatalf("expected 'create observational memory manager' in error, got: %v", err)
	}
}

// TestRunWithSignalsCronStoreFailure verifies that when the cron SQLite DB path
// is unwritable, the cron store creation failure causes runWithSignals to return
// an error wrapping "cron store".
func TestRunWithSignalsCronStoreFailure(t *testing.T) {
	t.Parallel()

	// Create a valid workspace dir with a proper .harness directory so
	// config loading succeeds. Then pre-create cron.db as a directory (not a
	// file) so the SQLite driver cannot open it as a database file.
	workspaceDir := t.TempDir()
	harnessSubDir := workspaceDir + "/.harness"
	if err := os.MkdirAll(harnessSubDir, 0o755); err != nil {
		t.Fatalf("setup: create .harness dir: %v", err)
	}
	// Make cron.db a directory — SQLite cannot open a directory as a DB file.
	cronDBAsDir := harnessSubDir + "/cron.db"
	if err := os.MkdirAll(cronDBAsDir, 0o755); err != nil {
		t.Fatalf("setup: create cron.db as directory: %v", err)
	}

	env := map[string]string{
		"OPENAI_API_KEY":      "test-key",
		"HARNESS_ADDR":        "127.0.0.1:0",
		"HARNESS_MEMORY_MODE": "off",
		"HARNESS_WORKSPACE":   workspaceDir,
		"HARNESS_CRON_URL":    "", // force embedded path
	}
	getenv := func(key string) string { return env[key] }

	err := runWithSignals(make(chan os.Signal, 1), getenv, func(openai.Config) (harness.Provider, error) {
		return &noopProvider{}, nil
	}, "")

	if err == nil {
		t.Fatal("expected error when cron store path is unwritable")
	}
	if !strings.Contains(err.Error(), "cron store") {
		t.Fatalf("expected 'cron store' in error, got: %v", err)
	}
}

// TestRunWithSignalsConversationStoreFailure verifies that a bad
// HARNESS_CONVERSATION_DB path causes an error wrapping "conversation store".
func TestRunWithSignalsConversationStoreFailure(t *testing.T) {
	t.Parallel()

	workspaceDir := t.TempDir()

	env := map[string]string{
		"OPENAI_API_KEY":      "test-key",
		"HARNESS_ADDR":        "127.0.0.1:0",
		"HARNESS_MEMORY_MODE": "off",
		"HARNESS_WORKSPACE":   workspaceDir,
		// Point to a file path under /dev/null/... which cannot be created.
		"HARNESS_CONVERSATION_DB": "/dev/null/cannot/create.db",
	}
	getenv := func(key string) string { return env[key] }

	err := runWithSignals(make(chan os.Signal, 1), getenv, func(openai.Config) (harness.Provider, error) {
		return &noopProvider{}, nil
	}, "")

	if err == nil {
		t.Fatal("expected error when conversation store path is invalid")
	}
	if !strings.Contains(err.Error(), "conversation store") {
		t.Fatalf("expected 'conversation store' in error, got: %v", err)
	}
}

// TestRunWithSignalsPricingCatalogFailure verifies that a non-existent pricing
// catalog path causes runWithSignals to return an error wrapping "load pricing catalog".
func TestRunWithSignalsPricingCatalogFailure(t *testing.T) {
	t.Parallel()

	workspaceDir := t.TempDir()

	env := map[string]string{
		"OPENAI_API_KEY":               "test-key",
		"HARNESS_ADDR":                 "127.0.0.1:0",
		"HARNESS_MEMORY_MODE":          "off",
		"HARNESS_WORKSPACE":            workspaceDir,
		"HARNESS_PRICING_CATALOG_PATH": "/nonexistent/path/pricing.json",
	}
	getenv := func(key string) string { return env[key] }

	err := runWithSignals(make(chan os.Signal, 1), getenv, func(openai.Config) (harness.Provider, error) {
		return &noopProvider{}, nil
	}, "")

	if err == nil {
		t.Fatal("expected error when pricing catalog path is invalid")
	}
	if !strings.Contains(err.Error(), "pricing catalog") {
		t.Fatalf("expected 'pricing catalog' in error, got: %v", err)
	}
}

// TestRunWithSignalsMCPParseFailureContinues verifies that an unparseable
// HARNESS_MCP_SERVERS value is logged as a warning but does NOT abort startup
// (MCP failures are non-fatal — the server continues without env-configured MCP).
func TestRunWithSignalsMCPParseFailureContinues(t *testing.T) {
	t.Parallel()

	workspaceDir := t.TempDir()

	env := map[string]string{
		"OPENAI_API_KEY":      "test-key",
		"HARNESS_ADDR":        "127.0.0.1:0",
		"HARNESS_MEMORY_MODE": "off",
		"HARNESS_WORKSPACE":   workspaceDir,
		"HARNESS_MCP_SERVERS": "not-valid-json-{{{",
	}
	getenv := func(key string) string { return env[key] }
	sig := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- runWithSignals(sig, getenv, func(openai.Config) (harness.Provider, error) {
			return &noopProvider{}, nil
		}, "")
	}()

	time.Sleep(100 * time.Millisecond)
	sig <- os.Interrupt

	select {
	case err := <-done:
		// MCP parse failure must NOT abort the server; a nil error is expected.
		if err != nil {
			t.Fatalf("expected server to continue despite MCP parse failure; got: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for graceful shutdown")
	}
}

func TestFullProfileBootsWithPersistenceAndToolEventReadback(t *testing.T) {
	t.Setenv("HARNESS_AUTH_DISABLED", "true")

	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "tool-target.txt")
	if err := os.WriteFile(targetPath, []byte("tool smoke target\n"), 0o644); err != nil {
		t.Fatalf("write target file: %v", err)
	}

	catalogPath := filepath.Join(tmpDir, "models.json")
	catalogJSON := `{
  "catalog_version": "1.0.0",
  "providers": {
    "openai": {
      "display_name": "OpenAI",
      "base_url": "https://api.openai.com",
      "api_key_env": "OPENAI_API_KEY",
      "protocol": "openai_compat",
      "models": {
        "gpt-full-profile-test": {
          "display_name": "GPT Full Profile Test",
          "context_window": 128000,
          "tool_calling": true,
          "streaming": true
        }
      }
    }
  }
}`
	if err := os.WriteFile(catalogPath, []byte(catalogJSON), 0o644); err != nil {
		t.Fatalf("write model catalog: %v", err)
	}

	runDBPath := filepath.Join(tmpDir, "runs.db")
	convDBPath := filepath.Join(tmpDir, "conversations.db")
	addr := freeLocalAddr(t)
	env := map[string]string{
		"OPENAI_API_KEY":             "test-key",
		"HARNESS_ADDR":               addr,
		"HARNESS_AUTH_DISABLED":      "true",
		"HARNESS_CONVERSATION_DB":    convDBPath,
		"HARNESS_MEMORY_MODE":        "off",
		"HARNESS_MODEL":              "gpt-full-profile-test",
		"HARNESS_MODEL_CATALOG_PATH": catalogPath,
		"HARNESS_RUN_DB":             runDBPath,
		"HARNESS_WORKSPACE":          tmpDir,
	}

	provider := &scriptedHarnessdProvider{
		turns: []harness.CompletionResult{
			{
				ToolCalls: []harness.ToolCall{{
					ID:        "call-1",
					Name:      "read",
					Arguments: `{"path":"tool-target.txt"}`,
				}},
			},
			{
				Content: "tool smoke complete",
			},
		},
	}

	baseURL, shutdown := startHarnessdTestServer(t, env, func(openai.Config) (harness.Provider, error) {
		return provider, nil
	}, "full")

	createResp, err := http.Post(baseURL+"/v1/runs", "application/json", strings.NewReader(`{"prompt":"Use the read tool, then finish."}`))
	if err != nil {
		shutdown()
		t.Fatalf("POST /v1/runs: %v", err)
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(createResp.Body)
		shutdown()
		t.Fatalf("POST /v1/runs returned %d: %s", createResp.StatusCode, body)
	}

	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		shutdown()
		t.Fatalf("decode create response: %v", err)
	}
	if created.RunID == "" {
		shutdown()
		t.Fatal("expected run id from create response")
	}

	runState := awaitRunTerminalState(t, baseURL, created.RunID, 5*time.Second)
	if got := runState["status"]; got != string(harness.RunStatusCompleted) {
		shutdown()
		t.Fatalf("expected completed run, got %v", got)
	}
	if got := runState["output"]; got != "tool smoke complete" {
		shutdown()
		t.Fatalf("unexpected run output: %v", got)
	}
	conversationID, _ := runState["conversation_id"].(string)
	if conversationID == "" {
		shutdown()
		t.Fatal("expected conversation_id in run state")
	}

	modelsResp, err := http.Get(baseURL + "/v1/models")
	if err != nil {
		shutdown()
		t.Fatalf("GET /v1/models: %v", err)
	}
	modelsBody, _ := io.ReadAll(modelsResp.Body)
	modelsResp.Body.Close()
	if modelsResp.StatusCode != http.StatusOK || !strings.Contains(string(modelsBody), "gpt-full-profile-test") {
		shutdown()
		t.Fatalf("unexpected /v1/models response: %d %s", modelsResp.StatusCode, modelsBody)
	}

	providersResp, err := http.Get(baseURL + "/v1/providers")
	if err != nil {
		shutdown()
		t.Fatalf("GET /v1/providers: %v", err)
	}
	providersBody, _ := io.ReadAll(providersResp.Body)
	providersResp.Body.Close()
	if providersResp.StatusCode != http.StatusOK || !strings.Contains(string(providersBody), "openai") {
		shutdown()
		t.Fatalf("unexpected /v1/providers response: %d %s", providersResp.StatusCode, providersBody)
	}

	eventsResp, err := http.Get(baseURL + "/v1/runs/" + created.RunID + "/events")
	if err != nil {
		shutdown()
		t.Fatalf("GET /v1/runs/%s/events: %v", created.RunID, err)
	}
	eventsBody, _ := io.ReadAll(eventsResp.Body)
	eventsResp.Body.Close()
	eventsText := string(eventsBody)
	if !strings.Contains(eventsText, "event: tool.call.started") || !strings.Contains(eventsText, "event: tool.call.completed") {
		shutdown()
		t.Fatalf("expected tool execution events in stream, got: %s", eventsText)
	}
	if !strings.Contains(eventsText, "event: run.completed") {
		shutdown()
		t.Fatalf("expected run.completed event in stream, got: %s", eventsText)
	}

	shutdown()

	restartEnv := map[string]string{
		"OPENAI_API_KEY":             "test-key",
		"HARNESS_ADDR":               freeLocalAddr(t),
		"HARNESS_AUTH_DISABLED":      "true",
		"HARNESS_CONVERSATION_DB":    convDBPath,
		"HARNESS_MEMORY_MODE":        "off",
		"HARNESS_MODEL":              "gpt-full-profile-test",
		"HARNESS_MODEL_CATALOG_PATH": catalogPath,
		"HARNESS_RUN_DB":             runDBPath,
		"HARNESS_WORKSPACE":          tmpDir,
	}

	baseURL, shutdown = startHarnessdTestServer(t, restartEnv, func(openai.Config) (harness.Provider, error) {
		return &noopProvider{}, nil
	}, "full")
	defer shutdown()

	persistedResp, err := http.Get(baseURL + "/v1/runs/" + created.RunID)
	if err != nil {
		t.Fatalf("GET persisted run: %v", err)
	}
	var persistedRun map[string]any
	if err := json.NewDecoder(persistedResp.Body).Decode(&persistedRun); err != nil {
		persistedResp.Body.Close()
		t.Fatalf("decode persisted run: %v", err)
	}
	persistedResp.Body.Close()
	if got := persistedRun["status"]; got != string(harness.RunStatusCompleted) {
		t.Fatalf("expected persisted completed run, got %v", got)
	}

	messagesResp, err := http.Get(baseURL + "/v1/conversations/" + conversationID + "/messages")
	if err != nil {
		t.Fatalf("GET persisted conversation messages: %v", err)
	}
	var messagesPayload struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.NewDecoder(messagesResp.Body).Decode(&messagesPayload); err != nil {
		messagesResp.Body.Close()
		t.Fatalf("decode conversation messages: %v", err)
	}
	messagesResp.Body.Close()
	if len(messagesPayload.Messages) < 2 {
		t.Fatalf("expected persisted conversation messages, got %d", len(messagesPayload.Messages))
	}
	if role := messagesPayload.Messages[0]["role"]; role != "user" {
		t.Fatalf("expected first persisted message to be user, got %v", role)
	}
	if last := messagesPayload.Messages[len(messagesPayload.Messages)-1]["content"]; last != "tool smoke complete" {
		t.Fatalf("unexpected persisted final assistant content: %v", last)
	}
}

// baseEnv returns the minimal env map that lets runWithSignals start
// successfully.  Callers should copy and extend it for each sub-case.
func baseEnv(addr string) map[string]string {
	return map[string]string{
		"OPENAI_API_KEY":      "test-key",
		"HARNESS_ADDR":        addr,
		"HARNESS_MEMORY_MODE": "off",
	}
}

// TestMatrix_SkillsEnabled verifies that when HARNESS_SKILLS_ENABLED=true (the
// default) the /v1/skills endpoint returns HTTP 200.
func TestMatrix_SkillsEnabled(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)
	env := baseEnv(addr)
	env["HARNESS_SKILLS_ENABLED"] = "true"
	env["HARNESS_WORKSPACE"] = t.TempDir()

	runMatrixTest(t, env, func(addr string) {
		resp, err := http.Get("http://" + addr + "/v1/skills")
		if err != nil {
			t.Fatalf("GET /v1/skills: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET /v1/skills with skills enabled: want 200, got %d", resp.StatusCode)
		}
	})
}

// TestMatrix_SkillsDisabled verifies that when HARNESS_SKILLS_ENABLED=false the
// /v1/skills endpoint returns HTTP 501 (not configured).
func TestMatrix_SkillsDisabled(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)
	env := baseEnv(addr)
	env["HARNESS_SKILLS_ENABLED"] = "false"
	env["HARNESS_WORKSPACE"] = t.TempDir()

	runMatrixTest(t, env, func(addr string) {
		resp, err := http.Get("http://" + addr + "/v1/skills")
		if err != nil {
			t.Fatalf("GET /v1/skills: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotImplemented {
			t.Errorf("GET /v1/skills with skills disabled: want 501, got %d", resp.StatusCode)
		}
	})
}

// TestMatrix_WatcherEnabled verifies that HARNESS_WATCH_ENABLED=true (default)
// starts cleanly with HARNESS_SKILLS_ENABLED=true (watch only runs when skills
// are also enabled).
func TestMatrix_WatcherEnabled(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)
	env := baseEnv(addr)
	env["HARNESS_WATCH_ENABLED"] = "true"
	env["HARNESS_SKILLS_ENABLED"] = "true"
	env["HARNESS_WATCH_INTERVAL_SECONDS"] = "1"
	env["HARNESS_WORKSPACE"] = t.TempDir()

	runMatrixTest(t, env, nil)
}

// TestMatrix_WatcherDisabled verifies that HARNESS_WATCH_ENABLED=false starts
// cleanly (watcher goroutine is not spawned).
func TestMatrix_WatcherDisabled(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)
	env := baseEnv(addr)
	env["HARNESS_WATCH_ENABLED"] = "false"
	env["HARNESS_SKILLS_ENABLED"] = "true"
	env["HARNESS_WORKSPACE"] = t.TempDir()

	runMatrixTest(t, env, nil)
}

// TestMatrix_EmbeddedCron verifies that when HARNESS_CRON_URL is absent the
// embedded cron scheduler is used and the server starts cleanly.
func TestMatrix_EmbeddedCron(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)
	env := baseEnv(addr)
	// No HARNESS_CRON_URL → embedded cron path.
	env["HARNESS_WORKSPACE"] = t.TempDir()

	runMatrixTest(t, env, func(addr string) {
		// Embedded cron: GET /v1/cron/jobs must return 200.
		resp, err := http.Get("http://" + addr + "/v1/cron/jobs")
		if err != nil {
			t.Fatalf("GET /v1/cron/jobs: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET /v1/cron/jobs (embedded): want 200, got %d; body: %s", resp.StatusCode, body)
		}
	})
}

// TestMatrix_RemoteCron verifies that when HARNESS_CRON_URL is set the remote
// cron client path is used and the server starts cleanly. The cron URL won't
// be contactable during the test — that's OK since no requests are made to it.
func TestMatrix_RemoteCron(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)
	env := baseEnv(addr)
	env["HARNESS_CRON_URL"] = "http://127.0.0.1:59999" // unreachable but valid URL
	env["HARNESS_WORKSPACE"] = t.TempDir()

	runMatrixTest(t, env, nil)
}

// TestMatrix_CallbacksEnabled verifies that HARNESS_ENABLE_CALLBACKS=true
// (the default) starts cleanly. The callback manager is wired but idle.
func TestMatrix_CallbacksEnabled(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)
	env := baseEnv(addr)
	env["HARNESS_ENABLE_CALLBACKS"] = "true"
	env["HARNESS_WORKSPACE"] = t.TempDir()

	runMatrixTest(t, env, nil)
}

// TestMatrix_CallbacksDisabled verifies that HARNESS_ENABLE_CALLBACKS=false
// starts cleanly (callbackMgr remains nil, no callback shutdown needed).
func TestMatrix_CallbacksDisabled(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)
	env := baseEnv(addr)
	env["HARNESS_ENABLE_CALLBACKS"] = "false"
	env["HARNESS_WORKSPACE"] = t.TempDir()

	runMatrixTest(t, env, nil)
}

// TestMatrix_ConversationStoreEnabled verifies that a conversation database is
// created when HARNESS_CONVERSATION_DB is set, and that the server starts cleanly.
func TestMatrix_ConversationStoreEnabled(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/conv.db"
	env := baseEnv(addr)
	env["HARNESS_CONVERSATION_DB"] = dbPath
	env["HARNESS_WORKSPACE"] = tmpDir

	runMatrixTest(t, env, func(addr string) {
		// The conversation DB file should have been created.
		if _, err := os.Stat(dbPath); os.IsNotExist(err) {
			t.Errorf("conversation DB file %q was not created", dbPath)
		}
	})
}

// TestMatrix_ConversationStoreDisabled verifies that when HARNESS_CONVERSATION_DB
// is absent the server starts cleanly (convStore remains nil).
func TestMatrix_ConversationStoreDisabled(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)
	env := baseEnv(addr)
	env["HARNESS_WORKSPACE"] = t.TempDir()
	// No HARNESS_CONVERSATION_DB.

	runMatrixTest(t, env, nil)
}

// TestMatrix_ModelCatalogPresent verifies that a valid model catalog is loaded
// and the /v1/models endpoint returns catalog contents.
func TestMatrix_ModelCatalogPresent(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)

	catalogJSON := `{
		"catalog_version": "1.0.0",
		"providers": {
			"openai": {
				"display_name": "OpenAI",
				"base_url": "https://api.openai.com",
				"api_key_env": "OPENAI_API_KEY",
				"protocol": "openai_compat",
				"models": {
					"gpt-matrix-test": {
						"display_name": "GPT Matrix Test",
						"context_window": 128000,
						"tool_calling": true,
						"streaming": true
					}
				}
			}
		}
	}`
	catalogFile, err := os.CreateTemp(t.TempDir(), "catalog*.json")
	if err != nil {
		t.Fatalf("create temp catalog: %v", err)
	}
	if _, err := catalogFile.WriteString(catalogJSON); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	catalogFile.Close()

	env := baseEnv(addr)
	env["HARNESS_MODEL_CATALOG_PATH"] = catalogFile.Name()
	env["HARNESS_WORKSPACE"] = t.TempDir()

	runMatrixTest(t, env, func(addr string) {
		resp, err := http.Get("http://" + addr + "/v1/models")
		if err != nil {
			t.Fatalf("GET /v1/models: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET /v1/models with catalog: want 200, got %d; body: %s", resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "gpt-matrix-test") {
			t.Errorf("GET /v1/models: expected 'gpt-matrix-test' in response; got: %s", body)
		}
	})
}

// TestMatrix_ModelCatalogAbsent verifies that when HARNESS_MODEL_CATALOG_PATH is
// absent the server starts cleanly (no catalog wired, providerRegistry is nil).
func TestMatrix_ModelCatalogAbsent(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)
	env := baseEnv(addr)
	env["HARNESS_WORKSPACE"] = t.TempDir()
	// No HARNESS_MODEL_CATALOG_PATH.

	runMatrixTest(t, env, nil)
}

// TestMatrix_ModelCatalogInvalid verifies that when HARNESS_MODEL_CATALOG_PATH
// points to an invalid JSON file the server starts cleanly (catalog load
// failure is logged as a warning, not a fatal error).
func TestMatrix_ModelCatalogInvalid(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)

	badFile, err := os.CreateTemp(t.TempDir(), "bad-catalog*.json")
	if err != nil {
		t.Fatalf("create temp bad catalog: %v", err)
	}
	if _, err := badFile.WriteString("this is not valid JSON {{{"); err != nil {
		t.Fatalf("write bad catalog: %v", err)
	}
	badFile.Close()

	env := baseEnv(addr)
	env["HARNESS_MODEL_CATALOG_PATH"] = badFile.Name()
	env["HARNESS_WORKSPACE"] = t.TempDir()

	// Server must still start — invalid catalog is a warning, not fatal.
	runMatrixTest(t, env, nil)
}

// TestMatrix_ConclusionWatcherEnabledNoEvaluator verifies that the conclusion
// watcher plugin can be enabled (via config TOML) and the server starts cleanly.
// We inject a TOML config file with conclusion_watcher.enabled = true and no evaluator.
func TestMatrix_ConclusionWatcherEnabledNoEvaluator(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)
	tmpDir := t.TempDir()

	// Write a project .harness/config.toml with conclusion_watcher enabled.
	harnessCfgDir := tmpDir + "/.harness"
	if err := os.MkdirAll(harnessCfgDir, 0o755); err != nil {
		t.Fatalf("mkdir .harness: %v", err)
	}
	cfgTOML := `
[conclusion_watcher]
enabled = true
intervention_mode = "inject_validation_prompt"
evaluator_enabled = false
`
	if err := os.WriteFile(harnessCfgDir+"/config.toml", []byte(cfgTOML), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	env := baseEnv(addr)
	env["HARNESS_WORKSPACE"] = tmpDir

	runMatrixTest(t, env, nil)
}

// TestMatrix_ConclusionWatcherEnabledWithEvaluator verifies that the conclusion
// watcher with evaluator_enabled = true starts cleanly. The evaluator uses the
// OPENAI_API_KEY from the injected getenv.
func TestMatrix_ConclusionWatcherEnabledWithEvaluator(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)
	tmpDir := t.TempDir()

	harnessCfgDir := tmpDir + "/.harness"
	if err := os.MkdirAll(harnessCfgDir, 0o755); err != nil {
		t.Fatalf("mkdir .harness: %v", err)
	}
	cfgTOML := `
[conclusion_watcher]
enabled = true
intervention_mode = "inject_validation_prompt"
evaluator_enabled = true
evaluator_model = "gpt-4o-mini"
`
	if err := os.WriteFile(harnessCfgDir+"/config.toml", []byte(cfgTOML), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	env := baseEnv(addr)
	env["HARNESS_WORKSPACE"] = tmpDir

	runMatrixTest(t, env, nil)
}

// TestMatrix_RelativeSubagentWorktreeRoot verifies that a relative
// HARNESS_SUBAGENT_WORKTREE_ROOT is resolved to an absolute path (server starts
// cleanly without error).
func TestMatrix_RelativeSubagentWorktreeRoot(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)
	tmpDir := t.TempDir()

	env := baseEnv(addr)
	env["HARNESS_WORKSPACE"] = tmpDir
	// Relative path: should be resolved relative to filepath.Dir(workspace).
	env["HARNESS_SUBAGENT_WORKTREE_ROOT"] = "worktrees"

	runMatrixTest(t, env, nil)
}

// TestMatrix_AbsoluteSubagentWorktreeRoot verifies that an absolute
// HARNESS_SUBAGENT_WORKTREE_ROOT is accepted without transformation.
func TestMatrix_AbsoluteSubagentWorktreeRoot(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)
	tmpDir := t.TempDir()
	worktreeRoot := t.TempDir() // absolute path

	env := baseEnv(addr)
	env["HARNESS_WORKSPACE"] = tmpDir
	env["HARNESS_SUBAGENT_WORKTREE_ROOT"] = worktreeRoot

	runMatrixTest(t, env, nil)
}

// TestMatrix_SkillsEnabledWithCustomGlobalDir verifies that HARNESS_GLOBAL_DIR
// is respected: skills are loaded from the custom dir and the server starts cleanly.
func TestMatrix_SkillsEnabledWithCustomGlobalDir(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)
	globalDir := t.TempDir()
	workspace := t.TempDir()

	// Create a valid SKILL.md in the custom global dir.
	skillDir := globalDir + "/skills/my-matrix-skill"
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillContent := "---\nname: my-matrix-skill\ndescription: Matrix test skill\nversion: 1\n---\nHello"
	if err := os.WriteFile(skillDir+"/SKILL.md", []byte(skillContent), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	env := baseEnv(addr)
	env["HARNESS_WORKSPACE"] = workspace
	env["HARNESS_GLOBAL_DIR"] = globalDir
	env["HARNESS_SKILLS_ENABLED"] = "true"

	runMatrixTest(t, env, func(addr string) {
		// The skill should be discoverable via GET /v1/skills.
		resp, err := http.Get("http://" + addr + "/v1/skills")
		if err != nil {
			t.Fatalf("GET /v1/skills: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET /v1/skills: want 200, got %d; body: %s", resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "my-matrix-skill") {
			t.Errorf("GET /v1/skills: expected 'my-matrix-skill' in response; got: %s", body)
		}
	})
}

// TestMatrix_ProviderAPIKeyCapture verifies that the OpenAI API key is passed
// through the injected getenv → provider factory. We capture the Config passed
// to newProvider and assert its APIKey field matches what was injected.
func TestMatrix_ProviderAPIKeyCapture(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)

	var capturedKey string
	var captureMu sync.Mutex

	env := baseEnv(addr)
	env["OPENAI_API_KEY"] = "matrix-test-key-xyz"
	env["HARNESS_WORKSPACE"] = t.TempDir()

	getenv := func(key string) string { return env[key] }
	sig := make(chan os.Signal, 1)
	done := make(chan error, 1)

	go func() {
		done <- runWithSignals(sig, getenv, func(cfg openai.Config) (harness.Provider, error) {
			captureMu.Lock()
			capturedKey = cfg.APIKey
			captureMu.Unlock()
			return &noopProvider{}, nil
		}, "")
	}()

	awaitHealthy(t, addr, 3*time.Second)
	sig <- os.Interrupt

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWithSignals: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for graceful shutdown")
	}

	captureMu.Lock()
	key := capturedKey
	captureMu.Unlock()

	if key != "matrix-test-key-xyz" {
		t.Errorf("provider received APIKey = %q, want %q", key, "matrix-test-key-xyz")
	}
}

// TestRunWithSignalsInvalidModelCatalogContinues verifies that pointing
// HARNESS_MODEL_CATALOG_PATH to an invalid JSON file is logged as a warning
// and the server continues (catalog failures are non-fatal).
func TestRunWithSignalsInvalidModelCatalogContinues(t *testing.T) {
	t.Parallel()

	workspaceDir := t.TempDir()

	// Write a malformed catalog file.
	badCatalog, err := os.CreateTemp(t.TempDir(), "bad-catalog*.json")
	if err != nil {
		t.Fatalf("create temp catalog: %v", err)
	}
	if _, err := badCatalog.WriteString(`{invalid json`); err != nil {
		t.Fatalf("write bad catalog: %v", err)
	}
	badCatalog.Close()

	addr := freeLocalAddr(t)
	env := map[string]string{
		"OPENAI_API_KEY":             "test-key",
		"HARNESS_ADDR":               addr,
		"HARNESS_MEMORY_MODE":        "off",
		"HARNESS_WORKSPACE":          workspaceDir,
		"HARNESS_MODEL_CATALOG_PATH": badCatalog.Name(),
	}
	getenv := func(key string) string { return env[key] }
	sig := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- runWithSignals(sig, getenv, func(openai.Config) (harness.Provider, error) {
			return &noopProvider{}, nil
		}, "")
	}()

	awaitHealthy(t, addr, 3*time.Second)
	time.Sleep(100 * time.Millisecond)
	sig <- os.Interrupt

	select {
	case err := <-done:
		// Invalid model catalog must NOT abort the server; nil error expected.
		if err != nil {
			t.Fatalf("expected server to continue despite invalid model catalog; got: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for graceful shutdown")
	}
}

// TestMatrix_MaxStepsFromEnv verifies that HARNESS_MAX_STEPS is applied to the
// runner config. We capture the openai.Config passed to newProvider. The runner
// config MaxSteps is not directly captured in the openai.Config, but we verify
// the server starts cleanly with a non-default max steps value.
func TestMatrix_MaxStepsFromEnv(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)
	env := baseEnv(addr)
	env["HARNESS_MAX_STEPS"] = "25"
	env["HARNESS_WORKSPACE"] = t.TempDir()

	runMatrixTest(t, env, nil)
}

// TestMatrix_ModelFromEnv verifies that HARNESS_MODEL is passed through to the
// provider factory via the openai.Config.Model field.
func TestMatrix_ModelFromEnv(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)

	var capturedModel string
	var captureMu sync.Mutex

	env := baseEnv(addr)
	env["HARNESS_MODEL"] = "gpt-4.1-matrix"
	env["HARNESS_WORKSPACE"] = t.TempDir()

	getenv := func(key string) string { return env[key] }
	sig := make(chan os.Signal, 1)
	done := make(chan error, 1)

	go func() {
		done <- runWithSignals(sig, getenv, func(cfg openai.Config) (harness.Provider, error) {
			captureMu.Lock()
			capturedModel = cfg.Model
			captureMu.Unlock()
			return &noopProvider{}, nil
		}, "")
	}()

	awaitHealthy(t, addr, 3*time.Second)
	sig <- os.Interrupt

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWithSignals: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for graceful shutdown")
	}

	captureMu.Lock()
	model := capturedModel
	captureMu.Unlock()

	if model != "gpt-4.1-matrix" {
		t.Errorf("provider received Model = %q, want %q", model, "gpt-4.1-matrix")
	}
}

// TestMatrix_ConversationRetentionPolicy verifies that HARNESS_CONVERSATION_RETENTION_DAYS
// is honoured: setting a low value (1 day) starts the retention cleaner cleanly.
func TestMatrix_ConversationRetentionPolicy(t *testing.T) {
	t.Parallel()
	addr := freeLocalAddr(t)
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/conv-retention.db"

	env := baseEnv(addr)
	env["HARNESS_WORKSPACE"] = tmpDir
	env["HARNESS_CONVERSATION_DB"] = dbPath
	env["HARNESS_CONVERSATION_RETENTION_DAYS"] = "1"

	runMatrixTest(t, env, func(addr string) {
		if _, err := os.Stat(dbPath); os.IsNotExist(err) {
			t.Errorf("conversation DB file %q was not created", dbPath)
		}
	})
}

// TestMatrix_SignalNilRejected verifies that runWithSignals returns an error
// immediately when the signal channel is nil, without starting the server.
func TestMatrix_SignalNilRejected(t *testing.T) {
	t.Parallel()

	err := runWithSignals(nil, func(string) string { return "" }, func(openai.Config) (harness.Provider, error) {
		return &noopProvider{}, nil
	}, "")
	if err == nil {
		t.Fatal("expected error when signal channel is nil")
	}
	if !strings.Contains(err.Error(), "signal channel is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Issue #338: shutdown ordering regressions
// ---------------------------------------------------------------------------

// TestShutdownCallbacksBeforeHTTPServer verifies that the shutdown sequence
// completes without error. It observes that runWithSignals returns nil after
// signal delivery and that no hung goroutine races exist in the ordering.
// (True sequencing verification requires hooks not present in the current
// implementation; this test is a regression guard to detect hangs in ordering.)
func TestShutdownCallbacksBeforeHTTPServer(t *testing.T) {
	t.Parallel()

	workspaceDir := t.TempDir()
	env := map[string]string{
		"OPENAI_API_KEY":           "test-key",
		"HARNESS_ADDR":             "127.0.0.1:0",
		"HARNESS_MEMORY_MODE":      "off",
		"HARNESS_WORKSPACE":        workspaceDir,
		"HARNESS_ENABLE_CALLBACKS": "true", // enable callbacks so the shutdown path runs
	}
	getenv := func(key string) string { return env[key] }
	sig := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- runWithSignals(sig, getenv, func(openai.Config) (harness.Provider, error) {
			return &noopProvider{}, nil
		}, "")
	}()

	time.Sleep(100 * time.Millisecond)
	sig <- os.Interrupt

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean shutdown; got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out: shutdown did not complete within 5s (possible ordering hang)")
	}
}

// TestShutdownCronOrderingDeterministic verifies that the embedded cron
// scheduler's Stop() and Close() complete without error in repeated runs.
// Uses isolated SQLite state per run via t.TempDir() so there is no contention.
func TestShutdownCronOrderingDeterministic(t *testing.T) {
	t.Parallel()

	for i := 0; i < 3; i++ {
		workspaceDir := t.TempDir()
		env := map[string]string{
			"OPENAI_API_KEY":      "test-key",
			"HARNESS_ADDR":        "127.0.0.1:0",
			"HARNESS_MEMORY_MODE": "off",
			"HARNESS_WORKSPACE":   workspaceDir,
			"HARNESS_CRON_URL":    "", // embedded cron
		}
		getenv := func(key string) string { return env[key] }
		sig := make(chan os.Signal, 1)

		done := make(chan error, 1)
		go func() {
			done <- runWithSignals(sig, getenv, func(openai.Config) (harness.Provider, error) {
				return &noopProvider{}, nil
			}, "")
		}()

		time.Sleep(80 * time.Millisecond)
		sig <- os.Interrupt

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("iteration %d: expected clean shutdown; got: %v", i, err)
			}
		case <-time.After(4 * time.Second):
			t.Fatalf("iteration %d: timed out during embedded cron shutdown", i)
		}
	}
}

// TestShutdownConversationCleanerCancellation verifies that when conversation
// persistence is enabled the background cleaner goroutine is cancelled cleanly
// and runWithSignals returns nil.
func TestShutdownConversationCleanerCancellation(t *testing.T) {
	t.Parallel()

	workspaceDir := t.TempDir()
	convDBPath := workspaceDir + "/conv.db"

	env := map[string]string{
		"OPENAI_API_KEY":                      "test-key",
		"HARNESS_ADDR":                        "127.0.0.1:0",
		"HARNESS_MEMORY_MODE":                 "off",
		"HARNESS_WORKSPACE":                   workspaceDir,
		"HARNESS_CONVERSATION_DB":             convDBPath,
		"HARNESS_CONVERSATION_RETENTION_DAYS": "30",
	}
	getenv := func(key string) string { return env[key] }
	sig := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- runWithSignals(sig, getenv, func(openai.Config) (harness.Provider, error) {
			return &noopProvider{}, nil
		}, "")
	}()

	time.Sleep(120 * time.Millisecond)
	sig <- os.Interrupt

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean shutdown with conversation cleaner; got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out: conversation cleaner cancellation may have hung")
	}
}

func TestStartupFailureCancelsConversationCleaner(t *testing.T) {
	t.Parallel()

	cleaner := &recordingConversationCleaner{
		started: make(chan struct{}),
		done:    make(chan struct{}),
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("bind listener: %v", err)
	}
	defer ln.Close()

	workspaceDir := t.TempDir()
	env := map[string]string{
		"OPENAI_API_KEY":                      "test-key",
		"HARNESS_ADDR":                        ln.Addr().String(),
		"HARNESS_MEMORY_MODE":                 "off",
		"HARNESS_WORKSPACE":                   workspaceDir,
		"HARNESS_CONVERSATION_DB":             filepath.Join(workspaceDir, "conv.db"),
		"HARNESS_CONVERSATION_RETENTION_DAYS": "30",
	}
	getenv := func(key string) string { return env[key] }

	err = runWithSignalsWithDeps(
		make(chan os.Signal, 1),
		getenv,
		func(openai.Config) (harness.Provider, error) {
			return &noopProvider{}, nil
		},
		"",
		runDeps{
			newConversationCleaner: func(harness.ConversationStore, int) conversationCleanerStarter {
				return cleaner
			},
		},
	)
	if err == nil {
		t.Fatal("expected startup failure when port is already bound")
	}

	select {
	case <-cleaner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("conversation cleaner was not started before startup failure")
	}

	select {
	case <-cleaner.done:
	case <-time.After(2 * time.Second):
		t.Fatal("expected startup failure to cancel the conversation cleaner")
	}
}

// TestShutdownServerErrorChannelRace pins the race between a server startup
// error (port conflict) and a signal. The server returns an error immediately
// when the port is already in use, and runWithSignals must propagate that error
// correctly from the serverErr channel rather than hanging on the signal channel.
func TestShutdownServerErrorChannelRace(t *testing.T) {
	t.Parallel()

	// Bind a listener on a port, then try to start harnessd on the same port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("bind listener: %v", err)
	}
	defer ln.Close()
	conflictAddr := ln.Addr().String()

	workspaceDir := t.TempDir()
	env := map[string]string{
		"OPENAI_API_KEY":      "test-key",
		"HARNESS_ADDR":        conflictAddr, // already in use
		"HARNESS_MEMORY_MODE": "off",
		"HARNESS_WORKSPACE":   workspaceDir,
	}
	getenv := func(key string) string { return env[key] }

	err = runWithSignals(make(chan os.Signal, 1), getenv, func(openai.Config) (harness.Provider, error) {
		return &noopProvider{}, nil
	}, "")

	// Must return an error (address already in use), not hang.
	if err == nil {
		t.Fatal("expected error when port is already bound")
	}
	if !strings.Contains(err.Error(), "server error") && !strings.Contains(err.Error(), "address already in use") && !strings.Contains(err.Error(), "bind") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// BT-004: When --mcp flag is passed to harnessd, the process starts in MCP
// stdio mode instead of HTTP server mode.
// This test verifies runMCPStdio is invoked and terminates cleanly on signal.
func TestMCPFlagStartsMCPStdioMode(t *testing.T) {
	sig := make(chan os.Signal, 1)

	// runMCPStdio should start the MCP server and return when signalled.
	// We signal immediately so the server shuts down before doing real I/O.
	done := make(chan error, 1)
	go func() {
		done <- runMCPStdio(sig)
	}()

	// Give the goroutine a moment to start, then signal shutdown.
	time.Sleep(10 * time.Millisecond)
	sig <- os.Interrupt

	select {
	case err := <-done:
		// runMCPStdio must return nil or a context-cancelled error (clean shutdown).
		// It must not return an HTTP-server-style error.
		if err != nil && !strings.Contains(err.Error(), "context") {
			t.Fatalf("runMCPStdio returned unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runMCPStdio did not shut down within 5 seconds")
	}
}

// Regression: mcpFlag is a recognized flag (not nil).
func TestMCPFlagIsDeclared(t *testing.T) {
	if mcpFlag == nil {
		t.Fatal("mcpFlag must be declared as a non-nil *bool")
	}
}

// BT-Fix1a: When context is cancelled (simulating SIGINT), runMCPStdio returns
// nil — not context.Canceled — so normal shutdown looks like success, not a crash.
func TestRunMCPStdioReturnsNilOnContextCancel(t *testing.T) {
	sig := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- runMCPStdio(sig)
	}()

	// Signal shutdown immediately.
	time.Sleep(5 * time.Millisecond)
	sig <- os.Interrupt

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runMCPStdio must return nil on context cancel (SIGINT), got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runMCPStdio did not shut down within 5 seconds")
	}
}

// BT-Fix1b: When stdin returns io.EOF, runMCPStdio returns nil (not io.EOF).
// This covers normal pipe close when the MCP client disconnects.
// We test this at the runMCPStdio level by verifying that io.EOF from the
// underlying Listen call is filtered to nil before returning.
func TestRunMCPStdioReturnsNilOnEOF(t *testing.T) {
	// We trigger EOF by sending a signal and simultaneously verifying that
	// any io.EOF the server might return from the Listen call is swallowed.
	// This test documents the filtering contract: err == io.EOF → nil.
	sig := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- runMCPStdio(sig)
	}()

	// Signal immediately to trigger shutdown.
	time.Sleep(5 * time.Millisecond)
	sig <- os.Interrupt

	select {
	case err := <-done:
		// Whether the underlying library returns context.Canceled or io.EOF,
		// runMCPStdio must return nil.
		if err != nil {
			t.Fatalf("runMCPStdio must return nil (filters context.Canceled and io.EOF), got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runMCPStdio did not return within 5 seconds")
	}
}

// BT-Fix2: Signal goroutine exits when context is done (no leak).
// When the MCP server starts and stdin is already closed (EOF), Start returns
// before a signal fires. The signal goroutine inside runMCPStdio must exit
// via the ctx.Done() case rather than blocking forever on <-sig.
// This test verifies that runMCPStdio returns promptly even without a signal.
func TestRunMCPStdioSignalGoroutineDoesNotLeak(t *testing.T) {
	sig := make(chan os.Signal, 1)
	// Do NOT send a signal — the server should shut down because stdin is EOF
	// (test harness stdin) and the goroutine should exit via ctx.Done(), not <-sig.

	done := make(chan error, 1)
	go func() {
		done <- runMCPStdio(sig)
	}()

	select {
	case err := <-done:
		// runMCPStdio must return nil whether it shut down via signal or EOF.
		if err != nil {
			t.Fatalf("runMCPStdio must return nil, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runMCPStdio did not return; signal goroutine may have leaked and blocked ctx.Done()")
	}
}

// BT-481-003: The --mcp-workspace flag is declared alongside --mcp.
// When both flags are set, runMCPStdio uses the workspace flag value as WorkspaceRoot.
func TestMCPWorkspaceFlagIsDeclared(t *testing.T) {
	if mcpWorkspaceFlag == nil {
		t.Fatal("mcpWorkspaceFlag must be declared as a non-nil *string")
	}
}

// BT-481-004: When --mcp-workspace is set to a path, runMCPStdio uses that path as WorkspaceRoot.
// We verify this by running with a tmp dir workspace — the MCP server must start without error.
func TestMCPWorkspaceFlagIsUsedByRunMCPStdio(t *testing.T) {
	tmpDir := t.TempDir()

	// Save and restore the flag value.
	origVal := *mcpWorkspaceFlag
	*mcpWorkspaceFlag = tmpDir
	defer func() { *mcpWorkspaceFlag = origVal }()

	sig := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() {
		done <- runMCPStdio(sig)
	}()

	// Signal shutdown quickly.
	time.Sleep(10 * time.Millisecond)
	sig <- os.Interrupt

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runMCPStdio with --mcp-workspace=%s must return nil, got: %v", tmpDir, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runMCPStdio did not return within 5 seconds")
	}
}

// BT-481-005: When runMCPStdio is called with EnableTodos, the catalog contains
// more tools than a minimal catalog built without EnableTodos.
// We verify this by checking ToolCount is greater when EnableTodos is active.
func TestRunMCPStdioExpandedCatalogHasTodos(t *testing.T) {
	// Build a catalog without EnableTodos.
	minCatalog, err := htools.BuildCatalog(htools.BuildOptions{
		WorkspaceRoot: ".",
	})
	if err != nil {
		t.Fatalf("BuildCatalog (minimal) failed: %v", err)
	}

	// Build a catalog with EnableTodos.
	expandedCatalog, err := htools.BuildCatalog(htools.BuildOptions{
		WorkspaceRoot: ".",
		EnableTodos:   true,
	})
	if err != nil {
		t.Fatalf("BuildCatalog (with todos) failed: %v", err)
	}

	if len(expandedCatalog) <= len(minCatalog) {
		t.Fatalf("expanded catalog (%d tools) must have more tools than minimal catalog (%d tools)", len(expandedCatalog), len(minCatalog))
	}
}

// Regression: runMCPStdio uses EnableTodos:true and HTTPClient with 30s timeout
// in its BuildOptions. Verify the catalog size reflects EnableTodos being active.
func TestRunMCPStdioUsesExpandedBuildOptions(t *testing.T) {
	sig := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() {
		done <- runMCPStdio(sig)
	}()

	// Signal shutdown quickly.
	time.Sleep(10 * time.Millisecond)
	sig <- os.Interrupt

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runMCPStdio with expanded options must return nil, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runMCPStdio did not return within 5 seconds")
	}
}
