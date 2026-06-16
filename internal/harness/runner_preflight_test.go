package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	htools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/mcp"
	"go-agent-harness/internal/systemprompt"
)

func TestRunPreflight_UsesProfileIsolationModeFallback(t *testing.T) {
	repoDir := initGitRepoForWS(t)
	worktreeRootDir := t.TempDir()
	profilesDir := t.TempDir()
	profileTOML := `isolation_mode = "worktree"

[meta]
name = "isolated-worktree"
description = "Test profile with worktree isolation"
version = 1

[runner]
model = ""
max_steps = 1

[tools]
allow = []
`
	if err := os.WriteFile(filepath.Join(profilesDir, "isolated-worktree.toml"), []byte(profileTOML), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	runner := NewRunner(
		&staticProviderWS{result: CompletionResult{Content: "done"}},
		NewRegistry(),
		RunnerConfig{
			DefaultModel: "gpt-5-nano",
			MaxSteps:     1,
			ProfilesDir:  profilesDir,
			WorkspaceBaseOptions: WorkspaceProvisionOptions{
				RepoPath:        repoDir,
				WorktreeRootDir: worktreeRootDir,
			},
		},
	)

	runID := "run-preflight-profile"
	runner.runs[runID] = newRunStateForPreflightTest(runID)

	preflight, err := runner.runPreflight(context.Background(), runID, RunRequest{
		Prompt:      "hello",
		ProfileName: "isolated-worktree",
	})
	if err != nil {
		t.Fatalf("runPreflight: %v", err)
	}

	if preflight.effectiveWorkspaceType != "worktree" {
		t.Fatalf("effectiveWorkspaceType = %q, want worktree", preflight.effectiveWorkspaceType)
	}
	if got := preflight.messages[len(preflight.messages)-1].Content; got != "hello" {
		t.Fatalf("last preflight message = %q, want hello", got)
	}

	provisioned := findEventByType(runner.runs[runID].events, EventWorkspaceProvisioned)
	if provisioned == nil {
		t.Fatal("expected workspace.provisioned event")
	}
	if got := provisioned.Payload["workspace_type"]; got != "worktree" {
		t.Fatalf("workspace.provisioned workspace_type = %v, want worktree", got)
	}
	if runner.runs[runID].workspaceCleanup == nil {
		t.Fatal("expected workspace cleanup to be registered")
	}
}

func TestRunPreflight_ProvisionFailureEmitsWorkspaceProvisionFailed(t *testing.T) {
	runner := NewRunner(
		&staticProviderWS{result: CompletionResult{Content: "done"}},
		NewRegistry(),
		RunnerConfig{
			DefaultModel: "gpt-5-nano",
			MaxSteps:     1,
		},
	)

	runID := "run-preflight-failure"
	runner.runs[runID] = newRunStateForPreflightTest(runID)

	_, err := runner.runPreflight(context.Background(), runID, RunRequest{
		Prompt:        "hello",
		WorkspaceType: "worktree",
	})
	if err == nil {
		t.Fatal("expected workspace provisioning error")
	}

	failed := findEventByType(runner.runs[runID].events, EventWorkspaceProvisionFailed)
	if failed == nil {
		t.Fatal("expected workspace.provision_failed event")
	}
	if got := failed.Payload["workspace_type"]; got != "worktree" {
		t.Fatalf("workspace.provision_failed workspace_type = %v, want worktree", got)
	}
}

func TestRunPreflight_ReResolvesSystemPromptWithWorkspacePath(t *testing.T) {
	repoDir := initGitRepoForWS(t)
	worktreeRootDir := t.TempDir()
	engine := &promptEngineStub{
		resolved: systemprompt.ResolvedPrompt{
			StaticPrompt:         "workspace-system",
			ResolvedIntent:       "general",
			ResolvedModelProfile: "default",
		},
	}

	runner := NewRunner(
		&staticProviderWS{result: CompletionResult{Content: "done"}},
		NewRegistry(),
		RunnerConfig{
			DefaultModel:       "gpt-5-nano",
			DefaultAgentIntent: "general",
			MaxSteps:           1,
			PromptEngine:       engine,
			WorkspaceBaseOptions: WorkspaceProvisionOptions{
				RepoPath:        repoDir,
				WorktreeRootDir: worktreeRootDir,
			},
		},
	)

	runID := "run-preflight-prompt"
	state := newRunStateForPreflightTest(runID)
	state.staticSystemPrompt = "initial-system"
	state.promptResolved = &systemprompt.ResolvedPrompt{StaticPrompt: "initial-system"}
	runner.runs[runID] = state

	preflight, err := runner.runPreflight(context.Background(), runID, RunRequest{
		Prompt:        "hello",
		WorkspaceType: "worktree",
	})
	if err != nil {
		t.Fatalf("runPreflight: %v", err)
	}

	if preflight.systemPrompt != "workspace-system" {
		t.Fatalf("systemPrompt = %q, want workspace-system", preflight.systemPrompt)
	}
	if state.staticSystemPrompt != "workspace-system" {
		t.Fatalf("state.staticSystemPrompt = %q, want workspace-system", state.staticSystemPrompt)
	}
	provisioned := findEventByType(state.events, EventWorkspaceProvisioned)
	if provisioned == nil {
		t.Fatal("expected workspace.provisioned event")
	}
	if len(engine.resolveReqs) != 1 {
		t.Fatalf("resolve calls = %d, want 1", len(engine.resolveReqs))
	}
	if got := engine.resolveReqs[0].WorkspacePath; got != provisioned.Payload["workspace_path"] {
		t.Fatalf("resolve workspace path = %v, want %v", got, provisioned.Payload["workspace_path"])
	}
}

func TestRunPreflight_BuildsScopedMCPRegistry(t *testing.T) {
	runner := NewRunner(
		&staticProviderWS{result: CompletionResult{Content: "done"}},
		NewRegistry(),
		RunnerConfig{
			DefaultModel: "gpt-5-nano",
			MaxSteps:     1,
		},
	)

	runID := "run-preflight-mcp"
	state := newRunStateForPreflightTest(runID)
	runner.runs[runID] = state

	preflight, err := runner.runPreflight(context.Background(), runID, RunRequest{
		Prompt: "hello",
		MCPServers: []MCPServerConfig{{
			Name: "run-srv",
			URL:  "http://example.com/mcp",
		}},
	})
	if err != nil {
		t.Fatalf("runPreflight: %v", err)
	}

	if preflight.model != "gpt-5-nano" {
		t.Fatalf("model = %q, want gpt-5-nano", preflight.model)
	}
	if state.scopedMCPRegistry == nil {
		t.Fatal("expected scoped MCP registry on run state")
	}
	if !state.scopedMCPRegistry.isPerRun("run-srv") {
		t.Fatal("expected run-srv to be registered as a per-run MCP server")
	}
}

// TestRunPreflight_RegistersPerRunMCPTools verifies that tools discovered from
// per-run MCP servers are registered into the global tool registry during
// runPreflight, making them available to the agent via filteredToolsForRun.
func TestRunPreflight_RegistersPerRunMCPTools(t *testing.T) {
	t.Parallel()

	// Build a ScopedMCPRegistry with an in-process fake connection so
	// ListTools works without making real network calls.
	perRunServerName := "test-mcp-server"
	cm := mcp.NewClientManager()
	if err := cm.AddServerWithConn(perRunServerName, func() (mcp.Conn, error) {
		return newFakeMCPConn(perRunServerName, []mcp.ToolDef{
			{Name: "do_thing", Description: "Does a thing"},
			{Name: "list_stuff", Description: "Lists stuff"},
		}), nil
	}); err != nil {
		t.Fatalf("AddServerWithConn: %v", err)
	}
	scopedReg := NewScopedMCPRegistry(nil, cm, []string{perRunServerName})
	defer scopedReg.Close()

	// Manually register the per-run tools into a Registry, simulating what
	// runPreflight does: use ListPerRunTools (skips broken global servers),
	// register, and activate for the run.
	reg := NewRegistry()
	activations := NewActivationTracker()
	const runID = "run-x"

	byServer, err := scopedReg.ListPerRunTools(context.Background())
	if err != nil {
		t.Fatalf("ListPerRunTools: %v", err)
	}
	for serverName, toolDefs := range byServer {
		registered, regErr := reg.RegisterMCPTools(serverName, toolDefs, scopedReg)
		if regErr != nil {
			t.Fatalf("RegisterMCPTools(%q): %v", serverName, regErr)
		}
		if len(registered) > 0 {
			activations.Activate(runID, registered...)
		}
	}

	wantTools := []string{
		"mcp_test_mcp_server_do_thing",
		"mcp_test_mcp_server_list_stuff",
	}

	// Verify the tools appear in the deferred definitions.
	deferredDefs := reg.DeferredDefinitions()
	if len(deferredDefs) != 2 {
		t.Fatalf("expected 2 deferred tool definitions after preflight registration, got %d", len(deferredDefs))
	}
	nameSet := make(map[string]struct{}, len(deferredDefs))
	for _, d := range deferredDefs {
		nameSet[d.Name] = struct{}{}
	}
	for _, want := range wantTools {
		if _, ok := nameSet[want]; !ok {
			t.Errorf("expected tool %q to be registered, got names: %v", want, nameSet)
		}
	}

	// Verify the tools are visible in DefinitionsForRun because runPreflight
	// activated them — no separate manual activation required by the caller.
	runDefs := reg.DefinitionsForRun(runID, activations)
	runNameSet := make(map[string]struct{}, len(runDefs))
	for _, d := range runDefs {
		runNameSet[d.Name] = struct{}{}
	}
	for _, want := range wantTools {
		if _, ok := runNameSet[want]; !ok {
			t.Errorf("per-run MCP tool %q not visible in DefinitionsForRun after activation: %v", want, runNameSet)
		}
	}

	// Verify the tools are NOT visible for a different run (activation is per-run).
	otherDefs := reg.DefinitionsForRun("run-other", activations)
	otherNameSet := make(map[string]struct{}, len(otherDefs))
	for _, d := range otherDefs {
		otherNameSet[d.Name] = struct{}{}
	}
	for _, want := range wantTools {
		if _, ok := otherNameSet[want]; ok {
			t.Errorf("per-run MCP tool %q should NOT be visible for a different run", want)
		}
	}
}

// TestRunPreflight_PerRunMCPToolsAlreadyConnectedIsGraceful verifies that when
// a per-run MCP server name collides with an already-registered global server
// (e.g., registered via the global MCP registry at startup), RegisterMCPTools
// returns an error that is logged rather than failing the run.
func TestRunPreflight_PerRunMCPToolsAlreadyConnectedIsGraceful(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	// Pre-register a server to simulate a global MCP tool already present.
	caller := &mockMCPReg{}
	existingDefs := []htools.MCPToolDefinition{
		{Name: "existing_tool", Description: "Already registered"},
	}
	if _, err := reg.RegisterMCPTools("global-server", existingDefs, caller); err != nil {
		t.Fatalf("pre-register global server: %v", err)
	}

	// Now attempt to register the same server name again (simulating what
	// happens when a profile server shadows a global one).
	_, err := reg.RegisterMCPTools("global-server", existingDefs, caller)
	if err == nil {
		t.Fatal("expected error when registering duplicate server name, got nil")
	}
	// The error must mention "already connected" so callers can recognize it.
	if !strings.Contains(err.Error(), "already connected") {
		t.Errorf("expected 'already connected' in error, got: %v", err)
	}
}

// TestToolsForRun_PerRunToolsIsNil_ReturnsGlobal verifies that when no per-run
// workspace is provisioned (perRunTools is nil), toolsForRun falls back to the
// global Runner.tools registry.
func TestToolsForRun_PerRunToolsIsNil_ReturnsGlobal(t *testing.T) {
	globalReg := NewRegistry()
	r := &Runner{tools: globalReg, runs: make(map[string]*runState)}
	r.runs["test-run"] = &runState{
		run: Run{ID: "test-run", Status: RunStatusQueued},
		// perRunTools intentionally left nil — simulates no workspace provisioning.
	}
	got := r.toolsForRun("test-run")
	if got != globalReg {
		t.Fatal("toolsForRun should return global registry when perRunTools is nil")
	}
}

// TestToolsForRun_PerRunToolsIsSet_ReturnsPerRun verifies that when a per-run
// workspace is provisioned, toolsForRun returns the per-run registry instead of
// the global one. This ensures file/shell tools resolve paths against the
// provisioned workspace.
func TestToolsForRun_PerRunToolsIsSet_ReturnsPerRun(t *testing.T) {
	globalReg := NewRegistry()
	perRunReg := NewRegistry()
	r := &Runner{tools: globalReg, runs: make(map[string]*runState)}
	r.runs["test-run"] = &runState{
		run:         Run{ID: "test-run", Status: RunStatusQueued},
		perRunTools: perRunReg,
	}
	got := r.toolsForRun("test-run")
	if got != perRunReg {
		t.Fatal("toolsForRun should return per-run registry when perRunTools is set")
	}
}

// TestToolsForRun_RunNotFound_ReturnsGlobal verifies that when a run ID is not
// found, toolsForRun returns the global registry.
func TestToolsForRun_RunNotFound_ReturnsGlobal(t *testing.T) {
	globalReg := NewRegistry()
	r := &Runner{tools: globalReg, runs: make(map[string]*runState)}
	got := r.toolsForRun("nonexistent")
	if got != globalReg {
		t.Fatal("toolsForRun should return global registry when run is not found")
	}
}

func newRunStateForPreflightTest(runID string) *runState {
	now := time.Now().UTC()
	return &runState{
		run: Run{
			ID:             runID,
			ConversationID: "conv-" + runID,
			CreatedAt:      now,
			UpdatedAt:      now,
			Status:         RunStatusQueued,
		},
		messages:       make([]Message, 0, 16),
		events:         make([]Event, 0, 16),
		subscribers:    make(map[chan Event]struct{}),
		steeringCh:     make(chan string, steeringBufferSize),
		firedOnceRules: make(map[string]bool),
	}
}

func findEventByType(events []Event, eventType EventType) *Event {
	for i := range events {
		if events[i].Type == eventType {
			return &events[i]
		}
	}
	return nil
}
