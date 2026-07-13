package harness

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
)

// TestPermissionRuleDenyBlocksDispatchedToolCall proves the rule engine is
// actually enforced at tool-dispatch time: a deny rule must block the call
// before the tool handler runs, emit EventToolCallBlocked, and still let the
// run complete.
func TestPermissionRuleDenyBlocksDispatchedToolCall(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{
		turns: []CompletionResult{
			{ToolCalls: []ToolCall{{
				ID:        "call_denied",
				Name:      "echo_json",
				Arguments: `{"value":"hello"}`,
			}}},
			{Content: "done"},
		},
	}

	var handlerRuns atomic.Int64
	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "echo_json",
		Description: "echoes payload",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"value": map[string]any{"type": "string"}},
		},
		ParallelSafe: true,
	}, func(_ context.Context, args json.RawMessage) (string, error) {
		handlerRuns.Add(1)
		return string(args), nil
	})

	runner := NewRunner(provider, registry, RunnerConfig{
		ApprovalBroker: NewInMemoryApprovalBroker(),
	})

	run, err := runner.StartRun(RunRequest{
		Prompt: "run a denied tool",
		Permissions: &PermissionConfig{
			Sandbox:  SandboxScopeUnrestricted,
			Approval: ApprovalPolicyNone,
			Rules:    NewPermissionRuleSet([]PermissionRule{{Pattern: "echo_json", Effect: PermissionEffectDeny}}),
		},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	if got := handlerRuns.Load(); got != 0 {
		t.Fatalf("denied tool handler ran %d times, want 0 — deny rule did not block dispatch", got)
	}

	var blocked bool
	for _, evt := range events {
		if evt.Type == EventToolCallBlocked {
			blocked = true
		}
	}
	if !blocked {
		t.Fatal("expected a tool.call.blocked event for the denied call, got none")
	}

	r, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("GetRun: not found")
	}
	if r.Status != RunStatusCompleted {
		t.Errorf("run status = %q, want completed (a denied call must not fail the run)", r.Status)
	}
}
