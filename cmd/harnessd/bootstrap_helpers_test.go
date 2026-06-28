package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/provider/catalog"
	openai "go-agent-harness/internal/provider/openai"
	"go-agent-harness/internal/relay"
)

func TestBuildCatalogBootstrapFallsBackToWorkspaceCatalog(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	if err := os.MkdirAll(workspace+"/catalog", 0o755); err != nil {
		t.Fatalf("mkdir catalog: %v", err)
	}
	if err := os.WriteFile(workspace+"/catalog/models.json", []byte(`{
  "catalog_version": "1.0.0",
  "providers": {
    "openrouter": {
      "display_name": "OpenRouter",
      "base_url": "https://openrouter.ai/api/v1",
      "api_key_env": "OPENROUTER_API_KEY",
      "models": {
        "openai/gpt-4.1-mini": {
          "display_name": "GPT-4.1 mini",
          "context_window": 128000,
          "modalities": ["text"],
          "tool_calling": true,
          "streaming": true,
          "api": "responses"
        }
      }
    }
  }
}`), 0o644); err != nil {
		t.Fatalf("write catalog: %v", err)
	}

	bootstrap, err := buildCatalogBootstrap(catalogBootstrapOptions{
		workspace: workspace,
		getenv:    func(string) string { return "" },
		newProvider: func(openai.Config) (harness.Provider, error) {
			return &noopProvider{}, nil
		},
	})
	if err != nil {
		t.Fatalf("buildCatalogBootstrap: %v", err)
	}
	if bootstrap.modelCatalog == nil {
		t.Fatal("expected model catalog")
	}
	if bootstrap.providerRegistry == nil {
		t.Fatal("expected provider registry")
	}
	if got := bootstrap.lookupModelAPI("openrouter", "openai/gpt-4.1-mini"); got != "responses" {
		t.Fatalf("lookupModelAPI: got %q", got)
	}
}

func TestBuildTriggerRuntimeHonorsSecrets(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"GITHUB_WEBHOOK_SECRET": "gh-secret",
		"SLACK_SIGNING_SECRET":  "slack-secret",
	}
	var logs []string
	runtime := buildTriggerRuntime(func(key string) string { return env[key] }, func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	})

	if runtime.validators == nil {
		t.Fatal("expected validator registry")
	}
	if validator, ok := runtime.validators.Get("github"); !ok {
		t.Fatal("expected github validator")
	} else if got := fmt.Sprintf("%T", validator); !strings.HasSuffix(got, ".GitHubValidator") {
		t.Fatalf("github validator type: got %q", got)
	}
	if validator, ok := runtime.validators.Get("slack"); !ok {
		t.Fatal("expected slack validator")
	} else if got := fmt.Sprintf("%T", validator); !strings.HasSuffix(got, ".SlackValidator") {
		t.Fatalf("slack validator type: got %q", got)
	}
	if _, ok := runtime.validators.Get("linear"); ok {
		t.Fatal("did not expect linear validator")
	}

	if got := fmt.Sprintf("%T", runtime.github); !strings.HasSuffix(got, ".GitHubAdapter") {
		t.Fatalf("expected github adapter, got %q", got)
	}
	if got := fmt.Sprintf("%T", runtime.slack); !strings.HasSuffix(got, ".SlackAdapter") {
		t.Fatalf("expected slack adapter, got %q", got)
	}
	if runtime.linear != nil {
		t.Fatal("did not expect linear adapter")
	}

	logText := strings.Join(logs, "\n")
	for _, want := range []string{
		"registered GitHub webhook validator",
		"registered Slack webhook validator",
		"registered GitHub webhook adapter for /v1/webhooks/github",
		"registered Slack webhook adapter for /v1/webhooks/slack",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("expected log %q in %q", want, logText)
		}
	}
}

