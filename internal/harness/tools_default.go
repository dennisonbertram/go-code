package harness

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	htools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/core"
	"go-agent-harness/internal/harness/tools/deferred"
	"go-agent-harness/internal/harness/tools/recipe"
	"go-agent-harness/internal/harness/tools/script"
	om "go-agent-harness/internal/observationalmemory"
	"go-agent-harness/internal/provider/catalog"
	"go-agent-harness/internal/skills/packs"
	"go-agent-harness/internal/workingmemory"
)

type DefaultRegistryOptions struct {
	ApprovalMode        ToolApprovalMode
	Policy              ToolPolicy
	SandboxScope        SandboxScope // controls filesystem/network restrictions
	AskUserBroker       htools.AskUserQuestionBroker
	AskUserTimeout      time.Duration
	MemoryManager       om.Manager
	WorkingMemoryStore  workingmemory.Store
	AgentRunner         htools.AgentRunner
	SkillLister         htools.SkillLister
	SkillVerifier       htools.SkillVerifier
	SkillsDir           string // directory where create_skill writes new SKILL.md files
	ModelCatalog        *catalog.Catalog
	CronClient          htools.CronClient
	CallbackManager     *htools.CallbackManager
	Activations         *ActivationTracker // activation tracker for deferred tools
	Sourcegraph         htools.SourcegraphConfig
	MCPConnector        deferred.MCPConnector      // optional: enables the connect_mcp tool
	MCPRegistry         htools.MCPRegistry         // optional: global MCP registry for dynamic MCP tools
	RecipesDir          string                     // directory to load *.yaml recipe files from
	PromptExtensionDirs htools.PromptExtensionDirs // directories for create_prompt_extension tool
	PackRegistry        *packs.PackRegistry        // optional skill pack registry
	ScriptToolsDir      string                     // optional: directory containing user script tools
	ConversationStore   ConversationStore          // optional: enables list_conversations and search_conversations
	MessageSummarizer   htools.MessageSummarizer   // optional: enables summarize/hybrid modes in compact_history
	// SubagentManager enables the run_agent tool for profile-based subagent delegation.
	SubagentManager htools.SubagentManager
	// ProfilesDir is the directory to search for user-global profiles (.toml files).
	// Defaults to ~/.harness/profiles/ if empty.
	ProfilesDir string
	// ProfileRunStore is the optional store for profile run history.
	// When non-nil, enables the get_efficiency_report tool.
	ProfileRunStore deferred.ProfileRunStoreIface
}

// conversationStoreAdapter adapts ConversationStore (harness package) to htools.ConversationReader.
type conversationStoreAdapter struct {
	store ConversationStore
}

func (a *conversationStoreAdapter) ListConversations(ctx context.Context, limit, offset int) ([]htools.ConversationSummary, error) {
	convs, err := a.store.ListConversations(ctx, ConversationFilter{}, limit, offset)
	if err != nil {
		return nil, err
	}
	result := make([]htools.ConversationSummary, 0, len(convs))
	for _, c := range convs {
		result = append(result, htools.ConversationSummary{
			ID:        c.ID,
			Title:     c.Title,
			CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt: c.UpdatedAt.UTC().Format(time.RFC3339),
			MsgCount:  c.MsgCount,
		})
	}
	return result, nil
}

func (a *conversationStoreAdapter) SearchConversations(ctx context.Context, query string, limit int) ([]htools.ConversationSearchResult, error) {
	// Thread the run's tenant into the search so an agent cannot full-text-search
	// conversations owned by other tenants via this LLM-exposed tool.
	// RunMetadata is injected into the tool context by the step engine before any
	// tool handler is invoked (runner_step_engine.go, ContextKeyRunMetadata).
	//
	// Tenant rules:
	//   • Non-empty TenantID (including "default") → scope search to that tenant.
	//   • No RunMetadata in context (auth-disabled local callers) → empty tenantID
	//     disables the filter, preserving the pre-fix behaviour for single-process
	//     deployments that have no tenant isolation requirement.
	tenantID := ""
	if meta, ok := htools.RunMetadataFromContext(ctx); ok {
		tenantID = meta.TenantID
	}
	msgs, err := a.store.SearchMessages(ctx, tenantID, query, limit)
	if err != nil {
		return nil, err
	}
	result := make([]htools.ConversationSearchResult, 0, len(msgs))
	for _, m := range msgs {
		result = append(result, htools.ConversationSearchResult{
			ConversationID: m.ConversationID,
			Role:           m.Role,
			Snippet:        m.Snippet,
		})
	}
	return result, nil
}

