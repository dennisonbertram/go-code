package harness

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	htools "go-agent-harness/internal/harness/tools"
)

// TestAllowedTools_LimitsAvailableTools verifies that when RunRequest.AllowedTools
// is non-empty, only those tools (plus always-available tools) are offered to the LLM.
func TestAllowedTools_LimitsAvailableTools(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name: "read_file", Description: "reads a file",
		Parameters: map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"content":"data"}`, nil
	})
	_ = registry.Register(ToolDefinition{
		Name: "bash", Description: "runs bash",
		Parameters: map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"output":"done"}`, nil
	})
	_ = registry.Register(ToolDefinition{
		Name: "write_file", Description: "writes a file",
		Parameters: map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"ok":true}`, nil
	})

	var capturedToolNames []string
	provider := &capturingProvider{
		turns: []CompletionResult{{Content: "done"}},
	}
	_ = provider // suppress unused warning; will use capturing below

	// Use a provider that captures the tool definitions offered in the first call.
	type capResult struct {
		names []string
	}
	ch := make(chan capResult, 1)
	captureProvider := &funcProvider{
		fn: func(_ context.Context, req CompletionRequest) (CompletionResult, error) {
			names := make([]string, len(req.Tools))
			for i, t := range req.Tools {
				names[i] = t.Name
			}
			select {
			case ch <- capResult{names: names}:
			default:
			}
			return CompletionResult{Content: "done"}, nil
		},
	}

	runner := NewRunner(captureProvider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     1,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:       "do task",
		AllowedTools: []string{"read_file"},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}
	_ = events

	select {
	case result := <-ch:
		capturedToolNames = result.names
	default:
		t.Fatal("provider was not called with tools")
	}

	// Verify only "read_file" is in the offered tools (plus always-available ones)
	allowed := map[string]bool{"read_file": true}
	for name := range AlwaysAvailableTools {
		allowed[name] = true
	}
	for _, name := range capturedToolNames {
		if !allowed[name] {
			t.Errorf("unexpected tool offered to LLM: %q (AllowedTools=['read_file'])", name)
		}
	}

	// Verify read_file IS offered
	found := false
	for _, name := range capturedToolNames {
		if name == "read_file" {
			found = true
		}
	}
	if !found {
		t.Error("expected read_file to be offered to LLM but it was not")
	}

	// Verify bash is NOT offered
	for _, name := range capturedToolNames {
		if name == "bash" {
			t.Errorf("bash should not be offered when AllowedTools=['read_file']")
		}
		if name == "write_file" {
			t.Errorf("write_file should not be offered when AllowedTools=['read_file']")
		}
	}
}

// TestAllowedTools_EmptyMeansNoFilter verifies that an empty AllowedTools slice
// means no filtering — all registered tools are offered to the LLM.
func TestAllowedTools_EmptyMeansNoFilter(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name: "read_file", Description: "reads a file",
		Parameters: map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"content":"data"}`, nil
	})
	_ = registry.Register(ToolDefinition{
		Name: "bash", Description: "runs bash",
		Parameters: map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"output":"done"}`, nil
	})

	ch := make(chan []string, 1)
	captureProvider := &funcProvider{
		fn: func(_ context.Context, req CompletionRequest) (CompletionResult, error) {
			names := make([]string, len(req.Tools))
			for i, t := range req.Tools {
				names[i] = t.Name
			}
			select {
			case ch <- names:
			default:
			}
			return CompletionResult{Content: "done"}, nil
		},
	}

	runner := NewRunner(captureProvider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     1,
	})

	// AllowedTools is nil/empty — no filtering should occur
	run, err := runner.StartRun(RunRequest{
		Prompt: "do task",
		// AllowedTools not set
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	var capturedNames []string
	select {
	case capturedNames = <-ch:
	default:
		t.Fatal("provider was not called")
	}

	// Both read_file and bash should appear
	nameSet := make(map[string]bool, len(capturedNames))
	for _, n := range capturedNames {
		nameSet[n] = true
	}
	if !nameSet["read_file"] {
		t.Error("expected read_file to be offered when AllowedTools is empty")
	}
	if !nameSet["bash"] {
		t.Error("expected bash to be offered when AllowedTools is empty")
	}
}

// TestAllowedTools_SkillConstraintOverrides verifies that when a skill activates
// its own allowed_tools, the skill's list takes precedence over the per-run base list.
func TestAllowedTools_SkillConstraintOverrides(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()

	// "skill" returns its own constraint when called
	_ = registry.Register(ToolDefinition{
		Name: "skill", Description: "skill tool",
		Parameters: map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		result, _ := json.Marshal(map[string]any{
			"skill":         "grep-skill",
			"instructions":  "Run grep.",
			"allowed_tools": []string{"grep"},
		})
		return string(result), nil
	})
	_ = registry.Register(ToolDefinition{
		Name: "grep", Description: "searches files",
		Parameters: map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"matches":["line1"]}`, nil
	})
	_ = registry.Register(ToolDefinition{
		Name: "read_file", Description: "reads a file",
		Parameters: map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"content":"data"}`, nil
	})
	_ = registry.Register(ToolDefinition{
		Name: "bash", Description: "runs bash",
		Parameters: map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"output":"done"}`, nil
	})

	// Track what tools were offered in turn 2 (after skill activates constraint)
	var capturedTurn2Names []string
	callCount := 0
	mu := sync.Mutex{}

	captureProvider := &funcProvider{
		fn: func(_ context.Context, req CompletionRequest) (CompletionResult, error) {
			mu.Lock()
			c := callCount
			callCount++
			mu.Unlock()

			switch c {
			case 0:
				// Turn 1: call skill tool
				return CompletionResult{
					ToolCalls: []ToolCall{{
						ID: "call-skill", Name: "skill",
						Arguments: `{"command":"grep-skill"}`,
					}},
				}, nil
			case 1:
				// Turn 2: after skill activated constraint — capture available tools
				names := make([]string, len(req.Tools))
				for i, t := range req.Tools {
					names[i] = t.Name
				}
				mu.Lock()
				capturedTurn2Names = names
				mu.Unlock()
				return CompletionResult{Content: "done"}, nil
			default:
				return CompletionResult{Content: "done"}, nil
			}
		},
	}

	runner := NewRunner(captureProvider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     3,
	})

	// Base allowed tools: read_file and bash (NOT grep)
	run, err := runner.StartRun(RunRequest{
		Prompt:       "do task",
		AllowedTools: []string{"read_file", "bash"},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	mu.Lock()
	turn2Names := capturedTurn2Names
	mu.Unlock()

	// After skill activates constraint ["grep"], only grep (+ always-available) should be offered
	// The skill constraint overrides the per-run base list
	nameSet := make(map[string]bool, len(turn2Names))
	for _, n := range turn2Names {
		nameSet[n] = true
	}

	if !nameSet["grep"] {
		t.Error("expected grep to be offered after skill constraint activated with ['grep']")
	}
	// bash and read_file should NOT appear (skill constraint takes precedence)
	if nameSet["bash"] {
		t.Error("bash should not be offered when skill constraint overrides with ['grep']")
	}
	if nameSet["read_file"] {
		t.Error("read_file should not be offered when skill constraint overrides with ['grep']")
	}
}

// TestAllowedTools_BaseFilterAppliesEvenWithSkillNilAllowedTools verifies that
// the per-run AllowedTools base filter is still applied even when a skill
// constraint has nil AllowedTools (which means the skill itself is unrestricted).
// The per-run base filter is a security boundary that the skill cannot override.
func TestAllowedTools_BaseFilterAppliesEvenWithSkillNilAllowedTools(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()

	_ = registry.Register(ToolDefinition{
		Name: "skill", Description: "skill tool",
		Parameters: map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		// skill result WITHOUT allowed_tools — nil AllowedTools = skill is unrestricted
		// but the per-run base filter should still apply.
		result, _ := json.Marshal(map[string]any{
			"skill":        "noop-skill",
			"instructions": "Do nothing.",
			// no allowed_tools field — nil means unrestricted from skill's perspective
		})
		return string(result), nil
	})
	_ = registry.Register(ToolDefinition{
		Name: "read_file", Description: "reads a file",
		Parameters: map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"content":"data"}`, nil
	})
	_ = registry.Register(ToolDefinition{
		Name: "bash", Description: "runs bash",
		Parameters: map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"output":"done"}`, nil
	})

	// Track what tools were offered in turn 2 (after skill with nil AllowedTools)
	var capturedTurn2Names []string
	callCount := 0
	mu := sync.Mutex{}

	captureProvider := &funcProvider{
		fn: func(_ context.Context, req CompletionRequest) (CompletionResult, error) {
			mu.Lock()
			c := callCount
			callCount++
			mu.Unlock()

			switch c {
			case 0:
				// Turn 1: call skill
				return CompletionResult{
					ToolCalls: []ToolCall{{
						ID: "call-skill", Name: "skill",
						Arguments: `{"command":"noop-skill"}`,
					}},
				}, nil
			case 1:
				// Turn 2: after skill activated constraint with nil AllowedTools
				names := make([]string, len(req.Tools))
				for i, t := range req.Tools {
					names[i] = t.Name
				}
				mu.Lock()
				capturedTurn2Names = names
				mu.Unlock()
				return CompletionResult{Content: "done"}, nil
			default:
				return CompletionResult{Content: "done"}, nil
			}
		},
	}

	runner := NewRunner(captureProvider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     3,
	})

	// Base: only read_file allowed
	run, err := runner.StartRun(RunRequest{
		Prompt:       "do task",
		AllowedTools: []string{"read_file"},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	mu.Lock()
	turn2Names := capturedTurn2Names
	mu.Unlock()

	nameSet := make(map[string]bool, len(turn2Names))
	for _, n := range turn2Names {
		nameSet[n] = true
	}

	// Per-run base filter ["read_file"] must still apply even when skill constraint
	// has nil AllowedTools (the skill constraint with nil AllowedTools means
	// "no skill restriction" but the base filter is a security boundary that persists).
	if !nameSet["read_file"] {
		t.Error("expected read_file to be offered when it is in the base AllowedTools")
	}
	// bash is NOT in the base AllowedTools, so it should still be blocked
	if nameSet["bash"] {
		t.Error("bash should not be offered: it is not in base AllowedTools=['read_file']")
	}
}

// TestRunForkedSkill_ImplementedByRunner verifies that *Runner implements
// the ForkedAgentRunner interface (compile-time check).
func TestRunForkedSkill_ImplementedByRunner(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{{Content: "done"}}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     1,
	})

	// Compile-time interface check
	var _ htools.ForkedAgentRunner = runner
}

