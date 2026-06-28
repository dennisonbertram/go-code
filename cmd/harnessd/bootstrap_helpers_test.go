package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
	htools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/provider/catalog"
	openai "go-agent-harness/internal/provider/openai"
	"go-agent-harness/internal/relay"
	"go-agent-harness/internal/subagents"
	scriptworkflow "go-agent-harness/internal/workflow"
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
		scriptWorkflows:  &scriptWorkflowServiceRef{},
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
	if opts.ScriptWorkflows == nil {
		t.Fatal("expected script workflows")
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

func TestScriptWorkflowServiceRefDelegatesAfterBinding(t *testing.T) {
	t.Parallel()

	engine := scriptworkflow.NewEngine(scriptworkflow.EngineOptions{Subagents: sourceNoopSubagentsForHarnessd{}})
	manager, err := scriptworkflow.NewSourceManager(scriptworkflow.SourceManagerOptions{
		Engine:   engine,
		CacheDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewSourceManager: %v", err)
	}
	ref := &scriptWorkflowServiceRef{}
	if _, err := ref.Start(t.Context(), "missing", nil); err == nil {
		t.Fatal("expected unbound ref to reject start")
	}
	if err := ref.Reload(t.Context()); err != nil {
		t.Fatalf("unbound reload should be a no-op: %v", err)
	}
	ref.Set(manager)
	if got := ref.List(); got == nil {
		t.Fatal("expected delegated list to return a non-nil slice")
	}
	if err := ref.Reload(t.Context()); err != nil {
		t.Fatalf("bound reload: %v", err)
	}
}

func TestScriptWorkflowServiceRefDelegatesAllMethods(t *testing.T) {
	t.Parallel()

	svc := &fakeScriptWorkflowServiceForHarnessd{
		run: &scriptworkflow.Run{ID: "wf_1", WorkflowName: "daily-review", Status: scriptworkflow.RunStatusCompleted},
		events: []scriptworkflow.Event{{
			RunID: "wf_1",
			Type:  scriptworkflow.EventWorkflowCompleted,
		}},
	}
	ref := &scriptWorkflowServiceRef{}
	ref.Set(svc)

	if _, err := ref.Resume(t.Context(), "wf_1", map[string]any{"resume": true}); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if _, err := ref.GetRun("wf_1"); err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	history, stream, cancel, err := ref.Subscribe("wf_1")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	cancel()
	if len(history) != 1 || stream == nil {
		t.Fatalf("Subscribe history=%d stream nil=%v", len(history), stream == nil)
	}
	if _, _, err := ref.Wait(t.Context(), "wf_1"); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	bundle, err := ref.CreateWorkflow(t.Context(), scriptworkflow.CreateWorkflowRequest{Name: "daily-review"})
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	if bundle.Manifest.Name != "daily-review" {
		t.Fatalf("bundle name = %q", bundle.Manifest.Name)
	}
}

func TestScriptSubagentAdapterMapsRequestsAndResults(t *testing.T) {
	t.Parallel()

	manager := &fakeSubagentManagerForHarnessd{
		item: subagents.Subagent{
			ID:     "subagent_1",
			RunID:  "run_1",
			Status: harness.RunStatusCompleted,
			Output: "done",
		},
	}
	adapter := scriptSubagentAdapter{manager: manager}
	result, err := adapter.Create(t.Context(), scriptworkflow.SubagentRequest{
		Prompt:        "inspect",
		Model:         "gpt-5-nano",
		Provider:      "openai",
		Profile:       "reviewer",
		AllowedTools:  []string{"read"},
		Isolation:     "worktree",
		CleanupPolicy: "preserve",
		MaxSteps:      5,
		MaxCostUSD:    0.5,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.ID != "subagent_1" || result.Output != "done" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if manager.req.Prompt != "inspect" || manager.req.ProviderName != "openai" || manager.req.ProfileName != "reviewer" {
		t.Fatalf("unexpected mapped request: %#v", manager.req)
	}
	if got := manager.req.AllowedTools; len(got) != 1 || got[0] != "read" {
		t.Fatalf("allowed tools = %#v", got)
	}
	if manager.req.Isolation != subagents.IsolationWorktree || manager.req.CleanupPolicy != subagents.CleanupPreserve {
		t.Fatalf("isolation=%q cleanup=%q", manager.req.Isolation, manager.req.CleanupPolicy)
	}

	got, err := adapter.Get(t.Context(), "subagent_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "subagent_1" || got.Status != string(harness.RunStatusCompleted) {
		t.Fatalf("unexpected get result: %#v", got)
	}
}

func TestWorkflowQuestionResponderUsesAskBroker(t *testing.T) {
	t.Parallel()

	broker := &fakeAskUserQuestionBrokerForHarnessd{}
	responder := workflowQuestionResponder{broker: broker, timeout: 2 * time.Second}
	answer, err := responder.AskWorkflowQuestion(t.Context(), scriptworkflow.QuestionRequest{
		RunID:  "wf_1",
		CallID: "call_1",
		Prompt: "Continue?",
		Choices: []scriptworkflow.QuestionOption{{
			Label:       "Continue",
			Description: "Keep going.",
		}},
	})
	if err != nil {
		t.Fatalf("AskWorkflowQuestion: %v", err)
	}
	if answer != "Continue" {
		t.Fatalf("answer = %#v", answer)
	}
	if broker.req.RunID != "wf_1" || broker.req.CallID != "call_1" || broker.req.Timeout != 2*time.Second {
		t.Fatalf("broker request = %#v", broker.req)
	}
	if len(broker.req.Questions) != 1 || broker.req.Questions[0].Question != "Continue?" {
		t.Fatalf("questions = %#v", broker.req.Questions)
	}
}

type fakeScriptWorkflowServiceForHarnessd struct {
	run    *scriptworkflow.Run
	events []scriptworkflow.Event
}

func (f *fakeScriptWorkflowServiceForHarnessd) List() []scriptworkflow.Meta {
	return []scriptworkflow.Meta{{Name: "daily-review"}}
}

func (f *fakeScriptWorkflowServiceForHarnessd) Start(context.Context, string, any) (*scriptworkflow.Run, error) {
	return f.run, nil
}

func (f *fakeScriptWorkflowServiceForHarnessd) Resume(context.Context, string, any) (*scriptworkflow.Run, error) {
	return f.run, nil
}

func (f *fakeScriptWorkflowServiceForHarnessd) GetRun(string) (*scriptworkflow.Run, error) {
	return f.run, nil
}

func (f *fakeScriptWorkflowServiceForHarnessd) Subscribe(string) ([]scriptworkflow.Event, <-chan scriptworkflow.Event, func(), error) {
	ch := make(chan scriptworkflow.Event)
	return f.events, ch, func() { close(ch) }, nil
}

func (f *fakeScriptWorkflowServiceForHarnessd) Wait(context.Context, string) (*scriptworkflow.Run, []scriptworkflow.Event, error) {
	return f.run, f.events, nil
}

func (f *fakeScriptWorkflowServiceForHarnessd) CreateWorkflow(_ context.Context, req scriptworkflow.CreateWorkflowRequest) (*scriptworkflow.SourceBundle, error) {
	return &scriptworkflow.SourceBundle{Manifest: scriptworkflow.SourceBundleManifest{Name: req.Name}}, nil
}

type fakeSubagentManagerForHarnessd struct {
	req  subagents.Request
	item subagents.Subagent
}

func (f *fakeSubagentManagerForHarnessd) Create(_ context.Context, req subagents.Request) (subagents.Subagent, error) {
	f.req = req
	return f.item, nil
}

func (f *fakeSubagentManagerForHarnessd) Get(context.Context, string) (subagents.Subagent, error) {
	return f.item, nil
}

func (f *fakeSubagentManagerForHarnessd) List(context.Context) ([]subagents.Subagent, error) {
	return nil, nil
}

func (f *fakeSubagentManagerForHarnessd) Delete(context.Context, string) error {
	return nil
}

func (f *fakeSubagentManagerForHarnessd) Cancel(context.Context, string) error {
	return nil
}

type fakeAskUserQuestionBrokerForHarnessd struct {
	req htools.AskUserQuestionRequest
}

func (f *fakeAskUserQuestionBrokerForHarnessd) Ask(_ context.Context, req htools.AskUserQuestionRequest) (map[string]string, time.Time, error) {
	f.req = req
	return map[string]string{req.Questions[0].Question: "Continue"}, time.Unix(1, 0), nil
}

func (f *fakeAskUserQuestionBrokerForHarnessd) Pending(string) (htools.AskUserQuestionPending, bool) {
	return htools.AskUserQuestionPending{}, false
}

func (f *fakeAskUserQuestionBrokerForHarnessd) Submit(string, map[string]string) error {
	return nil
}

type sourceNoopSubagentsForHarnessd struct{}

func (sourceNoopSubagentsForHarnessd) Create(context.Context, scriptworkflow.SubagentRequest) (scriptworkflow.SubagentResult, error) {
	return scriptworkflow.SubagentResult{ID: "subagent_1", Status: "completed"}, nil
}

func (sourceNoopSubagentsForHarnessd) Get(context.Context, string) (scriptworkflow.SubagentResult, error) {
	return scriptworkflow.SubagentResult{ID: "subagent_1", Status: "completed"}, nil
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
