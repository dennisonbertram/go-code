package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"encoding/json"

	"github.com/google/uuid"
	"go-agent-harness/internal/checkpoints"
	"go-agent-harness/internal/config"
	"go-agent-harness/internal/cron"
	"go-agent-harness/internal/fakeprovider"
	"go-agent-harness/internal/goals"
	"go-agent-harness/internal/harness"
	htools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/deferred"
	"go-agent-harness/internal/mcp"
	"go-agent-harness/internal/mcp/oauth"
	"go-agent-harness/internal/networks"
	om "go-agent-harness/internal/observationalmemory"
	"go-agent-harness/internal/plugins"
	"go-agent-harness/internal/profiles"
	"go-agent-harness/internal/provider/catalog"
	openai "go-agent-harness/internal/provider/openai"
	"go-agent-harness/internal/provider/pricing"
	"go-agent-harness/internal/server"
	"go-agent-harness/internal/skills"
	"go-agent-harness/internal/skills/packs"
	istore "go-agent-harness/internal/store"
	"go-agent-harness/internal/systemprompt"
	"go-agent-harness/internal/watcher"
	"go-agent-harness/internal/workflows"
	"go-agent-harness/internal/workingmemory"
)

// callbackRunStarter is a lazy adapter that bridges the CallbackManager's
// RunStarter interface to the harness Runner. It uses a mutex-guarded pointer
// so the CallbackManager can be created before the Runner exists.
type callbackRunStarter struct {
	mu     sync.Mutex
	runner *harness.Runner
}

func (a *callbackRunStarter) StartRun(prompt, conversationID, tenantID, agentID string) error {
	a.mu.Lock()
	r := a.runner
	a.mu.Unlock()
	if r == nil {
		return fmt.Errorf("runner not yet initialized")
	}
	_, err := r.StartRun(harness.RunRequest{
		Prompt:         prompt,
		ConversationID: conversationID,
		TenantID:       tenantID,
		AgentID:        agentID,
	})
	return err
}

type providerFactory func(cfg openai.Config) (harness.Provider, error)

type conversationCleanerStarter interface {
	Start(ctx context.Context, interval time.Duration)
}

type runDeps struct {
	newConversationCleaner func(store harness.ConversationStore, retentionDays int) conversationCleanerStarter
}

type runnerConfigOptions struct {
	DefaultProviderName  string
	DefaultSystemPrompt  string
	DefaultAgentIntent   string
	AskUserTimeout       time.Duration
	AskUserBroker        htools.AskUserQuestionBroker
	ApprovalBroker       harness.ApprovalBroker
	MemoryManager        om.Manager
	WorkingMemoryStore   workingmemory.Store
	PromptEngine         systemprompt.Engine
	ToolApprovalMode     harness.ToolApprovalMode
	ProviderRegistry     *catalog.ProviderRegistry
	ConversationStore    harness.ConversationStore
	Store                istore.Store
	Logger               harness.Logger
	Activations          *harness.ActivationTracker
	GlobalMCPRegistry    htools.MCPRegistry
	GlobalMCPServerNames []string
	RoleModels           harness.RoleModels
	RolloutDirOverride   string
	// Workspace is the harnessd's startup workspace path. Used both as the
	// repo path for per-run worktree provisioning and as the base path for
	// the per-run tool registry rebuild after workspace_type provisioning.
	Workspace string
	// WorktreeRootDir, when set, controls where per-run worktrees are
	// materialized. Empty means the workspace package picks a default.
	WorktreeRootDir string
	// BaseRegistryOptions are the options used to build the runner's
	// default tool registry. Stashed on RunnerConfig so the runner can
	// rebuild a workspace-rooted registry per run when isolation is used.
	BaseRegistryOptions harness.DefaultRegistryOptions
}

// buildRunnerConfig keeps config-driven runner behavior in one place so the
// merged harness config is the authoritative runtime contract for harnessd.
func buildRunnerConfig(harnessCfg config.Config, opts runnerConfigOptions) harness.RunnerConfig {
	rolloutDir := strings.TrimSpace(opts.RolloutDirOverride)
	if rolloutDir == "" {
		rolloutDir = strings.TrimSpace(harnessCfg.Forensics.RolloutDir)
	}

	return harness.RunnerConfig{
		DefaultModel:                  harnessCfg.Model,
		DefaultProviderName:           opts.DefaultProviderName,
		DefaultSystemPrompt:           opts.DefaultSystemPrompt,
		DefaultAgentIntent:            opts.DefaultAgentIntent,
		MaxSteps:                      harnessCfg.MaxSteps,
		AskUserTimeout:                opts.AskUserTimeout,
		AskUserBroker:                 opts.AskUserBroker,
		ApprovalBroker:                opts.ApprovalBroker,
		MemoryManager:                 opts.MemoryManager,
		WorkingMemoryStore:            opts.WorkingMemoryStore,
		PromptEngine:                  opts.PromptEngine,
		ToolApprovalMode:              opts.ToolApprovalMode,
		ProviderRegistry:              opts.ProviderRegistry,
		ConversationStore:             opts.ConversationStore,
		Store:                         opts.Store,
		Logger:                        opts.Logger,
		Activations:                   opts.Activations,
		RolloutDir:                    rolloutDir,
		RoleModels:                    opts.RoleModels,
		GlobalMCPRegistry:             opts.GlobalMCPRegistry,
		GlobalMCPServerNames:          opts.GlobalMCPServerNames,
		AutoCompactEnabled:            harnessCfg.AutoCompact.Enabled,
		AutoCompactMode:               harnessCfg.AutoCompact.Mode,
		AutoCompactThreshold:          harnessCfg.AutoCompact.Threshold,
		AutoCompactKeepLast:           harnessCfg.AutoCompact.KeepLast,
		ModelContextWindow:            harnessCfg.AutoCompact.ModelContextWindow,
		TraceToolDecisions:            harnessCfg.Forensics.TraceToolDecisions,
		DetectAntiPatterns:            harnessCfg.Forensics.DetectAntiPatterns,
		TraceHookMutations:            harnessCfg.Forensics.TraceHookMutations,
		CaptureRequestEnvelope:        harnessCfg.Forensics.CaptureRequestEnvelope,
		SnapshotMemorySnippet:         harnessCfg.Forensics.SnapshotMemorySnippet,
		ErrorChainEnabled:             harnessCfg.Forensics.ErrorChainEnabled,
		ErrorContextDepth:             harnessCfg.Forensics.ErrorContextDepth,
		CaptureReasoning:              harnessCfg.Forensics.CaptureReasoning,
		CostAnomalyDetectionEnabled:   harnessCfg.Forensics.CostAnomalyDetectionEnabled,
		CostAnomalyStepMultiplier:     harnessCfg.Forensics.CostAnomalyStepMultiplier,
		AuditTrailEnabled:             harnessCfg.Forensics.AuditTrailEnabled,
		ContextWindowSnapshotEnabled:  harnessCfg.Forensics.ContextWindowSnapshotEnabled,
		ContextWindowWarningThreshold: harnessCfg.Forensics.ContextWindowWarningThreshold,
		CausalGraphEnabled:            harnessCfg.Forensics.CausalGraphEnabled,
		WorkspaceBaseOptions: harness.WorkspaceProvisionOptions{
			RepoPath:        opts.Workspace,
			WorktreeRootDir: opts.WorktreeRootDir,
		},
		BaseRegistryOptions: opts.BaseRegistryOptions,
	}
}

// profileFlag is the --profile CLI flag. Registered at package level so it
// integrates cleanly with Go's test infrastructure flags.
var profileFlag = flag.String("profile", "", "named profile to load from ~/.harness/profiles/<name>.toml")

// mcpFlag enables MCP stdio mode. When set, harnessd starts an MCP server
// that exposes the harness tool catalog over stdin/stdout instead of HTTP.
var mcpFlag = flag.Bool("mcp", false, "start in MCP stdio mode instead of HTTP server mode")

// mcpWorkspaceFlag sets the workspace root used when --mcp is active.
// Defaults to the current working directory when empty.
var mcpWorkspaceFlag = flag.String("mcp-workspace", "", "workspace root for MCP stdio mode (default: current directory)")

var (
	runMain            = run
	exitFunc           = os.Exit
	runWithSignalsFunc = runWithSignals
)

func main() {
	flag.Parse()
	if err := runMain(); err != nil {
		log.Printf("fatal: %v", err)
		exitFunc(1)
	}
}

func run() error {
	sig := make(chan os.Signal, 1)

	if *mcpFlag {
		// MCP stdio mode shuts down on any delivered signal; SIGHUP is
		// deliberately not registered here so a hangup cannot kill it.
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sig)
		return runMCPStdio(sig)
	}

	// HTTP server mode: SIGHUP triggers a live config reload (epic #815)
	// instead of terminating the daemon; SIGINT/SIGTERM shut down as before.
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sig)

	return runWithSignalsFunc(sig, os.Getenv, func(cfg openai.Config) (harness.Provider, error) {
		return openai.NewClient(cfg)
	}, *profileFlag)
}