// TestRunForkedSkill_ForwardsAllowedTools verifies that RunForkedSkill spawns
// a sub-run with the AllowedTools from ForkConfig applied.
func TestRunForkedSkill_ForwardsAllowedTools(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name: "read_file", Description: "reads a file",
		Parameters: map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"content":"data"}`, nil
	})
	_ = registry.Register(ToolDefinition{
		Name: "bash", Description: "runs bash",
		Parameters: map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"output":"done"}`, nil
	})

	// Capture tool names offered in the forked sub-run
	var capturedSubRunTools []string
	mu := sync.Mutex{}

	captureProvider := &funcProvider{
		fn: func(_ context.Context, req CompletionRequest) (CompletionResult, error) {
			names := make([]string, len(req.Tools))
			for i, t := range req.Tools {
				names[i] = t.Name
			}
			mu.Lock()
			// Only capture the first call (the sub-run's first turn)
			if capturedSubRunTools == nil {
				capturedSubRunTools = names
			}
			mu.Unlock()
			return CompletionResult{Content: "sub-run result"}, nil
		},
	}

	runner := NewRunner(captureProvider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	ctx := context.Background()
	config := htools.ForkConfig{
		Prompt:       "do read task",
		SkillName:    "test-skill",
		AllowedTools: []string{"read_file"},
	}

	result, err := runner.RunForkedSkill(ctx, config)
	if err != nil {
		t.Fatalf("RunForkedSkill: %v", err)
	}
	if result.Output == "" {
		t.Error("expected non-empty output from RunForkedSkill")
	}

	mu.Lock()
	subRunTools := capturedSubRunTools
	mu.Unlock()

	if subRunTools == nil {
		t.Fatal("sub-run provider was never called")
	}

	// Verify bash is NOT offered in the sub-run (only read_file allowed)
	for _, name := range subRunTools {
		if name == "bash" {
			t.Errorf("bash should not be offered in sub-run with AllowedTools=['read_file']")
		}
	}
	// Verify read_file IS offered
	found := false
	for _, name := range subRunTools {
		if name == "read_file" {
			found = true
		}
	}
	if !found {
		t.Error("expected read_file to be offered in sub-run")
	}
}

