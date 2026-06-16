package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	htools "go-agent-harness/internal/harness/tools"
)

// ---------- Meta-message injection tests ----------

func TestRunnerInjectsMetaMessages(t *testing.T) {
	t.Parallel()

	// Register a tool that returns an enriched result with meta-messages
	registry := NewRegistry()
	err := registry.Register(ToolDefinition{
		Name:        "meta_tool",
		Description: "returns enriched result",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		tr := htools.ToolResult{
			Output: `{"status":"activated"}`,
			MetaMessages: []htools.MetaMessage{
				{Content: "Hidden instruction from tool"},
			},
		}
		return htools.WrapToolResult(tr)
	})
	if err != nil {
		t.Fatalf("register tool: %v", err)
	}

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-1",
				Name:      "meta_tool",
				Arguments: `{}`,
			}},
		},
		{Content: "Done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     100,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Test meta"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// Verify run completed
	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("run not found")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected completed, got %s", state.Status)
	}

	// Verify meta-message was injected into the conversation messages
	runner.mu.RLock()
	runState := runner.runs[run.ID]
	messages := append([]Message(nil), runState.messages...)
	runner.mu.RUnlock()

	foundMeta := false
	for _, msg := range messages {
		if msg.IsMeta {
			foundMeta = true
			if msg.Role != "system" {
				t.Errorf("expected meta-message role=system, got %s", msg.Role)
			}
			if msg.Content != "Hidden instruction from tool" {
				t.Errorf("unexpected meta-message content: %s", msg.Content)
			}
		}
	}
	if !foundMeta {
		t.Fatal("no meta-message found in conversation messages")
	}

	// Verify the EventMetaMessageInjected event was emitted
	foundEvent := false
	for _, ev := range events {
		if ev.Type == EventMetaMessageInjected {
			foundEvent = true
			if ev.Payload["tool"] != "meta_tool" {
				t.Errorf("expected tool=meta_tool, got %v", ev.Payload["tool"])
			}
			if ev.Payload["call_id"] != "call-1" {
				t.Errorf("expected call_id=call-1, got %v", ev.Payload["call_id"])
			}
			length, _ := ev.Payload["length"].(int)
			if length != len("Hidden instruction from tool") {
				t.Errorf("expected length=%d, got %d", len("Hidden instruction from tool"), length)
			}
		}
	}
	if !foundEvent {
		t.Fatal("EventMetaMessageInjected not found in events")
	}
}

func TestRunnerMetaMessageNotInUserTranscript(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	err := registry.Register(ToolDefinition{
		Name:        "meta_tool",
		Description: "returns enriched result",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		tr := htools.ToolResult{
			Output: `{"status":"ok"}`,
			MetaMessages: []htools.MetaMessage{
				{Content: "Secret instructions hidden from user"},
			},
		}
		return htools.WrapToolResult(tr)
	})
	if err != nil {
		t.Fatalf("register tool: %v", err)
	}

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-1",
				Name:      "meta_tool",
				Arguments: `{}`,
			}},
		},
		{Content: "Done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     100,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Test meta visibility"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// Transcript snapshot should NOT contain meta-messages
	snap := runner.transcriptSnapshot(run.ID, 0, true)
	for _, msg := range snap.Messages {
		if strings.Contains(msg.Content, "Secret instructions hidden from user") {
			t.Fatal("meta-message should not appear in transcript snapshot")
		}
	}

	// Verify internal messages DO contain the meta-message
	runner.mu.RLock()
	runState := runner.runs[run.ID]
	messages := append([]Message(nil), runState.messages...)
	runner.mu.RUnlock()

	foundMeta := false
	for _, msg := range messages {
		if msg.IsMeta && msg.Content == "Secret instructions hidden from user" {
			foundMeta = true
		}
	}
	if !foundMeta {
		t.Fatal("meta-message should exist in internal messages but not in transcript")
	}
}

func TestRunnerMetaMessageSentToAPI(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	err := registry.Register(ToolDefinition{
		Name:        "meta_tool",
		Description: "returns enriched result",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		tr := htools.ToolResult{
			Output: `{"status":"ok"}`,
			MetaMessages: []htools.MetaMessage{
				{Content: "Instructions for the LLM"},
			},
		}
		return htools.WrapToolResult(tr)
	})
	if err != nil {
		t.Fatalf("register tool: %v", err)
	}

	provider := &capturingProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-1",
				Name:      "meta_tool",
				Arguments: `{}`,
			}},
		},
		{Content: "Done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     100,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Test API messages"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// The second provider call should include the meta-message in messages
	if len(provider.calls) < 2 {
		t.Fatalf("expected at least 2 provider calls, got %d", len(provider.calls))
	}

	secondCall := provider.calls[1]
	foundMetaInAPI := false
	for _, msg := range secondCall.Messages {
		if msg.IsMeta && msg.Content == "Instructions for the LLM" {
			foundMetaInAPI = true
		}
	}
	if !foundMetaInAPI {
		t.Fatal("meta-message should be included in API call messages to the LLM")
	}
}

