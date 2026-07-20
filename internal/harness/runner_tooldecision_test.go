package harness

import (
	"context"
	"encoding/json"
	"testing"
)

// --------------------------------------------------------------------------
// Part 1: Tool Decision Tracing
// --------------------------------------------------------------------------

// TestToolDecisionEventEmittedWhenEnabled verifies that when TraceToolDecisions
// is true and the LLM makes tool calls, a tool.decision event is emitted with
// available_tools and selected_tools populated.
func TestToolDecisionEventEmittedWhenEnabled(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "read_file",
		Description: "reads a file",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "file content", nil
	})
	_ = registry.Register(ToolDefinition{
		Name:        "write_file",
		Description: "writes a file",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "written", nil
	})

	prov := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: "read_file", Arguments: `{}`},
			},
		},
		{Content: "done"},
	}}

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:       "test-model",
		MaxSteps:           5,
		TraceToolDecisions: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	var decisionEvents []Event
	for _, ev := range events {
		if ev.Type == EventToolDecision {
			decisionEvents = append(decisionEvents, ev)
		}
	}

	if len(decisionEvents) == 0 {
		t.Fatal("expected at least one tool.decision event, got none")
	}

	ev := decisionEvents[0]

	// Check available_tools is populated.
	available, ok := ev.Payload["available_tools"]
	if !ok {
		t.Fatal("tool.decision event missing available_tools field")
	}
	availableSlice, ok := available.([]string)
	if !ok {
		t.Fatalf("available_tools should be []string, got %T", available)
	}
	if len(availableSlice) == 0 {
		t.Error("available_tools should not be empty")
	}

	// Check selected_tools contains "read_file".
	selected, ok := ev.Payload["selected_tools"]
	if !ok {
		t.Fatal("tool.decision event missing selected_tools field")
	}
	selectedSlice, ok := selected.([]string)
	if !ok {
		t.Fatalf("selected_tools should be []string, got %T", selected)
	}
	if len(selectedSlice) != 1 || selectedSlice[0] != "read_file" {
		t.Errorf("selected_tools = %v, want [read_file]", selectedSlice)
	}

	// Check call_sequence is present and positive.
	seqVal, ok := ev.Payload["call_sequence"]
	if !ok {
		t.Fatal("tool.decision event missing call_sequence field")
	}
	seq := payloadInt(ev.Payload, "call_sequence")
	if seq <= 0 {
		t.Errorf("call_sequence should be > 0, got %v", seqVal)
	}

	// Check call_sequence_id is present and formatted correctly.
	callSeqID, ok := ev.Payload["call_sequence_id"].(string)
	if !ok || callSeqID == "" {
		t.Errorf("call_sequence_id should be a non-empty string, got %v", ev.Payload["call_sequence_id"])
	}

	// Check step is present.
	if _, ok := ev.Payload["step"]; !ok {
		t.Error("tool.decision event missing step field")
	}
}

// TestToolDecisionEventNotEmittedWhenDisabled verifies that no tool.decision
// event is emitted when TraceToolDecisions is false (the default).
func TestToolDecisionEventNotEmittedWhenDisabled(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "noop_td",
		Description: "does nothing",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})

	prov := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{
				{ID: "c1", Name: "noop_td", Arguments: `{}`},
			},
		},
		{Content: "done"},
	}}

	// TraceToolDecisions NOT set (defaults to false).
	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     5,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	for _, ev := range events {
		if ev.Type == EventToolDecision {
			t.Error("unexpected tool.decision event when TraceToolDecisions=false")
		}
	}
}

// TestToolDecisionCallSequenceIncrementsAcrossSteps verifies that the
// call_sequence counter increments across multiple LLM steps.
func TestToolDecisionCallSequenceIncrementsAcrossSteps(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "step_tool",
		Description: "tool called in multiple steps",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})

	// Two steps, each calling step_tool.
	prov := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{ID: "c1", Name: "step_tool", Arguments: `{"step":1}`}},
		},
		{
			ToolCalls: []ToolCall{{ID: "c2", Name: "step_tool", Arguments: `{"step":2}`}},
		},
		{Content: "done"},
	}}

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:       "test-model",
		MaxSteps:           5,
		TraceToolDecisions: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	var sequences []int
	for _, ev := range events {
		if ev.Type == EventToolDecision {
			sequences = append(sequences, payloadInt(ev.Payload, "call_sequence"))
		}
	}

	if len(sequences) != 2 {
		t.Fatalf("expected 2 tool.decision events, got %d", len(sequences))
	}
	if sequences[0] == sequences[1] {
		t.Errorf("call_sequence did not increment: both are %d", sequences[0])
	}
	if sequences[0] >= sequences[1] {
		t.Errorf("call_sequence should increase: first=%d second=%d", sequences[0], sequences[1])
	}
}