// runMCPStdio starts the MCP stdio server using the harness tool catalog.
// It blocks until the signal channel fires or stdin is closed, then returns nil.
func runMCPStdio(sig <-chan os.Signal) error {
	// Resolve workspace: --mcp-workspace flag takes precedence over the
	// HARNESS_WORKSPACE env var; both default to "." when empty.
	workspace := *mcpWorkspaceFlag
	if workspace == "" {
		workspace = os.Getenv("HARNESS_WORKSPACE")
	}
	if workspace == "" {
		workspace = "."
	}

	runtime, err := buildMCPStdioRuntime(workspace)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		select {
		case <-sig:
			cancel()
		case <-ctx.Done():
			// Context was cancelled by another path (e.g. Start returned early).
			// Exit the goroutine so it does not block forever.
		}
	}()

	log.Printf("harness mcp server starting (stdio transport, %d tools)", runtime.server.ToolCount())
	return runtime.server.Start(ctx)
}

func runWithSignals(sig <-chan os.Signal, getenv func(string) string, newProvider providerFactory, profileName string) error {
	return runWithSignalsWithDeps(sig, getenv, newProvider, profileName, runDeps{
		newConversationCleaner: func(store harness.ConversationStore, retentionDays int) conversationCleanerStarter {
			return harness.NewConversationCleaner(store, retentionDays)
		},
	})
}

