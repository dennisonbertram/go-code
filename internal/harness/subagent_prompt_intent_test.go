package harness

import (
	"testing"

	htools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/systemprompt"
)

// TestStartRun_DefaultsSubagentIntentForParentedRuns verifies that a run with
// a recorded parent (ParentContextHandoff.ParentRunID set) is resolved with
// AgentIntent "subagent" when the caller didn't specify one. This targets the
// failure mode observed in live testing: subagents on the unrestricted "full"
// profile (empty SystemPrompt, no AgentIntent) improvised hallucinated
// alternatives (fake HTTP calls, writing source files) instead of calling an
// available tool directly.
func TestStartRun_DefaultsSubagentIntentForParentedRuns(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{{Content: "child done"}}}
	engine := &promptEngineStub{resolved: systemprompt.ResolvedPrompt{StaticPrompt: "STATIC"}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     1,
		PromptEngine: engine,
	})

	_, err := runner.StartRun(RunRequest{
		Prompt:               "child task",
		ParentContextHandoff: &htools.ParentContextHandoff{ParentRunID: "run_parent_123"},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	if len(engine.resolveReqs) != 1 {
		t.Fatalf("expected exactly one Resolve call, got %d", len(engine.resolveReqs))
	}
	if got := engine.resolveReqs[0].AgentIntent; got != "subagent" {
		t.Fatalf("expected AgentIntent %q, got %q", "subagent", got)
	}
}

// TestStartRun_DoesNotDefaultSubagentIntentForTopLevelRuns verifies a
// top-level run (no ParentContextHandoff) is unaffected — it keeps whatever
// AgentIntent it already had (empty, in this case).
func TestStartRun_DoesNotDefaultSubagentIntentForTopLevelRuns(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{{Content: "done"}}}
	engine := &promptEngineStub{resolved: systemprompt.ResolvedPrompt{StaticPrompt: "STATIC"}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     1,
		PromptEngine: engine,
	})

	_, err := runner.StartRun(RunRequest{Prompt: "top level task"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	if len(engine.resolveReqs) != 1 {
		t.Fatalf("expected exactly one Resolve call, got %d", len(engine.resolveReqs))
	}
	if got := engine.resolveReqs[0].AgentIntent; got != "" {
		t.Fatalf("expected empty AgentIntent for a top-level run, got %q", got)
	}
}

// TestStartRun_DoesNotOverrideExplicitAgentIntentForSubagents verifies that a
// caller-supplied AgentIntent on a parented run is respected, not overwritten.
func TestStartRun_DoesNotOverrideExplicitAgentIntentForSubagents(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{{Content: "child done"}}}
	engine := &promptEngineStub{resolved: systemprompt.ResolvedPrompt{StaticPrompt: "STATIC"}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     1,
		PromptEngine: engine,
	})

	_, err := runner.StartRun(RunRequest{
		Prompt:               "child task",
		AgentIntent:          "code_review",
		ParentContextHandoff: &htools.ParentContextHandoff{ParentRunID: "run_parent_123"},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	if got := engine.resolveReqs[0].AgentIntent; got != "code_review" {
		t.Fatalf("expected explicit AgentIntent %q to be preserved, got %q", "code_review", got)
	}
}

// TestStartRun_DoesNotSetSubagentIntentWhenSystemPromptOverrideIsSet verifies
// that a parented run with an explicit SystemPrompt override never even calls
// the prompt engine (SystemPrompt always wins), so no AgentIntent defaulting
// is needed or applied.
func TestStartRun_DoesNotSetSubagentIntentWhenSystemPromptOverrideIsSet(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{{Content: "child done"}}}
	engine := &promptEngineStub{resolved: systemprompt.ResolvedPrompt{StaticPrompt: "SHOULD_NOT_USE"}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     1,
		PromptEngine: engine,
	})

	_, err := runner.StartRun(RunRequest{
		Prompt:               "child task",
		SystemPrompt:         "PROFILE_SYSTEM_PROMPT",
		ParentContextHandoff: &htools.ParentContextHandoff{ParentRunID: "run_parent_123"},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	if engine.resolveCalls != 0 {
		t.Fatalf("expected prompt engine not called when SystemPrompt override is set, got %d calls", engine.resolveCalls)
	}
}