// TestToolDecisionNotEmittedWithoutToolCalls verifies that no tool.decision
// event is emitted for steps that have no tool calls.
func TestToolDecisionNotEmittedWithoutToolCalls(t *testing.T) {
	t.Parallel()

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "direct answer, no tools"},
	}}

	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:       "test-model",
		MaxSteps:           5,
		TraceToolDecisions: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	for _, ev := range events {
		if ev.Type == EventToolDecision {
			t.Error("unexpected tool.decision event when no tool calls were made")
		}
	}
}

// --------------------------------------------------------------------------
// Part 2: Anti-Pattern Detection
// --------------------------------------------------------------------------

// TestAntiPatternRetryLoopDetected verifies that when the same tool is called
// with the same args 3 times, a tool.antipattern event is emitted.
func TestAntiPatternRetryLoopDetected(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "repeating_tool",
		Description: "a tool called repeatedly",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "result", nil
	})

	// The model calls repeating_tool with identical args 3 times then stops.
	sameArgs := `{"query":"same"}`
	prov := &stubProvider{turns: []CompletionResult{
		{ToolCalls: []ToolCall{{ID: "c1", Name: "repeating_tool", Arguments: sameArgs}}},
		{ToolCalls: []ToolCall{{ID: "c2", Name: "repeating_tool", Arguments: sameArgs}}},
		{ToolCalls: []ToolCall{{ID: "c3", Name: "repeating_tool", Arguments: sameArgs}}},
		{Content: "finally done"},
	}}

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:       "test-model",
		MaxSteps:           10,
		DetectAntiPatterns: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	var antiPatternEvents []Event
	for _, ev := range events {
		if ev.Type == EventToolAntiPattern {
			antiPatternEvents = append(antiPatternEvents, ev)
		}
	}

	if len(antiPatternEvents) == 0 {
		t.Fatal("expected a tool.antipattern event, got none")
	}

	ev := antiPatternEvents[0]
	if ev.Payload["type"] != "retry_loop" {
		t.Errorf("antipattern type = %v, want retry_loop", ev.Payload["type"])
	}
	if ev.Payload["tool"] != "repeating_tool" {
		t.Errorf("antipattern tool = %v, want repeating_tool", ev.Payload["tool"])
	}
	callCount := payloadInt(ev.Payload, "call_count")
	if callCount < 3 {
		t.Errorf("call_count = %d, want >= 3", callCount)
	}
}

// TestAntiPatternNotDetectedWithDifferentArgs verifies that different args
// for the same tool do NOT trigger an anti-pattern alert.
func TestAntiPatternNotDetectedWithDifferentArgs(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "varied_tool",
		Description: "tool called with different args each time",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})

	// 3 calls to the same tool but with different args each time.
	prov := &stubProvider{turns: []CompletionResult{
		{ToolCalls: []ToolCall{{ID: "c1", Name: "varied_tool", Arguments: `{"file":"a.txt"}`}}},
		{ToolCalls: []ToolCall{{ID: "c2", Name: "varied_tool", Arguments: `{"file":"b.txt"}`}}},
		{ToolCalls: []ToolCall{{ID: "c3", Name: "varied_tool", Arguments: `{"file":"c.txt"}`}}},
		{Content: "done"},
	}}

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:       "test-model",
		MaxSteps:           10,
		DetectAntiPatterns: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	for _, ev := range events {
		if ev.Type == EventToolAntiPattern {
			t.Errorf("unexpected tool.antipattern event with different args: %v", ev.Payload)
		}
	}
}

// TestAntiPatternNotDetectedWhenDisabled verifies that no tool.antipattern
// events are emitted when DetectAntiPatterns is false.
func TestAntiPatternNotDetectedWhenDisabled(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "ap_disabled_tool",
		Description: "tool for anti-pattern disabled test",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})

	sameArgs := `{"x":1}`
	prov := &stubProvider{turns: []CompletionResult{
		{ToolCalls: []ToolCall{{ID: "c1", Name: "ap_disabled_tool", Arguments: sameArgs}}},
		{ToolCalls: []ToolCall{{ID: "c2", Name: "ap_disabled_tool", Arguments: sameArgs}}},
		{ToolCalls: []ToolCall{{ID: "c3", Name: "ap_disabled_tool", Arguments: sameArgs}}},
		{Content: "done"},
	}}

	// DetectAntiPatterns NOT set (defaults to false).
	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     10,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	for _, ev := range events {
		if ev.Type == EventToolAntiPattern {
			t.Error("unexpected tool.antipattern event when DetectAntiPatterns=false")
		}
	}
}