func runWithSignalsWithDeps(sig <-chan os.Signal, getenv func(string) string, newProvider providerFactory, profileName string, deps runDeps) error {
	if sig == nil {
		return fmt.Errorf("signal channel is required")
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	if newProvider == nil {
		newProvider = func(config openai.Config) (harness.Provider, error) {
			return openai.NewClient(config)
		}
	}
	if deps.newConversationCleaner == nil {
		deps.newConversationCleaner = func(store harness.ConversationStore, retentionDays int) conversationCleanerStarter {
			return harness.NewConversationCleaner(store, retentionDays)
		}
	}

	// Local helpers that use the injected getenv instead of os.Getenv,
	// so tests can override environment values without touching the real env.
	envOrDefault := func(key, fallback string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return fallback
	}
	envIntOrDefault := func(key string, fallback int) int {
		v := getenv(key)
		if v == "" {
			return fallback
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return fallback
		}
		return n
	}
	envToolApprovalModeOrDefault := func(key string, fallback harness.ToolApprovalMode) harness.ToolApprovalMode {
		value := strings.TrimSpace(strings.ToLower(getenv(key)))
		if value == "" {
			return fallback
		}
		switch harness.ToolApprovalMode(value) {
		case harness.ToolApprovalModeFullAuto, harness.ToolApprovalModePermissions:
			return harness.ToolApprovalMode(value)
		default:
			return fallback
		}
	}
	envBoolOrDefault := func(key string, fallback bool) bool {
		value := strings.TrimSpace(strings.ToLower(getenv(key)))
		if value == "" {
			return fallback
		}
		switch value {
		case "1", "true", "yes", "y", "on":
			return true
		case "0", "false", "no", "n", "off":
			return false
		default:
			return fallback
		}
	}

	// Load the layered configuration stack (layers 1–5).
	// This resolves model, addr, max_steps, and cost settings from:
	//   ~/.harness/config.toml → .harness/config.toml → profile → HARNESS_* env vars.
	home, _ := os.UserHomeDir()
	workspace := envOrDefault("HARNESS_WORKSPACE", ".")
	harnessConfigDir := filepath.Join(home, ".harness")
	harnessProfilesDir := filepath.Join(harnessConfigDir, "profiles")
	harnessUserConfig := filepath.Join(harnessConfigDir, "config.toml")
	harnessProjectConfig := filepath.Join(workspace, ".harness", "config.toml")
	loadOpts := config.LoadOptions{
		UserConfigPath:    harnessUserConfig,
		ProjectConfigPath: harnessProjectConfig,
		ProfilesDir:       harnessProfilesDir,
		Getenv:            getenv,
	}
	startupProfile, err := loadStartupProfile(profileName, filepath.Join(workspace, ".harness", "profiles"), harnessProfilesDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	harnessCfg, cfgErr := loadHarnessConfig(loadOpts, startupProfile, getenv)
	if cfgErr != nil {
		return fmt.Errorf("load config: %w", cfgErr)
	}

	// Use the resolved config values. HARNESS_MODEL, HARNESS_ADDR,
	// HARNESS_MAX_STEPS, and HARNESS_MAX_COST_PER_RUN_USD env vars are
	// already applied by the config stack at layer 5 — backward-compatible.
	// The MaxSteps 0→8 daemon default was applied inside loadHarnessConfig.
	model := harnessCfg.Model
	addr := harnessCfg.Addr

	defaultSystemPrompt := "You are a practical coding assistant. Prefer using tools for file inspection and tests when needed."
	if startupProfile != nil {
		if prompt := strings.TrimSpace(startupProfile.Runner.SystemPrompt); prompt != "" {
			defaultSystemPrompt = prompt
		}
	}
	systemPrompt := envOrDefault("HARNESS_SYSTEM_PROMPT", defaultSystemPrompt)
	// Default to no intent overlay (base prompt only). Headless launchers
	// (benchmark, cloud, cron) opt into "autonomous" explicitly via this env var.
	defaultAgentIntent := envOrDefault("HARNESS_DEFAULT_AGENT_INTENT", "")
	promptsDir := strings.TrimSpace(envOrDefault("HARNESS_PROMPTS_DIR", findDefaultPromptsDir()))
	askUserTimeoutSeconds := envIntOrDefault("HARNESS_ASK_USER_TIMEOUT_SECONDS", 300)
	approvalMode := envToolApprovalModeOrDefault("HARNESS_TOOL_APPROVAL_MODE", harness.ToolApprovalModeFullAuto)
	memoryMode := om.Mode(strings.TrimSpace(strings.ToLower(harnessCfg.Memory.Mode)))
	if memoryMode == "" {
		memoryMode = om.ModeAuto
	}
	memoryDriver := strings.TrimSpace(strings.ToLower(harnessCfg.Memory.DBDriver))
	if memoryDriver == "" {
		memoryDriver = "sqlite"
	}
	memoryDBDSN := strings.TrimSpace(harnessCfg.Memory.DBDSN)
	memorySQLitePath := strings.TrimSpace(harnessCfg.Memory.SQLitePath)
	if memorySQLitePath == "" {
		memorySQLitePath = ".harness/state.db"
	}
	memoryDefaultEnabled := harnessCfg.Memory.DefaultEnabled
	memoryObserveMinTokens := harnessCfg.Memory.ObserveMinTokens
	if memoryObserveMinTokens == 0 {
		memoryObserveMinTokens = 1200
	}
	memorySnippetMaxTokens := harnessCfg.Memory.SnippetMaxTokens
	if memorySnippetMaxTokens == 0 {
		memorySnippetMaxTokens = 900
	}
	memoryReflectThresholdTokens := harnessCfg.Memory.ReflectThresholdTokens
	if memoryReflectThresholdTokens == 0 {
		memoryReflectThresholdTokens = 4000
	}
	defaultMemoryLLMMode := strings.TrimSpace(strings.ToLower(harnessCfg.Memory.LLMMode))
	if defaultMemoryLLMMode == "" {
		switch {
		case strings.TrimSpace(harnessCfg.Memory.LLMProvider) != "":
			defaultMemoryLLMMode = "provider"
		case strings.TrimSpace(getenv("OPENAI_API_KEY")) != "":
			defaultMemoryLLMMode = "openai"
		default:
			defaultMemoryLLMMode = "inherit"
		}
	}
	memoryLLMMode := defaultMemoryLLMMode
	memoryLLMProvider := strings.TrimSpace(harnessCfg.Memory.LLMProvider)
	memoryLLMModel := strings.TrimSpace(harnessCfg.Memory.LLMModel)
	if memoryLLMModel == "" && memoryLLMMode == "openai" {
		memoryLLMModel = "gpt-5-nano"
	}
	memoryLLMBaseURL := strings.TrimSpace(harnessCfg.Memory.LLMBaseURL)
	if memoryLLMBaseURL == "" {
		memoryLLMBaseURL = strings.TrimSpace(getenv("OPENAI_BASE_URL"))
	}
	memoryLLMAPIKey := strings.TrimSpace(getenv("HARNESS_MEMORY_LLM_API_KEY"))
	if memoryLLMAPIKey == "" {
		memoryLLMAPIKey = strings.TrimSpace(getenv("OPENAI_API_KEY"))
	}
	skillsEnabled := envBoolOrDefault("HARNESS_SKILLS_ENABLED", true)
	watchEnabled := envBoolOrDefault("HARNESS_WATCH_ENABLED", true)
	watchIntervalSeconds := envIntOrDefault("HARNESS_WATCH_INTERVAL_SECONDS", 5)
	recipesDir := strings.TrimSpace(getenv("HARNESS_RECIPES_DIR"))
	workflowsDir := strings.TrimSpace(getenv("HARNESS_WORKFLOWS_DIR"))
	networksDir := strings.TrimSpace(getenv("HARNESS_NETWORKS_DIR"))
	subagentBaseRef := strings.TrimSpace(envOrDefault("HARNESS_SUBAGENT_BASE_REF", "HEAD"))
	subagentWorktreeRoot := strings.TrimSpace(getenv("HARNESS_SUBAGENT_WORKTREE_ROOT"))
	if subagentWorktreeRoot != "" && !filepath.IsAbs(subagentWorktreeRoot) {
		subagentWorktreeRoot = filepath.Join(filepath.Dir(workspace), subagentWorktreeRoot)
	}
	cronURL := strings.TrimSpace(getenv("HARNESS_CRON_URL"))
	callbacksEnabled := envBoolOrDefault("HARNESS_ENABLE_CALLBACKS", true)
	sourcegraphEndpoint := strings.TrimSpace(getenv("HARNESS_SOURCEGRAPH_ENDPOINT"))
	sourcegraphToken := strings.TrimSpace(getenv("HARNESS_SOURCEGRAPH_TOKEN"))
	rolloutDir := strings.TrimSpace(getenv("HARNESS_ROLLOUT_DIR"))
	if rolloutDir != "" {
		harnessCfg.Forensics.RolloutDir = rolloutDir
	}

	catalogBootstrap, err := buildCatalogBootstrap(catalogBootstrapOptions{
		workspace:   workspace,
		getenv:      getenv,
		newProvider: newProvider,
		logger:      log.Printf,
	})
	if err != nil {
		return err
	}
	providerRegistry := catalogBootstrap.providerRegistry
	modelCatalog := catalogBootstrap.modelCatalog

	provider, err := resolveDefaultProvider(resolveDefaultProviderOptions{
		getenv:                getenv,
		newProvider:           newProvider,
		registry:              providerRegistry,
		pricingResolver:       catalogBootstrap.pricingResolver,
		model:                 model,
		lookupModelAPI:        catalogBootstrap.lookupModelAPI,
		lookupModelModalities: catalogBootstrap.lookupModelModalities,
	})
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}
	promptEngine, err := systemprompt.NewFileEngine(promptsDir)
	if err != nil {
		return fmt.Errorf("load prompt engine from %s: %w", promptsDir, err)
	}

	memoryManager, err := newObservationalMemoryManager(observationalMemoryManagerOptions{
		Mode:           memoryMode,
		Driver:         memoryDriver,
		DBDSN:          memoryDBDSN,
		SQLitePath:     memorySQLitePath,
		WorkspaceRoot:  workspace,
		Provider:       provider,
		Model:          model,
		DefaultEnabled: memoryDefaultEnabled,
		DefaultConfig: om.Config{
			ObserveMinTokens:       memoryObserveMinTokens,
			SnippetMaxTokens:       memorySnippetMaxTokens,
			ReflectThresholdTokens: memoryReflectThresholdTokens,
		},
		ProviderRegistry:  providerRegistry,
		MemoryLLMMode:     memoryLLMMode,
		MemoryLLMProvider: memoryLLMProvider,
		MemoryLLMModel:    memoryLLMModel,
		MemoryLLMBaseURL:  memoryLLMBaseURL,
		MemoryLLMAPIKey:   memoryLLMAPIKey,
	})
	if err != nil {
		return fmt.Errorf("create observational memory manager: %w", err)
	}
	defer func() {
		if memoryManager != nil {
			_ = memoryManager.Close()
		}
	}()

	orchestrationDBPath := memorySQLitePath
	if orchestrationDBPath == "" {
		orchestrationDBPath = ".harness/state.db"
	}
	if !filepath.IsAbs(orchestrationDBPath) {
		orchestrationDBPath = filepath.Join(workspace, orchestrationDBPath)
	}
	checkpointStore, err := checkpoints.NewSQLiteStore(orchestrationDBPath)
	if err != nil {
		return fmt.Errorf("create checkpoint store: %w", err)
	}
	if err := checkpointStore.Migrate(context.Background()); err != nil {
		_ = checkpointStore.Close()
		return fmt.Errorf("migrate checkpoint store: %w", err)
	}
	defer checkpointStore.Close()
	checkpointService := checkpoints.NewService(checkpointStore, time.Now)

	workflowStore, err := workflows.NewSQLiteStore(orchestrationDBPath)
	if err != nil {
		return fmt.Errorf("create workflow store: %w", err)
	}
	if err := workflowStore.Migrate(context.Background()); err != nil {
		_ = workflowStore.Close()
		return fmt.Errorf("migrate workflow store: %w", err)
	}
	defer workflowStore.Close()

	workingMemoryStore, err := workingmemory.NewSQLiteStore(orchestrationDBPath)
	if err != nil {
		return fmt.Errorf("create working memory store: %w", err)
	}
	if err := workingMemoryStore.Migrate(context.Background()); err != nil {
		_ = workingMemoryStore.Close()
		return fmt.Errorf("migrate working memory store: %w", err)
	}
	defer workingMemoryStore.Close()

	workflowDefinitions, err := workflows.LoadDefinitions(workflowsDir)
	if err != nil {
		return fmt.Errorf("load workflows: %w", err)
	}
	networkDefinitions, err := networks.LoadDefinitions(networksDir)
	if err != nil {
		return fmt.Errorf("load networks: %w", err)
	}

	// Skills system
	globalDir := envOrDefault("HARNESS_GLOBAL_DIR", filepath.Join(home, ".go-harness"))
	var skillLister htools.SkillLister
	var skillLoader *skills.Loader     // retained for hot-reload
	var skillRegistry *skills.Registry // retained for hot-reload
	if skillsEnabled {
		pluginRoot := filepath.Join(globalDir, "plugins")
		bundles, pluginErr := plugins.EnabledBundles(pluginRoot, plugins.NewStateStore(filepath.Join(pluginRoot, "state.json")))
		if pluginErr != nil {
			log.Printf("warning: failed to discover enabled plugin bundles: %v", pluginErr)
		}
		var pluginSkillDirs []string
		for _, bundle := range bundles {
			if bundle.SkillsDir != "" {
				pluginSkillDirs = append(pluginSkillDirs, bundle.SkillsDir)
			}
		}
		skillLoader = skills.NewLoader(skills.LoaderConfig{
			GlobalDir:    filepath.Join(globalDir, "skills"),
			WorkspaceDir: filepath.Join(workspace, ".go-harness", "skills"),
			PluginDirs:   pluginSkillDirs,
		})
		skillRegistry = skills.NewRegistry()
		if err := skillRegistry.Load(skillLoader); err != nil {
			log.Printf("warning: failed to load skills: %v (continuing without skills)", err)
			skillRegistry = nil
		} else {
			skillResolver := skills.NewResolver(skillRegistry)
			promptEngine.SetSkillResolver(skillResolver)
			skillLister = &skillListerAdapter{registry: skillRegistry, resolver: skillResolver, workspace: workspace}
			loaded := skillRegistry.List()
			if len(loaded) > 0 {
				log.Printf("loaded %d skill(s)", len(loaded))
			}
		}
	}

	var cronClient htools.CronClient
	var cronStore cron.Store
	var cronScheduler *cron.Scheduler

	cronBootstrap, err := buildCronBootstrap(workspace, cronURL, log.Printf)
	if err != nil {
		return err
	}
	cronClient = cronBootstrap.client
	cronStore = cronBootstrap.store
	cronScheduler = cronBootstrap.scheduler

	// Delayed callbacks
	var callbackStarter *callbackRunStarter
	var callbackBridge *harness.CallbackEventBridge
	var callbackMgr *htools.CallbackManager
	if callbacksEnabled {
		callbackStarter = &callbackRunStarter{}
		// The bridge forwards callback lifecycle events onto the originating
		// run's SSE stream. It is bound to the Runner lazily (see
		// buildHTTPRuntime), mirroring callbackStarter, because the manager is
		// constructed before the Runner exists.
		callbackBridge = harness.NewCallbackEventBridge()
		callbackMgr = htools.NewCallbackManager(callbackStarter, htools.WithEventSink(callbackBridge))
		log.Printf("delayed callbacks enabled")
	}

	// Daemon-level background bash job tracker (epic #814 slice 2). Every
	// tool registry built with baseRegistryOptions — main, per-run
	// provisioned-workspace, and subagent worktree registries — registers its
	// JobManager here so GET /v1/tasks can enumerate bash jobs daemon-wide
	// and POST /v1/jobs/{id}/kill can terminate them.
	jobTracker := harness.NewJobTracker()

	// MCP server startup: TOML config (layers 1-3, no profile) then env var
	// servers are registered additively. TOML entries take precedence over env
	// var entries with the same name.
	mcpManager := mcp.NewClientManager()
	defer func() { _ = mcpManager.Close() }() // safety-net defer; explicit close is in shutdown sequence below
	// Attach OAuth tokens from ~/.harness/mcp to HTTP MCP requests, with
	// silent refresh; static per-server Authorization headers take precedence.
	mcpManager.SetTokenProvider((&oauth.Flow{Store: mcp.DefaultTokenStore()}).TokenProvider())
	{
		// Load config WITHOUT a profile so the global ClientManager only gets
		// layers 1-3 (user global + project); profile-specific servers are
		// scoped to individual runs, not the global manager.
		globalCfg, globalCfgErr := config.Load(config.LoadOptions{
			UserConfigPath:    harnessUserConfig,
			ProjectConfigPath: harnessProjectConfig,
			Getenv:            getenv,
		})
		if globalCfgErr != nil {
			log.Printf("warning: failed to load config for MCP server registration: %v (continuing without TOML-configured MCP servers)", globalCfgErr)
		}

		var envServers []mcp.ServerConfig
		mcpConfigs, mcpErr := mcp.ParseMCPServersEnvWith(getenv)
		if mcpErr != nil {
			log.Printf("warning: failed to parse %s: %v (continuing without env-configured MCP servers)", mcp.EnvVarMCPServers, mcpErr)
		} else {
			envServers = mcpConfigs
		}
		pluginRoot := filepath.Join(globalDir, "plugins")
		trustedBundles, pluginErr := plugins.TrustedBundles(pluginRoot, plugins.NewStateStore(filepath.Join(pluginRoot, "state.json")))
		if pluginErr != nil {
			log.Printf("warning: failed to discover trusted plugin bundles: %v", pluginErr)
		} else if pluginServers, err := plugins.MCPServers(trustedBundles); err != nil {
			log.Printf("warning: failed to load trusted plugin MCP config: %v", err)
		} else {
			envServers = append(envServers, pluginServers...)
		}

		registerMCPServersFromConfig(mcpManager, globalCfg.MCPServers, envServers, log.Printf)
	}

	// Wrap mcpManager as htools.MCPRegistry for use in tools registry and runner.
	var mcpRegistry htools.MCPRegistry
	if len(mcpManager.ListServers()) > 0 {
		mcpRegistry = &clientManagerRegistry{cm: mcpManager}
	}

	// Run state persistence (issue #42: also used for S3 backup).
	// HARNESS_RUN_DB configures the SQLite path for run/event/message records.
	// When S3_BUCKET is set but HARNESS_RUN_DB is not, we default to a
	// sibling file next to HARNESS_CONVERSATION_DB (or a temp location).
	convRetentionDays := envIntOrDefault("HARNESS_CONVERSATION_RETENTION_DAYS", 30)
	persistenceBootstrap, err := buildPersistenceBootstrap(persistenceBootstrapOptions{
		workspace:         workspace,
		getenv:            getenv,
		convRetentionDays: convRetentionDays,
		logger:            log.Printf,
		newCleaner:        deps.newConversationCleaner,
	})
	if err != nil {
		return err
	}
	runStore := persistenceBootstrap.runStore
	if runStore != nil {
		defer runStore.Close()
	}
	convStore := persistenceBootstrap.conversationStore
	if convStore != nil {
		defer convStore.Close()
	}
	relayWorkerStore := persistenceBootstrap.relayWorkerStore
	if relayWorkerStore != nil {
		defer relayWorkerStore.Close()
	}
	relayControl := persistenceBootstrap.relayControl
	convCleanerCancel := persistenceBootstrap.convCleanerCancel
	if convCleanerCancel != nil {
		defer convCleanerCancel()
	}

	askUserBroker := harness.NewCheckpointAskUserQuestionBroker(checkpointService, time.Now)
	approvalBroker := harness.NewCheckpointApprovalBroker(checkpointService)
	activations := harness.NewActivationTracker()
	msgSummarizer := &lazySummarizer{}
	scriptWorkflowRef := &scriptWorkflowServiceRef{}
	promptBehaviorsDir, promptTalentsDir := promptEngine.ExtensionDirs()
	globalWorkflowsDir := filepath.Join(globalDir, "workflows")
	workspaceWorkflowsDir := filepath.Join(workspace, ".go-harness", "workflows")
	globalSkillsDir := filepath.Join(globalDir, "skills")
	workspaceSkillsDir := filepath.Join(workspace, ".go-harness", "skills")
	goWorkflowCacheDir := strings.TrimSpace(getenv("HARNESS_GO_WORKFLOW_CACHE_DIR"))
	if goWorkflowCacheDir == "" {
		goWorkflowCacheDir = filepath.Join(workspace, ".harness", "workflow-cache")
	}
	// Shared todo store: one instance backs both the todos tool and the
	// /v1/runs/{id}/todos HTTP route. The store is keyed by run ID internally,
	// so a single process-wide instance keeps runs isolated.
	todoManager, todosToolBuilder := deferred.NewTodoStore()

	// Profile run store: powers get_efficiency_report (read) and per-run history
	// persistence (write). Optional — on open failure the feature degrades to a
	// no-history report rather than failing startup.
	var profileReadStore deferred.ProfileRunStoreIface
	var profileWriteStore istore.ProfileRunStoreIface
	if ps, perr := istore.NewSQLiteProfileRunStore(filepath.Join(globalDir, "profile-runs.db")); perr != nil {
		log.Printf("profile run store disabled: %v", perr)
	} else {
		profileWriteStore = ps
		profileReadStore = profiles.NewEfficiencyReadAdapter(ps)
		defer ps.Close()
	}

	// Skill pack registry: powers manage_skill_packs. Returns an empty (but
	// usable) registry when the directory is absent.
	packRegistry, perr := packs.NewPackRegistry(filepath.Join(globalDir, "skill-packs"))
	if perr != nil {
		log.Printf("skill pack registry disabled: %v", perr)
		packRegistry = nil
	}

	// Goals: persistent, cross-session goal tracking (the goals tool). Backed by
	// SQLite; on open failure NewManager falls back to an in-memory store so the
	// tool still works within the process (non-persistent) rather than vanishing.
	var goalStore goals.Store
	if gs, gerr := goals.NewSQLiteStore(filepath.Join(globalDir, "goals.db")); gerr != nil {
		log.Printf("goals persistence disabled (using in-memory): %v", gerr)
	} else {
		goalStore = gs
		defer gs.Close()
	}
	goalManager := goals.NewManager(goalStore)

	// skillListerAdapter (assigned to skillLister above, when skills are
	// enabled) already implements GetSkillFilePath/UpdateSkillVerification,
	// so it satisfies htools.SkillVerifier as well as htools.SkillLister —
	// it was just never asserted to the wider interface, so the verify_skill
	// tool was never registered even though nothing new is needed to run it.
	var skillVerifier htools.SkillVerifier
	if sv, ok := skillLister.(htools.SkillVerifier); ok {
		skillVerifier = sv
	}

	baseRegistryOptions := harness.DefaultRegistryOptions{
		ApprovalMode:       approvalMode,
		Policy:             nil,
		AskUserBroker:      askUserBroker,
		AskUserTimeout:     time.Duration(askUserTimeoutSeconds) * time.Second,
		SkillVerifier:      skillVerifier,
		MemoryManager:      memoryManager,
		WorkingMemoryStore: workingMemoryStore,
		SkillLister:        skillLister,
		SkillsDir:          filepath.Join(globalDir, "skills"),
		ModelCatalog:       modelCatalog,
		CronClient:         cronClient,
		CallbackManager:    callbackMgr,
		JobTracker:         jobTracker,
		Activations:        activations,
		Sourcegraph: htools.SourcegraphConfig{
			Endpoint: sourcegraphEndpoint,
			Token:    sourcegraphToken,
		},
		RecipesDir: recipesDir,
		PromptExtensionDirs: htools.PromptExtensionDirs{
			BehaviorsDir: promptBehaviorsDir,
			TalentsDir:   promptTalentsDir,
		},
		ScriptToolsDir:    filepath.Join(globalDir, "tools"),
		WorkflowService:   scriptWorkflowRef,
		ConversationStore: convStore,
		MessageSummarizer: msgSummarizer,
		MCPRegistry:       mcpRegistry,
		ProfileRunStore:   profileReadStore,
		PackRegistry:      packRegistry,
		TodosTool:         todosToolBuilder,
		GoalManager:       goalManager,
	}
	if rolloutDir != "" {
		log.Printf("rollout recording enabled: %s", rolloutDir)
	}
	// assemblyDeps captures the long-lived wiring dependencies for building a
	// complete RunnerConfig. Startup uses them now; the config reloader
	// (POST /v1/config/reload, epic #815) reuses them to rebuild an identical
	// config shape on every reload.
	assemblyDeps := runnerConfigAssemblyDeps{
		opts: runnerConfigOptions{
			DefaultProviderName: func() string {
				name := strings.TrimSpace(getenv("HARNESS_PROVIDER"))
				if name == "fake" {
					return ""
				}
				return name
			}(),
			DefaultSystemPrompt:  systemPrompt,
			DefaultAgentIntent:   defaultAgentIntent,
			AskUserTimeout:       time.Duration(askUserTimeoutSeconds) * time.Second,
			AskUserBroker:        askUserBroker,
			ApprovalBroker:       approvalBroker,
			MemoryManager:        memoryManager,
			WorkingMemoryStore:   workingMemoryStore,
			PromptEngine:         promptEngine,
			ToolApprovalMode:     approvalMode,
			ProviderRegistry:     providerRegistry,
			ConversationStore:    convStore,
			Store:                runStore,
			Logger:               &stdLogger{},
			Activations:          activations,
			GlobalMCPRegistry:    mcpRegistry,
			GlobalMCPServerNames: mcpManager.ListServers(),
			RoleModels: harness.RoleModels{
				Primary:    strings.TrimSpace(getenv("HARNESS_ROLE_MODEL_PRIMARY")),
				Summarizer: strings.TrimSpace(getenv("HARNESS_ROLE_MODEL_SUMMARIZER")),
			},
			RolloutDirOverride:  rolloutDir,
			Workspace:           workspace,
			WorktreeRootDir:     subagentWorktreeRoot,
			BaseRegistryOptions: baseRegistryOptions,
		},
		profileRunStore: profileWriteStore,
		getenv:          getenv,
		workspace:       workspace,
		home:            home,
		globalDir:       globalDir,
	}
	runnerCfg, hooksSummary := assembleRunnerConfig(harnessCfg, assemblyDeps)

	// Config reloader (epic #815): re-runs the startup load sequence, diffs
	// against the last-known-good config, reassembles the runner config, and
	// applies it for subsequent runs. Bound to the runner inside
	// buildHTTPRuntime and served via POST /v1/config/reload.
	configReloader := newConfigReloader(func() (config.Config, error) {
		return loadHarnessConfig(loadOpts, startupProfile, getenv)
	}, harnessCfg, assemblyDeps)

	subagentConfigTOML, err := config.WorkspaceRunnerConfigFromConfig(harnessCfg).ToTOML()
	if err != nil {
		return fmt.Errorf("serialize subagent config: %w", err)
	}

	// Hot-reload file watcher: monitors skills directories and reloads
	// when SKILL.md files are created, modified, or deleted.
	watchCtx, watchCancel := context.WithCancel(context.Background())
	defer watchCancel()

	if watchEnabled && skillsEnabled && skillRegistry != nil && skillLoader != nil {
		pollInterval := time.Duration(watchIntervalSeconds) * time.Second
		w := watcher.New(pollInterval)

		reloadSkills := func() error {
			if err := skillRegistry.Reload(skillLoader); err != nil {
				log.Printf("watcher: skill reload error: %v", err)
				return err
			}
			if err := scriptWorkflowRef.Reload(context.Background()); err != nil {
				log.Printf("watcher: workflow reload error: %v", err)
				return err
			}
			log.Printf("watcher: skills reloaded (%d skill(s))", len(skillRegistry.List()))
			return nil
		}

		reloadWorkflows := func() error {
			if err := scriptWorkflowRef.Reload(context.Background()); err != nil {
				log.Printf("watcher: workflow reload error: %v", err)
				return err
			}
			log.Printf("watcher: script workflows reloaded")
			return nil
		}

		w.Watch(watcher.WatchedDir{Path: globalWorkflowsDir, Reload: reloadWorkflows})
		w.Watch(watcher.WatchedDir{Path: workspaceWorkflowsDir, Reload: reloadWorkflows})
		w.Watch(watcher.WatchedDir{Path: globalSkillsDir, Reload: reloadSkills})
		w.Watch(watcher.WatchedDir{Path: workspaceSkillsDir, Reload: reloadSkills})

		go w.Start(watchCtx)
		log.Printf("hot-reload watcher started (interval: %s, dirs: %s, %s, %s, %s)",
			pollInterval, globalWorkflowsDir, workspaceWorkflowsDir, globalSkillsDir, workspaceSkillsDir)
	}

	// Build the Skills SkillManager only when skillLister is a *skillListerAdapter
	// (i.e. skills are enabled). The SkillManager interface requires GetSkillFilePath
	// and UpdateSkillVerification in addition to the read-only methods.
	var skillManager server.SkillManager
	if sla, ok := skillLister.(*skillListerAdapter); ok {
		skillManager = sla
	}

	// Build the external-trigger validator registry from webhook secrets (issue #411).
	// Validators are only registered when the corresponding env var is non-empty;
	// missing secrets mean the source is simply unavailable (fail-closed).
	triggerRuntime := buildTriggerRuntime(getenv, log.Printf)

	runtime, err := buildHTTPRuntime(httpRuntimeOptions{
		addr:                 addr,
		workspace:            workspace,
		provider:             provider,
		runnerCfg:            runnerCfg,
		configReloader:       configReloader,
		checkpointService:    checkpointService,
		workflowDefinitions:  workflowDefinitions,
		workflowStore:        workflowStore,
		goWorkflowDirs:       []string{globalWorkflowsDir, workspaceWorkflowsDir},
		goWorkflowSkillDirs:  []string{globalSkillsDir, workspaceSkillsDir},
		goWorkflowCacheDir:   goWorkflowCacheDir,
		scriptWorkflowRef:    scriptWorkflowRef,
		networkDefinitions:   networkDefinitions,
		skillLister:          skillLister,
		baseRegistryOptions:  baseRegistryOptions,
		cronClient:           cronClient,
		modelCatalog:         modelCatalog,
		providerRegistry:     providerRegistry,
		runStore:             runStore,
		relayWorkerStore:     relayWorkerStore,
		relayControl:         relayControl,
		todos:                todoManager,
		triggers:             triggerRuntime,
		callbackStarter:      callbackStarter,
		callbackBridge:       callbackBridge,
		callbackMgr:          callbackMgr,
		jobTracker:           jobTracker,
		msgSummarizer:        msgSummarizer,
		skillManager:         skillManager,
		hooksSummary:         hooksSummary,
		subagentBaseRef:      subagentBaseRef,
		subagentWorktreeRoot: subagentWorktreeRoot,
		subagentConfigTOML:   subagentConfigTOML,
		askUserBroker:        askUserBroker,
		askUserTimeout:       time.Duration(askUserTimeoutSeconds) * time.Second,
	})
	if err != nil {
		return err
	}
	httpServer := runtime.httpServer

	serverErr := make(chan error, 1)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		log.Printf("harness server listening on %s", addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- fmt.Errorf("server error: %w", err)
		}
	}()

	// Wait for server failure, shutdown signal, or SIGHUP (config reload).
	// SIGINT/SIGTERM return nil here and fall through to graceful shutdown.
	if err := awaitServer(sig, serverErr, configReloader.reload); err != nil {
		return err
	}

	// Shut down callbacks before the HTTP server to prevent new runs during shutdown
	if callbackMgr != nil {
		callbackMgr.Shutdown()
	}

	// Shut down conversation retention cleaner goroutine.
	if convCleanerCancel != nil {
		convCleanerCancel()
	}

	// Shut down embedded cron scheduler
	if cronScheduler != nil {
		cronScheduler.Stop()
	}
	if cronStore != nil {
		cronStore.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}

	// Shut down the embedded MCP server (stops SSE broker and poller).
	if err := runtime.mcpServer.Shutdown(ctx); err != nil {
		log.Printf("mcp server shutdown error: %v", err)
	}

	// Explicitly close MCP connections after the HTTP server has drained
	// in-flight requests. This ensures any tool calls that were in progress
	// finish before their underlying MCP connections are torn down.
	// The defer above is kept as a safety net for abnormal exit paths.
	if err := mcpManager.Close(); err != nil {
		log.Printf("mcp shutdown error: %v", err)
	}

	select {
	case err := <-serverErr:
		return err
	case <-serverDone:
	}
	return nil
}

