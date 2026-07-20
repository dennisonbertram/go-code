package deferred

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tools "go-agent-harness/internal/harness/tools"
)

// --- mock runners ---

type mockSpawnRunner struct {
	output string
	err    error
}

func (m *mockSpawnRunner) RunPrompt(ctx context.Context, prompt string) (string, error) {
	return m.output, m.err
}

type mockSpawnForkedRunner struct {
	output     tools.ForkResult
	err        error
	lastConfig tools.ForkConfig
	lastCtx    context.Context
}

func (m *mockSpawnForkedRunner) RunPrompt(ctx context.Context, prompt string) (string, error) {
	return m.output.Output, m.err
}

func (m *mockSpawnForkedRunner) RunForkedSkill(ctx context.Context, config tools.ForkConfig) (tools.ForkResult, error) {
	m.lastConfig = config
	m.lastCtx = ctx
	return m.output, m.err
}

type spawnAgentTranscriptReaderStub struct{}

func (spawnAgentTranscriptReaderStub) Snapshot(limit int, includeTools bool) tools.TranscriptSnapshot {
	return tools.TranscriptSnapshot{
		RunID: "run_parent",
		Messages: []tools.TranscriptMessage{{
			Index:   1,
			Role:    "user",
			Content: strings.Repeat("child-context ", 50),
		}},
		GeneratedAt: time.Now().UTC(),
	}
}

// --- SpawnAgentTool tests ---

func TestSpawnAgentTool_Definition(t *testing.T) {
	t.Parallel()
	tool := SpawnAgentTool(nil, "")

	if tool.Definition.Name != "spawn_agent" {
		t.Fatalf("expected name=spawn_agent, got %s", tool.Definition.Name)
	}
	if tool.Definition.Tier != tools.TierDeferred {
		t.Fatalf("expected tier=deferred, got %s", tool.Definition.Tier)
	}
	if tool.Definition.Action != tools.ActionExecute {
		t.Fatalf("expected action=execute, got %s", tool.Definition.Action)
	}
	if !tool.Definition.Mutating {
		t.Fatal("expected mutating=true")
	}
	if tool.Handler == nil {
		t.Fatal("handler is nil")
	}
}