func TestRunnerPlainToolResultUnchanged(t *testing.T) {
	t.Parallel()

	// Register a tool that returns a plain string (no enrichment)
	registry := NewRegistry()
	err := registry.Register(ToolDefinition{
		Name:        "plain_tool",
		Description: "returns plain string",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"result":"plain output"}`, nil
	})
	if err != nil {
		t.Fatalf("register tool: %v", err)
	}

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-1",
				Name:      "plain_tool",
				Arguments: `{}`,
			}},
		},
		{Content: "Done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     100,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Test plain tool"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	// No meta-messages should exist
	runner.mu.RLock()
	runState := runner.runs[run.ID]
	messages := append([]Message(nil), runState.messages...)
	runner.mu.RUnlock()

	for _, msg := range messages {
		if msg.IsMeta {
			t.Fatal("no meta-messages should exist for plain tool results")
		}
	}

	// No EventMetaMessageInjected event should be emitted
	for _, ev := range events {
		if ev.Type == EventMetaMessageInjected {
			t.Fatal("EventMetaMessageInjected should not be emitted for plain tool results")
		}
	}

	// The tool result content should be the plain output
	foundToolResult := false
	for _, msg := range messages {
		if msg.Role == "tool" && msg.Name == "plain_tool" {
			foundToolResult = true
			if msg.Content != `{"result":"plain output"}` {
				t.Errorf("unexpected tool result content: %s", msg.Content)
			}
		}
	}
	if !foundToolResult {
		t.Fatal("tool result message not found")
	}
}

func TestRunnerMetaMessageToolOutputUnwrapped(t *testing.T) {
	t.Parallel()

	// Verify that the tool result message contains the unwrapped output,
	// not the full enriched envelope
	registry := NewRegistry()
	err := registry.Register(ToolDefinition{
		Name:        "enriched_tool",
		Description: "returns enriched result",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		tr := htools.ToolResult{
			Output: `{"result":"clean output"}`,
			MetaMessages: []htools.MetaMessage{
				{Content: "hidden"},
			},
		}
		return htools.WrapToolResult(tr)
	})
	if err != nil {
		t.Fatalf("register tool: %v", err)
	}

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-1",
				Name:      "enriched_tool",
				Arguments: `{}`,
			}},
		},
		{Content: "Done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     100,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Test unwrapping"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	runner.mu.RLock()
	runState := runner.runs[run.ID]
	messages := append([]Message(nil), runState.messages...)
	runner.mu.RUnlock()

	for _, msg := range messages {
		if msg.Role == "tool" && msg.Name == "enriched_tool" {
			// The tool result should be the clean output, not the envelope
			if msg.Content != `{"result":"clean output"}` {
				t.Errorf("tool result should be unwrapped output, got: %s", msg.Content)
			}
			if strings.Contains(msg.Content, "__tool_result__") {
				t.Fatal("tool result should not contain the envelope sentinel")
			}
		}
	}
}

func TestRunnerMetaMessageInEventPayload(t *testing.T) {
	t.Parallel()

	// Verify the EventToolCallCompleted payload contains unwrapped output
	registry := NewRegistry()
	err := registry.Register(ToolDefinition{
		Name:        "enriched_tool",
		Description: "returns enriched result",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		tr := htools.ToolResult{
			Output: `{"result":"clean"}`,
			MetaMessages: []htools.MetaMessage{
				{Content: "hidden"},
			},
		}
		return htools.WrapToolResult(tr)
	})
	if err != nil {
		t.Fatalf("register tool: %v", err)
	}

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{
				ID:        "call-1",
				Name:      "enriched_tool",
				Arguments: `{}`,
			}},
		},
		{Content: "Done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     100,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "Test events"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	events, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}

	for _, ev := range events {
		if ev.Type == EventToolCallCompleted {
			output, _ := ev.Payload["output"].(string)
			if strings.Contains(output, "__tool_result__") {
				t.Fatal("EventToolCallCompleted output should be unwrapped, not contain envelope")
			}
			if output != `{"result":"clean"}` {
				t.Errorf("expected unwrapped output in event, got: %s", output)
			}
		}
	}
}

func TestRunnerTranscriptSnapshotExcludesMetaMessages(t *testing.T) {
	t.Parallel()

	runner := NewRunner(&stubProvider{}, NewRegistry(), RunnerConfig{})
	now := time.Now().UTC()
	runner.mu.Lock()
	runner.runs["run_meta"] = &runState{
		run: Run{
			ID:             "run_meta",
			Status:         RunStatusRunning,
			TenantID:       "tenant",
			ConversationID: "conversation",
			AgentID:        "agent",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		messages: []Message{
			{Role: "user", Content: "hello"},
			{Role: "tool", Content: "tool result", Name: "read"},
			{Role: "system", Content: "hidden skill instructions", IsMeta: true},
			{Role: "assistant", Content: "done"},
		},
		subscribers: make(map[chan Event]struct{}),
	}
	runner.mu.Unlock()

	// With tools included
	snap := runner.transcriptSnapshot("run_meta", 0, true)
	for _, msg := range snap.Messages {
		if msg.Content == "hidden skill instructions" {
			t.Fatal("meta-message should be excluded from transcript snapshot")
		}
	}
	// Should have 3 messages (user, tool, assistant) but not the meta one
	if len(snap.Messages) != 3 {
		t.Fatalf("expected 3 messages in transcript (excluding meta), got %d", len(snap.Messages))
	}

	// Without tools
	snapNoTools := runner.transcriptSnapshot("run_meta", 0, false)
	for _, msg := range snapNoTools.Messages {
		if msg.Content == "hidden skill instructions" {
			t.Fatal("meta-message should be excluded from transcript snapshot")
		}
	}
	if len(snapNoTools.Messages) != 2 {
		t.Fatalf("expected 2 messages (user + assistant, no tool, no meta), got %d", len(snapNoTools.Messages))
	}
}

func TestRunnerConcurrentMetaMessageInjection(t *testing.T) {
	t.Parallel()

	// Verify no race conditions when two concurrent runs activate meta-messages
	registry := NewRegistry()
	err := registry.Register(ToolDefinition{
		Name:        "meta_tool",
		Description: "returns enriched result",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		tr := htools.ToolResult{
			Output: `{"status":"ok"}`,
			MetaMessages: []htools.MetaMessage{
				{Content: "concurrent meta"},
			},
		}
		return htools.WrapToolResult(tr)
	})
	if err != nil {
		t.Fatalf("register tool: %v", err)
	}

	const runCount = 5
	var wg sync.WaitGroup
	errs := make(chan error, runCount)

	for i := 0; i < runCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			provider := &stubProvider{turns: []CompletionResult{
				{
					ToolCalls: []ToolCall{{
						ID:        fmt.Sprintf("call-%d", idx),
						Name:      "meta_tool",
						Arguments: `{}`,
					}},
				},
				{Content: fmt.Sprintf("Done-%d", idx)},
			}}

			runner := NewRunner(provider, registry, RunnerConfig{
				DefaultModel: "gpt-4.1-mini",
				MaxSteps:     100,
			})

			run, err := runner.StartRun(RunRequest{Prompt: fmt.Sprintf("Test %d", idx)})
			if err != nil {
				errs <- fmt.Errorf("run %d start: %w", idx, err)
				return
			}

			_, err = collectRunEventsWithTimeout(runner, run.ID, 4*time.Second)
			if err != nil {
				errs <- fmt.Errorf("run %d events: %w", idx, err)
				return
			}

			state, ok := runner.GetRun(run.ID)
			if !ok {
				errs <- fmt.Errorf("run %d not found", idx)
				return
			}
			if state.Status != RunStatusCompleted {
				errs <- fmt.Errorf("run %d status=%s", idx, state.Status)
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

// collectRunEventsWithTimeout is like collectRunEvents but with configurable timeout.
func collectRunEventsWithTimeout(runner *Runner, runID string, timeout time.Duration) ([]Event, error) {
	history, stream, cancel, err := runner.Subscribe(runID)
	if err != nil {
		return nil, err
	}
	defer cancel()

	events := append([]Event(nil), history...)
	if hasTerminalEvent(events) {
		return events, nil
	}

	timer := time.After(timeout)
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
		case <-timer:
			return nil, context.DeadlineExceeded
		}
	}
}