// resolveDefaultProviderOptions holds all inputs needed to pick the default
// LLM provider at startup.
type resolveDefaultProviderOptions struct {
	getenv                func(string) string
	newProvider           providerFactory
	registry              *catalog.ProviderRegistry
	pricingResolver       pricing.Resolver
	model                 string
	lookupModelAPI        func(providerName, modelID string) string
	lookupModelModalities func(providerName, modelID string) []string
}

// resolveDefaultProvider picks the default harness.Provider at startup:
//  1. If the configured default model resolves to a configured catalog provider,
//     use that provider so startup behavior matches the selected model.
//  2. Else if OPENAI_API_KEY is set, keep the legacy OpenAI path.
//  3. Else return a clear error describing the missing model/provider config.
//
// fakeProviderTurnJSON is the on-disk JSON shape for a single scripted turn.
// It maps to fakeprovider.Turn.  Fields match the plan-defined schema:
//
//	{content, tool_calls?, usage{prompt,completion}?, cost_usd?, cost_status?}
//
// Note: usage uses short keys (prompt/completion) rather than the longer
// CompletionUsage field names to keep the shell-smoke file concise.
type fakeProviderTurnJSON struct {
	Content    string                 `json:"content"`
	ToolCalls  []harness.ToolCall     `json:"tool_calls,omitempty"`
	Usage      *fakeProviderUsageJSON `json:"usage,omitempty"`
	CostUSD    *float64               `json:"cost_usd,omitempty"`
	CostStatus harness.CostStatus     `json:"cost_status,omitempty"`
}

