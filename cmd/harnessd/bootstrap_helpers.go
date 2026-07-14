package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-agent-harness/internal/checkpoints"
	"go-agent-harness/internal/cron"
	githubadapter "go-agent-harness/internal/github"
	"go-agent-harness/internal/harness"
	htools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/deferred"
	linearadapter "go-agent-harness/internal/linear"
	"go-agent-harness/internal/networks"
	"go-agent-harness/internal/provider/anthropic"
	"go-agent-harness/internal/provider/catalog"
	openai "go-agent-harness/internal/provider/openai"
	"go-agent-harness/internal/provider/pricing"
	"go-agent-harness/internal/relay"
	"go-agent-harness/internal/server"
	slackadapter "go-agent-harness/internal/slack"
	istore "go-agent-harness/internal/store"
	"go-agent-harness/internal/subagents"
	"go-agent-harness/internal/trigger"
	scriptworkflow "go-agent-harness/internal/workflow"
	"go-agent-harness/internal/workflows"
)

type catalogBootstrapOptions struct {
	workspace   string
	getenv      func(string) string
	newProvider providerFactory
	logger      func(string, ...any)
}

type catalogBootstrap struct {
	modelCatalog     *catalog.Catalog
	providerRegistry *catalog.ProviderRegistry
	pricingResolver  pricing.Resolver
	lookupModelAPI   func(providerName, modelID string) string
}

func buildCatalogBootstrap(opts catalogBootstrapOptions) (catalogBootstrap, error) {
	if opts.getenv == nil {
		opts.getenv = os.Getenv
	}
	if opts.logger == nil {
		opts.logger = func(string, ...any) {}
	}

	pricingCatalogPath := strings.TrimSpace(opts.getenv("HARNESS_PRICING_CATALOG_PATH"))
	modelCatalogPath := strings.TrimSpace(opts.getenv("HARNESS_MODEL_CATALOG_PATH"))
	if modelCatalogPath == "" {
		candidates := []string{
			filepath.Join(opts.workspace, "catalog", "models.json"),
			"catalog/models.json",
		}
		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err == nil {
				modelCatalogPath = candidate
				break
			}
		}
	}

	var bootstrap catalogBootstrap
	if modelCatalogPath != "" {
		cat, err := catalog.LoadCatalog(modelCatalogPath)
		if err != nil {
			opts.logger("warning: failed to load model catalog from %s: %v (continuing without catalog)", modelCatalogPath, err)
		} else {
			bootstrap.modelCatalog = cat
			bootstrap.providerRegistry = catalog.NewProviderRegistryWithEnv(cat, opts.getenv)
			opts.logger("loaded model catalog with %d providers", len(cat.Providers))
		}
	}

	bootstrap.lookupModelAPI = func(providerName, modelID string) string {
		if bootstrap.modelCatalog == nil {
			return ""
		}
		entry, ok := bootstrap.modelCatalog.Providers[providerName]
		if !ok {
			return ""
		}
		resolved := modelID
		if target, ok := entry.Aliases[modelID]; ok {
			if _, exists := entry.Models[target]; exists {
				resolved = target
			}
		}
		model, ok := entry.Models[resolved]
		if !ok {
			return ""
		}
		return model.API
	}

	if pricingCatalogPath != "" {
		resolver, err := pricing.NewFileResolver(pricingCatalogPath)
		if err != nil {
			return catalogBootstrap{}, fmt.Errorf("load pricing catalog from %s: %w", pricingCatalogPath, err)
		}
		bootstrap.pricingResolver = resolver
	} else if bootstrap.modelCatalog != nil {
		// No explicit pricing file — fall back to the pricing blocks embedded in
		// the model catalog itself (catalog/models.json already has Anthropic and
		// OpenAI rates). This ensures cost reporting works out of the box without
		// requiring HARNESS_PRICING_CATALOG_PATH to be set.
		bootstrap.pricingResolver = catalog.NewCatalogPricingResolver(bootstrap.modelCatalog)
		opts.logger("pricing resolver wired from model catalog (fallback)")
	}

	if bootstrap.providerRegistry != nil {
		if _, ok := bootstrap.modelCatalog.Providers["openrouter"]; ok {
			bootstrap.providerRegistry.SetOpenRouterDiscovery(catalog.NewOpenRouterDiscovery(catalog.OpenRouterDiscoveryOptions{
				TTL: 5 * time.Minute,
			}))
		}
		bootstrap.providerRegistry.SetClientFactory(func(apiKey, baseURL, providerName string) (catalog.ProviderClient, error) {
			if providerName == "anthropic" {
				return anthropic.NewClient(anthropic.Config{
					APIKey:          apiKey,
					BaseURL:         baseURL,
					ProviderName:    providerName,
					PricingResolver: bootstrap.pricingResolver,
					// Catalog lets maxTokensForModel resolve each model's real
					// max_output_tokens (e.g. 16384) instead of silently
					// falling back to the package's 4096-token default.
					Catalog: bootstrap.modelCatalog,
				})
			}
			// Look up provider quirks from the static catalog so that features
			// like "reasoning_content_passback" are honoured without hardcoding
			// provider names in the factory.
			var providerQuirks []string
			if entry, ok := bootstrap.modelCatalog.Providers[providerName]; ok {
				providerQuirks = entry.Quirks
			}
			cfg := openai.Config{
				APIKey:          apiKey,
				BaseURL:         baseURL,
				ProviderName:    providerName,
				PricingResolver: bootstrap.pricingResolver,
				ModelAPILookup:  bootstrap.lookupModelAPI,
				NoParallelTools: providerName == "gemini",
				ModelIDPrefix: func() string {
					if providerName == "gemini" {
						return "models/"
					}
					return ""
				}(),
				Quirks: providerQuirks,
			}
			if providerName == "openrouter" {
				referer := os.Getenv("HARNESS_OPENROUTER_REFERER")
				if referer == "" {
					referer = "https://github.com/dennisonbertram/go-agent-harness"
				}
				title := os.Getenv("HARNESS_OPENROUTER_TITLE")
				if title == "" {
					title = "go-agent-harness"
				}
				cfg.OpenRouterReferer = referer
				cfg.OpenRouterTitle = title
			}
			return opts.newProvider(cfg)
		})
	}

	return bootstrap, nil
}