func NewDefaultRegistry(workspaceRoot string) *Registry {
	return NewDefaultRegistryWithOptions(workspaceRoot, DefaultRegistryOptions{
		ApprovalMode: ToolApprovalModeFullAuto,
	})
}

func NewDefaultRegistryWithPolicy(workspaceRoot string, mode ToolApprovalMode, policy ToolPolicy) *Registry {
	return NewDefaultRegistryWithOptions(workspaceRoot, DefaultRegistryOptions{
		ApprovalMode: mode,
		Policy:       policy,
	})
}

func NewDefaultRegistryWithOptions(workspaceRoot string, opts DefaultRegistryOptions) *Registry {
	approvalMode := htools.ApprovalMode(opts.ApprovalMode)
	if approvalMode == "" {
		approvalMode = htools.ApprovalModeFullAuto
	}
	askTimeout := opts.AskUserTimeout
	if askTimeout <= 0 {
		askTimeout = 5 * time.Minute
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}

	// Build shared resources
	jobManager := htools.NewJobManager(workspaceRoot, time.Now)
	if opts.SandboxScope != "" {
		jobManager.SetSandboxScope(htools.SandboxScope(opts.SandboxScope))
	}
	policyAdapter := toolPolicyAdapter{policy: opts.Policy}

	var convReader htools.ConversationReader
	if opts.ConversationStore != nil {
		convReader = &conversationStoreAdapter{store: opts.ConversationStore}
	}

	buildOpts := htools.BuildOptions{
		WorkspaceRoot:      workspaceRoot,
		ApprovalMode:       approvalMode,
		Policy:             policyAdapter,
		SandboxScope:       htools.SandboxScope(opts.SandboxScope),
		HTTPClient:         httpClient,
		Now:                time.Now,
		AskUserBroker:      opts.AskUserBroker,
		AskUserTimeout:     askTimeout,
		MemoryManager:      opts.MemoryManager,
		WorkingMemoryStore: opts.WorkingMemoryStore,
		AgentRunner:        opts.AgentRunner,
		SkillLister:        opts.SkillLister,
		SkillVerifier:      opts.SkillVerifier,
		CronClient:         opts.CronClient,
		EnableTodos:        true,
		// Code-intel/LSP tools (lsp_diagnostics, lsp_references, lsp_restart) are NOT
		// included in the default registry. They require a running language server and
		// are not supported in the default configuration. Implementations exist in
		// internal/harness/tools/deferred/lsp.go and can be wired by custom registry
		// builders that call deferred.LspDiagnosticsTool etc. directly.
		EnableMCP:           true,
		EnableAgent:         true,
		EnableWebOps:        true,
		ModelCatalog:        opts.ModelCatalog,
		EnableSkills:        opts.SkillLister != nil,
		EnableCron:          opts.CronClient != nil,
		CallbackManager:     opts.CallbackManager,
		EnableCallbacks:     opts.CallbackManager != nil,
		Sourcegraph:         opts.Sourcegraph,
		PromptExtensionDirs: opts.PromptExtensionDirs,
		ConversationStore:   convReader,
		EnableConversations: convReader != nil,
		MessageSummarizer:   opts.MessageSummarizer,
		MCPRegistry:         opts.MCPRegistry,
	}

	activations := opts.Activations
	if activations == nil {
		activations = NewActivationTracker()
	}

	// -- Build core tools --
	coreTools := []htools.Tool{
		core.ReadTool(buildOpts),
		core.WriteTool(buildOpts),
		core.EditTool(buildOpts),
		core.BashTool(jobManager),
		core.JobOutputTool(jobManager),
		core.JobKillTool(jobManager),
		core.ApplyPatchTool(buildOpts),
		core.AskUserQuestionTool(opts.AskUserBroker, askTimeout),
		core.WorkingMemoryTool(opts.WorkingMemoryStore),
		core.ObservationalMemoryTool(buildOpts),
		core.FileInspectTool(buildOpts),
		core.ContextStatusTool(),
		core.CompactHistoryTool(buildOpts.MessageSummarizer),
		deferred.DownloadTool(buildOpts),
	}

	// Skill tool: promoted to core with dynamic description containing available skills.
	// Only added when skills are enabled and at least one skill is registered.
	if buildOpts.EnableSkills && opts.SkillLister != nil {
		if skills := opts.SkillLister.ListSkills(); len(skills) > 0 {
			coreTools = append(coreTools, core.SkillTool(opts.SkillLister, opts.AgentRunner))
		}
	}

	// Conversation history tools: enabled when a ConversationStore is provided.
	if buildOpts.EnableConversations && convReader != nil {
		coreTools = append(coreTools,
			core.ListConversationsTool(convReader),
			core.SearchConversationsTool(convReader),
		)
	}

	// -- Build deferred tools --
	var deferredTools []htools.Tool

	// create_prompt_extension is always registered; the handler itself returns an error
	// if the prompt extension directories are not configured.
	deferredTools = append(deferredTools, deferred.CreatePromptExtensionTool(buildOpts.PromptExtensionDirs))

	if buildOpts.EnableTodos {
		coreTools = append(coreTools, deferred.TodosTool())
	}
	// LSP tools (lsp_diagnostics, lsp_references, lsp_restart) are intentionally
	// absent from the default registry. See BuildOptions comment above.
	if buildOpts.Sourcegraph.Endpoint != "" {
		deferredTools = append(deferredTools, deferred.SourcegraphTool(buildOpts))
	}
	if buildOpts.EnableMCP && buildOpts.MCPRegistry != nil {
		deferredTools = append(deferredTools,
			deferred.ListMCPResourcesTool(buildOpts.MCPRegistry),
			deferred.ReadMCPResourceTool(buildOpts.MCPRegistry),
		)
		dynamic, err := deferred.DynamicMCPTools(context.Background(), buildOpts.MCPRegistry)
		if err != nil {
			// Non-fatal: log and continue without dynamic MCP tools.
			// Individual server failures are common (server not yet started, etc.)
			log.Printf("warning: failed to discover dynamic MCP tools: %v", err)
		} else {
			deferredTools = append(deferredTools, dynamic...)
		}
	}
	if buildOpts.ModelCatalog != nil {
		deferredTools = append(deferredTools, deferred.ListModelsTool(buildOpts.ModelCatalog))
	}
	if buildOpts.EnableAgent && opts.AgentRunner != nil {
		deferredTools = append(deferredTools, deferred.AgentTool(opts.AgentRunner))
		// Recursive agent spawning tools (issue #235).
		// spawn_agent is visible at all depths; task_complete is depth-gated at
		// call time (returns error at depth 0).
		deferredTools = append(deferredTools,
			deferred.SpawnAgentTool(opts.AgentRunner, buildOpts.ProfilesDir),
			deferred.TaskCompleteTool(opts.AgentRunner),
		)
		if buildOpts.EnableWebOps && buildOpts.WebFetcher != nil {
			deferredTools = append(deferredTools,
				deferred.AgenticFetchTool(buildOpts.WebFetcher, opts.AgentRunner),
				deferred.WebSearchTool(buildOpts.WebFetcher),
				deferred.WebFetchTool(buildOpts.WebFetcher),
			)
		}
	}
	if buildOpts.EnableCron && opts.CronClient != nil {
		deferredTools = append(deferredTools,
			deferred.CronCreateTool(opts.CronClient),
			deferred.CronListTool(opts.CronClient),
			deferred.CronGetTool(opts.CronClient),
			deferred.CronDeleteTool(opts.CronClient),
			deferred.CronPauseTool(opts.CronClient),
			deferred.CronResumeTool(opts.CronClient),
		)
	}
	if buildOpts.EnableCallbacks && opts.CallbackManager != nil {
		deferredTools = append(deferredTools,
			deferred.SetDelayedCallbackTool(opts.CallbackManager),
			deferred.CancelDelayedCallbackTool(opts.CallbackManager),
			deferred.ListDelayedCallbacksTool(opts.CallbackManager),
		)
	}
	if buildOpts.EnableSkills && opts.SkillVerifier != nil {
		deferredTools = append(deferredTools, deferred.VerifySkillTool(opts.SkillVerifier))
	}
	if opts.PackRegistry != nil {
		deferredTools = append(deferredTools, deferred.ManageSkillPacksTool(opts.PackRegistry))
	}

	// -- Load and register recipes as a deferred tool --
	if opts.RecipesDir != "" {
		recipes, err := recipe.LoadRecipes(opts.RecipesDir)
		if err != nil {
			// Log but don't panic — a bad recipe file is not fatal.
			// The tool simply won't be registered.
			_ = err
		} else if len(recipes) > 0 {
			// Build a handler map from all core and deferred tools registered so far.
			handlers := make(recipe.HandlerMap)
			for _, t := range coreTools {
				t := t
				handlers[t.Definition.Name] = t.Handler
			}
			for _, t := range deferredTools {
				t := t
				handlers[t.Definition.Name] = t.Handler
			}
			recipeTool := deferred.RunRecipeTool(handlers, recipes)
			deferredTools = append(deferredTools, recipeTool)
		}
	}

	// -- Load script tools from configured directory --
	if opts.ScriptToolsDir != "" {
		scriptTools, err := script.LoadScriptTools(opts.ScriptToolsDir)
		if err != nil {
			log.Printf("warning: failed to load script tools from %s: %v (continuing without script tools)", opts.ScriptToolsDir, err)
		} else if len(scriptTools) > 0 {
			log.Printf("loaded %d script tool(s) from %s", len(scriptTools), opts.ScriptToolsDir)
			deferredTools = append(deferredTools, scriptTools...)
		}
	}

	// create_skill tool: available whenever a skills directory is configured.
	if opts.SkillsDir != "" {
		deferredTools = append(deferredTools, deferred.CreateSkillTool(opts.SkillsDir))
	}

	// subagent lifecycle and run_agent tools: available when a SubagentManager is configured.
	if opts.SubagentManager != nil {
		deferredTools = append(deferredTools,
			deferred.StartSubagentTool(opts.SubagentManager, opts.ProfilesDir),
			deferred.GetSubagentTool(opts.SubagentManager),
			deferred.WaitSubagentTool(opts.SubagentManager),
			deferred.CancelSubagentTool(opts.SubagentManager),
		)
		deferredTools = append(deferredTools, deferred.RunAgentTool(opts.SubagentManager, opts.ProfilesDir))
	}

	var registry *Registry

	// list_profiles and get_profile tools: always registered (built-in profiles always exist).
	deferredTools = append(deferredTools,
		deferred.ListProfilesTool(opts.ProfilesDir),
		deferred.GetProfileTool(opts.ProfilesDir),
		deferred.GetProfileManifestTool(func(profileName string) (map[string]any, error) {
			manifest, err := BuildProfileToolManifestWithRegistry("", opts.ProfilesDir, profileName, registry)
			if err != nil {
				return nil, err
			}
			raw, err := json.Marshal(manifest)
			if err != nil {
				return nil, err
			}
			var payload map[string]any
			if err := json.Unmarshal(raw, &payload); err != nil {
				return nil, err
			}
			return payload, nil
		}),
	)

	// Profile management tools: available when a ProfilesDir is configured.
	if opts.ProfilesDir != "" {
		deferredTools = append(deferredTools,
			deferred.CreateProfileTool(opts.ProfilesDir),
			deferred.UpdateProfileTool(opts.ProfilesDir),
			deferred.DeleteProfileTool(opts.ProfilesDir),
		)
	}
	// validate_profile is a read-only dry-run tool; always available.
	deferredTools = append(deferredTools, deferred.ValidateProfileTool(opts.ProfilesDir))

	// recommend_profile: always registered — deterministic, no external dependencies.
	deferredTools = append(deferredTools, deferred.RecommendProfileTool())

	// get_efficiency_report: always registered; returns no-history report when store is nil.
	deferredTools = append(deferredTools, deferred.GetEfficiencyReportTool(opts.ProfileRunStore))

	// Deep git history tools: always registered since git is already required by the
	// existing git_status and git_diff core tools.
	deferredTools = append(deferredTools,
		deferred.GitLogSearchTool(buildOpts),
		deferred.GitFileHistoryTool(buildOpts),
		deferred.GitBlameContextTool(buildOpts),
		deferred.GitDiffRangeTool(buildOpts),
		deferred.GitContributorContextTool(buildOpts),
	)

	// -- Apply policy wrapping to all tools --
	for i := range coreTools {
		coreTools[i].Handler = htools.ApplyPolicy(coreTools[i].Definition, approvalMode, policyAdapter, coreTools[i].Handler)
	}
	for i := range deferredTools {
		deferredTools[i].Handler = htools.ApplyPolicy(deferredTools[i].Definition, approvalMode, policyAdapter, deferredTools[i].Handler)
	}

	// -- Register all tools in the registry --
	registry = NewRegistry()

	for _, t := range coreTools {
		def := ToolDefinition{
			Name:         t.Definition.Name,
			Description:  t.Definition.Description,
			Parameters:   t.Definition.Parameters,
			ParallelSafe: t.Definition.ParallelSafe,
			Mutating:     t.Definition.Mutating,
		}
		handler := ToolHandler(func(ctx context.Context, args json.RawMessage) (string, error) {
			return t.Handler(ctx, args)
		})
		if err := registry.Register(def, handler); err != nil {
			panic(err)
		}
	}

	for _, t := range deferredTools {
		def := ToolDefinition{
			Name:         t.Definition.Name,
			Description:  t.Definition.Description,
			Parameters:   t.Definition.Parameters,
			ParallelSafe: t.Definition.ParallelSafe,
			Mutating:     t.Definition.Mutating,
		}
		handler := ToolHandler(func(ctx context.Context, args json.RawMessage) (string, error) {
			return t.Handler(ctx, args)
		})
		if err := registry.RegisterWithOptions(def, handler, RegisterOptions{
			Tier: htools.TierDeferred,
			Tags: t.Definition.Tags,
		}); err != nil {
			panic(err)
		}
	}

	// -- Register connect_mcp tool (requires the registry itself as the registrar) --
	// This must be done after the registry is created since the tool captures a reference to it.
	if opts.MCPConnector != nil {
		connectTool := deferred.ConnectMCPTool(registry, opts.MCPConnector)
		connectTool.Handler = htools.ApplyPolicy(connectTool.Definition, approvalMode, policyAdapter, connectTool.Handler)
		connectDef := ToolDefinition{
			Name:        connectTool.Definition.Name,
			Description: connectTool.Definition.Description,
			Parameters:  connectTool.Definition.Parameters,
		}
		connectHandler := ToolHandler(func(ctx context.Context, args json.RawMessage) (string, error) {
			return connectTool.Handler(ctx, args)
		})
		if err := registry.RegisterWithOptions(connectDef, connectHandler, RegisterOptions{
			Tier: htools.TierDeferred,
			Tags: connectTool.Definition.Tags,
		}); err != nil {
			panic(err)
		}
		deferredTools = append(deferredTools, connectTool)
	}

	// -- Create find_tool meta-tool if there are deferred tools --
	var findTool htools.Tool
	if len(deferredTools) > 0 {
		var deferredDefs []htools.Definition
		for _, t := range deferredTools {
			deferredDefs = append(deferredDefs, t.Definition)
		}
		findTool = htools.FindToolTool(
			&htools.KeywordSearcher{MaxResults: 10},
			deferredDefs,
			activations,
		)
		findTool.Handler = htools.ApplyPolicy(findTool.Definition, approvalMode, policyAdapter, findTool.Handler)
		findDef := ToolDefinition{
			Name:        findTool.Definition.Name,
			Description: findTool.Definition.Description,
			Parameters:  findTool.Definition.Parameters,
		}
		findHandler := ToolHandler(func(ctx context.Context, args json.RawMessage) (string, error) {
			return findTool.Handler(ctx, args)
		})
		if err := registry.Register(findDef, findHandler); err != nil {
			panic(err)
		}
	}

	return registry
}