type fakeProviderUsageJSON struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
}

// loadFakeTurns reads a JSON turns file and converts it to []fakeprovider.Turn.
func loadFakeTurns(path string) ([]fakeprovider.Turn, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fake turns file %q: %w", path, err)
	}
	var raw []fakeProviderTurnJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse fake turns file %q: %w", path, err)
	}
	turns := make([]fakeprovider.Turn, len(raw))
	for i, r := range raw {
		t := fakeprovider.Turn{
			Content:    r.Content,
			ToolCalls:  r.ToolCalls,
			CostUSD:    r.CostUSD,
			CostStatus: r.CostStatus,
		}
		if r.Usage != nil {
			total := r.Usage.Prompt + r.Usage.Completion
			t.Usage = &harness.CompletionUsage{
				PromptTokens:     r.Usage.Prompt,
				CompletionTokens: r.Usage.Completion,
				TotalTokens:      total,
			}
		}
		turns[i] = t
	}
	return turns, nil
}

func resolveDefaultProvider(opts resolveDefaultProviderOptions) (harness.Provider, error) {
	if opts.getenv == nil {
		opts.getenv = os.Getenv
	}

	// Path 0: explicit provider override. The fake provider remains a
	// key-free deterministic shell-smoke special case; catalog providers such
	// as codex-subscription resolve through their configured registry source.
	providerOverride := strings.TrimSpace(opts.getenv("HARNESS_PROVIDER"))
	if providerOverride == "fake" {
		turnsPath := strings.TrimSpace(opts.getenv("HARNESS_FAKE_TURNS"))
		turns, err := loadFakeTurns(turnsPath)
		if err != nil {
			return nil, fmt.Errorf("fake provider: %w", err)
		}
		return fakeprovider.New(turns), nil
	}
	if providerOverride != "" {
		if opts.registry == nil || opts.registry.Catalog() == nil {
			return nil, fmt.Errorf("configured provider %q is unavailable: model catalog is not loaded", providerOverride)
		}
		if _, ok := opts.registry.Catalog().Providers[providerOverride]; !ok {
			return nil, fmt.Errorf("configured provider %q is not in the model catalog", providerOverride)
		}
		if !opts.registry.IsConfigured(providerOverride) {
			if providerOverride == "codex-subscription" {
				return nil, fmt.Errorf("Codex subscription is not configured; run `codex login`, then `harnesscli auth codex login`")
			}
			return nil, fmt.Errorf("configured provider %q has no credentials", providerOverride)
		}
		client, err := opts.registry.GetClient(providerOverride)
		if err != nil {
			return nil, fmt.Errorf("create configured provider %q: %w", providerOverride, err)
		}
		resolved, ok := client.(harness.Provider)
		if !ok {
			return nil, fmt.Errorf("provider %q client does not implement harness.Provider", providerOverride)
		}
		return resolved, nil
	}

	model := strings.TrimSpace(opts.model)

	// Path 1: use the provider that serves the selected default model.
	if opts.registry != nil && opts.registry.Catalog() != nil && model != "" {
		providerName, found := opts.registry.ResolveProviderStatic(model)
		if !found {
			providerName, found = resolveDynamicProvider(model, opts.registry.Catalog())
		}
		if found {
			entry := opts.registry.Catalog().Providers[providerName]
			if !opts.registry.IsConfigured(providerName) {
				return nil, fmt.Errorf("default model %q resolves to provider %q, but API key env %q is not set", model, providerName, entry.APIKeyEnv)
			}
			client, err := opts.registry.GetClient(providerName)
			if err != nil {
				return nil, fmt.Errorf("create provider %q for default model %q: %w", providerName, model, err)
			}
			provider, ok := client.(harness.Provider)
			if !ok {
				return nil, fmt.Errorf("provider %q client does not implement harness.Provider", providerName)
			}
			return provider, nil
		}
	}

	// Path 2: OPENAI_API_KEY present — keep the legacy OpenAI bootstrap path.
	if apiKey := strings.TrimSpace(opts.getenv("OPENAI_API_KEY")); apiKey != "" {
		p, err := opts.newProvider(openai.Config{
			APIKey:              apiKey,
			BaseURL:             opts.getenv("OPENAI_BASE_URL"),
			Model:               model,
			PricingResolver:     opts.pricingResolver,
			ModelAPILookup:      opts.lookupModelAPI,
			ModelModalityLookup: opts.lookupModelModalities,
		})
		if err != nil {
			return nil, fmt.Errorf("create openai provider: %w", err)
		}
		return p, nil
	}

	// Path 3: registry exists, but the selected model is not usable there.
	if opts.registry != nil && opts.registry.Catalog() != nil {
		if model != "" {
			return nil, fmt.Errorf("default model %q is not available from any configured provider; set OPENAI_API_KEY or choose a model from the configured catalog", model)
		}
		return nil, fmt.Errorf("no provider configured: set OPENAI_API_KEY or configure a provider in the model catalog")
	}

	// Path 4: nothing configured.
	return nil, fmt.Errorf("no provider configured: set OPENAI_API_KEY or configure a provider in the model catalog")
}