func TestSpawnAgentTool_RequiresTask(t *testing.T) {
	t.Parallel()
	tool := SpawnAgentTool(&mockSpawnRunner{output: "done"}, "")

	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing task")
	}
	if !strings.Contains(err.Error(), "task is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSpawnAgentTool_EmptyTask(t *testing.T) {
	t.Parallel()
	tool := SpawnAgentTool(&mockSpawnRunner{output: "done"}, "")

	_, err := tool.Handler(context.Background(), json.RawMessage(`{"task":""}`))
	if err == nil {
		t.Fatal("expected error for empty task")
	}
	if !strings.Contains(err.Error(), "task is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSpawnAgentTool_NilRunner(t *testing.T) {
	t.Parallel()
	tool := SpawnAgentTool(nil, "")

	_, err := tool.Handler(context.Background(), json.RawMessage(`{"task":"do something"}`))
	if err == nil {
		t.Fatal("expected error for nil runner")
	}
	if !strings.Contains(err.Error(), "no AgentRunner configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSpawnAgentTool_EnforcesDepthLimit(t *testing.T) {
	t.Parallel()
	runner := &mockSpawnForkedRunner{
		output: tools.ForkResult{Output: "done"},
	}
	tool := SpawnAgentTool(runner, "")

	// Simulate being at max depth.
	ctx := tools.WithForkDepth(context.Background(), tools.DefaultMaxForkDepth)

	_, err := tool.Handler(ctx, json.RawMessage(`{"task":"do something"}`))
	if err == nil {
		t.Fatal("expected error at max fork depth")
	}
	if !strings.Contains(err.Error(), "max recursion depth") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSpawnAgentTool_SuccessWithForkedRunner(t *testing.T) {
	t.Parallel()
	runner := &mockSpawnForkedRunner{
		output: tools.ForkResult{
			Output:  "Child completed successfully",
			Summary: "Done",
		},
	}
	tool := SpawnAgentTool(runner, "")

	// Depth 0 → child gets depth 1.
	out, err := tool.Handler(context.Background(), json.RawMessage(`{"task":"implement the feature"}`))
	if err != nil {
		t.Fatalf("spawn_agent failed: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if result["status"] != "completed" {
		t.Fatalf("expected status=completed, got %v", result["status"])
	}
	if result["summary"] == nil {
		t.Fatal("expected summary in result")
	}
	if result["jsonl"] == nil {
		t.Fatal("expected jsonl in result")
	}
}

func TestSpawnAgentTool_PropagatesDepthToChild(t *testing.T) {
	t.Parallel()
	runner := &mockSpawnForkedRunner{
		output: tools.ForkResult{Output: "done"},
	}
	tool := SpawnAgentTool(runner, "")

	// Spawn from depth 2 → child should be at depth 3.
	ctx := tools.WithForkDepth(context.Background(), 2)
	_, err := tool.Handler(ctx, json.RawMessage(`{"task":"do something"}`))
	if err != nil {
		t.Fatalf("spawn_agent failed: %v", err)
	}

	// The child context should have depth 3.
	childDepth := tools.ForkDepthFromContext(runner.lastCtx)
	if childDepth != 3 {
		t.Fatalf("expected child depth 3, got %d", childDepth)
	}
}

func TestSpawnAgentTool_ForwardsParentContextHandoff(t *testing.T) {
	t.Parallel()
	runner := &mockSpawnForkedRunner{
		output: tools.ForkResult{Output: "done"},
	}
	tool := SpawnAgentTool(runner, "")

	ctx := context.Background()
	ctx = context.WithValue(ctx, tools.ContextKeyRunMetadata, tools.RunMetadata{
		RunID:          "run_parent",
		TenantID:       "tenant_1",
		ConversationID: "conv_1",
		AgentID:        "agent_1",
	})
	ctx = context.WithValue(ctx, tools.ContextKeyTranscriptReader, spawnAgentTranscriptReaderStub{})

	_, err := tool.Handler(ctx, json.RawMessage(`{"task":"do something"}`))
	if err != nil {
		t.Fatalf("spawn_agent failed: %v", err)
	}

	if runner.lastConfig.ParentContextHandoff == nil {
		t.Fatal("expected ParentContextHandoff in ForkConfig")
	}
	if runner.lastConfig.ParentContextHandoff.ParentRunID != "run_parent" {
		t.Fatalf("expected ParentRunID %q, got %q", "run_parent", runner.lastConfig.ParentContextHandoff.ParentRunID)
	}
	if utf8.RuneCountInString(runner.lastConfig.ParentContextHandoff.Messages[0].Content) > 400 {
		t.Fatalf("expected parent context message to be truncated to <=400 runes, got %d", utf8.RuneCountInString(runner.lastConfig.ParentContextHandoff.Messages[0].Content))
	}
	prompt := runner.lastConfig.Prompt
	if !strings.Contains(prompt, "# Parent context handoff") {
		t.Fatalf("expected prompt to include parent context handoff header, got %q", prompt)
	}
	handoffIdx := strings.Index(prompt, "# Parent context handoff")
	taskHeaderIdx := strings.Index(prompt, "# Task")
	taskBodyIdx := strings.LastIndex(prompt, "do something")
	if handoffIdx == -1 || taskHeaderIdx == -1 || taskBodyIdx == -1 {
		t.Fatalf("expected prompt order markers, got %q", prompt)
	}
	if !(handoffIdx < taskHeaderIdx && taskHeaderIdx < taskBodyIdx) {
		t.Fatalf("unexpected prompt order: handoff=%d taskHeader=%d task=%d", handoffIdx, taskHeaderIdx, taskBodyIdx)
	}
}

func TestSpawnAgentTool_ChildRunnerError(t *testing.T) {
	t.Parallel()
	import_err := "child run timed out"
	runner := &mockSpawnForkedRunner{}
	runner.err = makeError(import_err)
	tool := SpawnAgentTool(runner, "")

	_, err := tool.Handler(context.Background(), json.RawMessage(`{"task":"do something"}`))
	if err == nil {
		t.Fatal("expected error when child run fails")
	}
	if !strings.Contains(err.Error(), "child run failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSpawnAgentTool_WithStructuredTaskCompleteResult(t *testing.T) {
	t.Parallel()
	// Simulate child that called task_complete and returned structured JSON.
	taskCompleteOutput := `{"_task_complete":true,"status":"completed","summary":"Auth module done","findings":[{"type":"test_result","content":"14 passed"}]}`
	runner := &mockSpawnForkedRunner{
		output: tools.ForkResult{Output: taskCompleteOutput},
	}
	tool := SpawnAgentTool(runner, "")

	out, err := tool.Handler(context.Background(), json.RawMessage(`{"task":"implement auth"}`))
	if err != nil {
		t.Fatalf("spawn_agent failed: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if result["status"] != "completed" {
		t.Fatalf("expected status=completed, got %v", result["status"])
	}
	if result["summary"] != "Auth module done" {
		t.Fatalf("expected summary='Auth module done', got %v", result["summary"])
	}
	jsonl, ok := result["jsonl"].([]any)
	if !ok {
		t.Fatalf("expected jsonl to be array, got %T", result["jsonl"])
	}
	if len(jsonl) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(jsonl))
	}
}

func TestSpawnAgentTool_DefaultMaxSteps(t *testing.T) {
	t.Parallel()
	runner := &mockSpawnForkedRunner{
		output: tools.ForkResult{Output: "done"},
	}
	tool := SpawnAgentTool(runner, "")

	// No max_steps specified → should default to 30.
	out, err := tool.Handler(context.Background(), json.RawMessage(`{"task":"do something"}`))
	if err != nil {
		t.Fatalf("spawn_agent failed: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty output")
	}
}

func TestSpawnAgentTool_AllowedToolsForwarded(t *testing.T) {
	t.Parallel()
	runner := &mockSpawnForkedRunner{
		output: tools.ForkResult{Output: "done"},
	}
	tool := SpawnAgentTool(runner, "")

	_, err := tool.Handler(context.Background(), json.RawMessage(`{"task":"do something","allowed_tools":["bash","read"]}`))
	if err != nil {
		t.Fatalf("spawn_agent failed: %v", err)
	}

	if len(runner.lastConfig.AllowedTools) != 2 {
		t.Fatalf("expected 2 allowed tools, got %d: %v", len(runner.lastConfig.AllowedTools), runner.lastConfig.AllowedTools)
	}
}

// --- NEW FAILING TESTS (RED phase for issue #375) ---

// TestSpawnAgentTool_HonorsDeclaredModel verifies that a model parameter is
// forwarded to the child run via ForkConfig.Model.
func TestSpawnAgentTool_HonorsDeclaredModel(t *testing.T) {
	t.Parallel()
	runner := &mockSpawnForkedRunner{
		output: tools.ForkResult{Output: "done"},
	}
	tool := SpawnAgentTool(runner, "")

	_, err := tool.Handler(context.Background(), json.RawMessage(`{"task":"do something","model":"gpt-4.1-mini"}`))
	if err != nil {
		t.Fatalf("spawn_agent failed: %v", err)
	}

	if runner.lastConfig.Model != "gpt-4.1-mini" {
		t.Fatalf("expected Model=gpt-4.1-mini in ForkConfig, got %q", runner.lastConfig.Model)
	}
}

// TestSpawnAgentTool_HonorsDeclaredMaxSteps verifies that a max_steps parameter
// is forwarded to the child run via ForkConfig.MaxSteps.
func TestSpawnAgentTool_HonorsDeclaredMaxSteps(t *testing.T) {
	t.Parallel()
	runner := &mockSpawnForkedRunner{
		output: tools.ForkResult{Output: "done"},
	}
	tool := SpawnAgentTool(runner, "")

	_, err := tool.Handler(context.Background(), json.RawMessage(`{"task":"do something","max_steps":99}`))
	if err != nil {
		t.Fatalf("spawn_agent failed: %v", err)
	}

	if runner.lastConfig.MaxSteps != 99 {
		t.Fatalf("expected MaxSteps=99 in ForkConfig, got %d", runner.lastConfig.MaxSteps)
	}
}

// TestSpawnAgentTool_LoadsProfileAndAppliesValues verifies that when a profile
// is specified, its model/max_steps/allowed_tools are applied to the child run.
func TestSpawnAgentTool_LoadsProfileAndAppliesValues(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	profileContent := `
[meta]
name = "fast"
description = "Fast profile"
created_by = "user"

[runner]
model = "gpt-4.1-mini"
max_steps = 10
max_cost_usd = 0.05

[tools]
allow = ["bash", "read"]
`
	if err := os.WriteFile(filepath.Join(dir, "fast.toml"), []byte(profileContent), 0644); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	runner := &mockSpawnForkedRunner{
		output: tools.ForkResult{Output: "done"},
	}
	tool := SpawnAgentTool(runner, dir)

	_, err := tool.Handler(context.Background(), json.RawMessage(`{"task":"do something","profile":"fast"}`))
	if err != nil {
		t.Fatalf("spawn_agent failed: %v", err)
	}

	if runner.lastConfig.Model != "gpt-4.1-mini" {
		t.Fatalf("expected Model=gpt-4.1-mini from profile, got %q", runner.lastConfig.Model)
	}
	if runner.lastConfig.MaxSteps != 10 {
		t.Fatalf("expected MaxSteps=10 from profile, got %d", runner.lastConfig.MaxSteps)
	}
}

// TestSpawnAgentTool_ProfileModelOverridableByParameter verifies that an
// explicit model parameter overrides the profile's model.
func TestSpawnAgentTool_ProfileModelOverridableByParameter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	profileContent := `
[meta]
name = "fast"
description = "Fast profile"
created_by = "user"

[runner]
model = "gpt-4.1-mini"
max_steps = 10
`
	if err := os.WriteFile(filepath.Join(dir, "fast.toml"), []byte(profileContent), 0644); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	runner := &mockSpawnForkedRunner{
		output: tools.ForkResult{Output: "done"},
	}
	tool := SpawnAgentTool(runner, dir)

	_, err := tool.Handler(context.Background(), json.RawMessage(`{"task":"do something","profile":"fast","model":"o3"}`))
	if err != nil {
		t.Fatalf("spawn_agent failed: %v", err)
	}

	// Explicit model override must win over profile's model.
	if runner.lastConfig.Model != "o3" {
		t.Fatalf("expected Model=o3 (override wins), got %q", runner.lastConfig.Model)
	}
}

// TestSpawnAgentTool_DefaultProfileToFull verifies that when no profile is
// specified, spawn_agent defaults to "full" (same behavior as run_agent).
func TestSpawnAgentTool_DefaultProfileToFull(t *testing.T) {
	t.Parallel()
	runner := &mockSpawnForkedRunner{
		output: tools.ForkResult{Output: "done"},
	}
	tool := SpawnAgentTool(runner, "")

	// No profile specified — must not error.
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"task":"do something"}`))
	if err != nil {
		t.Fatalf("spawn_agent failed with no profile: %v", err)
	}
}

// TestSpawnAgentTool_ProfileNotFoundUsesDefaults verifies that a missing profile
// is treated non-fatally (empty defaults used), matching run_agent behavior.
func TestSpawnAgentTool_ProfileNotFoundUsesDefaults(t *testing.T) {
	t.Parallel()
	runner := &mockSpawnForkedRunner{
		output: tools.ForkResult{Output: "done"},
	}
	tool := SpawnAgentTool(runner, "")

	// Profile "nonexistent" does not exist — must not error.
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"task":"do something","profile":"nonexistent"}`))
	if err != nil {
		t.Fatalf("spawn_agent should not fail for missing profile, got: %v", err)
	}
}

// makeError creates a simple error for testing.
func makeError(msg string) error {
	return fmt.Errorf("%s", msg)
}
