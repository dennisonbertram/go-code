package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	htools "go-agent-harness/internal/harness/tools"
	om "go-agent-harness/internal/observationalmemory"
	"go-agent-harness/internal/provider/catalog"
	"go-agent-harness/internal/rollout"
)

type stubProvider struct {
	mu    sync.Mutex
	turns []CompletionResult
	calls int
}

func (s *stubProvider) Complete(_ context.Context, req CompletionRequest) (CompletionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls >= len(s.turns) {
		return CompletionResult{}, nil
	}
	turn := s.turns[s.calls]
	s.calls++
	if req.Stream != nil {
		for _, delta := range turn.Deltas {
			req.Stream(delta)
		}
	}
	return turn, nil
}

type errorProvider struct {
	err error
}

func (e *errorProvider) Complete(_ context.Context, _ CompletionRequest) (CompletionResult, error) {
	return CompletionResult{}, e.err
}

type capturingProvider struct {
	mu    sync.Mutex
	turns []CompletionResult
	calls []CompletionRequest
}

func (c *capturingProvider) Complete(_ context.Context, req CompletionRequest) (CompletionResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, req)
	if len(c.turns) == 0 {
		return CompletionResult{}, nil
	}
	turn := c.turns[0]
	c.turns = c.turns[1:]
	return turn, nil
}

type memoryStub struct {
	status  om.Status
	snippet string
}

func (m *memoryStub) Close() error                                           { return nil }
func (m *memoryStub) Mode() om.Mode                                          { return om.ModeLocalCoordinator }
func (m *memoryStub) Status(context.Context, om.ScopeKey) (om.Status, error) { return m.status, nil }
func (m *memoryStub) SetEnabled(context.Context, om.ScopeKey, bool, *om.Config, string, string) (om.Status, error) {
	return m.status, nil
}
func (m *memoryStub) Observe(_ context.Context, req om.ObserveRequest) (om.ObserveResult, error) {
	m.status.LastObservedMessageIndex = int64(len(req.Messages) - 1)
	return om.ObserveResult{Status: m.status, Observed: true}, nil
}
func (m *memoryStub) Snippet(context.Context, om.ScopeKey) (string, om.Status, error) {
	return m.snippet, m.status, nil
}
func (m *memoryStub) ReflectNow(context.Context, om.ScopeKey, string, string) (om.Status, error) {
	return m.status, nil
}
func (m *memoryStub) Export(context.Context, om.ScopeKey, string) (om.ExportResult, error) {
	return om.ExportResult{Status: m.status}, nil
}

func TestRunnerExecutesToolCallsAndPublishesEvents(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	err := registry.Register(ToolDefinition{
		Name:        "echo_json",
		Description: "echoes payload",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{"type": "string"},
			},
			"required": []string{"message"},
		},
	}, func(_ context.Context, raw json.RawMessage) (string, error) {
		var payload struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return "", err
		}
		return `{"echo":"` + payload.Message + `"}`, nil
	})
	if err != nil {
		t.Fatalf("register tool: %v", err)
	}

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-1",
				Name:      "echo_json",
				Arguments: `{"message":"hello"}`,
			}},
		},
		{Content: "All done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel:        "gpt-4.1-mini",
		DefaultSystemPrompt: "You are a coding harness.",
		MaxSteps:            4,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Say hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	if provider.calls != 2 {
		t.Fatalf("expected provider to be called twice, got %d", provider.calls)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected completed status, got %q", state.Status)
	}
	if state.Output != "All done" {
		t.Fatalf("unexpected run output: %q", state.Output)
	}

	requireEventOrder(t, events,
		"run.started",
		"llm.turn.completed",
		"tool.call.started",
		"tool.call.completed",
		"assistant.message",
		"run.completed",
	)
}

func TestRunnerInjectsMemorySnippetAndEmitsMemoryEvents(t *testing.T) {
	t.Parallel()

	provider := &capturingProvider{turns: []CompletionResult{{Content: "done"}}}
	mem := &memoryStub{
		status: om.Status{
			Mode:                     om.ModeLocalCoordinator,
			MemoryID:                 "default|conv|agent",
			Scope:                    om.ScopeKey{TenantID: "default", ConversationID: "conv", AgentID: "agent"},
			Enabled:                  true,
			LastObservedMessageIndex: -1,
			UpdatedAt:                time.Now().UTC(),
		},
		snippet: "<observational-memory>test</observational-memory>",
	}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:   "gpt-4.1-mini",
		MaxSteps:       2,
		MemoryManager:  mem,
		AskUserTimeout: time.Second,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}
	if len(provider.calls) == 0 {
		t.Fatalf("expected provider call")
	}
	// Volatile blocks (including observational memory) are injected at the tail,
	// after the conversation history, so the cached prefix is not invalidated.
	msgs0 := provider.calls[0].Messages
	if len(msgs0) < 1 || msgs0[len(msgs0)-1].Content != "<observational-memory>test</observational-memory>" {
		t.Fatalf("expected injected memory snippet at the tail of the first request: %+v", msgs0)
	}
	requireEventOrder(t, events, "memory.observe.started", "memory.observe.completed", "run.completed")
}

func TestRunnerFailsWhenProviderErrors(t *testing.T) {
	t.Parallel()

	runner := NewRunner(&errorProvider{err: errors.New("provider exploded")}, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Fail now"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}
	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Status != RunStatusFailed {
		t.Fatalf("expected failed status, got %q", state.Status)
	}
	if state.Error == "" {
		t.Fatalf("expected run error")
	}

	requireEventOrder(t, events, "run.started", "llm.turn.requested", "run.failed")
}

func TestRunnerEmitsAssistantMessageDeltaEvents(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{{
		Content: "Hello",
		Deltas: []CompletionDelta{
			{Content: "Hel"},
			{Content: "lo"},
		},
	}}}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Say hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	requireEventOrder(t, events,
		"run.started",
		"assistant.message.delta",
		"llm.turn.completed",
		"assistant.message",
		"run.completed",
	)

	var got []string
	for _, ev := range events {
		if ev.Type != "assistant.message.delta" {
			continue
		}
		content, _ := ev.Payload["content"].(string)
		got = append(got, content)
	}
	if !slices.Equal(got, []string{"Hel", "lo"}) {
		t.Fatalf("unexpected delta payloads: %+v", got)
	}
}

func TestRunnerEmitsToolCallDeltaEventsBeforeExecution(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(ToolDefinition{
		Name:        "echo_json",
		Description: "echoes payload",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{}`, nil
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-1",
				Name:      "echo_json",
				Arguments: `{"message":"hello"}`,
			}},
			Deltas: []CompletionDelta{
				{ToolCall: ToolCallDelta{Index: 0, ID: "call-1", Name: "echo_json"}},
				{ToolCall: ToolCallDelta{Index: 0, Arguments: `{"message":"`}},
				{ToolCall: ToolCallDelta{Index: 0, Arguments: `hello"}`}},
			},
		},
		{Content: "done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     4,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	requireEventOrder(t, events,
		"tool.call.delta",
		"llm.turn.completed",
		"tool.call.started",
		"tool.call.completed",
	)

	var argsParts []string
	for _, ev := range events {
		if ev.Type != "tool.call.delta" {
			continue
		}
		arguments, _ := ev.Payload["arguments"].(string)
		if arguments != "" {
			argsParts = append(argsParts, arguments)
		}
	}
	if !slices.Equal(argsParts, []string{`{"message":"`, `hello"}`}) {
		t.Fatalf("unexpected tool delta payloads: %+v", argsParts)
	}
}

func TestFailRunWithNilErrorUsesDefaultMessage(t *testing.T) {
	t.Parallel()

	runner := NewRunner(&stubProvider{}, NewRegistry(), RunnerConfig{})
	now := time.Now().UTC()
	runner.mu.Lock()
	runner.runs["run_manual"] = &runState{
		run: Run{
			ID:        "run_manual",
			Prompt:    "x",
			Model:     "gpt-4.1-mini",
			Status:    RunStatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
		events:      make([]Event, 0, 4),
		subscribers: make(map[chan Event]struct{}),
	}
	runner.mu.Unlock()

	runner.failRun("run_manual", nil)

	state, ok := runner.GetRun("run_manual")
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Status != RunStatusFailed {
		t.Fatalf("expected failed status, got %q", state.Status)
	}
	if state.Error != "run failed" {
		t.Fatalf("unexpected error: %q", state.Error)
	}
}

func TestMustJSONFallback(t *testing.T) {
	t.Parallel()

	got := mustJSON(map[string]any{"bad": make(chan int)})
	if got != `{"error":"failed to marshal tool error"}` {
		t.Fatalf("unexpected fallback json: %s", got)
	}
}

func TestRunnerAskUserQuestionWaitsAndResumes(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call_ask",
				Name:      htools.AskUserQuestionToolName,
				Arguments: `{"questions":[{"question":"Where next?","header":"Route","options":[{"label":"Docs","description":"Read docs"},{"label":"Code","description":"Read code"}],"multiSelect":false}]}`,
			}},
		},
		{Content: "All done"},
	}}

	broker := NewInMemoryAskUserQuestionBroker(time.Now)
	runner := NewRunner(provider, NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{
		ApprovalMode:   ToolApprovalModeFullAuto,
		AskUserBroker:  broker,
		AskUserTimeout: 2 * time.Second,
	}), RunnerConfig{
		DefaultModel:   "gpt-5-nano",
		MaxSteps:       4,
		AskUserBroker:  broker,
		AskUserTimeout: 2 * time.Second,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Need clarification"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	deadline := time.Now().Add(1500 * time.Millisecond)
	for {
		pending, err := runner.PendingInput(run.ID)
		if err == nil {
			if pending.CallID != "call_ask" {
				t.Fatalf("unexpected call id: %q", pending.CallID)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for pending input: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Status != RunStatusWaitingForUser {
		t.Fatalf("expected waiting_for_user status, got %q", state.Status)
	}

	if err := runner.SubmitInput(run.ID, map[string]string{"Where next?": "Docs"}); err != nil {
		t.Fatalf("submit input: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	state, ok = runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected completed status, got %q", state.Status)
	}
	if provider.calls != 2 {
		t.Fatalf("expected provider called twice, got %d", provider.calls)
	}

	requireEventOrder(t, events,
		"run.started",
		"tool.call.started",
		"run.waiting_for_user",
		"tool.call.completed",
		"run.resumed",
		"run.completed",
	)
}

func TestRunnerAskUserQuestionTimeoutFailsRun(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call_ask_timeout",
				Name:      htools.AskUserQuestionToolName,
				Arguments: `{"questions":[{"question":"Where next?","header":"Route","options":[{"label":"Docs","description":"Read docs"},{"label":"Code","description":"Read code"}],"multiSelect":false}]}`,
			}},
		},
		{Content: "should not happen"},
	}}

	broker := NewInMemoryAskUserQuestionBroker(time.Now)
	runner := NewRunner(provider, NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{
		ApprovalMode:   ToolApprovalModeFullAuto,
		AskUserBroker:  broker,
		AskUserTimeout: 20 * time.Millisecond,
	}), RunnerConfig{
		DefaultModel:   "gpt-5-nano",
		MaxSteps:       4,
		AskUserBroker:  broker,
		AskUserTimeout: 20 * time.Millisecond,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Need clarification"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Status != RunStatusFailed {
		t.Fatalf("expected failed status, got %q", state.Status)
	}
	if provider.calls != 1 {
		t.Fatalf("expected provider called once, got %d", provider.calls)
	}
	if !strings.Contains(state.Error, "timed out") {
		t.Fatalf("expected timeout error, got %q", state.Error)
	}

	requireEventOrder(t, events, "run.waiting_for_user", "run.failed")
}