// resolveDynamicProvider handles startup-only provider resolution for models
// that are intentionally not exhaustively hardcoded in the catalog. Today this
// is used for OpenRouter's large provider/model slug space.
func resolveDynamicProvider(model string, cat *catalog.Catalog) (string, bool) {
	if cat == nil {
		return "", false
	}
	model = strings.TrimSpace(model)
	if model == "" || !strings.Contains(model, "/") {
		return "", false
	}
	if _, ok := cat.Providers["openrouter"]; ok {
		return "openrouter", true
	}
	return "", false
}

func loadStartupProfile(profileName, projectProfilesDir, userProfilesDir string) (*profiles.Profile, error) {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return nil, nil
	}
	home, _ := os.UserHomeDir()
	pluginRoot := filepath.Join(home, ".go-harness", "plugins")
	trustedBundles, _ := plugins.TrustedBundles(pluginRoot, plugins.NewStateStore(filepath.Join(pluginRoot, "state.json")))
	var agentDirs []string
	for _, bundle := range trustedBundles {
		if bundle.AgentsDir != "" {
			agentDirs = append(agentDirs, bundle.AgentsDir)
		}
	}
	profile, err := profiles.LoadProfileWithExtraDirs(profileName, projectProfilesDir, userProfilesDir, agentDirs)
	if err != nil {
		return nil, err
	}
	return profile, nil
}

func applyProfileDefaults(cfg config.Config, profile *profiles.Profile, getenv func(string) string) config.Config {
	if profile == nil {
		return cfg
	}

	values := profile.ApplyValues()
	if values.Model != "" {
		cfg.Model = values.Model
	}
	if values.MaxSteps != 0 {
		cfg.MaxSteps = values.MaxSteps
	}
	if values.MaxCostUSD != 0 {
		cfg.Cost.MaxPerRunUSD = values.MaxCostUSD
	}

	if getenv != nil {
		if v := strings.TrimSpace(getenv("HARNESS_MODEL")); v != "" {
			cfg.Model = v
		}
		if v := strings.TrimSpace(getenv("HARNESS_MAX_STEPS")); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				cfg.MaxSteps = n
			}
		}
		if v := strings.TrimSpace(getenv("HARNESS_MAX_COST_PER_RUN_USD")); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				cfg.Cost.MaxPerRunUSD = f
			}
		}
	}

	return cfg
}

func getenvOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func findDefaultPromptsDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "prompts"
	}
	dir := cwd
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, "prompts")
		if _, statErr := os.Stat(filepath.Join(candidate, "catalog.yaml")); statErr == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "prompts"
}