func TestBuildServerOptionsForwardsBootstrapRuntime(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(&noopProvider{}, harness.NewDefaultRegistry(t.TempDir()), harness.RunnerConfig{})
	cat := &catalog.Catalog{}
	relayStore, err := relay.NewSQLiteWorkerStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatalf("NewSQLiteWorkerStore: %v", err)
	}
	t.Cleanup(func() { _ = relayStore.Close() })
	if err := relayStore.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate relay store: %v", err)
	}
	runtime := buildTriggerRuntime(func(key string) string {
		switch key {
		case "GITHUB_WEBHOOK_SECRET":
			return "gh-secret"
		case "LINEAR_WEBHOOK_SECRET":
			return "linear-secret"
		default:
			return ""
		}
	}, nil)

	opts := buildServerOptions(serverBootstrapOptions{
		runner:           runner,
		modelCatalog:     cat,
		providerRegistry: catalog.NewProviderRegistryWithEnv(cat, func(string) string { return "" }),
		triggers:         runtime,
		relayWorkerStore: relayStore,
	})

	if opts.Runner != runner {
		t.Fatal("expected runner")
	}
	if opts.AgentRunner != runner {
		t.Fatal("expected agent runner to match runner")
	}
	if opts.Catalog != cat {
		t.Fatal("expected model catalog")
	}
	if opts.ProviderRegistry == nil {
		t.Fatal("expected provider registry")
	}
	if opts.Validators == nil {
		t.Fatal("expected validators")
	}
	if opts.GitHubAdapter == nil {
		t.Fatal("expected github adapter")
	}
	if opts.LinearAdapter == nil {
		t.Fatal("expected linear adapter")
	}
	if opts.SlackAdapter != nil {
		t.Fatal("did not expect slack adapter")
	}
	if opts.RelayWorkerStore != relayStore {
		t.Fatal("expected relay worker store")
	}
}

func TestBuildPersistenceBootstrapInitializesStoresAndCleaner(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	getenv := func(key string) string {
		switch key {
		case "HARNESS_RUN_DB":
			return ".harness/runs.db"
		case "HARNESS_CONVERSATION_DB":
			return ".harness/conversations.db"
		case "HARNESS_RELAY_DB":
			return ".harness/relay.db"
		default:
			return ""
		}
	}

	bootstrap, err := buildPersistenceBootstrap(persistenceBootstrapOptions{
		workspace:         workspace,
		getenv:            getenv,
		convRetentionDays: 14,
	})
	if err != nil {
		t.Fatalf("buildPersistenceBootstrap: %v", err)
	}
	defer func() {
		if bootstrap.convCleanerCancel != nil {
			bootstrap.convCleanerCancel()
		}
		if bootstrap.relayWorkerStore != nil {
			_ = bootstrap.relayWorkerStore.Close()
		}
		if bootstrap.conversationStore != nil {
			_ = bootstrap.conversationStore.Close()
		}
		if bootstrap.runStore != nil {
			_ = bootstrap.runStore.Close()
		}
	}()

	if bootstrap.runStore == nil {
		t.Fatal("expected run store")
	}
	if bootstrap.conversationStore == nil {
		t.Fatal("expected conversation store")
	}
	if bootstrap.convCleanerCancel == nil {
		t.Fatal("expected conversation cleaner cancel func")
	}
	if bootstrap.relayWorkerStore == nil {
		t.Fatal("expected relay worker store")
	}

	if _, err := os.Stat(workspace + "/.harness/runs.db"); err != nil {
		t.Fatalf("expected run db to exist: %v", err)
	}
	if _, err := os.Stat(workspace + "/.harness/conversations.db"); err != nil {
		t.Fatalf("expected conversation db to exist: %v", err)
	}
	if _, err := os.Stat(workspace + "/.harness/relay.db"); err != nil {
		t.Fatalf("expected relay db to exist: %v", err)
	}
}

func TestBuildPersistenceBootstrapClosesRunStoreWhenConversationSetupFails(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	if err := os.MkdirAll(workspace+"/blocked.db", 0o755); err != nil {
		t.Fatalf("mkdir blocked path: %v", err)
	}

	bootstrap, err := buildPersistenceBootstrap(persistenceBootstrapOptions{
		workspace: workspace,
		getenv: func(key string) string {
			switch key {
			case "HARNESS_RUN_DB":
				return ".harness/runs.db"
			case "HARNESS_CONVERSATION_DB":
				return "blocked.db"
			default:
				return ""
			}
		},
	})
	if err == nil {
		if bootstrap.runStore != nil {
			_ = bootstrap.runStore.Close()
		}
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "create conversation store") {
		t.Fatalf("unexpected error: %v", err)
	}

	if bootstrap.runStore != nil {
		t.Fatal("expected run store to be cleaned up on failure")
	}
}
