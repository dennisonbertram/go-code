package harness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestRequestEnvelopeDisabledByDefault verifies that when CaptureRequestEnvelope
// is false (default), no llm.request.snapshot or llm.response.meta events are
// emitted.
func TestRequestEnvelopeDisabledByDefault(t *testing.T) {
	t.Parallel()

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done"},
	}}
	// CaptureRequestEnvelope not set — defaults to false.
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	for _, ev := range events {
		if ev.Type == EventLLMRequestSnapshot {
			t.Errorf("unexpected %s event when CaptureRequestEnvelope=false", EventLLMRequestSnapshot)
		}
		if ev.Type == EventLLMResponseMeta {
			t.Errorf("unexpected %s event when CaptureRequestEnvelope=false", EventLLMResponseMeta)
		}
	}
}

// TestRequestEnvelopeSnapshotEmitted verifies that when CaptureRequestEnvelope=true,
// a llm.request.snapshot event is emitted before each provider call.
func TestRequestEnvelopeSnapshotEmitted(t *testing.T) {
	t.Parallel()

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done"},
	}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:           "test-model",
		MaxSteps:               2,
		CaptureRequestEnvelope: true,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt:       "hello",
		SystemPrompt: "You are a helpful assistant.",
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	var snapshots []Event
	for _, ev := range events {
		if ev.Type == EventLLMRequestSnapshot {
			snapshots = append(snapshots, ev)
		}
	}
	if len(snapshots) == 0 {
		t.Fatal("expected at least one llm.request.snapshot event when CaptureRequestEnvelope=true")
	}

	snap := snapshots[0]

	// Check required fields are present.
	if _, ok := snap.Payload["step"]; !ok {
		t.Error("llm.request.snapshot missing 'step' field")
	}
	if _, ok := snap.Payload["prompt_hash"]; !ok {
		t.Error("llm.request.snapshot missing 'prompt_hash' field")
	}
	if _, ok := snap.Payload["tool_names"]; !ok {
		t.Error("llm.request.snapshot missing 'tool_names' field")
	}

	// prompt_hash should be a non-empty 64-char hex string (SHA-256).
	hash, _ := snap.Payload["prompt_hash"].(string)
	if len(hash) != 64 {
		t.Errorf("prompt_hash length: got %d, want 64 (SHA-256 hex)", len(hash))
	}

	// step should be 1 for the first step.
	step := payloadInt(snap.Payload, "step")
	if step != 1 {
		t.Errorf("step: got %d, want 1", step)
	}
}

// TestRequestEnvelopeResponseMetaEmitted verifies that when CaptureRequestEnvelope=true,
// a llm.response.meta event is emitted after each provider call.
func TestRequestEnvelopeResponseMetaEmitted(t *testing.T) {
	t.Parallel()

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done", ModelVersion: "gpt-4.1-2025-04-14"},
	}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:           "test-model",
		MaxSteps:               2,
		CaptureRequestEnvelope: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	var metas []Event
	for _, ev := range events {
		if ev.Type == EventLLMResponseMeta {
			metas = append(metas, ev)
		}
	}
	if len(metas) == 0 {
		t.Fatal("expected at least one llm.response.meta event when CaptureRequestEnvelope=true")
	}

	meta := metas[0]

	// Check required fields.
	if _, ok := meta.Payload["step"]; !ok {
		t.Error("llm.response.meta missing 'step' field")
	}
	if _, ok := meta.Payload["latency_ms"]; !ok {
		t.Error("llm.response.meta missing 'latency_ms' field")
	}
	if _, ok := meta.Payload["model_version"]; !ok {
		t.Error("llm.response.meta missing 'model_version' field")
	}

	// model_version should be set from the result.
	modelVersion, _ := meta.Payload["model_version"].(string)
	if modelVersion != "gpt-4.1-2025-04-14" {
		t.Errorf("model_version: got %q, want %q", modelVersion, "gpt-4.1-2025-04-14")
	}

	// latency_ms should be >= 0.
	switch v := meta.Payload["latency_ms"].(type) {
	case float64:
		if v < 0 {
			t.Errorf("latency_ms: got %v, want >= 0", v)
		}
	case int64:
		if v < 0 {
			t.Errorf("latency_ms: got %v, want >= 0", v)
		}
	case int:
		if v < 0 {
			t.Errorf("latency_ms: got %v, want >= 0", v)
		}
	default:
		t.Errorf("latency_ms has unexpected type %T", meta.Payload["latency_ms"])
	}
}