func getenvIntOrDefault(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func getenvToolApprovalModeOrDefault(key string, fallback harness.ToolApprovalMode) harness.ToolApprovalMode {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch harness.ToolApprovalMode(value) {
	case harness.ToolApprovalModeFullAuto, harness.ToolApprovalModePermissions:
		return harness.ToolApprovalMode(value)
	default:
		return fallback
	}
}

func getenvMemoryModeOrDefault(key string, fallback om.Mode) om.Mode {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch om.Mode(value) {
	case om.ModeAuto, om.ModeOff, om.ModeLocalCoordinator:
		return om.Mode(value)
	default:
		return fallback
	}
}

func getenvBoolOrDefault(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

// stdLogger wraps log.Printf to implement harness.Logger.
type stdLogger struct{}

func (l *stdLogger) Error(msg string, keysAndValues ...any) {
	args := []any{msg}
	args = append(args, keysAndValues...)
	log.Println(args...)
}

type observationalMemoryManagerOptions struct {
	Mode              om.Mode
	Driver            string
	DBDSN             string
	SQLitePath        string
	WorkspaceRoot     string
	Provider          harness.Provider
	ProviderRegistry  *catalog.ProviderRegistry
	Model             string
	DefaultEnabled    bool
	DefaultConfig     om.Config
	MemoryLLMMode     string
	MemoryLLMProvider string
	MemoryLLMModel    string
	MemoryLLMBaseURL  string
	MemoryLLMAPIKey   string
}

func newObservationalMemoryManager(opts observationalMemoryManagerOptions) (om.Manager, error) {
	mode := opts.Mode
	if mode == "" {
		mode = om.ModeAuto
	}
	if mode == om.ModeOff {
		return om.NewDisabledManager(mode), nil
	}

	var store om.Store
	switch opts.Driver {
	case "", "sqlite":
		path := opts.SQLitePath
		if strings.TrimSpace(path) == "" {
			path = ".harness/state.db"
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(opts.WorkspaceRoot, path)
		}
		sqliteStore, err := om.NewSQLiteStore(path)
		if err != nil {
			return nil, err
		}
		store = sqliteStore
	case "postgres":
		pgStore, err := om.NewPostgresStore(opts.DBDSN)
		if err != nil {
			return nil, err
		}
		store = pgStore
	default:
		return nil, fmt.Errorf("unsupported memory db driver %q", opts.Driver)
	}

	var model om.Model
	llmMode := strings.TrimSpace(strings.ToLower(opts.MemoryLLMMode))
	if llmMode == "" {
		llmMode = "inherit"
	}
	switch llmMode {
	case "inherit":
		model = observationalMemoryModel{
			provider: opts.Provider,
			model:    opts.Model,
		}
	case "provider":
		providerName := strings.TrimSpace(opts.MemoryLLMProvider)
		if providerName == "" {
			return nil, fmt.Errorf("memory llm provider is required")
		}
		if opts.ProviderRegistry == nil {
			return nil, fmt.Errorf("memory llm provider mode requires provider registry")
		}
		client, err := opts.ProviderRegistry.GetClient(providerName)
		if err != nil {
			return nil, fmt.Errorf("resolve memory provider %q: %w", providerName, err)
		}
		provider, ok := client.(harness.Provider)
		if !ok {
			return nil, fmt.Errorf("memory provider %q client does not implement harness.Provider", providerName)
		}
		modelName := strings.TrimSpace(opts.MemoryLLMModel)
		if modelName == "" {
			modelName = strings.TrimSpace(opts.Model)
		}
		if modelName == "" {
			return nil, fmt.Errorf("memory llm model is required")
		}
		model = observationalMemoryModel{
			provider: provider,
			model:    modelName,
		}
	case "openai":
		openAIModel, err := om.NewOpenAIModel(om.OpenAIConfig{
			APIKey:  opts.MemoryLLMAPIKey,
			BaseURL: opts.MemoryLLMBaseURL,
			Model:   opts.MemoryLLMModel,
		})
		if err != nil {
			return nil, fmt.Errorf("create observational memory openai model: %w", err)
		}
		model = openAIModel
	default:
		return nil, fmt.Errorf("unsupported memory llm mode %q", opts.MemoryLLMMode)
	}

	return om.NewService(om.ServiceOptions{
		Mode:           mode,
		Store:          store,
		Coordinator:    om.NewLocalCoordinator(),
		Observer:       om.ModelObserver{Model: model},
		Reflector:      om.ModelReflector{Model: model},
		Estimator:      om.RuneTokenEstimator{},
		DefaultConfig:  opts.DefaultConfig,
		DefaultEnabled: opts.DefaultEnabled,
		Now:            time.Now,
	})
}

// lazySummarizer implements htools.MessageSummarizer with deferred runner binding.
// The runner is created after the tool registry, so this adapter allows the
// compact_history tool to access the runner's summarization capability.
type lazySummarizer struct {
	mu         sync.Mutex
	summarizer htools.MessageSummarizer
}

func (s *lazySummarizer) SummarizeMessages(ctx context.Context, msgs []map[string]any) (string, error) {
	s.mu.Lock()
	inner := s.summarizer
	s.mu.Unlock()
	if inner == nil {
		return "", fmt.Errorf("summarizer not configured yet")
	}
	return inner.SummarizeMessages(ctx, msgs)
}

type observationalMemoryModel struct {
	provider harness.Provider
	model    string
}

func (m observationalMemoryModel) Complete(ctx context.Context, req om.ModelRequest) (string, error) {
	if m.provider == nil {
		return "", fmt.Errorf("provider is required")
	}
	messages := make([]harness.Message, 0, len(req.Messages))
	for _, msg := range req.Messages {
		messages = append(messages, harness.Message{Role: msg.Role, Content: msg.Content})
	}
	result, err := m.provider.Complete(ctx, harness.CompletionRequest{
		Model:    m.model,
		Messages: messages,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Content), nil
}

// skillListerAdapter bridges skills.Registry to htools.SkillLister.
type skillListerAdapter struct {
	registry  *skills.Registry
	resolver  *skills.Resolver
	workspace string
}

func (a *skillListerAdapter) GetSkill(name string) (htools.SkillInfo, bool) {
	s, ok := a.registry.Get(name)
	if !ok {
		return htools.SkillInfo{}, false
	}
	return htools.SkillInfo{
		Name:         s.Name,
		Description:  s.Description,
		ArgumentHint: s.ArgumentHint,
		Arguments:    s.Arguments,
		AllowedTools: s.AllowedTools,
		Source:       string(s.Source),
		Context:      string(s.Context),
		Agent:        s.Agent,
		Verified:     s.Verified,
		VerifiedAt:   s.VerifiedAt,
		VerifiedBy:   s.VerifiedBy,
		FilePath:     s.FilePath,
	}, true
}

func (a *skillListerAdapter) ListSkills() []htools.SkillInfo {
	all := a.registry.List()
	result := make([]htools.SkillInfo, len(all))
	for i, s := range all {
		result[i] = htools.SkillInfo{
			Name:         s.Name,
			Description:  s.Description,
			ArgumentHint: s.ArgumentHint,
			Arguments:    s.Arguments,
			AllowedTools: s.AllowedTools,
			Source:       string(s.Source),
			Context:      string(s.Context),
			Agent:        s.Agent,
			Verified:     s.Verified,
			VerifiedAt:   s.VerifiedAt,
			VerifiedBy:   s.VerifiedBy,
			FilePath:     s.FilePath,
		}
	}
	return result
}

func (a *skillListerAdapter) ResolveSkill(ctx context.Context, name, args, workspace string) (string, error) {
	ws := workspace
	if ws == "" {
		ws = a.workspace
	}
	return a.resolver.ResolveSkill(ctx, name, args, ws)
}

func (a *skillListerAdapter) GetSkillFilePath(name string) (string, bool) {
	return a.registry.GetFilePath(name)
}

func (a *skillListerAdapter) UpdateSkillVerification(ctx context.Context, name string, verified bool, verifiedAt time.Time, verifiedBy string) error {
	return a.registry.UpdateSkillVerification(ctx, name, verified, verifiedAt, verifiedBy)
}

// cronClientAdapter bridges cron.Client to htools.CronClient.
type cronClientAdapter struct {
	client *cron.Client
}

func (a *cronClientAdapter) CreateJob(ctx context.Context, req htools.CronCreateJobRequest) (htools.CronJob, error) {
	j, err := a.client.CreateJob(ctx, cron.CreateJobRequest{
		TenantID:   req.TenantID,
		Name:       req.Name,
		Schedule:   req.Schedule,
		ExecType:   req.ExecType,
		ExecConfig: req.ExecConfig,
		TimeoutSec: req.TimeoutSec,
		Tags:       req.Tags,
	})
	if err != nil {
		if cron.IsJobNotFound(err) {
			return htools.CronJob{}, htools.ErrCronJobNotFound
		}
		return htools.CronJob{}, err
	}
	return cronJobFromCron(j), nil
}

func (a *cronClientAdapter) ListJobs(ctx context.Context) ([]htools.CronJob, error) {
	jobs, err := a.client.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]htools.CronJob, len(jobs))
	for i, j := range jobs {
		result[i] = cronJobFromCron(j)
	}
	return result, nil
}

func (a *cronClientAdapter) GetJob(ctx context.Context, id string) (htools.CronJob, error) {
	j, err := a.client.GetJob(ctx, id)
	if err != nil {
		if cron.IsJobNotFound(err) {
			return htools.CronJob{}, htools.ErrCronJobNotFound
		}
		return htools.CronJob{}, err
	}
	return cronJobFromCron(j), nil
}

func (a *cronClientAdapter) UpdateJob(ctx context.Context, id string, req htools.CronUpdateJobRequest) (htools.CronJob, error) {
	j, err := a.client.UpdateJob(ctx, id, cron.UpdateJobRequest{
		Schedule:   req.Schedule,
		ExecConfig: req.ExecConfig,
		Status:     req.Status,
		TimeoutSec: req.TimeoutSec,
		Tags:       req.Tags,
	})
	if err != nil {
		if cron.IsJobNotFound(err) {
			return htools.CronJob{}, htools.ErrCronJobNotFound
		}
		return htools.CronJob{}, err
	}
	return cronJobFromCron(j), nil
}

func (a *cronClientAdapter) DeleteJob(ctx context.Context, id string) error {
	if err := a.client.DeleteJob(ctx, id); err != nil {
		if cron.IsJobNotFound(err) {
			return htools.ErrCronJobNotFound
		}
		return err
	}
	return nil
}

func (a *cronClientAdapter) ListExecutions(ctx context.Context, jobID string, limit, offset int) ([]htools.CronExecution, error) {
	execs, err := a.client.ListExecutions(ctx, jobID, limit, offset)
	if err != nil {
		return nil, err
	}
	result := make([]htools.CronExecution, len(execs))
	for i, e := range execs {
		result[i] = cronExecFromCron(e)
	}
	return result, nil
}

func (a *cronClientAdapter) Health(ctx context.Context) error {
	return a.client.Health(ctx)
}

func cronJobFromCron(j cron.Job) htools.CronJob {
	return htools.CronJob{
		ID:         j.ID,
		TenantID:   j.TenantID,
		Name:       j.Name,
		Schedule:   j.Schedule,
		ExecType:   j.ExecType,
		ExecConfig: j.ExecConfig,
		Status:     j.Status,
		TimeoutSec: j.TimeoutSec,
		Tags:       j.Tags,
		NextRunAt:  j.NextRunAt,
		LastRunAt:  j.LastRunAt,
		CreatedAt:  j.CreatedAt,
		UpdatedAt:  j.UpdatedAt,
	}
}

func cronExecFromCron(e cron.Execution) htools.CronExecution {
	return htools.CronExecution{
		ID:            e.ID,
		JobID:         e.JobID,
		StartedAt:     e.StartedAt,
		FinishedAt:    e.FinishedAt,
		Status:        e.Status,
		RunID:         e.RunID,
		OutputSummary: e.OutputSummary,
		Error:         e.Error,
		DurationMs:    e.DurationMs,
	}
}

// embeddedCronAdapter implements htools.CronClient by calling cron.Store
// and cron.Scheduler directly, without HTTP.
type embeddedCronAdapter struct {
	store     cron.Store
	scheduler *cron.Scheduler
	clock     cron.Clock
}

func (a *embeddedCronAdapter) CreateJob(ctx context.Context, req htools.CronCreateJobRequest) (htools.CronJob, error) {
	if req.Name == "" {
		return htools.CronJob{}, fmt.Errorf("name is required")
	}
	if req.Schedule == "" {
		return htools.CronJob{}, fmt.Errorf("schedule is required")
	}
	nextRun, err := cron.NextRunTime(req.Schedule, a.clock.Now())
	if err != nil {
		return htools.CronJob{}, fmt.Errorf("invalid schedule: %w", err)
	}
	if req.ExecType != cron.ExecTypeShell && req.ExecType != cron.ExecTypeHarness {
		return htools.CronJob{}, fmt.Errorf("execution_type must be \"shell\" or \"harness\"")
	}
	if req.TimeoutSec <= 0 {
		req.TimeoutSec = 30
	}
	now := a.clock.Now()
	job := cron.Job{
		ID:         uuid.New().String(),
		TenantID:   req.TenantID,
		Name:       req.Name,
		Schedule:   req.Schedule,
		ExecType:   req.ExecType,
		ExecConfig: req.ExecConfig,
		Status:     cron.StatusActive,
		TimeoutSec: req.TimeoutSec,
		Tags:       req.Tags,
		NextRunAt:  nextRun,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	job, err = a.store.CreateJob(ctx, job)
	if err != nil {
		if cron.IsJobNotFound(err) {
			return htools.CronJob{}, htools.ErrCronJobNotFound
		}
		return htools.CronJob{}, fmt.Errorf("store: %w", err)
	}
	if addErr := a.scheduler.AddJob(job); addErr != nil {
		return htools.CronJob{}, fmt.Errorf("scheduler: %w", addErr)
	}
	return cronJobFromCron(job), nil
}

func (a *embeddedCronAdapter) ListJobs(ctx context.Context) ([]htools.CronJob, error) {
	jobs, err := a.store.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]htools.CronJob, len(jobs))
	for i, j := range jobs {
		result[i] = cronJobFromCron(j)
	}
	return result, nil
}