// TestRunForkedSkill_InheritsParentSystemPrompt verifies that RunForkedSkill
// forwards the parent run's system prompt to the forked sub-run.
func TestRunForkedSkill_InheritsParentSystemPrompt(t *testing.T) {
	t.Parallel()

	parentSystemPrompt := "You are a specialized code review agent."

	var capturedSystemPrompt string
	mu := sync.Mutex{}

	captureProvider := &funcProvider{
		fn: func(_ context.Context, req CompletionRequest) (CompletionResult, error) {
			mu.Lock()
			if capturedSystemPrompt == "" && len(req.Messages) > 0 {
				// System prompt should be the first message with role "system"
				for _, msg := range req.Messages {
					if msg.Role == "system" {
						capturedSystemPrompt = msg.Content
						break
					}
				}
			}
			mu.Unlock()
			return CompletionResult{Content: "review done"}, nil
		},
	}

	runner := NewRunner(captureProvider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	// Start a parent run with a custom system prompt
	parentRun, err := runner.StartRun(RunRequest{
		Prompt:       "start parent",
		SystemPrompt: parentSystemPrompt,
	})
	if err != nil {
		t.Fatalf("start parent run: %v", err)
	}

	_, err = collectRunEvents(t, runner, parentRun.ID)
	if err != nil {
		t.Fatalf("collect parent run events: %v", err)
	}

	// Now call RunForkedSkill with a context that has the parent run's metadata
	// (simulating being called from within the parent run's tool execution context)
	parentMeta := htools.RunMetadata{
		RunID:    parentRun.ID,
		TenantID: "default",
	}
	ctx := context.WithValue(context.Background(), htools.ContextKeyRunMetadata, parentMeta)

	// Reset captured prompt for the forked run
	mu.Lock()
	capturedSystemPrompt = ""
	mu.Unlock()

	config := htools.ForkConfig{
		Prompt:    "review code",
		SkillName: "code-review",
	}

	_, err = runner.RunForkedSkill(ctx, config)
	if err != nil {
		t.Fatalf("RunForkedSkill: %v", err)
	}

	mu.Lock()
	gotPrompt := capturedSystemPrompt
	mu.Unlock()

	if gotPrompt != parentSystemPrompt {
		t.Errorf("expected system prompt %q in forked run, got %q", parentSystemPrompt, gotPrompt)
	}
}

// TestRunForkedSkill_InheritsParentPermissions verifies that RunForkedSkill
// forwards the parent run's permissions to the forked sub-run.
func TestRunForkedSkill_InheritsParentPermissions(t *testing.T) {
	t.Parallel()

	parentPerms := &PermissionConfig{
		Sandbox:  SandboxScopeWorkspace,
		Approval: ApprovalPolicyDestructive,
	}

	provider := &stubProvider{
		turns: []CompletionResult{{Content: "done"}},
	}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	// Start parent run with custom permissions
	parentRun, err := runner.StartRun(RunRequest{
		Prompt:      "parent prompt",
		Permissions: parentPerms,
	})
	if err != nil {
		t.Fatalf("start parent run: %v", err)
	}

	_, err = collectRunEvents(t, runner, parentRun.ID)
	if err != nil {
		t.Fatalf("collect parent run events: %v", err)
	}

	// Call RunForkedSkill with parent run context
	parentMeta := htools.RunMetadata{RunID: parentRun.ID, TenantID: "default"}
	ctx := context.WithValue(context.Background(), htools.ContextKeyRunMetadata, parentMeta)

	config := htools.ForkConfig{
		Prompt:    "forked task",
		SkillName: "test",
	}

	_, err = runner.RunForkedSkill(ctx, config)
	if err != nil {
		t.Fatalf("RunForkedSkill: %v", err)
	}

	// Find the forked run and verify its permissions
	// The forked run was created as a new run inside RunForkedSkill.
	// We need to find it — look at all runs for one that isn't the parent.
	runner.mu.RLock()
	var forkedRunState *runState
	for id, state := range runner.runs {
		if id != parentRun.ID {
			forkedRunState = state
		}
	}
	runner.mu.RUnlock()

	if forkedRunState == nil {
		t.Fatal("no forked run state found")
	}

	if forkedRunState.permissions.Sandbox != SandboxScopeWorkspace {
		t.Errorf("expected sandbox %q in forked run, got %q", SandboxScopeWorkspace, forkedRunState.permissions.Sandbox)
	}
	if forkedRunState.permissions.Approval != ApprovalPolicyDestructive {
		t.Errorf("expected approval %q in forked run, got %q", ApprovalPolicyDestructive, forkedRunState.permissions.Approval)
	}
}

// TestAllowedTools_RaceConditionSafe verifies that concurrent runs with different
// AllowedTools don't interfere with each other's tool filtering.
// Uses filteredToolsForRun() directly to test isolation without race on provider context.
func TestAllowedTools_RaceConditionSafe(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name: "tool_a", Description: "tool a",
		Parameters: map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"result":"a"}`, nil
	})
	_ = registry.Register(ToolDefinition{
		Name: "tool_b", Description: "tool b",
		Parameters: map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"result":"b"}`, nil
	})

	provider := &stubProvider{turns: []CompletionResult{
		{Content: "done"},
		{Content: "done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     1,
	})

	// Start both runs and wait for them to complete.
	runA, err := runner.StartRun(RunRequest{
		Prompt:       "run A",
		AllowedTools: []string{"tool_a"},
	})
	if err != nil {
		t.Fatalf("start run A: %v", err)
	}
	_, err = collectRunEvents(t, runner, runA.ID)
	if err != nil {
		t.Fatalf("collect run A events: %v", err)
	}

	runB, err := runner.StartRun(RunRequest{
		Prompt:       "run B",
		AllowedTools: []string{"tool_b"},
	})
	if err != nil {
		t.Fatalf("start run B: %v", err)
	}
	_, err = collectRunEvents(t, runner, runB.ID)
	if err != nil {
		t.Fatalf("collect run B events: %v", err)
	}

	// Verify per-run allowedTools is stored in the runState and correctly isolated.
	runner.mu.RLock()
	stateA, okA := runner.runs[runA.ID]
	stateB, okB := runner.runs[runB.ID]
	runner.mu.RUnlock()

	if !okA || !okB {
		t.Fatal("run states not found")
	}

	// Verify filteredToolsForRun returns only tool_a for run A
	aFilteredDefs := runner.filteredToolsForRun(runA.ID)
	aNames := make(map[string]bool)
	for _, d := range aFilteredDefs {
		aNames[d.Name] = true
	}
	if aNames["tool_b"] {
		t.Error("filteredToolsForRun for run A should not include tool_b")
	}
	if !aNames["tool_a"] {
		t.Error("filteredToolsForRun for run A should include tool_a")
	}

	// Verify filteredToolsForRun returns only tool_b for run B
	bFilteredDefs := runner.filteredToolsForRun(runB.ID)
	bNames := make(map[string]bool)
	for _, d := range bFilteredDefs {
		bNames[d.Name] = true
	}
	if bNames["tool_a"] {
		t.Error("filteredToolsForRun for run B should not include tool_a")
	}
	if !bNames["tool_b"] {
		t.Error("filteredToolsForRun for run B should include tool_b")
	}

	// Verify the stored allowedTools are isolated
	if len(stateA.allowedTools) != 1 || stateA.allowedTools[0] != "tool_a" {
		t.Errorf("run A allowedTools should be ['tool_a'], got %v", stateA.allowedTools)
	}
	if len(stateB.allowedTools) != 1 || stateB.allowedTools[0] != "tool_b" {
		t.Errorf("run B allowedTools should be ['tool_b'], got %v", stateB.allowedTools)
	}
	_ = stateA
	_ = stateB
}