type toolPolicyAdapter struct {
	policy ToolPolicy
}

func (a toolPolicyAdapter) Allow(ctx context.Context, in htools.PolicyInput) (htools.PolicyDecision, error) {
	if a.policy == nil {
		return htools.PolicyDecision{}, nil
	}
	decision, err := a.policy.Allow(ctx, ToolPolicyInput{
		ToolName:  in.ToolName,
		Action:    string(in.Action),
		Path:      in.Path,
		Arguments: in.Arguments,
		Mutating:  in.Mutating,
	})
	if err != nil {
		return htools.PolicyDecision{}, err
	}
	return htools.PolicyDecision{Allow: decision.Allow, Reason: decision.Reason}, nil
}

// Compatibility helpers kept in harness package for existing tests.
func validateWorkspaceRelativePattern(pattern string) error {
	return htools.ValidateWorkspaceRelativePattern(pattern)
}

func buildLineMatcher(query string, useRegex bool, caseSensitive bool) (func(string) bool, error) {
	return htools.BuildLineMatcher(query, useRegex, caseSensitive)
}

func runCommand(ctx context.Context, timeout time.Duration, command string, args ...string) (string, int, bool, error) {
	return htools.RunCommand(ctx, timeout, command, args...)
}

func isDangerousCommand(command string) bool {
	return htools.IsDangerousCommand(command)
}
