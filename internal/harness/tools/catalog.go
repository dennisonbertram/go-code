package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"go-agent-harness/internal/harness/tools/recipe"
)

func BuildCatalog(opts BuildOptions) ([]Tool, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if opts.ApprovalMode == "" {
		opts.ApprovalMode = ApprovalModeFullAuto
	}
	if opts.AskUserTimeout <= 0 {
		opts.AskUserTimeout = 5 * time.Minute
	}

	jobManager := NewJobManager(opts.WorkspaceRoot, opts.Now)
	if opts.SandboxScope != "" {
		jobManager.SetSandboxScope(opts.SandboxScope)
	}
	todos := newTodoStore()

	tools := []Tool{
		askUserQuestionTool(opts.AskUserBroker, opts.AskUserTimeout),
		observationalMemoryTool(opts.WorkspaceRoot, opts.MemoryManager, opts.AgentRunner, opts.SandboxScope),
		readTool(opts.WorkspaceRoot, opts.SandboxScope),
		writeTool(opts.WorkspaceRoot, opts.SandboxScope),
		editTool(opts.WorkspaceRoot, opts.SandboxScope),
		bashTool(jobManager),
		jobOutputTool(jobManager),
		jobKillTool(jobManager),
		lsTool(opts.WorkspaceRoot, opts.SandboxScope),
		globTool(opts.WorkspaceRoot, opts.SandboxScope),
		grepTool(opts.WorkspaceRoot, opts.SandboxScope),
		applyPatchTool(opts.WorkspaceRoot, opts.SandboxScope),
		gitStatusTool(opts.WorkspaceRoot),
		gitDiffTool(opts.WorkspaceRoot, opts.SandboxScope),
		fetchTool(opts.HTTPClient, opts.NetworkAllowlist),
		downloadTool(opts.WorkspaceRoot, opts.HTTPClient, opts.SandboxScope, opts.NetworkAllowlist),
		contextStatusTool(),
		compactHistoryTool(opts.MessageSummarizer),
	}

	if opts.EnableTodos {
		tools = append(tools, todosTool(todos))
	}
	if opts.EnableLSP {
		tools = append(tools, lspDiagnosticsTool(opts.WorkspaceRoot, opts.SandboxScope), lspReferencesTool(opts.WorkspaceRoot, opts.SandboxScope), lspRestartTool(opts.WorkspaceRoot))
	}
	if opts.Sourcegraph.Endpoint != "" {
		tools = append(tools, sourcegraphTool(opts.HTTPClient, opts.Sourcegraph))
	}
	if opts.EnableMCP && opts.MCPRegistry != nil {
		tools = append(tools, listMCPResourcesTool(opts.MCPRegistry), readMCPResourceTool(opts.MCPRegistry))
		dynamic, err := dynamicMCPTools(context.Background(), opts.MCPRegistry)
		if err != nil {
			return nil, err
		}
		tools = append(tools, dynamic...)
	}
	if opts.ModelCatalog != nil {
		tools = append(tools, listModelsTool(opts.ModelCatalog))
	}
	if opts.EnableSkills && opts.SkillLister != nil {
		tools = append(tools, skillTool(opts.SkillLister, opts.AgentRunner))
	}
	if opts.EnableSkills && opts.SkillVerifier != nil {
		tools = append(tools, verifySkillTool(opts.SkillVerifier))
	}
	if opts.EnableAgent && opts.AgentRunner != nil {
		tools = append(tools, agentTool(opts.AgentRunner))
		if opts.EnableWebOps && opts.WebFetcher != nil {
			// GAP-2: web_fetch/web_search/agentic_fetch are all backed by
			// WebFetcher, whose Fetch(url) argument is chosen by the LLM.
			// Wrap with the same dial-time SSRF guard used by the
			// fetch/download tools (ssrf_guard.go) rather than trusting
			// whatever transport the supplied WebFetcher implementation uses
			// internally. See web_fetcher_guard.go.
			guardedFetcher := NewGuardedWebFetcher(opts.WebFetcher, opts.NetworkAllowlist)
			tools = append(tools, agenticFetchTool(guardedFetcher, opts.AgentRunner), webSearchTool(guardedFetcher), webFetchTool(guardedFetcher))
		}
	}
	if opts.EnableCron && opts.CronClient != nil {
		tools = append(tools,
			cronCreateTool(opts.CronClient),
			cronListTool(opts.CronClient),
			cronGetTool(opts.CronClient),
			cronDeleteTool(opts.CronClient),
			cronPauseTool(opts.CronClient),
			cronResumeTool(opts.CronClient),
		)
	}

	if opts.EnableCallbacks && opts.CallbackManager != nil {
		tools = append(tools,
			setDelayedCallbackTool(opts.CallbackManager),
			cancelDelayedCallbackTool(opts.CallbackManager),
			listDelayedCallbacksTool(opts.CallbackManager),
		)
	}

	if opts.EnableRecipes {
		recipes, err := recipe.LoadRecipes(opts.RecipesDir)
		if err != nil {
			return nil, err
		}
		if len(recipes) > 0 {
			// Build a HandlerMap from the current tool catalog so recipe steps
			// can dispatch to any already-registered tool.
			handlers := buildHandlerMap(tools)
			tools = append(tools, runRecipeTool(handlers, recipes))
		}
	}

	for i := range tools {
		tools[i].Handler = applyPolicy(tools[i].Definition, opts.ApprovalMode, opts.Policy, tools[i].Handler)
	}

	sort.SliceStable(tools, func(i, j int) bool {
		return tools[i].Definition.Name < tools[j].Definition.Name
	})
	return tools, nil
}

// buildHandlerMap constructs a handler map from a slice of tools.
// It is used to give the recipe executor access to all registered tool handlers.
func buildHandlerMap(tools []Tool) recipe.HandlerMap {
	m := make(recipe.HandlerMap, len(tools))
	for _, t := range tools {
		m[t.Definition.Name] = func(ctx context.Context, args json.RawMessage) (string, error) {
			return t.Handler(ctx, args)
		}
	}
	return m
}