func TestRunnerEmitsUsageDeltaAndPersistsTotals(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(ToolDefinition{
		Name:        "echo_json",
		Description: "echoes payload",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{}`, nil
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{ID: "call-1", Name: "echo_json", Arguments: `{}`}},
			Usage: &CompletionUsage{
				PromptTokens:     10,
				CompletionTokens: 4,
				TotalTokens:      14,
			},
			CostUSD:     floatPtr(0.001),
			UsageStatus: UsageStatusProviderReported,
			CostStatus:  CostStatusAvailable,
			Cost: &CompletionCost{
				TotalUSD: 0.001,
			},
		},
		{
			Content: "done",
			Usage: &CompletionUsage{
				PromptTokens:     8,
				CompletionTokens: 3,
				TotalTokens:      11,
			},
			CostUSD:     floatPtr(0.002),
			UsageStatus: UsageStatusProviderReported,
			CostStatus:  CostStatusAvailable,
			Cost: &CompletionCost{
				TotalUSD: 0.002,
			},
		},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     4,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	usageDeltaCount := 0
	var completed Event
	for _, ev := range events {
		if ev.Type == "usage.delta" {
			usageDeltaCount++
		}
		if ev.Type == "run.completed" {
			completed = ev
		}
	}
	if usageDeltaCount != 2 {
		t.Fatalf("expected two usage.delta events, got %d", usageDeltaCount)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.UsageTotals == nil || state.CostTotals == nil {
		t.Fatalf("expected usage and cost totals on run state")
	}
	if state.UsageTotals.PromptTokensTotal != 18 || state.UsageTotals.CompletionTokensTotal != 7 || state.UsageTotals.TotalTokens != 25 {
		t.Fatalf("unexpected run usage totals: %+v", state.UsageTotals)
	}
	if math.Abs(state.CostTotals.CostUSDTotal-0.003) > 1e-12 {
		t.Fatalf("unexpected run cost totals: %+v", state.CostTotals)
	}
	if state.CostTotals.CostStatus != CostStatusAvailable {
		t.Fatalf("unexpected run cost status: %q", state.CostTotals.CostStatus)
	}

	usageTotals, ok := completed.Payload["usage_totals"].(RunUsageTotals)
	if !ok {
		t.Fatalf("expected usage_totals in run.completed payload: %+v", completed.Payload)
	}
	if usageTotals.TotalTokens != 25 {
		t.Fatalf("unexpected completed usage totals: %+v", usageTotals)
	}
	costTotals, ok := completed.Payload["cost_totals"].(RunCostTotals)
	if !ok {
		t.Fatalf("expected cost_totals in run.completed payload: %+v", completed.Payload)
	}
	if math.Abs(costTotals.CostUSDTotal-0.003) > 1e-12 {
		t.Fatalf("unexpected completed cost totals: %+v", costTotals)
	}
}

func TestRunnerFailedRunIncludesPartialUsageTotals(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(ToolDefinition{
		Name:        "echo_json",
		Description: "echoes payload",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{}`, nil
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	provider := &flakyProvider{
		turns: []CompletionResult{
			{
				ToolCalls: []ToolCall{{ID: "call-1", Name: "echo_json", Arguments: `{}`}},
				Usage: &CompletionUsage{
					PromptTokens:     5,
					CompletionTokens: 2,
					TotalTokens:      7,
				},
				CostUSD:     floatPtr(0.0007),
				UsageStatus: UsageStatusProviderReported,
				CostStatus:  CostStatusAvailable,
				Cost: &CompletionCost{
					TotalUSD: 0.0007,
				},
			},
		},
		errAt: 1,
		err:   errors.New("provider exploded"),
	}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     4,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	var failed Event
	usageDeltaCount := 0
	for _, ev := range events {
		if ev.Type == "usage.delta" {
			usageDeltaCount++
		}
		if ev.Type == "run.failed" {
			failed = ev
		}
	}
	if usageDeltaCount != 1 {
		t.Fatalf("expected one usage.delta event, got %d", usageDeltaCount)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Status != RunStatusFailed {
		t.Fatalf("expected failed status, got %q", state.Status)
	}
	if state.UsageTotals == nil || state.UsageTotals.TotalTokens != 7 {
		t.Fatalf("unexpected run usage totals: %+v", state.UsageTotals)
	}
	if state.CostTotals == nil || math.Abs(state.CostTotals.CostUSDTotal-0.0007) > 1e-12 {
		t.Fatalf("unexpected run cost totals: %+v", state.CostTotals)
	}

	usageTotals, ok := failed.Payload["usage_totals"].(RunUsageTotals)
	if !ok {
		t.Fatalf("expected usage_totals in run.failed payload: %+v", failed.Payload)
	}
	if usageTotals.TotalTokens != 7 {
		t.Fatalf("unexpected failed usage totals payload: %+v", usageTotals)
	}
	costTotals, ok := failed.Payload["cost_totals"].(RunCostTotals)
	if !ok {
		t.Fatalf("expected cost_totals in run.failed payload: %+v", failed.Payload)
	}
	if math.Abs(costTotals.CostUSDTotal-0.0007) > 1e-12 {
		t.Fatalf("unexpected failed cost totals payload: %+v", costTotals)
	}
}

func collectRunEvents(t *testing.T, runner *Runner, runID string) ([]Event, error) {
	t.Helper()

	history, stream, cancel, err := runner.Subscribe(runID)
	if err != nil {
		return nil, err
	}
	defer cancel()

	events := append([]Event(nil), history...)
	if hasTerminalEvent(events) {
		return events, nil
	}

	timeout := time.After(4 * time.Second)
	for {
		select {
		case ev, ok := <-stream:
			if !ok {
				return events, nil
			}
			events = append(events, ev)
			if IsTerminalEvent(ev.Type) {
				return events, nil
			}
		case <-timeout:
			return nil, context.DeadlineExceeded
		}
	}
}

func hasTerminalEvent(events []Event) bool {
	for _, ev := range events {
		if IsTerminalEvent(ev.Type) {
			return true
		}
	}
	return false
}

func requireEventOrder(t *testing.T, events []Event, expected ...string) {
	t.Helper()

	positions := make(map[string]int, len(expected))
	for i, ev := range events {
		if _, exists := positions[string(ev.Type)]; !exists {
			positions[string(ev.Type)] = i
		}
	}

	prev := -1
	for _, eventType := range expected {
		idx, ok := positions[eventType]
		if !ok {
			t.Fatalf("missing event %q in %+v", eventType, eventTypes(events))
		}
		if idx <= prev {
			t.Fatalf("event %q out of order in %+v", eventType, eventTypes(events))
		}
		prev = idx
	}
}

func eventTypes(events []Event) []string {
	result := make([]string, 0, len(events))
	for _, ev := range events {
		result = append(result, string(ev.Type))
	}
	return result
}

type flakyProvider struct {
	turns []CompletionResult
	errAt int
	err   error
	calls int
}

func (f *flakyProvider) Complete(_ context.Context, _ CompletionRequest) (CompletionResult, error) {
	if f.calls == f.errAt {
		f.calls++
		if f.err == nil {
			return CompletionResult{}, errors.New("provider error")
		}
		return CompletionResult{}, f.err
	}
	if f.calls >= len(f.turns) {
		f.calls++
		return CompletionResult{}, nil
	}
	out := f.turns[f.calls]
	f.calls++
	return out, nil
}

func floatPtr(v float64) *float64 {
	n := v
	return &n
}

func TestRunnerStoresConversationOnCompletion(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{{Content: "done"}}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:         "hello",
		ConversationID: "conv-1",
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	msgs, ok := runner.ConversationMessages("conv-1")
	if !ok {
		t.Fatalf("expected conversation messages to be stored")
	}
	if len(msgs) < 2 {
		t.Fatalf("expected at least user + assistant messages, got %d", len(msgs))
	}

	hasUser := false
	hasAssistant := false
	for _, m := range msgs {
		if m.Role == "user" {
			hasUser = true
		}
		if m.Role == "assistant" {
			hasAssistant = true
		}
	}
	if !hasUser {
		t.Fatalf("expected user message in conversation history")
	}
	if !hasAssistant {
		t.Fatalf("expected assistant message in conversation history")
	}
}

func TestRunnerSecondRunGetsPriorMessages(t *testing.T) {
	t.Parallel()

	provider := &capturingProvider{turns: []CompletionResult{
		{Content: "first answer"},
		{Content: "second answer"},
	}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	// First run
	run1, err := runner.StartRun(RunRequest{
		Prompt:         "first question",
		ConversationID: "conv-multi",
	})
	if err != nil {
		t.Fatalf("start run 1: %v", err)
	}
	_, err = collectRunEvents(t, runner, run1.ID)
	if err != nil {
		t.Fatalf("collect events run 1: %v", err)
	}

	// Second run with same conversation ID
	run2, err := runner.StartRun(RunRequest{
		Prompt:         "follow up",
		ConversationID: "conv-multi",
	})
	if err != nil {
		t.Fatalf("start run 2: %v", err)
	}
	events2, err := collectRunEvents(t, runner, run2.ID)
	if err != nil {
		t.Fatalf("collect events run 2: %v", err)
	}

	// The second provider call should have prior messages
	if len(provider.calls) < 2 {
		t.Fatalf("expected at least 2 provider calls, got %d", len(provider.calls))
	}

	secondCallMsgs := provider.calls[1].Messages
	// Filter out system messages
	var nonSystem []Message
	for _, m := range secondCallMsgs {
		if m.Role != "system" {
			nonSystem = append(nonSystem, m)
		}
	}

	// Should have: prior user ("first question"), prior assistant ("first answer"), new user ("follow up")
	if len(nonSystem) < 3 {
		t.Fatalf("expected at least 3 non-system messages in second call, got %d: %+v", len(nonSystem), nonSystem)
	}

	if nonSystem[0].Role != "user" || nonSystem[0].Content != "first question" {
		t.Fatalf("expected first prior message to be user 'first question', got %+v", nonSystem[0])
	}
	if nonSystem[1].Role != "assistant" || nonSystem[1].Content != "first answer" {
		t.Fatalf("expected second prior message to be assistant 'first answer', got %+v", nonSystem[1])
	}
	if nonSystem[len(nonSystem)-1].Role != "user" || nonSystem[len(nonSystem)-1].Content != "follow up" {
		t.Fatalf("expected last message to be user 'follow up', got %+v", nonSystem[len(nonSystem)-1])
	}

	// Check for conversation.continued event
	foundContinued := false
	for _, ev := range events2 {
		if ev.Type == "conversation.continued" {
			foundContinued = true
			convID, _ := ev.Payload["conversation_id"].(string)
			if convID != "conv-multi" {
				t.Fatalf("expected conversation_id 'conv-multi', got %q", convID)
			}
			break
		}
	}
	if !foundContinued {
		t.Fatalf("expected conversation.continued event in second run, got events: %+v", eventTypes(events2))
	}
}

func TestRunnerConversationNotFound(t *testing.T) {
	t.Parallel()

	runner := NewRunner(&stubProvider{}, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	msgs, ok := runner.ConversationMessages("nonexistent")
	if ok {
		t.Fatalf("expected ok=false for nonexistent conversation")
	}
	if msgs != nil {
		t.Fatalf("expected nil messages for nonexistent conversation, got %+v", msgs)
	}
}

func TestEventIDsArePerRunSequential(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{
		{Content: "done1"},
		{Content: "done2"},
	}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	// Start two runs
	run1, err := runner.StartRun(RunRequest{Prompt: "first"})
	if err != nil {
		t.Fatalf("start run 1: %v", err)
	}
	events1, err := collectRunEvents(t, runner, run1.ID)
	if err != nil {
		t.Fatalf("collect events run 1: %v", err)
	}

	run2, err := runner.StartRun(RunRequest{Prompt: "second"})
	if err != nil {
		t.Fatalf("start run 2: %v", err)
	}
	events2, err := collectRunEvents(t, runner, run2.ID)
	if err != nil {
		t.Fatalf("collect events run 2: %v", err)
	}

	// Both runs should have events starting at :0 and sequential
	for _, tc := range []struct {
		name   string
		runID  string
		events []Event
	}{
		{"run1", run1.ID, events1},
		{"run2", run2.ID, events2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.events) == 0 {
				t.Fatal("expected events")
			}
			for i, ev := range tc.events {
				expectedID := fmt.Sprintf("%s:%d", tc.runID, i)
				if ev.ID != expectedID {
					t.Errorf("event[%d].ID = %q, want %q", i, ev.ID, expectedID)
				}
				// Verify ParseEventID roundtrips
				parsedRun, parsedSeq, err := ParseEventID(ev.ID)
				if err != nil {
					t.Errorf("ParseEventID(%q) error: %v", ev.ID, err)
					continue
				}
				if parsedRun != tc.runID {
					t.Errorf("ParseEventID(%q) runID = %q, want %q", ev.ID, parsedRun, tc.runID)
				}
				if parsedSeq != uint64(i) {
					t.Errorf("ParseEventID(%q) seq = %d, want %d", ev.ID, parsedSeq, i)
				}
			}
		})
	}
}

func TestEmitCompletionDelta_Reasoning(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{{
		Content: "Hello",
		Deltas: []CompletionDelta{
			{Reasoning: "thinking about this..."},
			{Reasoning: "still thinking..."},
			{Content: "Hello"},
		},
	}}}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:     "gpt-4.1-mini",
		MaxSteps:         2,
		CaptureReasoning: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Say hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	requireEventOrder(t, events,
		"run.started",
		"assistant.thinking.delta",
		"assistant.message.delta",
		"llm.turn.completed",
		"run.completed",
	)

	var thinkingParts []string
	for _, ev := range events {
		if ev.Type != EventAssistantThinkingDelta {
			continue
		}
		content, _ := ev.Payload["content"].(string)
		thinkingParts = append(thinkingParts, content)
	}
	if !slices.Equal(thinkingParts, []string{"thinking about this...", "still thinking..."}) {
		t.Fatalf("unexpected thinking delta payloads: %+v", thinkingParts)
	}
}

func TestRunnerPassesReasoningEffortToProvider(t *testing.T) {
	t.Parallel()

	provider := &capturingProvider{turns: []CompletionResult{{Content: "done"}}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:          "Hello",
		ReasoningEffort: "medium",
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	if len(provider.calls) == 0 {
		t.Fatalf("expected at least one provider call")
	}
	if provider.calls[0].ReasoningEffort != "medium" {
		t.Fatalf("expected ReasoningEffort=%q in CompletionRequest, got %q", "medium", provider.calls[0].ReasoningEffort)
	}
}

func TestRunnerOmitsReasoningEffortWhenNotSet(t *testing.T) {
	t.Parallel()

	provider := &capturingProvider{turns: []CompletionResult{{Content: "done"}}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt: "Hello",
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	if len(provider.calls) == 0 {
		t.Fatalf("expected at least one provider call")
	}
	if provider.calls[0].ReasoningEffort != "" {
		t.Fatalf("expected empty ReasoningEffort in CompletionRequest, got %q", provider.calls[0].ReasoningEffort)
	}
}

func TestRunnerFailedRunDoesNotStore(t *testing.T) {
	t.Parallel()

	runner := NewRunner(&errorProvider{err: errors.New("boom")}, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:         "fail please",
		ConversationID: "conv-fail",
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	msgs, ok := runner.ConversationMessages("conv-fail")
	if ok {
		t.Fatalf("expected ok=false for failed run conversation")
	}
	if msgs != nil {
		t.Fatalf("expected nil messages for failed run, got %+v", msgs)
	}
}

// --- Provider routing tests ---

// newTestCatalogWithModel creates a catalog with one provider that has the given model.
func newTestCatalogWithModel(providerName, modelID, apiKeyEnv string) *catalog.Catalog {
	return &catalog.Catalog{
		CatalogVersion: "1.0",
		Providers: map[string]catalog.ProviderEntry{
			providerName: {
				DisplayName: providerName,
				BaseURL:     "https://api.example.com",
				APIKeyEnv:   apiKeyEnv,
				Protocol:    "openai",
				Models: map[string]catalog.Model{
					modelID: {
						DisplayName:   modelID,
						ContextWindow: 128000,
						ToolCalling:   true,
						Streaming:     true,
					},
				},
			},
		},
	}
}

// newTestRegistryWithProvider creates a ProviderRegistry that has a pre-configured
// client factory returning the given provider stub. The getenv function controls
// whether the API key is found.
func newTestRegistryWithProvider(cat *catalog.Catalog, stub ProviderClient, getenv func(string) string) *catalog.ProviderRegistry {
	reg := catalog.NewProviderRegistryWithEnv(cat, getenv)
	reg.SetClientFactory(func(apiKey, baseURL, providerName string) (catalog.ProviderClient, error) {
		return stub, nil
	})
	return reg
}

// ProviderClient type alias so test stubs satisfy the catalog.ProviderClient interface.
// (Both stubProvider and capturingProvider already do since ProviderClient is interface{}.)
type ProviderClient = catalog.ProviderClient

func TestRunnerUsesDefaultProviderWhenNoRegistry(t *testing.T) {
	t.Parallel()

	defaultProvider := &capturingProvider{turns: []CompletionResult{{Content: "from default"}}}
	runner := NewRunner(defaultProvider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
		// No ProviderRegistry set
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected completed, got %q", state.Status)
	}
	if state.Output != "from default" {
		t.Fatalf("expected output 'from default', got %q", state.Output)
	}
	if len(defaultProvider.calls) != 1 {
		t.Fatalf("expected default provider called once, got %d", len(defaultProvider.calls))
	}

	// Should still emit provider.resolved with "default"
	found := false
	for _, ev := range events {
		if ev.Type == EventProviderResolved {
			found = true
			prov, _ := ev.Payload["provider"].(string)
			if prov != "default" {
				t.Fatalf("expected provider 'default', got %q", prov)
			}
		}
	}
	if !found {
		t.Fatalf("expected provider.resolved event, got events: %+v", eventTypes(events))
	}
}

func TestRunnerRoutesToRegistryProvider(t *testing.T) {
	t.Parallel()

	registryProvider := &capturingProvider{turns: []CompletionResult{{Content: "from registry"}}}
	defaultProvider := &capturingProvider{turns: []CompletionResult{{Content: "from default"}}}

	cat := newTestCatalogWithModel("deepseek", "deepseek-chat", "DEEPSEEK_API_KEY")
	reg := newTestRegistryWithProvider(cat, registryProvider, func(key string) string {
		if key == "DEEPSEEK_API_KEY" {
			return "sk-fake-deepseek-key"
		}
		return ""
	})

	runner := NewRunner(defaultProvider, NewRegistry(), RunnerConfig{
		DefaultModel:     "gpt-4.1-mini",
		MaxSteps:         2,
		ProviderRegistry: reg,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt: "hello",
		Model:  "deepseek-chat",
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected completed, got %q", state.Status)
	}
	if state.Output != "from registry" {
		t.Fatalf("expected output 'from registry', got %q", state.Output)
	}
	if state.ProviderName != "deepseek" {
		t.Fatalf("expected provider_name 'deepseek', got %q", state.ProviderName)
	}
	if len(registryProvider.calls) != 1 {
		t.Fatalf("expected registry provider called once, got %d", len(registryProvider.calls))
	}
	if len(defaultProvider.calls) != 0 {
		t.Fatalf("expected default provider NOT called, got %d", len(defaultProvider.calls))
	}

	// Verify provider.resolved event
	found := false
	for _, ev := range events {
		if ev.Type == EventProviderResolved {
			found = true
			prov, _ := ev.Payload["provider"].(string)
			if prov != "deepseek" {
				t.Fatalf("expected provider 'deepseek', got %q", prov)
			}
			model, _ := ev.Payload["model"].(string)
			if model != "deepseek-chat" {
				t.Fatalf("expected model 'deepseek-chat', got %q", model)
			}
		}
	}
	if !found {
		t.Fatalf("expected provider.resolved event, got events: %+v", eventTypes(events))
	}
}

func TestRunnerRoutesDynamicOpenRouterSlugViaRegistry(t *testing.T) {
	t.Parallel()

	registryProvider := &capturingProvider{turns: []CompletionResult{{Content: "from openrouter dynamic"}}}
	defaultProvider := &capturingProvider{turns: []CompletionResult{{Content: "from default"}}}

	cat := &catalog.Catalog{
		CatalogVersion: "1.0",
		Providers: map[string]catalog.ProviderEntry{
			"openrouter": {
				DisplayName: "OpenRouter",
				BaseURL:     "https://openrouter.ai/api/v1",
				APIKeyEnv:   "OPENROUTER_API_KEY",
				Protocol:    "openai",
				Models: map[string]catalog.Model{
					"openai/gpt-4.1-mini": {
						DisplayName:   "openai/gpt-4.1-mini",
						ContextWindow: 128000,
						ToolCalling:   true,
						Streaming:     true,
					},
				},
			},
		},
	}
	reg := newTestRegistryWithProvider(cat, registryProvider, func(key string) string {
		if key == "OPENROUTER_API_KEY" {
			return "sk-fake-openrouter-key"
		}
		return ""
	})
	reg.SetDiscovery("openrouter", runnerTestOpenRouterDiscovery{
		models: []catalog.DiscoveredModel{
			{ID: "moonshotai/kimi-k2.5", Name: "Kimi K2.5", ContextWindow: 262144},
		},
	})

	runner := NewRunner(defaultProvider, NewRegistry(), RunnerConfig{
		DefaultModel:     "moonshotai/kimi-k2.5",
		MaxSteps:         2,
		ProviderRegistry: reg,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt: "hello",
		Model:  "moonshotai/kimi-k2.5",
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected completed, got %q", state.Status)
	}
	if state.Output != "from openrouter dynamic" {
		t.Fatalf("expected output 'from openrouter dynamic', got %q", state.Output)
	}
	if state.ProviderName != "openrouter" {
		t.Fatalf("expected provider_name 'openrouter', got %q", state.ProviderName)
	}
	if len(registryProvider.calls) != 1 {
		t.Fatalf("expected registry provider called once, got %d", len(registryProvider.calls))
	}
	if len(defaultProvider.calls) != 0 {
		t.Fatalf("expected default provider NOT called, got %d", len(defaultProvider.calls))
	}

	found := false
	for _, ev := range events {
		if ev.Type == EventProviderResolved {
			found = true
			prov, _ := ev.Payload["provider"].(string)
			if prov != "openrouter" {
				t.Fatalf("expected provider 'openrouter', got %q", prov)
			}
			model, _ := ev.Payload["model"].(string)
			if model != "moonshotai/kimi-k2.5" {
				t.Fatalf("expected model 'moonshotai/kimi-k2.5', got %q", model)
			}
		}
	}
	if !found {
		t.Fatalf("expected provider.resolved event, got events: %+v", eventTypes(events))
	}
}

type runnerTestOpenRouterDiscovery struct {
	models []catalog.DiscoveredModel
	err    error
}

func (d runnerTestOpenRouterDiscovery) Models(context.Context) ([]catalog.DiscoveredModel, error) {
	out := make([]catalog.DiscoveredModel, len(d.models))
	copy(out, d.models)
	return out, d.err
}

func TestRunnerFailsWhenModelNotFoundNoFallback(t *testing.T) {
	t.Parallel()

	defaultProvider := &stubProvider{turns: []CompletionResult{{Content: "should not reach"}}}

	// Registry with empty catalog (no models)
	cat := &catalog.Catalog{
		CatalogVersion: "1.0",
		Providers:      map[string]catalog.ProviderEntry{},
	}
	reg := catalog.NewProviderRegistryWithEnv(cat, func(string) string { return "" })

	runner := NewRunner(defaultProvider, NewRegistry(), RunnerConfig{
		DefaultModel:     "gpt-4.1-mini",
		MaxSteps:         2,
		ProviderRegistry: reg,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:        "hello",
		Model:         "nonexistent-model",
		AllowFallback: false,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Status != RunStatusFailed {
		t.Fatalf("expected failed, got %q", state.Status)
	}
	if !strings.Contains(state.Error, "nonexistent-model") {
		t.Fatalf("expected error mentioning model name, got %q", state.Error)
	}

	// Should have run.failed event
	found := false
	for _, ev := range events {
		if ev.Type == EventRunFailed {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected run.failed event")
	}
}

func TestRunnerFallsBackWhenAllowed(t *testing.T) {
	t.Parallel()

	defaultProvider := &capturingProvider{turns: []CompletionResult{{Content: "from fallback"}}}

	// Registry with empty catalog (no models) -- model won't be found
	cat := &catalog.Catalog{
		CatalogVersion: "1.0",
		Providers:      map[string]catalog.ProviderEntry{},
	}
	reg := catalog.NewProviderRegistryWithEnv(cat, func(string) string { return "" })

	runner := NewRunner(defaultProvider, NewRegistry(), RunnerConfig{
		DefaultModel:     "gpt-4.1-mini",
		MaxSteps:         2,
		ProviderRegistry: reg,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:        "hello",
		Model:         "nonexistent-model",
		AllowFallback: true,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected completed, got %q (error: %s)", state.Status, state.Error)
	}
	if state.Output != "from fallback" {
		t.Fatalf("expected output 'from fallback', got %q", state.Output)
	}
	if len(defaultProvider.calls) != 1 {
		t.Fatalf("expected default provider called once, got %d", len(defaultProvider.calls))
	}

	// Should emit prompt.warning with provider_fallback code
	foundWarning := false
	for _, ev := range events {
		if ev.Type == EventPromptWarning {
			code, _ := ev.Payload["code"].(string)
			if code == "provider_fallback" {
				foundWarning = true
			}
		}
	}
	if !foundWarning {
		t.Fatalf("expected prompt.warning event with code 'provider_fallback', got events: %+v", eventTypes(events))
	}
}

func TestRunnerFallsBackOnMissingAPIKey(t *testing.T) {
	t.Parallel()

	defaultProvider := &capturingProvider{turns: []CompletionResult{{Content: "from default fallback"}}}

	// Catalog has the model but getenv returns empty (missing API key)
	cat := newTestCatalogWithModel("deepseek", "deepseek-chat", "DEEPSEEK_API_KEY")
	reg := catalog.NewProviderRegistryWithEnv(cat, func(string) string { return "" })
	reg.SetClientFactory(func(apiKey, baseURL, providerName string) (catalog.ProviderClient, error) {
		return &stubProvider{}, nil
	})

	runner := NewRunner(defaultProvider, NewRegistry(), RunnerConfig{
		DefaultModel:     "gpt-4.1-mini",
		MaxSteps:         2,
		ProviderRegistry: reg,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:        "hello",
		Model:         "deepseek-chat",
		AllowFallback: true,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected completed, got %q (error: %s)", state.Status, state.Error)
	}
	if state.Output != "from default fallback" {
		t.Fatalf("expected output 'from default fallback', got %q", state.Output)
	}
	if len(defaultProvider.calls) != 1 {
		t.Fatalf("expected default provider called once, got %d", len(defaultProvider.calls))
	}

	// Should emit prompt.warning with provider_fallback code
	foundWarning := false
	for _, ev := range events {
		if ev.Type == EventPromptWarning {
			code, _ := ev.Payload["code"].(string)
			if code == "provider_fallback" {
				foundWarning = true
			}
		}
	}
	if !foundWarning {
		t.Fatalf("expected prompt.warning event with code 'provider_fallback', got events: %+v", eventTypes(events))
	}
}

// notAProvider is a type that does NOT implement the Provider interface.
// Used to test the client.(Provider) type assertion failure branch.
type notAProvider struct{}

func TestRunnerFailsWhenClientNotProvider_NoFallback(t *testing.T) {
	t.Parallel()

	defaultProvider := &capturingProvider{turns: []CompletionResult{{Content: "should not reach"}}}

	// Catalog has the model and API key is present, but the client factory
	// returns a notAProvider{} which does not implement Provider.
	cat := newTestCatalogWithModel("badprov", "bad-model", "BAD_API_KEY")
	reg := catalog.NewProviderRegistryWithEnv(cat, func(key string) string {
		if key == "BAD_API_KEY" {
			return "sk-fake-key"
		}
		return ""
	})
	reg.SetClientFactory(func(apiKey, baseURL, providerName string) (catalog.ProviderClient, error) {
		return notAProvider{}, nil
	})

	runner := NewRunner(defaultProvider, NewRegistry(), RunnerConfig{
		DefaultModel:     "gpt-4.1-mini",
		MaxSteps:         2,
		ProviderRegistry: reg,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:        "hello",
		Model:         "bad-model",
		AllowFallback: false,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Status != RunStatusFailed {
		t.Fatalf("expected failed, got %q", state.Status)
	}
	if !strings.Contains(state.Error, "does not implement Provider interface") {
		t.Fatalf("expected error mentioning 'does not implement Provider interface', got %q", state.Error)
	}

	// Default provider should NOT have been called
	if len(defaultProvider.calls) != 0 {
		t.Fatalf("expected default provider NOT called, got %d calls", len(defaultProvider.calls))
	}

	// Should have run.failed event
	found := false
	for _, ev := range events {
		if ev.Type == EventRunFailed {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected run.failed event, got events: %+v", eventTypes(events))
	}
}

func TestRunnerFallsBackWhenClientNotProvider(t *testing.T) {
	t.Parallel()

	defaultProvider := &capturingProvider{turns: []CompletionResult{{Content: "from default"}}}

	// Catalog has the model and API key is present, but the client factory
	// returns a notAProvider{} which does not implement Provider.
	cat := newTestCatalogWithModel("badprov", "bad-model", "BAD_API_KEY")
	reg := catalog.NewProviderRegistryWithEnv(cat, func(key string) string {
		if key == "BAD_API_KEY" {
			return "sk-fake-key"
		}
		return ""
	})
	reg.SetClientFactory(func(apiKey, baseURL, providerName string) (catalog.ProviderClient, error) {
		return notAProvider{}, nil
	})

	runner := NewRunner(defaultProvider, NewRegistry(), RunnerConfig{
		DefaultModel:     "gpt-4.1-mini",
		MaxSteps:         2,
		ProviderRegistry: reg,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:        "hello",
		Model:         "bad-model",
		AllowFallback: true,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected completed, got %q (error: %s)", state.Status, state.Error)
	}
	if state.Output != "from default" {
		t.Fatalf("expected output 'from default', got %q", state.Output)
	}
	if len(defaultProvider.calls) != 1 {
		t.Fatalf("expected default provider called once, got %d", len(defaultProvider.calls))
	}

	// Should emit prompt.warning with provider_fallback code and message about Provider interface
	foundWarning := false
	for _, ev := range events {
		if ev.Type == EventPromptWarning {
			code, _ := ev.Payload["code"].(string)
			message, _ := ev.Payload["message"].(string)
			if code == "provider_fallback" && strings.Contains(message, "does not implement Provider interface") {
				foundWarning = true
			}
		}
	}
	if !foundWarning {
		t.Fatalf("expected prompt.warning with code 'provider_fallback' mentioning Provider interface, got events: %+v", eventTypes(events))
	}
}

func TestRunnerFailsWhenMissingAPIKey_NoFallback(t *testing.T) {
	t.Parallel()

	defaultProvider := &capturingProvider{turns: []CompletionResult{{Content: "should not reach"}}}

	// Catalog has the model, but getenv returns empty string for the API key.
	cat := newTestCatalogWithModel("deepseek", "deepseek-chat", "DEEPSEEK_API_KEY")
	reg := catalog.NewProviderRegistryWithEnv(cat, func(string) string { return "" })
	reg.SetClientFactory(func(apiKey, baseURL, providerName string) (catalog.ProviderClient, error) {
		return &stubProvider{}, nil
	})

	runner := NewRunner(defaultProvider, NewRegistry(), RunnerConfig{
		DefaultModel:     "gpt-4.1-mini",
		MaxSteps:         2,
		ProviderRegistry: reg,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:        "hello",
		Model:         "deepseek-chat",
		AllowFallback: false,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Status != RunStatusFailed {
		t.Fatalf("expected failed, got %q", state.Status)
	}
	if !strings.Contains(state.Error, "deepseek-chat") {
		t.Fatalf("expected error mentioning model name 'deepseek-chat', got %q", state.Error)
	}

	// Default provider should NOT have been called
	if len(defaultProvider.calls) != 0 {
		t.Fatalf("expected default provider NOT called, got %d calls", len(defaultProvider.calls))
	}

	// Should have run.failed event
	found := false
	for _, ev := range events {
		if ev.Type == EventRunFailed {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected run.failed event, got events: %+v", eventTypes(events))
	}
}

func TestRunnerFallsBackWhenClientFactoryErrors(t *testing.T) {
	t.Parallel()

	defaultProvider := &capturingProvider{turns: []CompletionResult{{Content: "from default"}}}

	// Catalog has the model and API key is present, but the client factory returns an error.
	cat := newTestCatalogWithModel("flaky", "flaky-model", "FLAKY_API_KEY")
	reg := catalog.NewProviderRegistryWithEnv(cat, func(key string) string {
		if key == "FLAKY_API_KEY" {
			return "sk-flaky-key"
		}
		return ""
	})
	reg.SetClientFactory(func(apiKey, baseURL, providerName string) (catalog.ProviderClient, error) {
		return nil, fmt.Errorf("connection refused")
	})

	runner := NewRunner(defaultProvider, NewRegistry(), RunnerConfig{
		DefaultModel:     "gpt-4.1-mini",
		MaxSteps:         2,
		ProviderRegistry: reg,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:        "hello",
		Model:         "flaky-model",
		AllowFallback: true,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected completed, got %q (error: %s)", state.Status, state.Error)
	}
	if state.Output != "from default" {
		t.Fatalf("expected output 'from default', got %q", state.Output)
	}
	if len(defaultProvider.calls) != 1 {
		t.Fatalf("expected default provider called once, got %d", len(defaultProvider.calls))
	}

	// Should emit prompt.warning with provider_fallback code
	foundWarning := false
	for _, ev := range events {
		if ev.Type == EventPromptWarning {
			code, _ := ev.Payload["code"].(string)
			if code == "provider_fallback" {
				foundWarning = true
			}
		}
	}
	if !foundWarning {
		t.Fatalf("expected prompt.warning event with code 'provider_fallback', got events: %+v", eventTypes(events))
	}
}

func TestRunnerFallsBackWhenMissingAPIKey(t *testing.T) {
	t.Parallel()

	defaultProvider := &capturingProvider{turns: []CompletionResult{{Content: "from default"}}}

	// Catalog has the model, but getenv returns empty string for the API key.
	cat := newTestCatalogWithModel("deepseek", "deepseek-chat", "DEEPSEEK_API_KEY")
	reg := catalog.NewProviderRegistryWithEnv(cat, func(string) string { return "" })
	reg.SetClientFactory(func(apiKey, baseURL, providerName string) (catalog.ProviderClient, error) {
		return &stubProvider{}, nil
	})

	runner := NewRunner(defaultProvider, NewRegistry(), RunnerConfig{
		DefaultModel:     "gpt-4.1-mini",
		MaxSteps:         2,
		ProviderRegistry: reg,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:        "hello",
		Model:         "deepseek-chat",
		AllowFallback: true,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected completed, got %q (error: %s)", state.Status, state.Error)
	}
	if state.Output != "from default" {
		t.Fatalf("expected output 'from default', got %q", state.Output)
	}
	if len(defaultProvider.calls) != 1 {
		t.Fatalf("expected default provider called once, got %d", len(defaultProvider.calls))
	}

	// Should emit prompt.warning with provider_fallback code
	foundWarning := false
	for _, ev := range events {
		if ev.Type == EventPromptWarning {
			code, _ := ev.Payload["code"].(string)
			if code == "provider_fallback" {
				foundWarning = true
			}
		}
	}
	if !foundWarning {
		t.Fatalf("expected prompt.warning event with code 'provider_fallback', got events: %+v", eventTypes(events))
	}
}

func TestRunnerFailsWhenClientFactoryErrors_NoFallback(t *testing.T) {
	t.Parallel()

	defaultProvider := &capturingProvider{turns: []CompletionResult{{Content: "should not reach"}}}

	// Catalog has the model and API key is present, but the client factory returns an error.
	cat := newTestCatalogWithModel("flaky", "flaky-model", "FLAKY_API_KEY")
	reg := catalog.NewProviderRegistryWithEnv(cat, func(key string) string {
		if key == "FLAKY_API_KEY" {
			return "sk-flaky-key"
		}
		return ""
	})
	reg.SetClientFactory(func(apiKey, baseURL, providerName string) (catalog.ProviderClient, error) {
		return nil, fmt.Errorf("connection refused")
	})

	runner := NewRunner(defaultProvider, NewRegistry(), RunnerConfig{
		DefaultModel:     "gpt-4.1-mini",
		MaxSteps:         2,
		ProviderRegistry: reg,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:        "hello",
		Model:         "flaky-model",
		AllowFallback: false,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Status != RunStatusFailed {
		t.Fatalf("expected failed, got %q", state.Status)
	}
	if !strings.Contains(state.Error, "flaky-model") {
		t.Fatalf("expected error mentioning model name 'flaky-model', got %q", state.Error)
	}

	// Default provider should NOT have been called
	if len(defaultProvider.calls) != 0 {
		t.Fatalf("expected default provider NOT called, got %d calls", len(defaultProvider.calls))
	}

	// Should have run.failed event
	found := false
	for _, ev := range events {
		if ev.Type == EventRunFailed {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected run.failed event, got events: %+v", eventTypes(events))
	}
}

func TestRunnerEmitsProviderResolvedEvent(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{{Content: "done"}}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected completed, got %q", state.Status)
	}

	// Find provider.resolved event
	found := false
	for _, ev := range events {
		if ev.Type == EventProviderResolved {
			found = true
			model, _ := ev.Payload["model"].(string)
			provider, _ := ev.Payload["provider"].(string)
			if model != "gpt-4.1-mini" {
				t.Fatalf("expected model 'gpt-4.1-mini', got %q", model)
			}
			if provider != "default" {
				t.Fatalf("expected provider 'default', got %q", provider)
			}
		}
	}
	if !found {
		t.Fatalf("expected provider.resolved event, got events: %+v", eventTypes(events))
	}

	// Verify ordering: provider.resolved should come before llm.turn.requested
	requireEventOrder(t, events, "run.started", "provider.resolved", "llm.turn.requested")
}

// -----------------------------------------------------------------------
// Issue #2: SSE step events and max-steps structured reason
// -----------------------------------------------------------------------

func TestRunnerEmitsStepStartedAndCompletedEvents(t *testing.T) {
	t.Parallel()

	// Provider returns one tool call then finishes — that gives us 2 steps.
	registry := NewRegistry()
	err := registry.Register(ToolDefinition{
		Name:        "noop",
		Description: "does nothing",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"ok":true}`, nil
	})
	if err != nil {
		t.Fatalf("register tool: %v", err)
	}

	provider := &stubProvider{turns: []CompletionResult{
		{ToolCalls: []ToolCall{{ID: "c1", Name: "noop", Arguments: `{}`}}},
		{Content: "all done"},
	}}
	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "test",
		MaxSteps:     4,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "go"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// Count step events
	var stepStarted, stepCompleted []Event
	for _, ev := range events {
		switch ev.Type {
		case EventRunStepStarted:
			stepStarted = append(stepStarted, ev)
		case EventRunStepCompleted:
			stepCompleted = append(stepCompleted, ev)
		}
	}

	if len(stepStarted) != 2 {
		t.Errorf("expected 2 run.step.started events, got %d (events: %v)", len(stepStarted), eventTypes(events))
	}
	if len(stepCompleted) != 2 {
		t.Errorf("expected 2 run.step.completed events, got %d (events: %v)", len(stepCompleted), eventTypes(events))
	}

	// Each step.started must carry the step number.
	// Payloads are map[string]any stored without JSON round-trip so numeric
	// values are int, not float64.
	for i, ev := range stepStarted {
		step, ok := ev.Payload["step"].(int)
		if !ok {
			t.Errorf("step.started[%d] missing step field: %+v", i, ev.Payload)
			continue
		}
		if step != i+1 {
			t.Errorf("step.started[%d] step = %v, want %d", i, step, i+1)
		}
	}
	for i, ev := range stepCompleted {
		step, ok := ev.Payload["step"].(int)
		if !ok {
			t.Errorf("step.completed[%d] missing step field: %+v", i, ev.Payload)
			continue
		}
		if step != i+1 {
			t.Errorf("step.completed[%d] step = %v, want %d", i, step, i+1)
		}
	}

	// run.step.started must come before llm.turn.requested, which must come before run.step.completed
	requireEventOrder(t, events,
		"run.started",
		"run.step.started",
		"llm.turn.requested",
		"run.step.completed",
		"run.completed",
	)
}

func TestRunnerMaxStepsReachedEmitsStructuredReason(t *testing.T) {
	t.Parallel()

	// Provider always returns tool calls — never self-terminates — forces max steps.
	registry := NewRegistry()
	err := registry.Register(ToolDefinition{
		Name:        "inf_tool",
		Description: "always needs another call",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"ok":true}`, nil
	})
	if err != nil {
		t.Fatalf("register tool: %v", err)
	}

	// Always emit a tool call — never terminates naturally
	provider := &stubProvider{}
	for i := 0; i < 10; i++ {
		provider.turns = append(provider.turns, CompletionResult{
			ToolCalls: []ToolCall{{ID: fmt.Sprintf("c%d", i), Name: "inf_tool", Arguments: `{}`}},
		})
	}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "test",
		MaxSteps:     2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "infinite"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// Find run.failed event and check it carries reason="max_steps_reached"
	var failedEv *Event
	for i := range events {
		if events[i].Type == EventRunFailed {
			failedEv = &events[i]
			break
		}
	}
	if failedEv == nil {
		t.Fatalf("expected run.failed event, got: %v", eventTypes(events))
	}

	reason, _ := failedEv.Payload["reason"].(string)
	if reason != "max_steps_reached" {
		t.Errorf("run.failed reason = %q, want \"max_steps_reached\"", reason)
	}

	// max_steps is emitted as int (no JSON round-trip in test)
	maxSteps, _ := failedEv.Payload["max_steps"].(int)
	if maxSteps != 2 {
		t.Errorf("run.failed max_steps = %v, want 2", maxSteps)
	}
}

func TestRunnerNonMaxStepsFailureHasNoMaxStepsReason(t *testing.T) {
	t.Parallel()

	// A provider error — not a max-steps exhaustion — should NOT carry reason="max_steps_reached"
	runner := NewRunner(&errorProvider{err: errors.New("provider down")}, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
		MaxSteps:     4,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "fail"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	var failedEv *Event
	for i := range events {
		if events[i].Type == EventRunFailed {
			failedEv = &events[i]
			break
		}
	}
	if failedEv == nil {
		t.Fatalf("expected run.failed event, got: %v", eventTypes(events))
	}

	// reason must NOT be max_steps_reached
	reason, _ := failedEv.Payload["reason"].(string)
	if reason == "max_steps_reached" {
		t.Errorf("non-max-steps failure should not carry reason=max_steps_reached")
	}
}

func TestRunnerGetConversationStoreNil(t *testing.T) {
	t.Parallel()

	runner := NewRunner(&stubProvider{}, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
		MaxSteps:     2,
	})
	if got := runner.GetConversationStore(); got != nil {
		t.Fatalf("expected nil ConversationStore, got %v", got)
	}
}

func TestRunnerGetConversationStoreSet(t *testing.T) {
	t.Parallel()

	store := newTestConversationStore(t)
	runner := NewRunner(&stubProvider{}, NewRegistry(), RunnerConfig{
		DefaultModel:      "test",
		MaxSteps:          2,
		ConversationStore: store,
	})
	if got := runner.GetConversationStore(); got == nil {
		t.Fatalf("expected non-nil ConversationStore")
	}
	if got := runner.GetConversationStore(); got != store {
		t.Fatalf("expected same store instance")
	}
}

// TestRunnerConversationPersistenceWithStoreErrors verifies the runner
// still completes a run when the conversation store returns errors.
// Note: no ConversationID is supplied (auto-assigned) so the ownership
// check is bypassed — this test targets store errors during save, not check.
func TestRunnerConversationPersistenceWithStoreErrors(t *testing.T) {
	t.Parallel()

	runner := NewRunner(&stubProvider{turns: []CompletionResult{{Content: "done"}}}, NewRegistry(), RunnerConfig{
		DefaultModel:      "test",
		MaxSteps:          2,
		ConversationStore: &failingConversationStore{},
	})

	run, err := runner.StartRun(RunRequest{
		Prompt: "hello",
		// No ConversationID supplied: runner auto-assigns one (run.ID), so
		// the ownership pre-check is skipped and store errors only affect
		// save/persist, not startup.
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		// The run should still complete even with store errors
		t.Fatalf("collect events: %v", err)
	}

	foundCompleted := false
	for _, ev := range events {
		if ev.Type == EventRunCompleted {
			foundCompleted = true
			break
		}
	}
	if !foundCompleted {
		t.Fatalf("expected run.completed event even with store errors, got: %+v", eventTypes(events))
	}
}

// failingConversationStore always returns errors.
type failingConversationStore struct{}

func (f *failingConversationStore) Migrate(_ context.Context) error { return fmt.Errorf("fail") }
func (f *failingConversationStore) Close() error                    { return nil }
func (f *failingConversationStore) SaveConversation(_ context.Context, _ string, _ []Message) error {
	return fmt.Errorf("store save failed")
}
func (f *failingConversationStore) SaveConversationWithCost(_ context.Context, _ string, _ []Message, _ ConversationTokenCost) error {
	return fmt.Errorf("store save with cost failed")
}
func (f *failingConversationStore) LoadMessages(_ context.Context, _ string) ([]Message, error) {
	return nil, fmt.Errorf("store load failed")
}
func (f *failingConversationStore) ListConversations(_ context.Context, _ ConversationFilter, _, _ int) ([]Conversation, error) {
	return nil, fmt.Errorf("store list failed")
}
func (f *failingConversationStore) DeleteConversation(_ context.Context, _ string) error {
	return fmt.Errorf("store delete failed")
}
func (f *failingConversationStore) SearchMessages(_ context.Context, _ string, _ string, _ int) ([]MessageSearchResult, error) {
	return nil, fmt.Errorf("store search failed")
}
func (f *failingConversationStore) DeleteOldConversations(_ context.Context, _ time.Time) (int, error) {
	return 0, fmt.Errorf("store delete old failed")
}
func (f *failingConversationStore) PinConversation(_ context.Context, _ string, _ bool) error {
	return fmt.Errorf("store pin failed")
}
func (f *failingConversationStore) CompactConversation(_ context.Context, _ string, _ int, _ Message) error {
	return fmt.Errorf("store compact failed")
}
func (f *failingConversationStore) UndoPrompts(_ context.Context, _ string, _ int) (int, error) {
	return 0, fmt.Errorf("store undo failed")
}
func (f *failingConversationStore) UpdateConversationMeta(_ context.Context, _, _, _ string) error {
	return fmt.Errorf("store update meta failed")
}
func (f *failingConversationStore) GetConversationOwner(_ context.Context, _ string) (*Conversation, error) {
	return nil, fmt.Errorf("store get owner failed")
}
func (f *failingConversationStore) ForkConversation(_ context.Context, _, _ string) (*Conversation, error) {
	return nil, fmt.Errorf("store fork failed")
}

// ---------------------------------------------------------------------------
// Token/cost wiring: runner → ConversationStore (Issue #32)
// ---------------------------------------------------------------------------

// capturingConversationStore records the last SaveConversationWithCost call.
type capturingConversationStore struct {
	savedCost ConversationTokenCost
	saveCount int
}

func (c *capturingConversationStore) Migrate(_ context.Context) error { return nil }
func (c *capturingConversationStore) Close() error                    { return nil }
func (c *capturingConversationStore) SaveConversation(_ context.Context, _ string, _ []Message) error {
	c.saveCount++
	return nil
}
func (c *capturingConversationStore) SaveConversationWithCost(_ context.Context, _ string, _ []Message, cost ConversationTokenCost) error {
	c.savedCost = cost
	c.saveCount++
	return nil
}
func (c *capturingConversationStore) LoadMessages(_ context.Context, _ string) ([]Message, error) {
	return nil, nil
}
func (c *capturingConversationStore) ListConversations(_ context.Context, _ ConversationFilter, _, _ int) ([]Conversation, error) {
	return nil, nil
}
func (c *capturingConversationStore) DeleteConversation(_ context.Context, _ string) error {
	return nil
}
func (c *capturingConversationStore) SearchMessages(_ context.Context, _ string, _ string, _ int) ([]MessageSearchResult, error) {
	return nil, nil
}
func (c *capturingConversationStore) DeleteOldConversations(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
}
func (c *capturingConversationStore) PinConversation(_ context.Context, _ string, _ bool) error {
	return nil
}
func (c *capturingConversationStore) CompactConversation(_ context.Context, _ string, _ int, _ Message) error {
	return nil
}
func (c *capturingConversationStore) UndoPrompts(_ context.Context, _ string, _ int) (int, error) {
	return 0, nil
}
func (c *capturingConversationStore) UpdateConversationMeta(_ context.Context, _, _, _ string) error {
	return nil
}
func (c *capturingConversationStore) GetConversationOwner(_ context.Context, _ string) (*Conversation, error) {
	return nil, nil
}
func (c *capturingConversationStore) ForkConversation(_ context.Context, _, _ string) (*Conversation, error) {
	return nil, nil
}

func TestRunnerPersistsTokenCostOnCompletion(t *testing.T) {
	t.Parallel()

	// Provider returns a result with usage and cost data.
	usage := &CompletionUsage{
		PromptTokens:     200,
		CompletionTokens: 75,
		TotalTokens:      275,
	}
	costUSD := 0.00456
	provider := &stubProvider{
		turns: []CompletionResult{
			{
				Content:    "done",
				Usage:      usage,
				CostUSD:    &costUSD,
				CostStatus: CostStatusAvailable,
			},
		},
	}

	store := &capturingConversationStore{}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:      "test",
		MaxSteps:          2,
		ConversationStore: store,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:         "hello",
		ConversationID: "conv-cost-wire",
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	if _, err := collectRunEvents(t, runner, run.ID); err != nil {
		t.Fatalf("collect events: %v", err)
	}

	if store.saveCount == 0 {
		t.Fatal("expected SaveConversationWithCost to be called at least once")
	}
	if store.savedCost.PromptTokens != 200 {
		t.Errorf("expected PromptTokens=200, got %d", store.savedCost.PromptTokens)
	}
	if store.savedCost.CompletionTokens != 75 {
		t.Errorf("expected CompletionTokens=75, got %d", store.savedCost.CompletionTokens)
	}
	if store.savedCost.CostUSD < 0.004 || store.savedCost.CostUSD > 0.005 {
		t.Errorf("expected CostUSD~0.00456, got %f", store.savedCost.CostUSD)
	}
}

func TestRunnerPersistsZeroCostWhenNoUsage(t *testing.T) {
	t.Parallel()

	// Provider returns no usage data.
	provider := &stubProvider{
		turns: []CompletionResult{
			{Content: "done"},
		},
	}

	store := &capturingConversationStore{}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:      "test",
		MaxSteps:          2,
		ConversationStore: store,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:         "hello",
		ConversationID: "conv-zero-wire",
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	if _, err := collectRunEvents(t, runner, run.ID); err != nil {
		t.Fatalf("collect events: %v", err)
	}

	if store.saveCount == 0 {
		t.Fatal("expected SaveConversationWithCost to be called at least once")
	}
	// No usage → all zeros in the stored cost
	if store.savedCost.PromptTokens != 0 {
		t.Errorf("expected PromptTokens=0, got %d", store.savedCost.PromptTokens)
	}
	if store.savedCost.CompletionTokens != 0 {
		t.Errorf("expected CompletionTokens=0, got %d", store.savedCost.CompletionTokens)
	}
	if store.savedCost.CostUSD != 0 {
		t.Errorf("expected CostUSD=0, got %f", store.savedCost.CostUSD)
	}
}

// ---------------------------------------------------------------------------
// Coverage: GetProviderRegistry accessor
// ---------------------------------------------------------------------------

func TestGetProviderRegistryReturnsConfigured(t *testing.T) {
	t.Parallel()

	cat := &catalog.Catalog{CatalogVersion: "v1-test"}
	reg := catalog.NewProviderRegistry(cat)

	runner := NewRunner(&stubProvider{}, NewRegistry(), RunnerConfig{
		DefaultModel:     "gpt-4.1-mini",
		MaxSteps:         2,
		ProviderRegistry: reg,
	})

	got := runner.GetProviderRegistry()
	if got != reg {
		t.Fatalf("expected same registry pointer, got %v", got)
	}
}

func TestGetProviderRegistryNil(t *testing.T) {
	t.Parallel()

	runner := NewRunner(&stubProvider{}, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	if runner.GetProviderRegistry() != nil {
		t.Fatalf("expected nil provider registry")
	}
}

// ---------------------------------------------------------------------------
// Coverage: GetRunSummary
// ---------------------------------------------------------------------------

func TestGetRunSummaryNotFound(t *testing.T) {
	t.Parallel()

	runner := NewRunner(&stubProvider{}, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	_, err := runner.GetRunSummary("nonexistent")
	if err == nil || err != ErrRunNotFound {
		t.Fatalf("expected ErrRunNotFound, got %v", err)
	}
}

func TestGetRunSummaryStillRunning(t *testing.T) {
	t.Parallel()

	// Use a provider that blocks so the run stays in-progress.
	blocker := make(chan struct{})
	blockingProvider := &blockingProviderForSummary{ch: blocker}
	runner2 := NewRunner(blockingProvider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	run, err := runner2.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Give the run a moment to start.
	time.Sleep(50 * time.Millisecond)

	_, err = runner2.GetRunSummary(run.ID)
	if err == nil {
		t.Fatalf("expected error for in-progress run")
	}
	if !strings.Contains(err.Error(), "still") {
		t.Fatalf("expected 'still' in error, got: %v", err)
	}

	// Unblock and let run finish.
	close(blocker)
	time.Sleep(100 * time.Millisecond)
}

type blockingProviderForSummary struct {
	ch chan struct{}
}

func (b *blockingProviderForSummary) Complete(_ context.Context, _ CompletionRequest) (CompletionResult, error) {
	<-b.ch
	return CompletionResult{Content: "done"}, nil
}

func TestGetRunSummaryCompleted(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{
		{Content: "All done"},
	}}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	summary, err := runner.GetRunSummary(run.ID)
	if err != nil {
		t.Fatalf("GetRunSummary: %v", err)
	}

	if summary.RunID != run.ID {
		t.Fatalf("RunID: got %q, want %q", summary.RunID, run.ID)
	}
	if summary.Status != RunStatusCompleted {
		t.Fatalf("Status: got %q, want %q", summary.Status, RunStatusCompleted)
	}
}

func TestRunner_SkillConstraint_BlocksDisallowedTool(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()

	// Register "skill" tool that returns allowed_tools
	_ = registry.Register(ToolDefinition{
		Name:        "skill",
		Description: "skill tool",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		result, _ := json.Marshal(map[string]any{
			"skill":         "code-review",
			"instructions":  "Review the code.",
			"allowed_tools": []string{"read_file", "grep"},
		})
		return string(result), nil
	})

	// Register tools that might be called
	_ = registry.Register(ToolDefinition{
		Name:        "read_file",
		Description: "reads a file",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"content":"file data"}`, nil
	})
	_ = registry.Register(ToolDefinition{
		Name:        "bash",
		Description: "runs bash",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"output":"done"}`, nil
	})

	// Turn 1: LLM calls "skill" tool
	// Turn 2: LLM calls "bash" (which should be blocked) then responds with text
	// Turn 3: LLM responds with final text
	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-skill",
				Name:      "skill",
				Arguments: `{"command":"code-review"}`,
			}},
		},
		{
			ToolCalls: []ToolCall{{
				ID:        "call-bash",
				Name:      "bash",
				Arguments: `{"command":"echo hello"}`,
			}},
		},
		{Content: "Done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     4,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "review code"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// Verify skill constraint activation event
	var foundActivation, foundBlocked bool
	for _, ev := range events {
		if ev.Type == EventSkillConstraintActivated {
			foundActivation = true
			skill, _ := ev.Payload["skill"].(string)
			if skill != "code-review" {
				t.Errorf("expected skill 'code-review', got %q", skill)
			}
		}
		if ev.Type == EventToolCallBlocked {
			foundBlocked = true
			tool, _ := ev.Payload["tool"].(string)
			if tool != "bash" {
				t.Errorf("expected blocked tool 'bash', got %q", tool)
			}
			skill, _ := ev.Payload["skill"].(string)
			if skill != "code-review" {
				t.Errorf("expected blocking skill 'code-review', got %q", skill)
			}
		}
	}
	if !foundActivation {
		t.Error("expected skill.constraint.activated event")
	}
	if !foundBlocked {
		t.Error("expected tool.call.blocked event")
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("expected run state")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected completed, got %q", state.Status)
	}
}

func TestRunner_SkillConstraint_AllowsListedTool(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()

	_ = registry.Register(ToolDefinition{
		Name:        "skill",
		Description: "skill tool",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		result, _ := json.Marshal(map[string]any{
			"skill":         "deploy",
			"instructions":  "Deploy the app.",
			"allowed_tools": []string{"bash"},
		})
		return string(result), nil
	})

	bashCalled := false
	_ = registry.Register(ToolDefinition{
		Name:        "bash",
		Description: "runs bash",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		bashCalled = true
		return `{"output":"deployed"}`, nil
	})

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-skill",
				Name:      "skill",
				Arguments: `{"command":"deploy"}`,
			}},
		},
		{
			ToolCalls: []ToolCall{{
				ID:        "call-bash",
				Name:      "bash",
				Arguments: `{"command":"deploy.sh"}`,
			}},
		},
		{Content: "Deployed"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     4,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "deploy"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	if !bashCalled {
		t.Error("expected bash tool to be executed (it is in allowed_tools)")
	}

	// Verify no tool.call.blocked event
	for _, ev := range events {
		if ev.Type == EventToolCallBlocked {
			t.Errorf("unexpected tool.call.blocked event for tool %v", ev.Payload["tool"])
		}
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("expected run state")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected completed, got %q", state.Status)
	}
}

func TestRunner_SkillConstraint_NilAllowedToolsNoFiltering(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()

	_ = registry.Register(ToolDefinition{
		Name:        "skill",
		Description: "skill tool",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		result, _ := json.Marshal(map[string]any{
			"skill":        "unrestricted",
			"instructions": "Do anything.",
		})
		return string(result), nil
	})

	bashCalled := false
	_ = registry.Register(ToolDefinition{
		Name:        "bash",
		Description: "runs bash",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		bashCalled = true
		return `{"output":"done"}`, nil
	})

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-skill",
				Name:      "skill",
				Arguments: `{"command":"unrestricted"}`,
			}},
		},
		{
			ToolCalls: []ToolCall{{
				ID:        "call-bash",
				Name:      "bash",
				Arguments: `{"command":"echo hello"}`,
			}},
		},
		{Content: "Done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     4,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "anything"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	if !bashCalled {
		t.Error("expected bash to be called (nil allowed_tools = unrestricted)")
	}

	// Verify activation event says unrestricted
	for _, ev := range events {
		if ev.Type == EventSkillConstraintActivated {
			unrestricted, _ := ev.Payload["unrestricted"].(bool)
			if !unrestricted {
				t.Error("expected unrestricted=true for nil allowed_tools")
			}
		}
		if ev.Type == EventToolCallBlocked {
			t.Error("unexpected tool.call.blocked event")
		}
	}
}

func TestRunner_SkillConstraint_CleanupOnComplete(t *testing.T) {
	t.Parallel()

	tracker := NewSkillConstraintTracker()
	registry := NewRegistry()

	_ = registry.Register(ToolDefinition{
		Name:        "skill",
		Description: "skill tool",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		result, _ := json.Marshal(map[string]any{
			"skill":         "test-skill",
			"instructions":  "Test.",
			"allowed_tools": []string{"bash"},
		})
		return string(result), nil
	})

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-skill",
				Name:      "skill",
				Arguments: `{"command":"test-skill"}`,
			}},
		},
		{Content: "Done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel:     "gpt-4.1-mini",
		MaxSteps:         4,
		SkillConstraints: tracker,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// Verify constraint is cleaned up after run completion
	_, active := tracker.Active(run.ID)
	if active {
		t.Error("expected skill constraint to be cleaned up after run completion")
	}
}

func TestRunner_SkillConstraint_CleanupOnFail(t *testing.T) {
	t.Parallel()

	tracker := NewSkillConstraintTracker()

	// Pre-activate a constraint for a run that will fail
	tracker.Activate("run_1", SkillConstraint{
		SkillName:    "test",
		AllowedTools: []string{"bash"},
	})

	runner := NewRunner(
		&errorProvider{err: errors.New("provider exploded")},
		NewRegistry(),
		RunnerConfig{
			DefaultModel:     "gpt-4.1-mini",
			MaxSteps:         2,
			SkillConstraints: tracker,
		},
	)

	run, err := runner.StartRun(RunRequest{Prompt: "fail"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// Verify constraint is cleaned up after run failure
	_, active := tracker.Active(run.ID)
	if active {
		t.Error("expected skill constraint to be cleaned up after run failure")
	}
}

func TestRunner_SkillConstraint_AlwaysAvailableToolsNotBlocked(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()

	_ = registry.Register(ToolDefinition{
		Name:        "skill",
		Description: "skill tool",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		result, _ := json.Marshal(map[string]any{
			"skill":         "strict",
			"instructions":  "Strict mode.",
			"allowed_tools": []string{"read_file"},
		})
		return string(result), nil
	})

	_ = registry.Register(ToolDefinition{
		Name:        "read_file",
		Description: "reads a file",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"content":"data"}`, nil
	})

	findToolCalled := false
	_ = registry.Register(ToolDefinition{
		Name:        "find_tool",
		Description: "finds a tool",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		findToolCalled = true
		return `{"tools":[]}`, nil
	})

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-skill",
				Name:      "skill",
				Arguments: `{"command":"strict"}`,
			}},
		},
		{
			ToolCalls: []ToolCall{{
				ID:        "call-find",
				Name:      "find_tool",
				Arguments: `{"query":"bash"}`,
			}},
		},
		{Content: "Done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     4,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "strict"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	if !findToolCalled {
		t.Error("expected find_tool to be called (it is always-available)")
	}

	for _, ev := range events {
		if ev.Type == EventToolCallBlocked {
			t.Errorf("unexpected tool.call.blocked event for tool %v", ev.Payload["tool"])
		}
	}
}

func TestRunner_SkillConstraint_FiltersToolDefinitions(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()

	_ = registry.Register(ToolDefinition{
		Name:        "skill",
		Description: "skill tool",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		result, _ := json.Marshal(map[string]any{
			"skill":         "limited",
			"instructions":  "Limited tools.",
			"allowed_tools": []string{"read_file"},
		})
		return string(result), nil
	})

	_ = registry.Register(ToolDefinition{
		Name:        "read_file",
		Description: "reads a file",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"content":"data"}`, nil
	})

	_ = registry.Register(ToolDefinition{
		Name:        "bash",
		Description: "runs bash",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{}`, nil
	})

	// Use a capturingProvider so we can inspect what tools the LLM sees
	capture := &capturingProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-skill",
				Name:      "skill",
				Arguments: `{"command":"limited"}`,
			}},
		},
		{Content: "Done"},
	}}

	runner := NewRunner(capture, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     4,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// First call should have all tools (before skill activation)
	if len(capture.calls) < 2 {
		t.Fatalf("expected at least 2 provider calls, got %d", len(capture.calls))
	}

	firstCallTools := capture.calls[0].Tools
	firstToolNames := make(map[string]bool)
	for _, td := range firstCallTools {
		firstToolNames[td.Name] = true
	}
	if !firstToolNames["bash"] {
		t.Error("expected bash in first call tools (before constraint)")
	}
	if !firstToolNames["read_file"] {
		t.Error("expected read_file in first call tools")
	}
	if !firstToolNames["skill"] {
		t.Error("expected skill in first call tools")
	}

	// Second call should have filtered tools (after skill activation)
	secondCallTools := capture.calls[1].Tools
	secondToolNames := make(map[string]bool)
	for _, td := range secondCallTools {
		secondToolNames[td.Name] = true
	}
	if secondToolNames["bash"] {
		t.Error("expected bash to be filtered out of second call tools (not in allowed_tools)")
	}
	if !secondToolNames["read_file"] {
		t.Error("expected read_file in second call tools (in allowed_tools)")
	}
	if !secondToolNames["skill"] {
		t.Error("expected skill in second call tools (always-available)")
	}
}

func TestRunner_SkillConstraint_ReplacesOldConstraint(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()

	skillCallCount := 0
	_ = registry.Register(ToolDefinition{
		Name:        "skill",
		Description: "skill tool",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		skillCallCount++
		var allowed []string
		var skillName string
		if skillCallCount == 1 {
			skillName = "first-skill"
			allowed = []string{"read_file"}
		} else {
			skillName = "second-skill"
			allowed = []string{"bash"}
		}
		result, _ := json.Marshal(map[string]any{
			"skill":         skillName,
			"instructions":  "Do something.",
			"allowed_tools": allowed,
		})
		return string(result), nil
	})

	_ = registry.Register(ToolDefinition{
		Name:        "read_file",
		Description: "reads a file",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"content":"data"}`, nil
	})

	bashCalled := false
	_ = registry.Register(ToolDefinition{
		Name:        "bash",
		Description: "runs bash",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		bashCalled = true
		return `{"output":"done"}`, nil
	})

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-skill-1",
				Name:      "skill",
				Arguments: `{"command":"first-skill"}`,
			}},
		},
		{
			ToolCalls: []ToolCall{{
				ID:        "call-skill-2",
				Name:      "skill",
				Arguments: `{"command":"second-skill"}`,
			}},
		},
		{
			ToolCalls: []ToolCall{{
				ID:        "call-bash",
				Name:      "bash",
				Arguments: `{"command":"echo"}`,
			}},
		},
		{Content: "Done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     5,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// After second skill activation, bash should be allowed
	if !bashCalled {
		t.Error("expected bash to be called under second-skill constraint")
	}

	// Verify deactivation event for the first skill
	var foundDeactivation bool
	for _, ev := range events {
		if ev.Type == EventSkillConstraintDeactivated {
			foundDeactivation = true
			skill, _ := ev.Payload["skill"].(string)
			if skill != "first-skill" {
				t.Errorf("expected deactivated skill 'first-skill', got %q", skill)
			}
			reason, _ := ev.Payload["reason"].(string)
			if reason != "replaced_by_new_skill" {
				t.Errorf("expected reason 'replaced_by_new_skill', got %q", reason)
			}
		}
	}
	if !foundDeactivation {
		t.Error("expected skill.constraint.deactivated event when replacing skill")
	}
}

// loopingTurnProvider is a provider whose turns always contain a tool call,
// forcing the runner to loop until a step limit is reached.
type loopingTurnProvider struct {
	calls int
}

func (p *loopingTurnProvider) Complete(_ context.Context, _ CompletionRequest) (CompletionResult, error) {
	p.calls++
	return CompletionResult{
		ToolCalls: []ToolCall{{
			ID:        fmt.Sprintf("call-%d", p.calls),
			Name:      "noop_tool",
			Arguments: `{}`,
		}},
	}, nil
}

// TestPerRunMaxSteps_OverridesConfig verifies that a per-run MaxSteps value
// takes precedence over the runner-level config.MaxSteps.
func TestPerRunMaxSteps_OverridesConfig(t *testing.T) {
	t.Parallel()

	// Provider always returns tool calls so the runner would loop until
	// the step limit is hit. Config allows 10 steps, but the per-run
	// request caps it at 3. A noop_tool is registered so tool calls
	// succeed and the runner loops back to the LLM.
	registry := NewRegistry()
	err := registry.Register(ToolDefinition{
		Name:        "noop_tool",
		Description: "does nothing",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("register noop_tool: %v", err)
	}

	prov := &loopingTurnProvider{}
	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     10,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:   "hello",
		MaxSteps: 3,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// Should have failed after 3 steps (per-run limit), not 10 (config)
	var llmTurns int
	for _, ev := range events {
		if ev.Type == EventLLMTurnRequested {
			llmTurns++
		}
	}
	if llmTurns != 3 {
		t.Fatalf("expected 3 LLM turns (per-run limit), got %d", llmTurns)
	}

	// Run should fail with "max steps reached" after 3 steps
	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("run not found")
	}
	if state.Status != RunStatusFailed {
		t.Fatalf("expected failed status, got %q", state.Status)
	}
	if !strings.Contains(state.Error, "max steps") {
		t.Fatalf("expected max steps error, got %q", state.Error)
	}
	// Verify the per-run limit (3) appears in the error, not the config limit (10)
	if !strings.Contains(state.Error, "3") {
		t.Fatalf("expected error to mention per-run limit 3, got %q", state.Error)
	}
}

// TestPerRunMaxSteps_ZeroFallsBackToConfig verifies that MaxSteps=0 in RunRequest
// falls back to the runner config limit (not unlimited).
func TestPerRunMaxSteps_ZeroFallsBackToConfig(t *testing.T) {
	t.Parallel()

	// Provider always returns tool calls so the runner loops until stopped.
	// Config MaxSteps=2, per-run MaxSteps=0 → should use config (2 steps).
	registry := NewRegistry()
	err := registry.Register(ToolDefinition{
		Name:        "noop_tool2",
		Description: "does nothing",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("register noop_tool2: %v", err)
	}

	prov := &loopingTurnProvider{}
	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2, // config limit
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:   "hello",
		MaxSteps: 0, // zero = use config
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	var llmTurns int
	for _, ev := range events {
		if ev.Type == EventLLMTurnRequested {
			llmTurns++
		}
	}
	// MaxSteps=0 in request means use config (2), so we get 2 LLM turns
	if llmTurns != 2 {
		t.Fatalf("expected 2 LLM turns (config limit), got %d", llmTurns)
	}
	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("run not found")
	}
	if state.Status != RunStatusFailed {
		t.Fatalf("expected failed status when config max steps reached, got %q", state.Status)
	}
	if !strings.Contains(state.Error, "max steps") {
		t.Fatalf("expected max steps error, got %q", state.Error)
	}
}

// TestConfigMaxSteps_ZeroMeansUnlimited verifies that MaxSteps=0 in
// RunnerConfig means unlimited — the run completes naturally without
// hitting any artificial step cap.
func TestConfigMaxSteps_ZeroMeansUnlimited(t *testing.T) {
	t.Parallel()

	// Provider returns 5 content turns and then an empty turn.
	// With unlimited steps the run should complete after the first
	// non-empty-content turn (the runner completes when it gets a
	// content-only response with no tool calls).
	provider := &stubProvider{turns: []CompletionResult{
		{Content: "done"},
	}}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     0, // unlimited
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("run not found")
	}
	// Should complete naturally, not fail with max steps
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected completed status with unlimited steps, got %q (error: %q)", state.Status, state.Error)
	}
	_ = events
}

// TestPerRunMaxSteps_NegativeIsInvalid verifies that a negative MaxSteps in
// RunRequest is rejected at StartRun time.
func TestPerRunMaxSteps_NegativeIsInvalid(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{{Content: "done"}}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     4,
	})

	_, err := runner.StartRun(RunRequest{
		Prompt:   "hello",
		MaxSteps: -1,
	})
	if err == nil {
		t.Fatal("expected error for negative MaxSteps, got nil")
	}
	if !strings.Contains(err.Error(), "max_steps") {
		t.Fatalf("expected error to mention max_steps, got %q", err.Error())
	}
}

// TestCostCeiling_RunCompletesWhenCeilingExceeded verifies that when max_cost_usd
// is set and the cumulative cost exceeds it after an LLM turn, the run is
// terminated with a run.cost_limit_reached event and the run status is "completed"
// (not "failed").
func TestCostCeiling_RunCompletesWhenCeilingExceeded(t *testing.T) {
	t.Parallel()

	// Two turns, each costing $0.002. The ceiling is $0.003, so after the first
	// turn ($0.002 total) the limit is not reached; after the second turn
	// ($0.004 total) the limit is exceeded and the run should stop.
	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls:  []ToolCall{{ID: "call-1", Name: "echo_json", Arguments: `{}`}},
			CostUSD:    floatPtr(0.002),
			CostStatus: CostStatusAvailable,
			Cost:       &CompletionCost{TotalUSD: 0.002},
		},
		{
			ToolCalls:  []ToolCall{{ID: "call-2", Name: "echo_json", Arguments: `{}`}},
			CostUSD:    floatPtr(0.002),
			CostStatus: CostStatusAvailable,
			Cost:       &CompletionCost{TotalUSD: 0.002},
		},
		// This turn should never be reached.
		{Content: "unreachable"},
	}}

	registry := NewRegistry()
	if err := registry.Register(ToolDefinition{
		Name:        "echo_json",
		Description: "echoes payload",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{}`, nil
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     10, // plenty of steps; cost should stop it first
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:     "hello",
		MaxCostUSD: 0.003,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// Find the cost_limit_reached event.
	var costLimitEvent *Event
	var terminalEvent *Event
	for i := range events {
		ev := &events[i]
		if ev.Type == EventRunCostLimitReached {
			costLimitEvent = ev
		}
		if IsTerminalEvent(ev.Type) {
			terminalEvent = ev
		}
	}

	if costLimitEvent == nil {
		t.Fatal("expected run.cost_limit_reached event, got none")
	}
	if terminalEvent == nil {
		t.Fatal("expected a terminal event, got none")
	}
	// The run should complete (not fail) when hitting the cost ceiling.
	if terminalEvent.Type != EventRunCompleted {
		t.Fatalf("expected run.completed as terminal event, got %q", terminalEvent.Type)
	}

	// Verify the cost_limit_reached payload contains useful info.
	payload := costLimitEvent.Payload
	if payload["max_cost_usd"] == nil {
		t.Errorf("expected max_cost_usd in cost_limit_reached payload")
	}
	if payload["cumulative_cost_usd"] == nil {
		t.Errorf("expected cumulative_cost_usd in cost_limit_reached payload")
	}

	// The run state should reflect completed status.
	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("expected run state")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected run status completed, got %q", state.Status)
	}
	// Provider should have been called exactly twice (cost limit hit after turn 2).
	if provider.calls != 2 {
		t.Fatalf("expected 2 provider calls, got %d", provider.calls)
	}
}

// TestCostCeiling_NegativeIsInvalid verifies that a negative max_cost_usd is
// rejected at StartRun time.
func TestCostCeiling_NegativeIsInvalid(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{{Content: "done"}}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     4,
	})

	_, err := runner.StartRun(RunRequest{
		Prompt:     "hello",
		MaxCostUSD: -0.01,
	})
	if err == nil {
		t.Fatal("expected error for negative MaxCostUSD, got nil")
	}
	if !strings.Contains(err.Error(), "max_cost_usd") {
		t.Fatalf("expected error to mention max_cost_usd, got %q", err.Error())
	}
}

// TestCostCeiling_ZeroMeansUnlimited verifies that max_cost_usd=0 (the default)
// means no cost ceiling is applied.
func TestCostCeiling_ZeroMeansUnlimited(t *testing.T) {
	t.Parallel()

	// Three turns, each costing $1.00. No cost ceiling — run completes normally.
	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls:  []ToolCall{{ID: "c1", Name: "echo_json", Arguments: `{}`}},
			CostUSD:    floatPtr(1.00),
			CostStatus: CostStatusAvailable,
			Cost:       &CompletionCost{TotalUSD: 1.00},
		},
		{
			ToolCalls:  []ToolCall{{ID: "c2", Name: "echo_json", Arguments: `{}`}},
			CostUSD:    floatPtr(1.00),
			CostStatus: CostStatusAvailable,
			Cost:       &CompletionCost{TotalUSD: 1.00},
		},
		{
			Content:    "done",
			CostUSD:    floatPtr(1.00),
			CostStatus: CostStatusAvailable,
			Cost:       &CompletionCost{TotalUSD: 1.00},
		},
	}}

	registry := NewRegistry()
	if err := registry.Register(ToolDefinition{
		Name:        "echo_json",
		Description: "echoes payload",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{}`, nil
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     10,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:     "hello",
		MaxCostUSD: 0, // explicitly zero = unlimited
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// Must NOT see a cost_limit_reached event.
	for _, ev := range events {
		if ev.Type == EventRunCostLimitReached {
			t.Fatal("unexpected run.cost_limit_reached event when max_cost_usd=0")
		}
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("expected run state")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected run status completed, got %q", state.Status)
	}
}

// TestCostCeiling_UnpricedModelDoesNotTrigger verifies that when the model's
// cost is unknown (CostStatusUnpricedModel), the cost ceiling is never tripped
// even if max_cost_usd is set.
func TestCostCeiling_UnpricedModelDoesNotTrigger(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{turns: []CompletionResult{
		{
			Content:    "done",
			CostStatus: CostStatusUnpricedModel,
			// No CostUSD set — pricing unavailable.
		},
	}}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     4,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:     "hello",
		MaxCostUSD: 0.001, // very low ceiling; but cost is unpriced
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// Must NOT see a cost_limit_reached event when pricing is unavailable.
	for _, ev := range events {
		if ev.Type == EventRunCostLimitReached {
			t.Fatal("unexpected run.cost_limit_reached event for unpriced model")
		}
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("expected run state")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected run status completed, got %q", state.Status)
	}
}

// TestCostCeiling_CeilingAtExactBoundary verifies that a ceiling is triggered
// when cumulative cost exactly equals max_cost_usd (>= comparison).
func TestCostCeiling_CeilingAtExactBoundary(t *testing.T) {
	t.Parallel()

	// Each turn costs exactly $0.005. Ceiling is $0.005.
	// After step 1: total = 0.005 >= 0.005 → should stop.
	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls:  []ToolCall{{ID: "c1", Name: "echo_json", Arguments: `{}`}},
			CostUSD:    floatPtr(0.005),
			CostStatus: CostStatusAvailable,
			Cost:       &CompletionCost{TotalUSD: 0.005},
		},
		// This turn must not be reached.
		{Content: "unreachable"},
	}}

	registry := NewRegistry()
	if err := registry.Register(ToolDefinition{
		Name:        "echo_json",
		Description: "echoes payload",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{}`, nil
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     10,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:     "hello",
		MaxCostUSD: 0.005,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	var sawCostLimit bool
	for _, ev := range events {
		if ev.Type == EventRunCostLimitReached {
			sawCostLimit = true
		}
	}
	if !sawCostLimit {
		t.Fatal("expected run.cost_limit_reached event at exact boundary, got none")
	}
	if provider.calls != 1 {
		t.Fatalf("expected exactly 1 provider call, got %d", provider.calls)
	}
}

// TestToolOutputDeltaEvents verifies that a tool which uses OutputStreamerFromContext
// causes the runner to emit tool.output.delta events for each streamed chunk.
func TestToolOutputDeltaEvents(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	err := registry.Register(ToolDefinition{
		Name:        "streaming_tool",
		Description: "a tool that streams output chunks",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}, func(ctx context.Context, _ json.RawMessage) (string, error) {
		streamer, ok := htools.OutputStreamerFromContext(ctx)
		if ok {
			streamer("chunk-one\n")
			streamer("chunk-two\n")
		}
		return `{"done":true}`, nil
	})
	if err != nil {
		t.Fatalf("register tool: %v", err)
	}

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-stream-1",
				Name:      "streaming_tool",
				Arguments: `{}`,
			}},
		},
		{Content: "All done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     4,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Stream something"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// Verify event ordering: tool.call.started before tool.output.delta before tool.call.completed.
	requireEventOrder(t, events,
		"run.started",
		"tool.call.started",
		"tool.output.delta",
		"tool.call.completed",
		"run.completed",
	)

	// Collect all tool.output.delta events and verify their contents.
	var deltaContents []string
	for _, ev := range events {
		if ev.Type != EventToolOutputDelta {
			continue
		}
		content, _ := ev.Payload["content"].(string)
		deltaContents = append(deltaContents, content)
		// Every delta must carry the call_id and tool name.
		callID, _ := ev.Payload["call_id"].(string)
		if callID != "call-stream-1" {
			t.Errorf("tool.output.delta missing or wrong call_id: %v", ev.Payload)
		}
		toolName, _ := ev.Payload["tool"].(string)
		if toolName != "streaming_tool" {
			t.Errorf("tool.output.delta missing or wrong tool: %v", ev.Payload)
		}
	}

	if !slices.Equal(deltaContents, []string{"chunk-one\n", "chunk-two\n"}) {
		t.Fatalf("unexpected tool.output.delta content sequence: %v", deltaContents)
	}
}

// TestToolOutputDeltaAbsentWhenToolDoesNotStream verifies that no tool.output.delta
// events are emitted when the tool does not call the streamer.
func TestToolOutputDeltaAbsentWhenToolDoesNotStream(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	err := registry.Register(ToolDefinition{
		Name:        "silent_tool",
		Description: "a tool that does not stream",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"done":true}`, nil
	})
	if err != nil {
		t.Fatalf("register tool: %v", err)
	}

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-silent-1",
				Name:      "silent_tool",
				Arguments: `{}`,
			}},
		},
		{Content: "Done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     4,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "No streaming"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	for _, ev := range events {
		if ev.Type == EventToolOutputDelta {
			t.Fatalf("unexpected tool.output.delta event for non-streaming tool: %v", ev)
		}
	}
}

// TestAllEventTypesIncludesToolOutputDelta verifies that EventToolOutputDelta
// is registered in the AllEventTypes list.
func TestAllEventTypesIncludesToolOutputDelta(t *testing.T) {
	t.Parallel()

	found := false
	for _, et := range AllEventTypes() {
		if et == EventToolOutputDelta {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("EventToolOutputDelta missing from AllEventTypes()")
	}
}

// TestToolOutputDeltaStreamIndex verifies that stream_index increments monotonically
// across tool.output.delta events for a single tool call.
func TestToolOutputDeltaStreamIndex(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	err := registry.Register(ToolDefinition{
		Name:        "multi_chunk_tool",
		Description: "a tool that emits 4 chunks",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}, func(ctx context.Context, _ json.RawMessage) (string, error) {
		streamer, ok := htools.OutputStreamerFromContext(ctx)
		if ok {
			streamer("alpha\n")
			streamer("beta\n")
			streamer("gamma\n")
			streamer("delta\n")
		}
		return `{"done":true}`, nil
	})
	if err != nil {
		t.Fatalf("register tool: %v", err)
	}

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-index-test",
				Name:      "multi_chunk_tool",
				Arguments: `{}`,
			}},
		},
		{Content: "Done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     4,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Test stream_index"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// Collect tool.output.delta events in order.
	var deltas []Event
	for _, ev := range events {
		if ev.Type == EventToolOutputDelta {
			deltas = append(deltas, ev)
		}
	}

	if len(deltas) != 4 {
		t.Fatalf("expected 4 tool.output.delta events, got %d", len(deltas))
	}

	// Verify stream_index increments from 0 to 3.
	expectedContents := []string{"alpha\n", "beta\n", "gamma\n", "delta\n"}
	for i, ev := range deltas {
		idx, ok := ev.Payload["stream_index"]
		if !ok {
			t.Fatalf("delta event %d missing stream_index field: %v", i, ev.Payload)
		}
		// stream_index is stored as int in the in-process event map.
		idxInt, ok := idx.(int)
		if !ok {
			t.Fatalf("delta event %d: stream_index is %T (value %v), want int", i, idx, idx)
		}
		if idxInt != i {
			t.Errorf("delta event %d: stream_index = %d, want %d", i, idxInt, i)
		}
		content, _ := ev.Payload["content"].(string)
		if content != expectedContents[i] {
			t.Errorf("delta event %d: content = %q, want %q", i, content, expectedContents[i])
		}
	}
}

// TestToolOutputDeltaStreamIndexResetsPerCall verifies that stream_index resets to 0
// for each independent tool call within the same run.
func TestToolOutputDeltaStreamIndexResetsPerCall(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	callCount := 0
	err := registry.Register(ToolDefinition{
		Name:        "resetting_tool",
		Description: "a tool that streams on each invocation",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}, func(ctx context.Context, _ json.RawMessage) (string, error) {
		callCount++
		streamer, ok := htools.OutputStreamerFromContext(ctx)
		if ok {
			streamer("first\n")
			streamer("second\n")
		}
		return `{"call":true}`, nil
	})
	if err != nil {
		t.Fatalf("register tool: %v", err)
	}

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{
				{ID: "call-A", Name: "resetting_tool", Arguments: `{}`},
			},
		},
		{
			ToolCalls: []ToolCall{
				{ID: "call-B", Name: "resetting_tool", Arguments: `{}`},
			},
		},
		{Content: "Done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     6,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Test index reset"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// Collect deltas grouped by call_id.
	callADeltas := []Event{}
	callBDeltas := []Event{}
	for _, ev := range events {
		if ev.Type != EventToolOutputDelta {
			continue
		}
		callID, _ := ev.Payload["call_id"].(string)
		switch callID {
		case "call-A":
			callADeltas = append(callADeltas, ev)
		case "call-B":
			callBDeltas = append(callBDeltas, ev)
		}
	}

	if len(callADeltas) != 2 {
		t.Fatalf("expected 2 deltas for call-A, got %d", len(callADeltas))
	}
	if len(callBDeltas) != 2 {
		t.Fatalf("expected 2 deltas for call-B, got %d", len(callBDeltas))
	}

	// Verify each call's stream_index starts at 0 and increments.
	for i, ev := range callADeltas {
		idx, _ := ev.Payload["stream_index"].(int)
		if idx != i {
			t.Errorf("call-A delta %d: stream_index = %d, want %d", i, idx, i)
		}
	}
	for i, ev := range callBDeltas {
		idx, _ := ev.Payload["stream_index"].(int)
		if idx != i {
			t.Errorf("call-B delta %d: stream_index = %d, want %d", i, idx, i)
		}
	}
}

// ---------------------------------------------------------------------------
// resolveProvider with ProviderName tests
// ---------------------------------------------------------------------------

// namedProvider is a test Provider that records which provider name it was created for.
type namedProvider struct {
	name   string
	result CompletionResult
}

func (n *namedProvider) Complete(_ context.Context, req CompletionRequest) (CompletionResult, error) {
	return n.result, nil
}

// TestResolveProviderByName verifies that when RunRequest.ProviderName is set,
// the runner routes to that specific provider from the registry.
func TestResolveProviderByName(t *testing.T) {
	t.Parallel()

	// Build a catalog with two providers.
	cat := &catalog.Catalog{
		Providers: map[string]catalog.ProviderEntry{
			"openai": {
				DisplayName: "OpenAI",
				APIKeyEnv:   "OPENAI_API_KEY",
				Models: map[string]catalog.Model{
					"gpt-4.1": {DisplayName: "GPT-4.1", ContextWindow: 128000},
				},
			},
			"codex": {
				DisplayName: "Codex",
				APIKeyEnv:   "CODEX_API_KEY",
				Models: map[string]catalog.Model{
					"gpt-5.3-codex": {DisplayName: "GPT-5.3 Codex", ContextWindow: 128000, API: "responses"},
				},
			},
		},
	}

	codexProvider := &namedProvider{name: "codex", result: CompletionResult{Content: "codex response"}}
	defaultProvider := &namedProvider{name: "default", result: CompletionResult{Content: "default response"}}

	registry := catalog.NewProviderRegistryWithEnv(cat, func(key string) string {
		if key == "CODEX_API_KEY" {
			return "fake-codex-key"
		}
		if key == "OPENAI_API_KEY" {
			return "fake-openai-key"
		}
		return ""
	})
	registry.SetClientFactory(func(apiKey, baseURL, providerName string) (catalog.ProviderClient, error) {
		if providerName == "codex" {
			return codexProvider, nil
		}
		return defaultProvider, nil
	})

	runner := NewRunner(defaultProvider, NewRegistry(), RunnerConfig{
		DefaultModel:     "gpt-4.1-mini",
		MaxSteps:         2,
		ProviderRegistry: registry,
	})

	// Start a run with ProviderName = "codex".
	run, err := runner.StartRun(RunRequest{
		Prompt:       "hello codex",
		ProviderName: "codex",
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.ProviderName != "codex" {
		t.Errorf("expected provider_name=codex, got %q", state.ProviderName)
	}
}

// TestResolveProviderByNameNotFound verifies that when the specified provider is not in
// the registry and AllowFallback is false, the run fails with a descriptive error.
func TestResolveProviderByNameNotFound(t *testing.T) {
	t.Parallel()

	cat := &catalog.Catalog{
		Providers: map[string]catalog.ProviderEntry{
			"openai": {
				DisplayName: "OpenAI",
				APIKeyEnv:   "OPENAI_API_KEY",
				Models:      map[string]catalog.Model{"gpt-4.1": {ContextWindow: 128000}},
			},
		},
	}

	registry := catalog.NewProviderRegistryWithEnv(cat, func(key string) string {
		if key == "OPENAI_API_KEY" {
			return "fake-key"
		}
		return ""
	})
	defaultProvider := &namedProvider{name: "default", result: CompletionResult{Content: "ok"}}
	registry.SetClientFactory(func(apiKey, baseURL, providerName string) (catalog.ProviderClient, error) {
		return defaultProvider, nil
	})

	runner := NewRunner(defaultProvider, NewRegistry(), RunnerConfig{
		DefaultModel:     "gpt-4.1-mini",
		MaxSteps:         2,
		ProviderRegistry: registry,
	})

	// Request a provider that doesn't exist, with AllowFallback=false.
	run, err := runner.StartRun(RunRequest{
		Prompt:        "fail me",
		ProviderName:  "nonexistent",
		AllowFallback: false,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// Verify the run failed.
	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Status != RunStatusFailed {
		t.Errorf("expected failed status, got %q", state.Status)
	}
	_ = events
}

// TestResolveProviderByNameFallback verifies that when the specified provider is not in
// the registry and AllowFallback is true, the run falls back to the default provider.
func TestResolveProviderByNameFallback(t *testing.T) {
	t.Parallel()

	cat := &catalog.Catalog{
		Providers: map[string]catalog.ProviderEntry{
			"openai": {
				DisplayName: "OpenAI",
				APIKeyEnv:   "OPENAI_API_KEY",
				Models:      map[string]catalog.Model{"gpt-4.1": {ContextWindow: 128000}},
			},
		},
	}

	registry := catalog.NewProviderRegistryWithEnv(cat, func(key string) string {
		if key == "OPENAI_API_KEY" {
			return "fake-key"
		}
		return ""
	})
	defaultProvider := &namedProvider{name: "default", result: CompletionResult{Content: "fallback response"}}
	registry.SetClientFactory(func(apiKey, baseURL, providerName string) (catalog.ProviderClient, error) {
		return defaultProvider, nil
	})

	runner := NewRunner(defaultProvider, NewRegistry(), RunnerConfig{
		DefaultModel:     "gpt-4.1-mini",
		MaxSteps:         2,
		ProviderRegistry: registry,
	})

	// Request a provider that doesn't exist, with AllowFallback=true — should fall back.
	run, err := runner.StartRun(RunRequest{
		Prompt:        "try fallback",
		ProviderName:  "nonexistent",
		AllowFallback: true,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	// Should have completed (not failed) because fallback to default provider succeeded.
	if state.Status != RunStatusCompleted {
		t.Errorf("expected completed status, got %q (error: %q)", state.Status, state.Error)
	}
}

// ---------------------------------------------------------------------------
// CanonicalModelForProvider regression tests (issue #574)
// ---------------------------------------------------------------------------

// TestRunnerCanonicalizesModelForDirectProvider verifies that when a run request
// specifies a preferred direct (non-OpenRouter) provider and the model is an
// OpenRouter-qualified slug, the runner canonicalizes the model by stripping the
// OpenRouter provider prefix before storing it in the run state.
func TestRunnerCanonicalizesModelForDirectProvider(t *testing.T) {
	t.Parallel()

	cat := &catalog.Catalog{
		Providers: map[string]catalog.ProviderEntry{
			"deepseek": {
				DisplayName: "DeepSeek",
				APIKeyEnv:   "DEEPSEEK_API_KEY",
				Models: map[string]catalog.Model{
					"deepseek-v4-flash": {DisplayName: "DeepSeek V4 Flash", ContextWindow: 64000},
				},
			},
		},
	}

	deepseekProvider := &namedProvider{name: "deepseek", result: CompletionResult{Content: "deepseek response"}}

	registry := catalog.NewProviderRegistryWithEnv(cat, func(key string) string {
		if key == "DEEPSEEK_API_KEY" {
			return "fake-ds-key"
		}
		return ""
	})
	registry.SetClientFactory(func(apiKey, baseURL, providerName string) (catalog.ProviderClient, error) {
		return deepseekProvider, nil
	})

	defaultProvider := &namedProvider{name: "default", result: CompletionResult{Content: "ok"}}
	runner := NewRunner(defaultProvider, NewRegistry(), RunnerConfig{
		DefaultModel:     "gpt-4.1-mini",
		MaxSteps:         2,
		ProviderRegistry: registry,
	})

	// Run with an OpenRouter-qualified deepseek slug and a preferred deepseek provider.
	run, err := runner.StartRun(RunRequest{
		Prompt:       "use deepseek v4 flash",
		Model:        "deepseek/deepseek-v4-flash",
		ProviderName: "deepseek",
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	// The model should have been canonicalized to the native ID.
	if state.Model != "deepseek-v4-flash" {
		t.Errorf("expected canonical model deepseek-v4-flash, got %q", state.Model)
	}
	if state.ProviderName != "deepseek" {
		t.Errorf("expected provider_name deepseek, got %q", state.ProviderName)
	}
}

// TestRunnerPreservesOpenRouterSlugForOpenRouterProvider verifies that when the
// preferred provider IS OpenRouter, the OpenRouter-qualified slug is preserved.
func TestRunnerPreservesOpenRouterSlugForOpenRouterProvider(t *testing.T) {
	t.Parallel()

	cat := &catalog.Catalog{
		Providers: map[string]catalog.ProviderEntry{
			"openrouter": {
				DisplayName: "OpenRouter",
				APIKeyEnv:   "OPENROUTER_API_KEY",
				Models: map[string]catalog.Model{
					"deepseek/deepseek-v4-flash": {DisplayName: "DeepSeek V4 Flash (via OR)", ContextWindow: 64000},
				},
			},
		},
	}

	orProvider := &namedProvider{name: "openrouter", result: CompletionResult{Content: "or response"}}

	registry := catalog.NewProviderRegistryWithEnv(cat, func(key string) string {
		if key == "OPENROUTER_API_KEY" {
			return "fake-or-key"
		}
		return ""
	})
	registry.SetClientFactory(func(apiKey, baseURL, providerName string) (catalog.ProviderClient, error) {
		return orProvider, nil
	})

	defaultProvider := &namedProvider{name: "default", result: CompletionResult{Content: "ok"}}
	runner := NewRunner(defaultProvider, NewRegistry(), RunnerConfig{
		DefaultModel:     "gpt-4.1-mini",
		MaxSteps:         2,
		ProviderRegistry: registry,
	})

	// The OpenRouter slug must be preserved when routing to openrouter.
	run, err := runner.StartRun(RunRequest{
		Prompt:       "use deepseek v4 flash via openrouter",
		Model:        "deepseek/deepseek-v4-flash",
		ProviderName: "openrouter",
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	// The OpenRouter slug must remain intact.
	if state.Model != "deepseek/deepseek-v4-flash" {
		t.Errorf("expected openrouter slug preserved, got %q", state.Model)
	}
}

// TestRunnerCanonicalizesModelForXAIProvider verifies x-ai prefix is resolved for xai.
func TestRunnerCanonicalizesModelForXAIProvider(t *testing.T) {
	t.Parallel()

	cat := &catalog.Catalog{
		Providers: map[string]catalog.ProviderEntry{
			"xai": {
				DisplayName: "xAI",
				APIKeyEnv:   "XAI_API_KEY",
				Models: map[string]catalog.Model{
					"grok-3-mini": {DisplayName: "Grok 3 Mini", ContextWindow: 131072},
				},
			},
		},
	}

	xaiProvider := &namedProvider{name: "xai", result: CompletionResult{Content: "xai response"}}

	registry := catalog.NewProviderRegistryWithEnv(cat, func(key string) string {
		if key == "XAI_API_KEY" {
			return "fake-xai-key"
		}
		return ""
	})
	registry.SetClientFactory(func(apiKey, baseURL, providerName string) (catalog.ProviderClient, error) {
		return xaiProvider, nil
	})

	defaultProvider := &namedProvider{name: "default", result: CompletionResult{Content: "ok"}}
	runner := NewRunner(defaultProvider, NewRegistry(), RunnerConfig{
		DefaultModel:     "gpt-4.1-mini",
		MaxSteps:         2,
		ProviderRegistry: registry,
	})

	// x-ai prefix should map to xai provider.
	run, err := runner.StartRun(RunRequest{
		Prompt:       "use grok",
		Model:        "x-ai/grok-3-mini",
		ProviderName: "xai",
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatalf("expected run state")
	}
	if state.Model != "grok-3-mini" {
		t.Errorf("expected canonical model grok-3-mini, got %q", state.Model)
	}
}

// ---------------------------------------------------------------------------
// Regression tests for issue #230: channel-based recorder goroutine
// ---------------------------------------------------------------------------

// TestRecorderGoroutine_NilWhenNoRolloutDir verifies that when no RolloutDir
// is set, state.recorderCh and state.recorderDone remain nil (no goroutine
// is spawned).  We check after the run completes to avoid racing with emit().
func TestRecorderGoroutine_NilWhenNoRolloutDir(t *testing.T) {
	t.Parallel()
	prov := &stubProvider{turns: []CompletionResult{{Content: "done"}}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     1,
		// No RolloutDir set.
	})
	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	// Wait for run to complete before reading state fields to avoid racing with execute().
	if _, err := collectRunEvents(t, runner, run.ID); err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}
	runner.mu.RLock()
	state := runner.runs[run.ID]
	var recCh chan rollout.RecordableEvent
	var recDone chan struct{}
	var closeOnce func()
	if state != nil {
		recCh = state.recorderCh
		recDone = state.recorderDone
		closeOnce = state.closeRecorderOnce
	}
	runner.mu.RUnlock()
	if state == nil {
		t.Fatal("run state not found")
	}
	if recCh != nil {
		t.Error("recorderCh should be nil when no RolloutDir is configured")
	}
	if recDone != nil {
		t.Error("recorderDone should be nil when no RolloutDir is configured")
	}
	if closeOnce != nil {
		t.Error("closeRecorderOnce should be nil when no RolloutDir is configured")
	}
}

// TestRecorderGoroutine_DoneClosedAfterRun verifies that recorderDone is
// closed (goroutine exits) once the run reaches a terminal event.
func TestRecorderGoroutine_DoneClosedAfterRun(t *testing.T) {
	t.Parallel()
	rolloutDir := t.TempDir()
	release := make(chan struct{})
	prov := &blockingProvider{blocker: release}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     2,
		RolloutDir:   rolloutDir,
	})
	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Capture recorderDone under lock before execute() can nil it out.
	// Poll briefly: execute() may not have set it yet if the goroutine is fast.
	var recDone chan struct{}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runner.mu.RLock()
		state := runner.runs[run.ID]
		if state != nil {
			recDone = state.recorderDone
		}
		runner.mu.RUnlock()
		if recDone != nil {
			break
		}
		runtime.Gosched()
	}
	if recDone == nil {
		t.Fatal("recorderDone should be non-nil when RolloutDir is set; never saw it")
	}

	close(release)

	// Wait for run to complete.
	if _, err = collectRunEvents(t, runner, run.ID); err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	// recorderDone must be closed within a reasonable time.
	select {
	case <-recDone:
		// Good: goroutine exited.
	case <-time.After(5 * time.Second):
		t.Fatal("recorder goroutine did not exit within timeout after terminal event")
	}
}

// TestRecorderGoroutine_RaceWithConcurrentEmit verifies that concurrent
// emit calls around the terminal event do not trigger data races.  This is
// the core regression test for issue #230.
func TestRecorderGoroutine_RaceWithConcurrentEmit(t *testing.T) {
	t.Parallel()
	rolloutDir := t.TempDir()
	// Use a provider that immediately returns so the run transitions to
	// terminal quickly; hammering emits concurrently stresses the race window.
	prov := &stubProvider{turns: []CompletionResult{{Content: "done"}}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     1,
		RolloutDir:   rolloutDir,
	})

	const parallelRuns = 10
	var wg sync.WaitGroup
	for i := 0; i < parallelRuns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, err := runner.StartRun(RunRequest{Prompt: "concurrent"})
			if err != nil {
				t.Errorf("StartRun: %v", err)
				return
			}
			if _, err := collectRunEvents(t, runner, run.ID); err != nil {
				t.Errorf("collectRunEvents for %s: %v", run.ID, err)
			}
		}()
	}
	wg.Wait()
}

// TestRecorderGoroutine_NoLeakAfterTerminal verifies that the recorder
// goroutine does not remain live after the run completes.  We measure the
// goroutine count before and after to detect leaks.
func TestRecorderGoroutine_NoLeakAfterTerminal(t *testing.T) {
	t.Parallel()
	rolloutDir := t.TempDir()
	prov := &stubProvider{turns: []CompletionResult{{Content: "done"}}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     2,
		RolloutDir:   rolloutDir,
	})

	before := runtime.NumGoroutine()

	run, err := runner.StartRun(RunRequest{Prompt: "leak test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Capture recorderDone under lock before execute() zeros the fields.
	// Poll briefly in case execute() hasn't started yet.
	var recDone chan struct{}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runner.mu.RLock()
		state := runner.runs[run.ID]
		if state != nil {
			recDone = state.recorderDone
		}
		runner.mu.RUnlock()
		if recDone != nil {
			break
		}
		runtime.Gosched()
	}

	if _, err := collectRunEvents(t, runner, run.ID); err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	if recDone != nil {
		select {
		case <-recDone:
		case <-time.After(5 * time.Second):
			t.Fatal("recorder goroutine did not exit within timeout")
		}
	}

	// Allow a brief window for goroutine cleanup.
	gcDeadline := time.Now().Add(2 * time.Second)
	var after int
	for time.Now().Before(gcDeadline) {
		after = runtime.NumGoroutine()
		if after <= before+2 { // +2 tolerance for test infra goroutines
			break
		}
		runtime.Gosched()
	}
	if after > before+2 {
		t.Errorf("goroutine count after run: %d (was %d before), possible leak", after, before)
	}
}

// TestSafeRecorderSend_ChannelFull verifies that safeRecorderSend returns
// false when the channel is full and does not block.
func TestSafeRecorderSend_ChannelFull(t *testing.T) {
	t.Parallel()
	ch := make(chan rollout.RecordableEvent, 1)
	// Fill the channel.
	ev := rollout.RecordableEvent{ID: "test:0", RunID: "test", Type: "run.started"}
	if !safeRecorderSend(ch, ev) {
		t.Fatal("first send to empty channel should return true")
	}
	// Channel is now full; second send should return false.
	if safeRecorderSend(ch, ev) {
		t.Fatal("send to full channel should return false")
	}
}

// TestSafeRecorderSend_ClosedChannel verifies that safeRecorderSend returns
// false on a closed channel without panicking.
func TestSafeRecorderSend_ClosedChannel(t *testing.T) {
	t.Parallel()
	ch := make(chan rollout.RecordableEvent, 1)
	close(ch)
	ev := rollout.RecordableEvent{ID: "test:0", RunID: "test", Type: "run.started"}
	if safeRecorderSend(ch, ev) {
		t.Fatal("send to closed channel should return false")
	}
}

// TestStartRecorderGoroutine_DrainOnClose verifies that after closeRecorderOnce
// is called the goroutine drains all buffered events and then closes recorderDone.
func TestStartRecorderGoroutine_DrainOnClose(t *testing.T) {
	t.Parallel()
	rolloutDir := t.TempDir()
	rec, err := rollout.NewRecorder(rollout.RecorderConfig{Dir: rolloutDir, RunID: "test-drain"})
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	state := &runState{}
	startRecorderGoroutine(state, rec)

	// Send several events before closing.
	for i := 0; i < 5; i++ {
		ev := rollout.RecordableEvent{
			ID:        fmt.Sprintf("test-drain:%d", i),
			RunID:     "test-drain",
			Type:      "run.started",
			Timestamp: time.Now().UTC(),
			Seq:       uint64(i),
		}
		state.recorderCh <- ev
	}
	state.closeRecorderOnce()

	// recorderDone must close after all events are drained.
	select {
	case <-state.recorderDone:
	case <-time.After(5 * time.Second):
		t.Fatal("recorderDone did not close after closeRecorderOnce()")
	}

	// The JSONL file should have been written.
	dateDir := filepath.Join(rolloutDir, time.Now().UTC().Format("2006-01-02"))
	jsonlPath := filepath.Join(dateDir, "test-drain.jsonl")
	data, err := os.ReadFile(jsonlPath)
	if err != nil {
		t.Fatalf("read rollout file: %v", err)
	}
	if len(data) == 0 {
		t.Error("rollout file is empty after draining 5 events")
	}
}

// TestStartRecorderGoroutine_CloseIdempotent verifies that calling
// closeRecorderOnce multiple times does not panic and that recorderDone
// is closed exactly once.
func TestStartRecorderGoroutine_CloseIdempotent(t *testing.T) {
	t.Parallel()
	rolloutDir := t.TempDir()
	rec, err := rollout.NewRecorder(rollout.RecorderConfig{Dir: rolloutDir, RunID: "test-idem"})
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	state := &runState{}
	startRecorderGoroutine(state, rec)

	// Call closeRecorderOnce many times concurrently; none should panic.
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state.closeRecorderOnce()
		}()
	}
	wg.Wait()

	select {
	case <-state.recorderDone:
	case <-time.After(3 * time.Second):
		t.Fatal("recorderDone did not close after concurrent closeRecorderOnce calls")
	}
}

// TestRecorderGoroutine_JSONLWrittenAfterRun is an integration test verifying
// that events are actually flushed to the JSONL file when RolloutDir is set.
func TestRecorderGoroutine_JSONLWrittenAfterRun(t *testing.T) {
	t.Parallel()
	rolloutDir := t.TempDir()
	prov := &stubProvider{turns: []CompletionResult{{Content: "hello world"}}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     2,
		RolloutDir:   rolloutDir,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "write test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no events collected")
	}

	// The JSONL file should exist and be non-empty after the run completes.
	// Poll briefly in case the recorder goroutine is still flushing.
	dateDir := filepath.Join(rolloutDir, time.Now().UTC().Format("2006-01-02"))
	jsonlPath := filepath.Join(dateDir, run.ID+".jsonl")

	deadline := time.Now().Add(5 * time.Second)
	var fileData []byte
	for time.Now().Before(deadline) {
		fileData, err = os.ReadFile(jsonlPath)
		if err == nil && len(fileData) > 0 {
			break
		}
		runtime.Gosched()
	}
	if err != nil {
		t.Fatalf("rollout file not created at %s: %v", jsonlPath, err)
	}
	if len(fileData) == 0 {
		t.Fatal("rollout JSONL file is empty after run completed")
	}
}
