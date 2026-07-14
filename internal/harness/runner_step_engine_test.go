package harness

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"go-agent-harness/internal/systemprompt"
)

// TestRunnerStepLoop_SteeringDrainBeforeTurnRequest characterizes the current
// step-boundary contract: a steering message is drained at the top of the next
// step, before the provider sees the next llm.turn.requested turn.
func TestRunnerStepLoop_SteeringDrainBeforeTurnRequest(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var capturedMessages [][]Message

	blockDuringFirst := make(chan struct{})
	releaseDuringFirst := make(chan struct{})

	provider := &steerGatingProvider{
		turns: []CompletionResult{
			{ToolCalls: []ToolCall{{ID: "tc1", Name: "noop_step_engine", Arguments: `{}`}}},
			{Content: "done"},
		},
		beforeCall: func(idx int) {
			if idx == 0 {
				close(blockDuringFirst)
				<-releaseDuringFirst
			}
		},
		afterCall: func(_ int, req CompletionRequest) {
			mu.Lock()
			capturedMessages = append(capturedMessages, append([]Message(nil), req.Messages...))
			mu.Unlock()
		},
	}

	registry := NewRegistry()
	registerNoopTool(t, registry, "noop_step_engine")

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     4,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	<-blockDuringFirst

	if err := runner.SteerRun(run.ID, "please focus on the main issue"); err != nil {
		t.Fatalf("SteerRun: %v", err)
	}

	close(releaseDuringFirst)

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	stepTwoStart := -1
	stepTwoTurnRequested := -1
	steeringReceived := -1
	stepTwoCompleted := -1
	for i, ev := range events {
		switch ev.Type {
		case EventRunStepStarted:
			if step, _ := ev.Payload["step"].(int); step == 2 && stepTwoStart == -1 {
				stepTwoStart = i
			}
		case EventSteeringReceived:
			if steeringReceived == -1 {
				steeringReceived = i
			}
		case EventLLMTurnRequested:
			if step, _ := ev.Payload["step"].(int); step == 2 && stepTwoTurnRequested == -1 {
				stepTwoTurnRequested = i
			}
		case EventRunStepCompleted:
			if step, _ := ev.Payload["step"].(int); step == 2 && stepTwoCompleted == -1 {
				stepTwoCompleted = i
			}
		}
	}
	if stepTwoStart == -1 || steeringReceived == -1 || stepTwoTurnRequested == -1 || stepTwoCompleted == -1 {
		t.Fatalf("missing step 2 boundary events: stepTwoStart=%d steeringReceived=%d stepTwoTurnRequested=%d stepTwoCompleted=%d events=%v",
			stepTwoStart, steeringReceived, stepTwoTurnRequested, stepTwoCompleted, eventTypes(events))
	}
	if !(stepTwoStart < steeringReceived && steeringReceived < stepTwoTurnRequested && stepTwoTurnRequested < stepTwoCompleted) {
		t.Fatalf("unexpected step 2 boundary ordering: start=%d steering=%d turn=%d completed=%d events=%v",
			stepTwoStart, steeringReceived, stepTwoTurnRequested, stepTwoCompleted, eventTypes(events))
	}

	mu.Lock()
	defer mu.Unlock()
	if len(capturedMessages) < 2 {
		t.Fatalf("expected at least 2 LLM calls, got %d", len(capturedMessages))
	}

	secondCallMsgs := capturedMessages[1]
	found := false
	for _, msg := range secondCallMsgs {
		if msg.Role == "user" && msg.Content == "please focus on the main issue" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("steering message not found in second LLM call messages: %v", secondCallMsgs)
	}
}