// TestAntiPatternAlertEmittedOnlyOnce verifies that when a retry loop is
// detected for a (tool, args) pair, the alert is emitted only once (not
// again for subsequent calls of the same pair).
func TestAntiPatternAlertEmittedOnlyOnce(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "once_alert_tool",
		Description: "tool for once-alert test",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})

	// Call the same tool with identical args 5 times.
	sameArgs := `{"k":"v"}`
	prov := &stubProvider{turns: []CompletionResult{
		{ToolCalls: []ToolCall{{ID: "c1", Name: "once_alert_tool", Arguments: sameArgs}}},
		{ToolCalls: []ToolCall{{ID: "c2", Name: "once_alert_tool", Arguments: sameArgs}}},
		{ToolCalls: []ToolCall{{ID: "c3", Name: "once_alert_tool", Arguments: sameArgs}}},
		{ToolCalls: []ToolCall{{ID: "c4", Name: "once_alert_tool", Arguments: sameArgs}}},
		{ToolCalls: []ToolCall{{ID: "c5", Name: "once_alert_tool", Arguments: sameArgs}}},
		{Content: "done"},
	}}

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:       "test-model",
		MaxSteps:           10,
		DetectAntiPatterns: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	var apCount int
	for _, ev := range events {
		if ev.Type == EventToolAntiPattern {
			apCount++
		}
	}

	if apCount != 1 {
		t.Errorf("expected exactly 1 tool.antipattern event, got %d", apCount)
	}
}

// --------------------------------------------------------------------------
// Part 3: Hook Mutation Tracing
// --------------------------------------------------------------------------

// modifyingPreToolHook is a test hook that replaces args with modified JSON.
type modifyingPreToolHook struct {
	name    string
	newArgs json.RawMessage
}

func (h *modifyingPreToolHook) Name() string { return h.name }
func (h *modifyingPreToolHook) PreToolUse(_ context.Context, _ PreToolUseEvent) (*PreToolUseResult, error) {
	return &PreToolUseResult{
		Decision:     ToolHookAllow,
		ModifiedArgs: h.newArgs,
	}, nil
}

// denyingPreToolHook is a test hook that blocks all tool calls.
type denyingPreToolHook struct {
	name string
}

func (h *denyingPreToolHook) Name() string { return h.name }
func (h *denyingPreToolHook) PreToolUse(_ context.Context, _ PreToolUseEvent) (*PreToolUseResult, error) {
	return &PreToolUseResult{
		Decision: ToolHookDeny,
		Reason:   "blocked for test",
	}, nil
}

// allowingPreToolHook is a test hook that passes through without modification.
type allowingPreToolHook struct {
	name string
}

func (h *allowingPreToolHook) Name() string { return h.name }
func (h *allowingPreToolHook) PreToolUse(_ context.Context, _ PreToolUseEvent) (*PreToolUseResult, error) {
	return &PreToolUseResult{Decision: ToolHookAllow}, nil
}

// TestHookMutationEventEmittedOnModify verifies that a tool.hook.mutation
// event is emitted when a hook modifies the tool call arguments.
func TestHookMutationEventEmittedOnModify(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "mutation_tool",
		Description: "tool for mutation test",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})

	originalArgs := `{"path":"/dangerous"}`
	safeArgs := `{"path":"/safe"}`

	prov := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{
				{ID: "call_mut", Name: "mutation_tool", Arguments: originalArgs},
			},
		},
		{Content: "done"},
	}}

	modHook := &modifyingPreToolHook{
		name:    "sanitize_hook",
		newArgs: json.RawMessage(safeArgs),
	}

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:       "test-model",
		MaxSteps:           5,
		PreToolUseHooks:    []PreToolUseHook{modHook},
		TraceHookMutations: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	var mutationEvents []Event
	for _, ev := range events {
		if ev.Type == EventToolHookMutation {
			mutationEvents = append(mutationEvents, ev)
		}
	}

	if len(mutationEvents) == 0 {
		t.Fatal("expected at least one tool.hook.mutation event, got none")
	}

	ev := mutationEvents[0]
	if ev.Payload["action"] != "Modify" {
		t.Errorf("action = %v, want Modify", ev.Payload["action"])
	}
	if ev.Payload["args_before"] != originalArgs {
		t.Errorf("args_before = %v, want %q", ev.Payload["args_before"], originalArgs)
	}
	if ev.Payload["args_after"] != safeArgs {
		t.Errorf("args_after = %v, want %q", ev.Payload["args_after"], safeArgs)
	}
	if ev.Payload["hook"] != "sanitize_hook" {
		t.Errorf("hook = %v, want sanitize_hook", ev.Payload["hook"])
	}
	if ev.Payload["tool_call_id"] != "call_mut" {
		t.Errorf("tool_call_id = %v, want call_mut", ev.Payload["tool_call_id"])
	}
}