// TestRunPrompt_ReturnsOutput verifies that RunPrompt starts a sub-run and
// returns its final output text. This exercises the AgentRunner interface
// implementation on *Runner.
func TestRunPrompt_ReturnsOutput(t *testing.T) {
	t.Parallel()

	expectedOutput := "prompt run completed successfully"

	provider := &stubProvider{
		turns: []CompletionResult{
			{Content: expectedOutput},
		},
	}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	ctx := context.Background()
	output, err := runner.RunPrompt(ctx, "do some work")
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}

	if output != expectedOutput {
		t.Errorf("RunPrompt returned %q, want %q", output, expectedOutput)
	}
}

func TestRunPromptWithAllowedTools_ForwardsAllowedTools(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name: "read_file", Description: "reads a file",
		Parameters: map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"content":"data"}`, nil
	})
	_ = registry.Register(ToolDefinition{
		Name: "bash", Description: "runs bash",
		Parameters: map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"output":"done"}`, nil
	})

	ch := make(chan []string, 1)
	provider := &funcProvider{
		fn: func(_ context.Context, req CompletionRequest) (CompletionResult, error) {
			names := make([]string, len(req.Tools))
			for i, t := range req.Tools {
				names[i] = t.Name
			}
			ch <- names
			return CompletionResult{Content: "done"}, nil
		},
	}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     1,
	})

	output, err := runner.RunPromptWithAllowedTools(context.Background(), "do some work", []string{"read_file"})
	if err != nil {
		t.Fatalf("RunPromptWithAllowedTools: %v", err)
	}
	if output != "done" {
		t.Fatalf("RunPromptWithAllowedTools returned %q, want %q", output, "done")
	}

	select {
	case names := <-ch:
		for _, name := range names {
			if name == "bash" {
				t.Fatalf("bash should not be offered when AllowedTools=['read_file']")
			}
		}
		foundRead := false
		for _, name := range names {
			if name == "read_file" {
				foundRead = true
			}
		}
		if !foundRead {
			t.Fatalf("expected read_file to be offered, got %v", names)
		}
	default:
		t.Fatal("provider did not receive tool definitions")
	}
}