func (a *embeddedCronAdapter) GetJob(ctx context.Context, id string) (htools.CronJob, error) {
	job, err := a.store.GetJob(ctx, id)
	if err != nil {
		if !cron.IsJobNotFound(err) {
			return htools.CronJob{}, err
		}
		job, err = a.store.GetJobByName(ctx, id)
		if err != nil {
			if cron.IsJobNotFound(err) {
				return htools.CronJob{}, htools.ErrCronJobNotFound
			}
			return htools.CronJob{}, err
		}
	}
	return cronJobFromCron(job), nil
}

func (a *embeddedCronAdapter) UpdateJob(ctx context.Context, id string, req htools.CronUpdateJobRequest) (htools.CronJob, error) {
	job, err := a.store.GetJob(ctx, id)
	if err != nil {
		if cron.IsJobNotFound(err) {
			return htools.CronJob{}, htools.ErrCronJobNotFound
		}
		return htools.CronJob{}, err
	}

	if req.Schedule != nil {
		trimmed := strings.TrimSpace(*req.Schedule)
		if trimmed == "" {
			return htools.CronJob{}, fmt.Errorf("schedule must not be empty")
		}
		nextRun, err := cron.NextRunTime(*req.Schedule, a.clock.Now())
		if err != nil {
			return htools.CronJob{}, fmt.Errorf("invalid schedule: %w", err)
		}
		job.Schedule = *req.Schedule
		job.NextRunAt = nextRun
	}
	if req.ExecConfig != nil {
		job.ExecConfig = *req.ExecConfig
	}
	if req.TimeoutSec != nil {
		job.TimeoutSec = *req.TimeoutSec
	}
	if req.Tags != nil {
		job.Tags = *req.Tags
	}

	if req.Status != nil {
		if *req.Status != cron.StatusActive && *req.Status != cron.StatusPaused {
			return htools.CronJob{}, fmt.Errorf("status must be \"active\" or \"paused\"")
		}
		oldStatus := job.Status
		job.Status = *req.Status

		if *req.Status == cron.StatusPaused && oldStatus != cron.StatusPaused {
			a.scheduler.RemoveJob(job.ID)
		}
		if *req.Status == cron.StatusActive && oldStatus != cron.StatusActive {
			if addErr := a.scheduler.AddJob(job); addErr != nil {
				return htools.CronJob{}, fmt.Errorf("scheduler: %w", addErr)
			}
		}
	}

	// Gate on job.Status (the EFFECTIVE post-update status), not on
	// req.Status (the raw request field) — mirrors the fix in
	// internal/cron/server.go's handleUpdateJob. A schedule-only update
	// (req.Status == nil) must not re-arm a job whose stored status is
	// paused: job.Status already reflects that live status in that case.
	if req.Schedule != nil && job.Status == cron.StatusActive {
		if err := a.scheduler.UpdateJobSchedule(job); err != nil {
			return htools.CronJob{}, fmt.Errorf("scheduler: %w", err)
		}
	}

	job.UpdatedAt = a.clock.Now()
	if err := a.store.UpdateJob(ctx, job); err != nil {
		if cron.IsJobNotFound(err) {
			return htools.CronJob{}, htools.ErrCronJobNotFound
		}
		return htools.CronJob{}, fmt.Errorf("store: %w", err)
	}
	return cronJobFromCron(job), nil
}

func (a *embeddedCronAdapter) DeleteJob(ctx context.Context, id string) error {
	if err := a.store.DeleteJob(ctx, id); err != nil {
		if cron.IsJobNotFound(err) {
			return htools.ErrCronJobNotFound
		}
		return err
	}
	a.scheduler.RemoveJob(id)
	return nil
}

func (a *embeddedCronAdapter) ListExecutions(ctx context.Context, jobID string, limit, offset int) ([]htools.CronExecution, error) {
	execs, err := a.store.ListExecutions(ctx, jobID, limit, offset)
	if err != nil {
		return nil, err
	}
	result := make([]htools.CronExecution, len(execs))
	for i, e := range execs {
		result[i] = cronExecFromCron(e)
	}
	return result, nil
}

func (a *embeddedCronAdapter) Health(_ context.Context) error {
	return nil
}