// TestRequestEnvelopeToolNamesInSnapshot verifies that the tool_names list in
// the snapshot contains the registered tool names.
func TestRequestEnvelopeToolNamesInSnapshot(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "my_tool_alpha",
		Description: "alpha tool",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})
	_ = registry.Register(ToolDefinition{
		Name:        "my_tool_beta",
		Description: "beta tool",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done"},
	}}
	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:           "test-model",
		MaxSteps:               2,
		CaptureRequestEnvelope: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	var snapshots []Event
	for _, ev := range events {
		if ev.Type == EventLLMRequestSnapshot {
			snapshots = append(snapshots, ev)
		}
	}
	if len(snapshots) == 0 {
		t.Fatal("no llm.request.snapshot events found")
	}

	snap := snapshots[0]
	rawToolNames := snap.Payload["tool_names"]

	// tool_names can come back as []interface{} from the payload map.
	var toolNames []string
	switch v := rawToolNames.(type) {
	case []string:
		toolNames = v
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				toolNames = append(toolNames, s)
			}
		}
	default:
		t.Fatalf("tool_names has unexpected type %T", rawToolNames)
	}

	toolNameSet := make(map[string]bool)
	for _, n := range toolNames {
		toolNameSet[n] = true
	}
	if !toolNameSet["my_tool_alpha"] {
		t.Errorf("expected tool_names to contain 'my_tool_alpha', got: %v", toolNames)
	}
	if !toolNameSet["my_tool_beta"] {
		t.Errorf("expected tool_names to contain 'my_tool_beta', got: %v", toolNames)
	}
}

// TestRequestEnvelopeMemorySnippetInSnapshot verifies that when a memory snippet
// is available and SnapshotMemorySnippet=true, it is captured in the snapshot's
// memory_snippet field.
func TestRequestEnvelopeMemorySnippetInSnapshot(t *testing.T) {
	t.Parallel()

	memStub := &memoryStub{snippet: "Remember: user likes brevity"}

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done"},
	}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:           "test-model",
		MaxSteps:               2,
		CaptureRequestEnvelope: true,
		SnapshotMemorySnippet:  true,
		MemoryManager:          memStub,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	var snapshots []Event
	for _, ev := range events {
		if ev.Type == EventLLMRequestSnapshot {
			snapshots = append(snapshots, ev)
		}
	}
	if len(snapshots) == 0 {
		t.Fatal("no llm.request.snapshot events found")
	}

	snap := snapshots[0]
	memSnippet, _ := snap.Payload["memory_snippet"].(string)
	if !strings.Contains(memSnippet, "brevity") {
		t.Errorf("memory_snippet: got %q, expected to contain 'brevity'", memSnippet)
	}
}

// TestRequestEnvelopeNoMemorySnippetWhenEmpty verifies that when there is no
// memory snippet, memory_snippet field is empty or absent in the snapshot.
func TestRequestEnvelopeNoMemorySnippetWhenEmpty(t *testing.T) {
	t.Parallel()

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done"},
	}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:           "test-model",
		MaxSteps:               2,
		CaptureRequestEnvelope: true,
		// No MemoryManager set
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	var snapshots []Event
	for _, ev := range events {
		if ev.Type == EventLLMRequestSnapshot {
			snapshots = append(snapshots, ev)
		}
	}
	if len(snapshots) == 0 {
		t.Fatal("no llm.request.snapshot events found")
	}

	snap := snapshots[0]
	memSnippet, _ := snap.Payload["memory_snippet"].(string)
	if memSnippet != "" {
		t.Errorf("expected empty memory_snippet when no MemoryManager, got %q", memSnippet)
	}
}

// TestRequestEnvelopeMultipleSteps verifies that snapshot and meta events are
// emitted per-step when there are multiple LLM turns.
func TestRequestEnvelopeMultipleSteps(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "noop_envelope",
		Description: "does nothing",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})

	prov := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{
				{ID: "c1", Name: "noop_envelope", Arguments: `{}`},
			},
		},
		{Content: "all done"},
	}}
	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:           "test-model",
		MaxSteps:               5,
		CaptureRequestEnvelope: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	var snapshots, metas []Event
	for _, ev := range events {
		switch ev.Type {
		case EventLLMRequestSnapshot:
			snapshots = append(snapshots, ev)
		case EventLLMResponseMeta:
			metas = append(metas, ev)
		}
	}

	if len(snapshots) != 2 {
		t.Errorf("expected 2 llm.request.snapshot events for 2 LLM turns, got %d", len(snapshots))
	}
	if len(metas) != 2 {
		t.Errorf("expected 2 llm.response.meta events for 2 LLM turns, got %d", len(metas))
	}

	// Verify step numbers are different across turns.
	if len(snapshots) == 2 {
		step1 := payloadInt(snapshots[0].Payload, "step")
		step2 := payloadInt(snapshots[1].Payload, "step")
		if step1 == step2 {
			t.Errorf("both snapshots have same step %d, expected different steps", step1)
		}
	}
}

// TestRequestEnvelopeSnapshotBeforeResponseMeta verifies that the snapshot event
// is emitted BEFORE the response.meta event in the event stream.
func TestRequestEnvelopeSnapshotBeforeResponseMeta(t *testing.T) {
	t.Parallel()

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done"},
	}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:           "test-model",
		MaxSteps:               2,
		CaptureRequestEnvelope: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	snapshotIdx := -1
	metaIdx := -1
	for i, ev := range events {
		if ev.Type == EventLLMRequestSnapshot && snapshotIdx == -1 {
			snapshotIdx = i
		}
		if ev.Type == EventLLMResponseMeta && metaIdx == -1 {
			metaIdx = i
		}
	}

	if snapshotIdx == -1 {
		t.Fatal("no llm.request.snapshot event found")
	}
	if metaIdx == -1 {
		t.Fatal("no llm.response.meta event found")
	}
	if snapshotIdx >= metaIdx {
		t.Errorf("llm.request.snapshot (idx %d) should come before llm.response.meta (idx %d)", snapshotIdx, metaIdx)
	}
}

