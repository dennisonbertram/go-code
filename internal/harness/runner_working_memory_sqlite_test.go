package harness

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"go-agent-harness/internal/harness/tools/core"
	om "go-agent-harness/internal/observationalmemory"
	"go-agent-harness/internal/workingmemory"
)

// registerWorkingMemoryTool wires the real core.working_memory tool (which keys
// its scope off RunMetadata, exactly like the runner's injection scopeKey) into
// a harness registry backed by the given store.
func registerWorkingMemoryTool(t *testing.T, registry *Registry, store workingmemory.Store) {
	t.Helper()
	tool := core.WorkingMemoryTool(store)
	def := ToolDefinition{
		Name:         tool.Definition.Name,
		Description:  tool.Definition.Description,
		Parameters:   tool.Definition.Parameters,
		ParallelSafe: tool.Definition.ParallelSafe,
		Mutating:     tool.Definition.Mutating,
	}
	handler := ToolHandler(func(ctx context.Context, args json.RawMessage) (string, error) {
		return tool.Handler(ctx, args)
	})
	if err := registry.Register(def, handler); err != nil {
		t.Fatalf("register working_memory tool: %v", err)
	}
}

// newWorkingMemoryWriterProvider returns a provider whose first turn issues a
// working_memory `set` tool call (so the run writes a scoped entry via the real
// tool) and whose second turn returns a final answer.
func newWorkingMemoryWriterProvider(key, value string) *capturingProvider {
	return &capturingProvider{
		turns: []CompletionResult{
			{ToolCalls: []ToolCall{{
				ID:        "wm-set",
				Name:      "working_memory",
				Arguments: `{"action":"set","key":"` + key + `","value":"` + value + `"}`,
			}}},
			{Content: "stored"},
		},
	}
}

// TestRunnerWorkingMemoryCrossRunRecallAndScopeIsolation proves, end-to-end
// through a REAL SQLite working-memory store, that:
//  1. A run under tenant A / conversation C / agent G that writes a
//     working-memory entry (turn 1, via the working_memory tool) persists it.
//  2. A LATER run on the SAME axes recalls the entry — the runner injects it as
//     a system message at turn start (cross-run recall via SQLite).
//  3. A run on a DIFFERENT conversation (same tenant/agent) does NOT see the
//     entry (scope isolation — no leak across conversations).
//  4. A run on a DIFFERENT tenant does NOT see the entry (scope isolation — no
//     leak across tenants).
func TestRunnerWorkingMemoryCrossRunRecallAndScopeIsolation(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "working_memory.db")
	store, err := workingmemory.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	const (
		tenantA = "tenant-A"
		convC   = "conversation-C"
		agentG  = "agent-G"
		wmKey   = "deploy_target"
		wmValue = "staging-cluster-7"
	)

	// --- Turn 1 (write): run writes a working-memory entry under A / C / G. ---
	writeRegistry := NewRegistry()
	registerWorkingMemoryTool(t, writeRegistry, store)
	writer := newWorkingMemoryWriterProvider(wmKey, wmValue)
	writeRunner := NewRunner(writer, writeRegistry, RunnerConfig{
		DefaultModel:       "test-model",
		MaxSteps:           4,
		WorkingMemoryStore: store,
	})
	writeRun, err := writeRunner.StartRun(RunRequest{
		Prompt:         "remember the deploy target",
		TenantID:       tenantA,
		ConversationID: convC,
		AgentID:        agentG,
	})
	if err != nil {
		t.Fatalf("StartRun (write): %v", err)
	}
	if got := waitForStatus(t, writeRunner, writeRun.ID, RunStatusCompleted, RunStatusFailed); got != RunStatusCompleted {
		t.Fatalf("write run status = %s, want completed", got)
	}

	// Sanity: the entry is durably persisted in SQLite under the A/C/G scope.
	if value, ok, err := store.Get(context.Background(), scopeKeyFor(tenantA, convC, agentG), wmKey); err != nil {
		t.Fatalf("store.Get: %v", err)
	} else if !ok || !strings.Contains(value, wmValue) {
		t.Fatalf("persisted value = %q (found=%v), want to contain %q", value, ok, wmValue)
	}

	// --- Turn 2 (recall): a NEW run on the SAME axes recalls the entry. ---
	recallSnippet := firstSystemMessageForRun(t, store, RunRequest{
		Prompt:         "what is the deploy target?",
		TenantID:       tenantA,
		ConversationID: convC,
		AgentID:        agentG,
	})
	if !strings.Contains(recallSnippet, "<working-memory>") {
		t.Fatalf("recall: first system message = %q, want a working-memory snippet", recallSnippet)
	}
	if !strings.Contains(recallSnippet, wmKey) || !strings.Contains(recallSnippet, wmValue) {
		t.Fatalf("recall: working-memory snippet = %q, want it to contain %q=%q", recallSnippet, wmKey, wmValue)
	}

	// --- Isolation: a DIFFERENT conversation does NOT see the entry. ---
	otherConvSnippet := firstSystemMessageForRun(t, store, RunRequest{
		Prompt:         "what is the deploy target?",
		TenantID:       tenantA,
		ConversationID: "conversation-OTHER",
		AgentID:        agentG,
	})
	if strings.Contains(otherConvSnippet, wmKey) || strings.Contains(otherConvSnippet, wmValue) {
		t.Fatalf("cross-conversation leak: snippet = %q must not contain %q/%q", otherConvSnippet, wmKey, wmValue)
	}

	// --- Isolation: a DIFFERENT tenant does NOT see the entry. ---
	otherTenantSnippet := firstSystemMessageForRun(t, store, RunRequest{
		Prompt:         "what is the deploy target?",
		TenantID:       "tenant-B",
		ConversationID: convC,
		AgentID:        agentG,
	})
	if strings.Contains(otherTenantSnippet, wmKey) || strings.Contains(otherTenantSnippet, wmValue) {
		t.Fatalf("cross-tenant leak: snippet = %q must not contain %q/%q", otherTenantSnippet, wmKey, wmValue)
	}
}

// firstSystemMessageForRun starts a single-turn run (no tool calls) on the
// given axes against the shared store and returns the content of the first
// message the runner sent to the provider — i.e. the working-memory injection
// point at turn start. Empty string when no system message was injected.
func firstSystemMessageForRun(t *testing.T, store workingmemory.Store, req RunRequest) string {
	t.Helper()
	provider := &capturingProvider{turns: []CompletionResult{{Content: "done"}}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:       "test-model",
		MaxSteps:           1,
		WorkingMemoryStore: store,
	})
	run, err := runner.StartRun(req)
	if err != nil {
		t.Fatalf("StartRun (probe): %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.calls) == 0 {
		t.Fatal("probe: expected at least one provider call")
	}
	msgs := provider.calls[0].Messages
	if len(msgs) == 0 {
		return ""
	}
	return msgs[0].Content
}

// scopeKeyFor mirrors Runner.scopeKey for direct store assertions in tests.
func scopeKeyFor(tenant, conversation, agent string) om.ScopeKey {
	return om.ScopeKey{TenantID: tenant, ConversationID: conversation, AgentID: agent}
}
