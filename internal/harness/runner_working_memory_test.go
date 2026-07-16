package harness

import (
	"context"
	"strings"
	"testing"

	om "go-agent-harness/internal/observationalmemory"
	"go-agent-harness/internal/workingmemory"
)

// TestScopeKeyNormalization_EmptyRun verifies that scopeKey normalises empty
// TenantID / ConversationID / AgentID on a run to the same defaults that
// workingMemoryScopeFromContext uses in the working_memory WRITE tool.
// Before the fix, the runner READ side used raw empty strings while the tool
// WRITE side normalised to "default" / runID / "default", so a tool-written
// entry was never recalled during injection.
func TestScopeKeyNormalization_EmptyRun(t *testing.T) {
	t.Parallel()

	provider := &capturingProvider{
		turns: []CompletionResult{{Content: "done"}},
	}
	registry := NewRegistry()
	memStore := workingmemory.NewMemoryStore()

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel:       "test-model",
		MaxSteps:           1,
		WorkingMemoryStore: memStore,
	})

	// Start a run with NO explicit ConversationID / TenantID / AgentID so all
	// three fields are empty on state.run; scopeKey must normalise them.
	run, err := runner.StartRun(RunRequest{Prompt: "hi"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	// scopeKey must resolve to the normalised defaults.
	sk := runner.scopeKey(run.ID)
	if sk.TenantID != "default" {
		t.Errorf("TenantID = %q, want \"default\"", sk.TenantID)
	}
	if sk.ConversationID == "" {
		t.Errorf("ConversationID is empty; want the run ID %q", run.ID)
	}
	if sk.AgentID != "default" {
		t.Errorf("AgentID = %q, want \"default\"", sk.AgentID)
	}

	// An entry written under the normalised scope must be recalled.
	writeScope := om.ScopeKey{TenantID: "default", ConversationID: sk.ConversationID, AgentID: "default"}
	if err := memStore.Set(context.Background(), writeScope, "key1", "value1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	snippet, snippetErr := memStore.Snippet(context.Background(), sk)
	if snippetErr != nil {
		t.Fatalf("Snippet: %v", snippetErr)
	}
	if !strings.Contains(snippet, "key1") {
		t.Errorf("working memory snippet = %q, want it to contain \"key1\"", snippet)
	}
}

func TestRunnerInjectsWorkingMemoryBeforeObservationalMemory(t *testing.T) {
	t.Parallel()

	provider := &capturingProvider{
		turns: []CompletionResult{{Content: "done"}},
	}
	registry := NewRegistry()
	memStore := workingmemory.NewMemoryStore()
	scope := om.ScopeKey{TenantID: "default", ConversationID: "conv-working-memory", AgentID: "default"}
	if err := memStore.Set(context.Background(), scope, "plan", map[string]any{"step": "collect"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel:       "test-model",
		MaxSteps:           1,
		WorkingMemoryStore: memStore,
		MemoryManager: &memoryStub{
			status:  om.Status{Mode: om.ModeLocalCoordinator, Scope: scope},
			snippet: "<observational-memory>\nremember this\n</observational-memory>",
		},
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:         "hello",
		ConversationID: "conv-working-memory",
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	if len(provider.calls) == 0 {
		t.Fatal("expected provider call")
	}
	messages := provider.calls[0].Messages
	if len(messages) < 3 {
		t.Fatalf("message count = %d, want at least 3", len(messages))
	}
	// Volatile blocks are injected at the tail (after history) for cache
	// friendliness, with working memory still ordered before observational memory.
	if !strings.Contains(messages[len(messages)-2].Content, "<working-memory>") {
		t.Fatalf("second-to-last message = %q, want working-memory snippet", messages[len(messages)-2].Content)
	}
	if !strings.Contains(messages[len(messages)-1].Content, "<observational-memory>") {
		t.Fatalf("last message = %q, want observational-memory snippet", messages[len(messages)-1].Content)
	}
}