// TestCompletionResultModelVersionField verifies that CompletionResult has the
// ModelVersion field.
func TestCompletionResultModelVersionField(t *testing.T) {
	t.Parallel()

	result := CompletionResult{
		Content:      "hello",
		ModelVersion: "gpt-4.1-2025-04-14",
	}
	if result.ModelVersion != "gpt-4.1-2025-04-14" {
		t.Errorf("ModelVersion: got %q, want %q", result.ModelVersion, "gpt-4.1-2025-04-14")
	}
}

// TestRunnerConfigCaptureRequestEnvelopeDefault verifies that
// CaptureRequestEnvelope defaults to false (zero value).
func TestRunnerConfigCaptureRequestEnvelopeDefault(t *testing.T) {
	t.Parallel()

	cfg := RunnerConfig{}
	if cfg.CaptureRequestEnvelope {
		t.Error("expected CaptureRequestEnvelope to default to false")
	}
}

// TestSnapshotMemorySnippetOmittedByDefault verifies that memory_snippet is
// absent from llm.request.snapshot when SnapshotMemorySnippet is false (the
// default), even when a memory snippet is present (#229).
func TestSnapshotMemorySnippetOmittedByDefault(t *testing.T) {
	t.Parallel()

	memStub := &memoryStub{snippet: "sk-secret-api-key-12345678901234567890"}

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done"},
	}}
	// CaptureRequestEnvelope=true but SnapshotMemorySnippet not set (defaults false).
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:           "test-model",
		MaxSteps:               2,
		CaptureRequestEnvelope: true,
		MemoryManager:          memStub,
		// SnapshotMemorySnippet intentionally omitted — defaults false.
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	var snapshots []Event
	for _, ev := range events {
		if ev.Type == EventLLMRequestSnapshot {
			snapshots = append(snapshots, ev)
		}
	}
	if len(snapshots) == 0 {
		t.Fatal("no llm.request.snapshot events found")
	}

	snap := snapshots[0]
	// memory_snippet must be absent or empty when SnapshotMemorySnippet=false.
	if val, exists := snap.Payload["memory_snippet"]; exists && val != "" {
		t.Errorf("memory_snippet should be omitted when SnapshotMemorySnippet=false, got %q", val)
	}
}

// TestSnapshotMemorySnippetIncludedWhenOptIn verifies that memory_snippet IS
// included in the snapshot when SnapshotMemorySnippet=true (#229).
func TestSnapshotMemorySnippetIncludedWhenOptIn(t *testing.T) {
	t.Parallel()

	memStub := &memoryStub{snippet: "Remember: user prefers brevity"}

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done"},
	}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:           "test-model",
		MaxSteps:               2,
		CaptureRequestEnvelope: true,
		SnapshotMemorySnippet:  true,
		MemoryManager:          memStub,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	var snapshots []Event
	for _, ev := range events {
		if ev.Type == EventLLMRequestSnapshot {
			snapshots = append(snapshots, ev)
		}
	}
	if len(snapshots) == 0 {
		t.Fatal("no llm.request.snapshot events found")
	}

	snap := snapshots[0]
	memSnippet, _ := snap.Payload["memory_snippet"].(string)
	if !strings.Contains(memSnippet, "brevity") {
		t.Errorf("memory_snippet: got %q, expected to contain 'brevity' when SnapshotMemorySnippet=true", memSnippet)
	}
}

// TestRequestEnvelopePromptHashDifferentAcrossSteps verifies that the prompt_hash
// changes across steps when the messages change (as they do with tool results).
func TestRequestEnvelopePromptHashDifferentAcrossSteps(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	_ = registry.Register(ToolDefinition{
		Name:        "noop_hash_test",
		Description: "does nothing",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "tool result data", nil
	})

	prov := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{
				{ID: "c1", Name: "noop_hash_test", Arguments: `{}`},
			},
		},
		{Content: "done"},
	}}
	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:           "test-model",
		MaxSteps:               5,
		CaptureRequestEnvelope: true,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	var hashes []string
	for _, ev := range events {
		if ev.Type == EventLLMRequestSnapshot {
			if h, ok := ev.Payload["prompt_hash"].(string); ok {
				hashes = append(hashes, h)
			}
		}
	}

	if len(hashes) != 2 {
		t.Fatalf("expected 2 prompt hashes, got %d", len(hashes))
	}
	if hashes[0] == hashes[1] {
		t.Error("expected prompt_hash to differ across steps as messages grow, but both are equal")
	}
}
