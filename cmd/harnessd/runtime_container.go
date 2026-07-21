package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"go-agent-harness/internal/checkpoints"
	"go-agent-harness/internal/harness"
	htools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/deferred"
	"go-agent-harness/internal/hooks"
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
	runnerCfg            harness.RunnerConfig
	configReloader       *configReloader
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
	relayControl         *relay.ControlPlane
	todos                deferred.TodoManager
	triggers             triggerRuntime
	callbackStarter      *callbackRunStarter
	callbackBridge       *harness.CallbackEventBridge
	callbackMgr          *htools.CallbackManager
	jobTracker           *harness.JobTracker
	msgSummarizer        *lazySummarizer
	skillManager         server.SkillManager
	hooksSummary         hooks.Summary
	subagentBaseRef      string
	subagentWorktreeRoot string
	subagentConfigTOML   string
	askUserBroker        htools.AskUserQuestionBroker
	askUserTimeout       time.Duration
}

type httpRuntime struct {
	runner          *harness.Runner
	tools           *harness.Registry
	subagentManager subagents.Manager
	mcpServer       *mcpserver.Server
	handler         http.Handler
	httpServer      *http.Server
}

// subagentRunnerHandoff implements subagents.RunEngine AND
// htools.ConstrainedAgentRunner by forwarding to a *harness.Runner that is not
// available yet at construction time. It exists to break an initialization
// cycle in buildHTTPRuntime: the SubagentManager (and the plain "agent" tool)
// must be resolvable before the tool registry is built, so that
// internal/harness/tools_default.go's `if opts.SubagentManager != nil` and
// `if opts.AgentRunner != nil` gates register the subagent-lifecycle tools
// (start_subagent, get_subagent, wait_subagent, cancel_subagent, run_agent)
// and the agent/spawn_agent tools onto the registry the top-level runner
// uses — but both need a *harness.Runner, which in turn needs that same
// registry. setRunner is called once, before buildHTTPRuntime returns and the
// server starts accepting requests; the atomic pointer makes that handoff
// race-safe under `go test -race`.
type subagentRunnerHandoff struct {
	runner atomic.Pointer[harness.Runner]
}

func (h *subagentRunnerHandoff) setRunner(r *harness.Runner) { h.runner.Store(r) }

func (h *subagentRunnerHandoff) StartRun(req harness.RunRequest) (harness.Run, error) {
	return h.runner.Load().StartRun(req)
}

func (h *subagentRunnerHandoff) GetRun(runID string) (harness.Run, bool) {
	return h.runner.Load().GetRun(runID)
}

func (h *subagentRunnerHandoff) Subscribe(runID string) ([]harness.Event, <-chan harness.Event, func(), error) {
	return h.runner.Load().Subscribe(runID)
}

func (h *subagentRunnerHandoff) CancelRun(runID string) error {
	return h.runner.Load().CancelRun(runID)
}

func (h *subagentRunnerHandoff) RunPrompt(ctx context.Context, prompt string) (string, error) {
	return h.runner.Load().RunPrompt(ctx, prompt)
}

func (h *subagentRunnerHandoff) RunPromptWithAllowedTools(ctx context.Context, prompt string, allowedTools []string) (string, error) {
	return h.runner.Load().RunPromptWithAllowedTools(ctx, prompt, allowedTools)
}

// SteerRun implements htools.RunSteerer, backing message_subagent and
// notify_parent (in each direction — the target run ID differs, the
// mechanism is identical: *harness.Runner.SteerRun).
func (h *subagentRunnerHandoff) SteerRun(runID, message string) error {
	return h.runner.Load().SteerRun(runID, message)
}

// ParentRunID implements the other half of htools.RunSteerer, backing
// notify_parent: it looks up runID's own Run record and reads the parent run
// ID that BuildParentContextHandoffFromContext recorded on it at spawn time.
func (h *subagentRunnerHandoff) ParentRunID(runID string) (string, bool) {
	run, ok := h.runner.Load().GetRun(runID)
	if !ok || run.ParentContextHandoff == nil {
		return "", false
	}
	parentID := strings.TrimSpace(run.ParentContextHandoff.ParentRunID)
	return parentID, parentID != ""
}

func buildHTTPRuntime(opts httpRuntimeOptions) (httpRuntime, error) {
	handoff := &subagentRunnerHandoff{}

	// registryOpts carries the subagent manager so the TOP-LEVEL registry
	// (built below) registers start_subagent/get_subagent/wait_subagent/
	// cancel_subagent/run_agent. The WorktreeRunnerFactory closure below
	// deliberately keeps using the original opts.baseRegistryOptions (manager
	// unset) so a worktree-isolated subagent cannot itself spawn subagents —
	// only this top-level (and same-registry inline-subagent) path gains the
	// tools this fix adds.
	registryOpts := opts.baseRegistryOptions

	subagentMgr, err := subagents.NewManager(subagents.Options{
		InlineRunner:  handoff,
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
	// tools_default.go's deferred subagent tools (start_subagent, etc.) want
	// the tools.SubagentManager shape (CreateAndWait/Start/Get/Wait/Cancel),
	// not the subagents.Manager shape (Create/Get/List/Delete/Cancel) used by
	// the HTTP /v1/subagents/* routes below. subagents.NewInlineManager
	// already exists to bridge exactly this gap; it was simply never wired
	// in anywhere.
	registryOpts.SubagentManager = subagents.NewInlineManager(subagentMgr)
	// Same circular-dependency fix, for the plain "agent"/"spawn_agent" tools:
	// *harness.Runner already implements htools.ConstrainedAgentRunner, but it
	// doesn't exist until after the registry does. handoff forwards to it once
	// setRunner is called below.
	registryOpts.AgentRunner = handoff
	// Same handoff object also backs message_subagent/notify_parent — it
	// already forwards to the runner once setRunner is called below.
	registryOpts.RunSteerer = handoff

	tools := harness.NewDefaultRegistryWithOptions(opts.workspace, registryOpts)
	runner := harness.NewRunner(opts.provider, tools, opts.runnerCfg)
	handoff.setRunner(runner)

	workflowEngine := workflows.NewEngine(workflows.Options{
		Definitions: opts.workflowDefinitions,
		Runner:      runner,
		Tools:       tools,
		Checkpoints: opts.checkpointService,
		Store:       opts.workflowStore,
		Now:         time.Now,
	})
	networkEngine := networks.NewEngine(networks.Options{
		Definitions: opts.networkDefinitions,
		Workflows:   workflowEngine,
	})

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

	if opts.configReloader != nil {
		opts.configReloader.bindRunner(runner)
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
		relayControl:     opts.relayControl,
		tools:            tools,
		todos:            opts.todos,
		triggers:         opts.triggers,
		rolloutDir:       opts.runnerCfg.RolloutDir,
		hooksSummary:     opts.hooksSummary,
		callbackMgr:      opts.callbackMgr,
		jobTracker:       opts.jobTracker,
		configReloader:   opts.configReloader,
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
		tools:           tools,
		subagentManager: subagentMgr,
		mcpServer:       mcpSrv,
		handler:         topMux,
		httpServer:      httpServer,
	}, nil
}