type cronBootstrap struct {
	client    htools.CronClient
	store     cron.Store
	scheduler *cron.Scheduler
}

func buildCronBootstrap(workspace, cronURL string, logger func(string, ...any)) (cronBootstrap, error) {
	if logger == nil {
		logger = func(string, ...any) {}
	}
	if strings.TrimSpace(cronURL) != "" {
		return cronBootstrap{
			client: &cronClientAdapter{client: cron.NewClient(strings.TrimSpace(cronURL))},
		}, nil
	}

	cronDBPath := filepath.Join(workspace, ".harness", "cron.db")
	store, err := cron.NewSQLiteStore(cronDBPath)
	if err != nil {
		return cronBootstrap{}, fmt.Errorf("create cron store: %w", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		store.Close()
		return cronBootstrap{}, fmt.Errorf("migrate cron store: %w", err)
	}
	clock := cron.RealClock{}
	scheduler := cron.NewScheduler(store, &cron.ShellExecutor{}, clock, cron.SchedulerConfig{MaxConcurrent: 5})
	if err := scheduler.Start(context.Background()); err != nil {
		store.Close()
		return cronBootstrap{}, fmt.Errorf("start cron scheduler: %w", err)
	}
	logger("embedded cron scheduler started (db: %s)", cronDBPath)
	return cronBootstrap{
		client:    &embeddedCronAdapter{store: store, scheduler: scheduler, clock: clock},
		store:     store,
		scheduler: scheduler,
	}, nil
}

type persistenceBootstrapOptions struct {
	workspace         string
	getenv            func(string) string
	convRetentionDays int
	logger            func(string, ...any)
	newCleaner        func(store harness.ConversationStore, retentionDays int) conversationCleanerStarter
}

type persistenceBootstrap struct {
	runStore          istore.Store
	conversationStore harness.ConversationStore
	relayWorkerStore  relay.WorkerStore
	relayControl      *relay.ControlPlane
	convCleanerCancel context.CancelFunc
}

func buildPersistenceBootstrap(opts persistenceBootstrapOptions) (_ persistenceBootstrap, err error) {
	if opts.getenv == nil {
		opts.getenv = os.Getenv
	}
	if opts.logger == nil {
		opts.logger = func(string, ...any) {}
	}
	if opts.newCleaner == nil {
		opts.newCleaner = func(store harness.ConversationStore, retentionDays int) conversationCleanerStarter {
			return harness.NewConversationCleaner(store, retentionDays)
		}
	}

	var bootstrap persistenceBootstrap
	defer func() {
		if err == nil {
			return
		}
		if bootstrap.convCleanerCancel != nil {
			bootstrap.convCleanerCancel()
		}
		if bootstrap.conversationStore != nil {
			_ = bootstrap.conversationStore.Close()
		}
		if bootstrap.relayWorkerStore != nil {
			_ = bootstrap.relayWorkerStore.Close()
		}
		if bootstrap.runStore != nil {
			_ = bootstrap.runStore.Close()
		}
	}()

	if runDBPath := strings.TrimSpace(opts.getenv("HARNESS_RUN_DB")); runDBPath != "" {
		if !filepath.IsAbs(runDBPath) {
			runDBPath = filepath.Join(opts.workspace, runDBPath)
		}
		runStore, openErr := istore.NewSQLiteStore(runDBPath)
		if openErr != nil {
			err = fmt.Errorf("create run store: %w", openErr)
			return persistenceBootstrap{}, err
		}
		if migrateErr := runStore.Migrate(context.Background()); migrateErr != nil {
			_ = runStore.Close()
			err = fmt.Errorf("migrate run store: %w", migrateErr)
			return persistenceBootstrap{}, err
		}
		bootstrap.runStore = runStore
		opts.logger("run persistence enabled: %s", runDBPath)
	}

	if dbPath := strings.TrimSpace(opts.getenv("HARNESS_CONVERSATION_DB")); dbPath != "" {
		if !filepath.IsAbs(dbPath) {
			dbPath = filepath.Join(opts.workspace, dbPath)
		}
		convStore, openErr := harness.NewSQLiteConversationStore(dbPath)
		if openErr != nil {
			err = fmt.Errorf("create conversation store: %w", openErr)
			return persistenceBootstrap{}, err
		}
		if migrateErr := convStore.Migrate(context.Background()); migrateErr != nil {
			_ = convStore.Close()
			err = fmt.Errorf("migrate conversation store: %w", migrateErr)
			return persistenceBootstrap{}, err
		}
		bootstrap.conversationStore = convStore
		opts.logger("conversation persistence enabled: %s", dbPath)

		if opts.convRetentionDays > 0 {
			opts.logger("conversation retention policy: %d days", opts.convRetentionDays)
			cleanerCtx, cleanerCancel := context.WithCancel(context.Background())
			opts.newCleaner(convStore, opts.convRetentionDays).Start(cleanerCtx, 24*time.Hour)
			bootstrap.convCleanerCancel = cleanerCancel
		}
	}

	if relayDBPath := strings.TrimSpace(opts.getenv("HARNESS_RELAY_DB")); relayDBPath != "" {
		if !filepath.IsAbs(relayDBPath) {
			relayDBPath = filepath.Join(opts.workspace, relayDBPath)
		}
		relayStore, openErr := relay.NewSQLiteWorkerStore(relayDBPath)
		if openErr != nil {
			err = fmt.Errorf("create relay worker store: %w", openErr)
			return persistenceBootstrap{}, err
		}
		if migrateErr := relayStore.Migrate(context.Background()); migrateErr != nil {
			_ = relayStore.Close()
			err = fmt.Errorf("migrate relay worker store: %w", migrateErr)
			return persistenceBootstrap{}, err
		}
		bootstrap.relayWorkerStore = relayStore

		// Build the self-contained control plane (capability + event stores,
		// placement router, composer, policy, operator views) over the same DB.
		control, controlErr := relay.NewControlPlane(context.Background(), relayStore)
		if controlErr != nil {
			err = fmt.Errorf("build relay control plane: %w", controlErr)
			return persistenceBootstrap{}, err
		}
		bootstrap.relayControl = control
		opts.logger("relay worker persistence + control plane enabled: %s", relayDBPath)
	}

	return bootstrap, nil
}

type triggerRuntime struct {
	validators *trigger.ValidatorRegistry
	github     *githubadapter.GitHubAdapter
	slack      *slackadapter.SlackAdapter
	linear     *linearadapter.LinearAdapter
}

func buildTriggerRuntime(getenv func(string) string, logger func(string, ...any)) triggerRuntime {
	if getenv == nil {
		getenv = os.Getenv
	}
	if logger == nil {
		logger = func(string, ...any) {}
	}

	runtime := triggerRuntime{
		validators: trigger.NewValidatorRegistry(),
	}
	if secret := strings.TrimSpace(getenv("GITHUB_WEBHOOK_SECRET")); secret != "" {
		runtime.validators.Register("github", &trigger.GitHubValidator{Secret: secret})
		runtime.github = githubadapter.NewGitHubAdapter(secret)
		logger("registered GitHub webhook validator")
		logger("registered GitHub webhook adapter for /v1/webhooks/github")
	}
	if secret := strings.TrimSpace(getenv("SLACK_SIGNING_SECRET")); secret != "" {
		runtime.validators.Register("slack", &trigger.SlackValidator{Secret: secret})
		runtime.slack = slackadapter.NewSlackAdapter()
		logger("registered Slack webhook validator")
		logger("registered Slack webhook adapter for /v1/webhooks/slack")
	}
	if secret := strings.TrimSpace(getenv("LINEAR_WEBHOOK_SECRET")); secret != "" {
		runtime.validators.Register("linear", &trigger.LinearValidator{Secret: secret})
		runtime.linear = linearadapter.NewLinearAdapter()
		logger("registered Linear webhook validator")
		logger("registered Linear webhook adapter for /v1/webhooks/linear")
	}
	return runtime
}

type serverBootstrapOptions struct {
	runner           *harness.Runner
	modelCatalog     *catalog.Catalog
	skillLister      htools.SkillLister
	skillManager     server.SkillManager
	cronClient       htools.CronClient
	subagentManager  subagents.Manager
	checkpoints      *checkpoints.Service
	workflows        *workflows.Engine
	scriptWorkflows  scriptworkflow.SourceService
	networks         *networks.Engine
	providerRegistry *catalog.ProviderRegistry
	runStore         istore.Store
	relayWorkerStore relay.WorkerStore
	relayControl     *relay.ControlPlane
	tools            *harness.Registry
	todos            deferred.TodoManager
	triggers         triggerRuntime
	rolloutDir       string
}

func buildServerOptions(opts serverBootstrapOptions) server.ServerOptions {
	return server.ServerOptions{
		Runner:           opts.runner,
		Catalog:          opts.modelCatalog,
		AgentRunner:      opts.runner,
		SkillLister:      opts.skillLister,
		Skills:           opts.skillManager,
		CronClient:       opts.cronClient,
		SubagentManager:  opts.subagentManager,
		Checkpoints:      opts.checkpoints,
		Workflows:        opts.workflows,
		ScriptWorkflows:  opts.scriptWorkflows,
		Networks:         opts.networks,
		ProviderRegistry: opts.providerRegistry,
		Store:            opts.runStore,
		RelayWorkerStore: opts.relayWorkerStore,
		RelayControl:     opts.relayControl,
		Tools:            opts.tools,
		Todos:            opts.todos,
		Validators:       opts.triggers.validators,
		GitHubAdapter:    opts.triggers.github,
		SlackAdapter:     opts.triggers.slack,
		LinearAdapter:    opts.triggers.linear,
		RolloutDir:       opts.rolloutDir,
	}
}