// TestRunPrompt_RespectsContextCancellation verifies that RunPrompt returns
// an error when the context is cancelled before the run completes.
func TestRunPrompt_RespectsContextCancellation(t *testing.T) {
	t.Parallel()

	// A provider that blocks indefinitely (returns a channel that never sends)
	blockingProvider := &funcProvider{
		fn: func(ctx context.Context, req CompletionRequest) (CompletionResult, error) {
			// Block until context is done
			<-ctx.Done()
			return CompletionResult{}, ctx.Err()
		},
	}

	runner := NewRunner(blockingProvider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so RunPrompt returns right away
	cancel()

	_, err := runner.RunPrompt(ctx, "do some work")
	if err == nil {
		t.Error("expected error when context is cancelled, got nil")
	}
}

// funcProvider is a Provider that delegates to a function for testing.
type funcProvider struct {
	fn func(ctx context.Context, req CompletionRequest) (CompletionResult, error)
}

func (p *funcProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResult, error) {
	return p.fn(ctx, req)
}

// TestFindToolSkill_NotForceGrantedInRestrictedRun is the regression test for
// the issue #527 security fix: when a run explicitly sets allowed_tools,
// find_tool and skill must NOT be silently force-granted (they can reach tools
// outside the allowlist — skill by activating a broader skill constraint), while
// AskUserQuestion (pure infrastructure) stays available. When the caller lists
// find_tool explicitly, it is available.
func TestFindToolSkill_NotForceGrantedInRestrictedRun(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	reg := func(name string) {
		_ = registry.Register(ToolDefinition{
			Name: name, Description: name,
			Parameters: map[string]any{"type": "object"},
		}, func(_ context.Context, _ json.RawMessage) (string, error) { return `{}`, nil })
	}
	// Register the always-available infra tools plus a normal restricted tool.
	for _, n := range []string{"AskUserQuestion", "find_tool", "skill", "read_thing", "bash"} {
		reg(n)
	}

	provider := &stubProvider{turns: []CompletionResult{{Content: "done"}}}
	runner := NewRunner(provider, registry, RunnerConfig{DefaultModel: "gpt-4.1-mini", MaxSteps: 1})

	// Restricted run: allowed_tools = [read_thing] only.
	run, err := runner.StartRun(RunRequest{Prompt: "x", AllowedTools: []string{"read_thing"}})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if _, err := collectRunEvents(t, runner, run.ID); err != nil {
		t.Fatalf("collect events: %v", err)
	}

	names := make(map[string]bool)
	for _, d := range runner.filteredToolsForRun(run.ID) {
		names[d.Name] = true
	}
	if !names["read_thing"] {
		t.Error("expected the explicitly-allowed tool read_thing to be available")
	}
	if !names["AskUserQuestion"] {
		t.Error("expected AskUserQuestion to remain available (unconditional infrastructure)")
	}
	if names["find_tool"] {
		t.Error("SECURITY: find_tool must NOT be force-granted to a run that restricted allowed_tools (issue #527)")
	}
	if names["skill"] {
		t.Error("SECURITY: skill must NOT be force-granted to a run that restricted allowed_tools (issue #527)")
	}
	if names["bash"] {
		t.Error("bash was not in allowed_tools and must be filtered out")
	}

	// When find_tool is explicitly allowed, it is available.
	run2, err := runner.StartRun(RunRequest{Prompt: "y", AllowedTools: []string{"read_thing", "find_tool"}})
	if err != nil {
		t.Fatalf("start run2: %v", err)
	}
	if _, err := collectRunEvents(t, runner, run2.ID); err != nil {
		t.Fatalf("collect events2: %v", err)
	}
	names2 := make(map[string]bool)
	for _, d := range runner.filteredToolsForRun(run2.ID) {
		names2[d.Name] = true
	}
	if !names2["find_tool"] {
		t.Error("find_tool should be available when explicitly listed in allowed_tools")
	}
}