// TestHookMutationEventEmittedOnBlock verifies that a tool.hook.mutation event
// with action=Block is emitted when a hook denies a tool call.
func TestHookMutationEventEmittedOnBlock(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "block_tool",
		Description: "tool for block mutation test",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})

	prov := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{
				{ID: "call_block", Name: "block_tool", Arguments: `{"x":1}`},
			},
		},
		{Content: "done"},
	}}

	denyHook := &denyingPreToolHook{name: "deny_hook"}

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:       "test-model",
		MaxSteps:           5,
		PreToolUseHooks:    []PreToolUseHook{denyHook},
		TraceHookMutations: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	var mutationEvents []Event
	for _, ev := range events {
		if ev.Type == EventToolHookMutation {
			mutationEvents = append(mutationEvents, ev)
		}
	}

	if len(mutationEvents) == 0 {
		t.Fatal("expected a tool.hook.mutation event for block, got none")
	}

	ev := mutationEvents[0]
	if ev.Payload["action"] != "Block" {
		t.Errorf("action = %v, want Block", ev.Payload["action"])
	}
}

// TestHookMutationNoEventForAllow verifies that no tool.hook.mutation event
// is emitted when a hook allows the call without modification.
func TestHookMutationNoEventForAllow(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "allow_tool",
		Description: "tool for allow mutation test",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})

	prov := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{
				{ID: "call_allow", Name: "allow_tool", Arguments: `{"x":1}`},
			},
		},
		{Content: "done"},
	}}

	allowHook := &allowingPreToolHook{name: "allow_hook"}

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:       "test-model",
		MaxSteps:           5,
		PreToolUseHooks:    []PreToolUseHook{allowHook},
		TraceHookMutations: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	for _, ev := range events {
		if ev.Type == EventToolHookMutation {
			t.Errorf("unexpected tool.hook.mutation event for plain allow hook: %v", ev.Payload)
		}
	}
}

// TestHookMutationNotEmittedWhenDisabled verifies that no tool.hook.mutation
// events are emitted when TraceHookMutations=false.
func TestHookMutationNotEmittedWhenDisabled(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "trace_disabled_tool",
		Description: "tool for trace disabled test",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})

	prov := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{
				{ID: "c1", Name: "trace_disabled_tool", Arguments: `{"x":1}`},
			},
		},
		{Content: "done"},
	}}

	modHook := &modifyingPreToolHook{
		name:    "mod_hook",
		newArgs: json.RawMessage(`{"x":99}`),
	}

	// TraceHookMutations NOT set (defaults to false).
	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:    "test-model",
		MaxSteps:        5,
		PreToolUseHooks: []PreToolUseHook{modHook},
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	for _, ev := range events {
		if ev.Type == EventToolHookMutation {
			t.Error("unexpected tool.hook.mutation event when TraceHookMutations=false")
		}
	}
}

// TestRunnerConfigForensicsDefaultsToFalse verifies that all three forensics
// flags default to false in a zero-value RunnerConfig.
func TestRunnerConfigForensicsDefaultsToFalse(t *testing.T) {
	t.Parallel()

	cfg := RunnerConfig{}
	if cfg.TraceToolDecisions {
		t.Error("expected TraceToolDecisions to default to false")
	}
	if cfg.DetectAntiPatterns {
		t.Error("expected DetectAntiPatterns to default to false")
	}
	if cfg.TraceHookMutations {
		t.Error("expected TraceHookMutations to default to false")
	}
}

// TestAllEventTypesIncludesForensicsEvents verifies that the new event types
// appear in the AllEventTypes() list.
func TestAllEventTypesIncludesForensicsEvents(t *testing.T) {
	t.Parallel()

	all := AllEventTypes()
	want := map[EventType]bool{
		EventToolDecision:     false,
		EventToolAntiPattern:  false,
		EventToolHookMutation: false,
	}
	for _, et := range all {
		if _, ok := want[et]; ok {
			want[et] = true
		}
	}
	for et, found := range want {
		if !found {
			t.Errorf("AllEventTypes() missing %q", et)
		}
	}
}
