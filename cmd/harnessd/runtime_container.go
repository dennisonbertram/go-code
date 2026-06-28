package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go-agent-harness/internal/checkpoints"
	"go-agent-harness/internal/harness"
	htools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/mcpserver"
	"go-agent-harness/internal/networks"
	"go-agent-harness/internal/provider/catalog"
	"go-agent-harness/internal/relay"
	"go-agent-harness/internal/server"
	istore "go-agent-harness/internal/store"
	"go-agent-harness/internal/subagents"
	scriptworkflow "go-agent-harness/internal/workflow"
	"go-agent-harness/internal/workflows"
)

type mcpStdioRuntime struct {
	workspace string
	catalog   []htools.Tool
	server    *mcpserver.StdioServer
}

func buildMCPStdioRuntime(workspace string) (mcpStdioRuntime, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		workspace = "."
	}

	catalogTools, err := htools.BuildCatalog(htools.BuildOptions{
		WorkspaceRoot: workspace,
		EnableTodos:   true,
		HTTPClient:    &http.Client{Timeout: 30 * time.Second},
	})
	if err != nil {
		return mcpStdioRuntime{}, fmt.Errorf("mcp: build tool catalog: %w", err)
	}

	srv, err := mcpserver.NewStdioServer(catalogTools)
	if err != nil {
		return mcpStdioRuntime{}, fmt.Errorf("mcp: create stdio server: %w", err)
	}

	return mcpStdioRuntime{
		workspace: workspace,
		catalog:   catalogTools,
		server:    srv,
	}, nil
}

type httpRuntimeOptions struct {
	addr                 string
	workspace            string
	provider             harness.Provider
	tools                *harness.Registry
	runnerCfg            harness.RunnerConfig
	checkpointService    *checkpoints.Service
	workflowDefinitions  []workflows.Definition
	workflowStore        workflows.Store
	goWorkflowDirs       []string
	goWorkflowSkillDirs  []string
	goWorkflowCacheDir   string
	scriptWorkflowRef    *scriptWorkflowServiceRef
	networkDefinitions   []networks.Definition
	skillLister          htools.SkillLister
	baseRegistryOptions  harness.DefaultRegistryOptions
	cronClient           htools.CronClient
	modelCatalog         *catalog.Catalog
	providerRegistry     *catalog.ProviderRegistry
	runStore             istore.Store
	relayWorkerStore     relay.WorkerStore
	triggers             triggerRuntime
	callbackStarter      *callbackRunStarter
	callbackBridge       *harness.CallbackEventBridge
	msgSummarizer        *lazySummarizer
	skillManager         server.SkillManager
	subagentBaseRef      string
	subagentWorktreeRoot string
	subagentConfigTOML   string
	askUserBroker        htools.AskUserQuestionBroker
	askUserTimeout       time.Duration
}

type httpRuntime struct {
	runner          *harness.Runner
	subagentManager subagents.Manager
	mcpServer       *mcpserver.Server
	handler         http.Handler
	httpServer      *http.Server
}

func buildHTTPRuntime(opts httpRuntimeOptions) (httpRuntime, error) {
	runner := harness.NewRunner(opts.provider, opts.tools, opts.runnerCfg)

	workflowEngine := workflows.NewEngine(workflows.Options{
		Definitions: opts.workflowDefinitions,
		Runner:      runner,
		Tools:       opts.tools,
		Checkpoints: opts.checkpointService,
		Store:       opts.workflowStore,
		Now:         time.Now,
	})
	networkEngine := networks.NewEngine(networks.Options{
		Definitions: opts.networkDefinitions,
		Workflows:   workflowEngine,
	})

	subagentMgr, err := subagents.NewManager(subagents.Options{
		InlineRunner:  runner,
		SkillResolver: opts.skillLister,
		WorktreeRunnerFactory: func(workspaceRoot string) (subagents.RunEngine, error) {
			childTools := harness.NewDefaultRegistryWithOptions(workspaceRoot, opts.baseRegistryOptions)
			return harness.NewRunner(opts.provider, childTools, opts.runnerCfg), nil
		},
		RepoPath:            opts.workspace,
		DefaultWorktreeRoot: opts.subagentWorktreeRoot,
		DefaultBaseRef:      opts.subagentBaseRef,
		ConfigTOML:          opts.subagentConfigTOML,
	})
	if err != nil {
		return httpRuntime{}, fmt.Errorf("create subagent manager: %w", err)
	}

	scriptEngine := scriptworkflow.NewEngine(scriptworkflow.EngineOptions{
		Subagents: scriptSubagentAdapter{manager: subagentMgr},
		QuestionResponder: workflowQuestionResponder{
			broker:  opts.askUserBroker,
			timeout: opts.askUserTimeout,
		},
	})
	sourceWorkflows, err := scriptworkflow.NewSourceManager(scriptworkflow.SourceManagerOptions{
		Engine:       scriptEngine,
		WorkflowDirs: opts.goWorkflowDirs,
		SkillDirs:    opts.goWorkflowSkillDirs,
		CacheDir:     opts.goWorkflowCacheDir,
	})
	if err != nil {
		return httpRuntime{}, fmt.Errorf("create script workflow manager: %w", err)
	}
	if err := sourceWorkflows.Load(context.Background()); err != nil {
		return httpRuntime{}, fmt.Errorf("load script workflows: %w", err)
	}
	if opts.scriptWorkflowRef != nil {
		opts.scriptWorkflowRef.Set(sourceWorkflows)
	}

	if opts.callbackStarter != nil {
		opts.callbackStarter.mu.Lock()
		opts.callbackStarter.runner = runner
		opts.callbackStarter.mu.Unlock()
	}

	if opts.callbackBridge != nil {
		opts.callbackBridge.BindRunner(runner)
	}

	if opts.msgSummarizer != nil {
		opts.msgSummarizer.mu.Lock()
		opts.msgSummarizer.summarizer = runner.NewMessageSummarizer()
		opts.msgSummarizer.mu.Unlock()
	}

	mainHandler := server.NewWithOptions(buildServerOptions(serverBootstrapOptions{
		runner:           runner,
		modelCatalog:     opts.modelCatalog,
		skillLister:      opts.skillLister,
		skillManager:     opts.skillManager,
		cronClient:       opts.cronClient,
		subagentManager:  subagentMgr,
		checkpoints:      opts.checkpointService,
		workflows:        workflowEngine,
		scriptWorkflows:  sourceWorkflows,
		networks:         networkEngine,
		providerRegistry: opts.providerRegistry,
		runStore:         opts.runStore,
		relayWorkerStore: opts.relayWorkerStore,
		triggers:         opts.triggers,
		rolloutDir:       opts.runnerCfg.RolloutDir,
	}))

	// Mount the MCP server at /mcp so external MCP clients can drive the harness.
	mcpSrv := mcpserver.NewServer(&mcpRunnerAdapter{runner: runner, store: opts.runStore})

	topMux := http.NewServeMux()
	topMux.Handle("/mcp", mcpSrv.Handler())
	topMux.Handle("/", mainHandler)

	httpServer := &http.Server{
		Addr:              opts.addr,
		Handler:           topMux,
		ReadTimeout:       60 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	return httpRuntime{
		runner:          runner,
		subagentManager: subagentMgr,
		mcpServer:       mcpSrv,
		handler:         topMux,
		httpServer:      httpServer,
	}, nil
}