// TestRunnerStepEngine_ResetContextBreaksRemainingToolResults verifies that
// when a reset_context result is returned by a non-first tool call in a turn,
// the remaining tool results of that turn are not appended to the reset seed.
func TestRunnerStepEngine_ResetContextBreaksRemainingToolResults(t *testing.T) {
	t.Parallel()

	reg := makeResetContextRegistry()
	for _, name := range []string{"toolA", "toolB"} {
		n := name
		if err := reg.Register(ToolDefinition{
			Name:        n,
			Description: "test tool",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		}, func(_ context.Context, _ json.RawMessage) (string, error) {
			return n + "-output", nil
		}); err != nil {
			t.Fatalf("Register %s: %v", n, err)
		}
	}

	provider := &capturingProvider{
		turns: []CompletionResult{
			{
				ToolCalls: []ToolCall{
					{ID: "c1", Name: "toolA", Arguments: `{}`},
					{ID: "c2", Name: "reset_context", Arguments: `{"persist":{}}`},
					{ID: "c3", Name: "toolB", Arguments: `{}`},
				},
			},
			{Content: "all done"},
		},
	}

	runner := NewRunner(provider, reg, RunnerConfig{
		DefaultModel:        "test",
		DefaultSystemPrompt: "system prompt",
		MaxSteps:            10,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "start"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForRunCompletion(t, runner, run.ID)

	msgs := runner.GetRunMessages(run.ID)
	foundReset := false
	for _, m := range msgs {
		if m.Role == "user" && strings.Contains(m.Content, "Context Reset") {
			foundReset = true
		}
		if m.Role == "tool" {
			t.Errorf("unexpected tool message after reset: name=%s content=%q", m.Name, m.Content)
		}
		if strings.Contains(m.Content, "toolA-output") || strings.Contains(m.Content, "toolB-output") {
			t.Errorf("tool output leaked into messages after reset: %q", m.Content)
		}
	}
	if !foundReset {
		t.Errorf("reset opening message not found in messages: %+v", msgs)
	}
}

// TestRunnerStepEngine_AutoCompactRebuildPreservesDynamicRuleAndRuntimeContext
// verifies that the auto-compact rebuild block re-injects the dynamic-rule
// content and the runtime-context block that were computed for the turn.
func TestRunnerStepEngine_AutoCompactRebuildPreservesDynamicRuleAndRuntimeContext(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(ToolDefinition{
		Name:        "trigger_tool",
		Description: "test tool",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "trigger result", nil
	}); err != nil {
		t.Fatalf("Register trigger_tool: %v", err)
	}

	provider := &capturingProvider{
		turns: []CompletionResult{
			{
				ToolCalls: []ToolCall{{ID: "c1", Name: "trigger_tool", Arguments: `{}`}},
			},
			{Content: "done"},
		},
	}

	engine := &promptEngineStub{resolved: systemprompt.ResolvedPrompt{
		StaticPrompt:   "STATIC_PROMPT",
		ResolvedIntent: "general",
	}}

	// Large prompt forces auto-compaction on step 2.
	largePrompt := strings.Repeat("word ", 200)

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel:         "test",
		DefaultAgentIntent:   "general",
		MaxSteps:             5,
		PromptEngine:         engine,
		AutoCompactEnabled:   true,
		ModelContextWindow:   20,
		AutoCompactThreshold: 0.5,
		AutoCompactKeepLast:  10,
		AutoCompactMode:      "strip",
	})

	run, err := runner.StartRun(RunRequest{
		Prompt: largePrompt,
		DynamicRules: []DynamicRule{
			{
				ID:       "fire-once-rule",
				Trigger:  RuleTrigger{ToolNames: []string{"trigger_tool"}},
				Content:  "INJECTED-RULE-CONTENT",
				FireOnce: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForRunCompletion(t, runner, run.ID)

	if len(provider.calls) < 2 {
		t.Fatalf("expected at least 2 provider calls, got %d", len(provider.calls))
	}
	step2Req := provider.calls[1]

	foundRule := false
	foundRuntime := false
	for _, m := range step2Req.Messages {
		if m.Role == "system" && strings.Contains(m.Content, "INJECTED-RULE-CONTENT") {
			foundRule = true
		}
		if m.Role == "system" && strings.Contains(m.Content, "runtime-step-2") {
			foundRuntime = true
		}
	}
	if !foundRule {
		t.Errorf("dynamic rule content missing from step 2 request messages: %+v", step2Req.Messages)
	}
	if !foundRuntime {
		t.Errorf("runtime context missing from step 2 request messages: %+v", step2Req.Messages)
	}

	_ = run
}
