package harness

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go-agent-harness/internal/forensics/redaction"
)

// TestRunnerEmit_RedactionPipeline_Wired verifies that when a RedactionPipeline
// is configured on the Runner, event payloads containing secrets are redacted
// before being appended to the run's event list.
func TestRunnerEmit_RedactionPipeline_Wired(t *testing.T) {
	t.Parallel()

	pipeline := redaction.NewPipeline(redaction.NewRedactor(nil), redaction.EventClassConfig{})

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done"},
	}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            2,
		RedactionPipeline:   pipeline,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	if len(events) == 0 {
		t.Fatal("expected at least one event")
	}
	// All events should still be present; pipeline defaults to redacted mode (keep=true).
	for _, evt := range events {
		if evt.Payload == nil {
			t.Errorf("event %s (type=%s): nil payload", evt.ID, evt.Type)
		}
	}
}

// TestRunnerEmit_RedactionPipeline_RedactsSecrets verifies that a secret injected
// into an event payload via emit() is redacted in the stored event.
// The synthetic event must be emitted while the run is still active (before
// terminal), because post-terminal events are correctly dropped.
func TestRunnerEmit_RedactionPipeline_RedactsSecrets(t *testing.T) {
	t.Parallel()

	pipeline := redaction.NewPipeline(redaction.NewRedactor(nil), redaction.EventClassConfig{})

	blocker := make(chan struct{})
	prov := &blockingProvider{blocker: blocker}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            2,
		RedactionPipeline:   pipeline,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Wait until the run is actually running so emit() is not post-terminal.
	deadline := time.Now().Add(2 * time.Second)
	for {
		r, _ := runner.GetRun(run.ID)
		if r.Status == RunStatusRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for running status")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Emit the synthetic event while the run is still active.
	runner.emit(run.ID, EventType("run.test"), map[string]any{
		"content": "postgres://user:secret@localhost:5432/prod",
	})

	// Unblock the provider and wait for completion.
	close(blocker)
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	var found bool
	for _, evt := range events {
		if string(evt.Type) != "run.test" {
			continue
		}
		found = true
		content, _ := evt.Payload["content"].(string)
		if !strings.Contains(content, "[REDACTED:connection_string]") {
			t.Errorf("secret not redacted in stored event payload: %q", content)
		}
	}
	if !found {
		t.Error("did not find synthetic run.test event")
	}
}

// TestRunnerEmit_NoRedactionPipeline_Passthrough verifies that without a
// RedactionPipeline, payloads are stored verbatim (no redaction).
// The synthetic event must be emitted while the run is still active (before
// terminal), because post-terminal events are correctly dropped.
func TestRunnerEmit_NoRedactionPipeline_Passthrough(t *testing.T) {
	t.Parallel()

	blocker := make(chan struct{})
	prov := &blockingProvider{blocker: blocker}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            2,
		// No RedactionPipeline set.
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Wait until the run is actually running so emit() is not post-terminal.
	deadline := time.Now().Add(2 * time.Second)
	for {
		r, _ := runner.GetRun(run.ID)
		if r.Status == RunStatusRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for running status")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Emit the synthetic event while the run is still active.
	secret := "postgres://user:secret@localhost:5432/prod"
	runner.emit(run.ID, EventType("run.test"), map[string]any{
		"content": secret,
	})

	// Unblock the provider and wait for completion.
	close(blocker)
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	var found bool
	for _, evt := range events {
		if string(evt.Type) != "run.test" {
			continue
		}
		found = true
		content, _ := evt.Payload["content"].(string)
		// Without a pipeline the payload is stored verbatim.
		if content != secret {
			t.Errorf("expected verbatim content %q, got %q", secret, content)
		}
	}
	if !found {
		t.Error("did not find synthetic run.test event")
	}
}

// TestRunnerEmit_RedactionPipeline_NoneMode verifies that an event whose type
// maps to StorageModeNone is dropped from the run's event list.
func TestRunnerEmit_RedactionPipeline_NoneMode(t *testing.T) {
	t.Parallel()

	cfg := redaction.EventClassConfig{
		"run.test.drop": redaction.StorageModeNone,
	}
	pipeline := redaction.NewPipeline(redaction.NewRedactor(nil), cfg)

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done"},
	}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            2,
		RedactionPipeline:   pipeline,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	runner.emit(run.ID, EventType("run.test.drop"), map[string]any{
		"content": "should be dropped",
	})

	events := collectEvents(t, runner, run.ID)
	for _, evt := range events {
		if string(evt.Type) == "run.test.drop" {
			t.Errorf("expected event run.test.drop to be dropped, but found it in run events")
		}
	}
}

// payloadContainsString recursively walks a map[string]any and returns true if
// any string value (at any nesting depth) contains substr.
func payloadContainsString(payload map[string]any, substr string) bool {
	return payloadValueContainsString(payload, substr)
}

func payloadValueContainsString(v any, substr string) bool {
	switch val := v.(type) {
	case string:
		return strings.Contains(val, substr)
	case map[string]any:
		for _, child := range val {
			if payloadValueContainsString(child, substr) {
				return true
			}
		}
	case []any:
		for _, elem := range val {
			if payloadValueContainsString(elem, substr) {
				return true
			}
		}
	}
	return false
}

// allEventPayloadsAsJSON marshals every event's full payload to a JSON string
// and returns them concatenated for easy scanning.
func allEventPayloadsAsJSON(t *testing.T, events []Event) string {
	t.Helper()
	var sb strings.Builder
	for _, ev := range events {
		b, err := json.Marshal(ev.Payload)
		if err != nil {
			t.Fatalf("marshal event %s payload: %v", ev.ID, err)
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// TestTC1_SnapshotMemorySnippetSecretRedacted (T-C1) verifies that when a
// RedactionPipeline is configured with the built-in api_key pattern, a
// sk-...–style secret placed in the observational memory snippet is redacted
// in the stored llm.request.snapshot event and does not appear verbatim in any
// event payload across all sinks.
func TestTC1_SnapshotMemorySnippetSecretRedacted(t *testing.T) {
	t.Parallel()

	// A realistic-looking fake API key that matches the built-in sk-... pattern.
	// The regex is: sk-[A-Za-z0-9_-]{20,}
	const rawSecret = "sk-fakesecretkey1234567890abcdef"

	// Configure the built-in redaction pipeline (no custom patterns needed;
	// the api_key rule matches sk-... out of the box).
	pipeline := redaction.NewPipeline(redaction.NewRedactor(nil), redaction.EventClassConfig{})

	memStub := &memoryStub{snippet: "Context: " + rawSecret}

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done"},
	}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:           "test-model",
		DefaultSystemPrompt:    "You are helpful.",
		MaxSteps:               2,
		CaptureRequestEnvelope: true,
		SnapshotMemorySnippet:  true,
		MemoryManager:          memStub,
		RedactionPipeline:      pipeline,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)

	// --- Assertion 1: llm.request.snapshot must be present and its
	// memory_snippet must contain the REDACTED marker, not the raw secret.
	var snapshots []Event
	for _, ev := range events {
		if ev.Type == EventLLMRequestSnapshot {
			snapshots = append(snapshots, ev)
		}
	}
	if len(snapshots) == 0 {
		t.Fatal("no llm.request.snapshot events found — CaptureRequestEnvelope may not be working")
	}
	snap := snapshots[0]
	memSnippet, _ := snap.Payload["memory_snippet"].(string)
	if !strings.Contains(memSnippet, "[REDACTED:api_key]") {
		t.Errorf("T-C1: memory_snippet in llm.request.snapshot was not redacted: got %q, want it to contain [REDACTED:api_key]", memSnippet)
	}
	if strings.Contains(memSnippet, rawSecret) {
		t.Errorf("T-C1: raw secret still present in memory_snippet: %q", memSnippet)
	}

	// --- Assertion 2: the raw secret must not appear in ANY event payload
	// (across all event types and all nesting levels) stored in the run.
	for _, ev := range events {
		if payloadContainsString(ev.Payload, rawSecret) {
			t.Errorf("T-C1: raw secret found in event %s (type=%s) payload", ev.ID, ev.Type)
		}
	}

	// Cross-check: the concatenated JSON of all payloads must not contain the secret.
	allPayloads := allEventPayloadsAsJSON(t, events)
	if strings.Contains(allPayloads, rawSecret) {
		t.Errorf("T-C1: raw secret appears in serialized event payloads (cross-check)")
	}
}

// TestTC2_SnapshotEventSuppressedByStorageModeNone (T-C2) verifies that an
// operator can fully suppress the llm.request.snapshot event class by mapping
// its event type to StorageModeNone in the RedactionPipeline config. No
// llm.request.snapshot event should appear in the stored run history.
func TestTC2_SnapshotEventSuppressedByStorageModeNone(t *testing.T) {
	t.Parallel()

	// Map the snapshot event class to StorageModeNone: the pipeline drops it.
	cfg := redaction.EventClassConfig{
		string(EventLLMRequestSnapshot): redaction.StorageModeNone,
	}
	pipeline := redaction.NewPipeline(redaction.NewRedactor(nil), cfg)

	memStub := &memoryStub{snippet: "some memory context"}

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done"},
	}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:           "test-model",
		DefaultSystemPrompt:    "You are helpful.",
		MaxSteps:               2,
		CaptureRequestEnvelope: true,
		SnapshotMemorySnippet:  true,
		MemoryManager:          memStub,
		RedactionPipeline:      pipeline,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)

	// The run must have completed with at least some events (sanity check).
	if len(events) == 0 {
		t.Fatal("T-C2: expected at least one event (run.started or run.completed)")
	}

	// No llm.request.snapshot event must be present — StorageModeNone suppresses it.
	for _, ev := range events {
		if ev.Type == EventLLMRequestSnapshot {
			t.Errorf("T-C2: llm.request.snapshot event found in stored run history but should have been suppressed by StorageModeNone")
		}
	}

	// Verify other events (run.started, run.completed) are still present —
	// only the snapshot class was suppressed.
	var hasStarted, hasCompleted bool
	for _, ev := range events {
		if ev.Type == EventRunStarted {
			hasStarted = true
		}
		if ev.Type == EventRunCompleted {
			hasCompleted = true
		}
	}
	if !hasStarted {
		t.Error("T-C2: run.started event missing — only llm.request.snapshot should be suppressed")
	}
	if !hasCompleted {
		t.Error("T-C2: run.completed event missing — only llm.request.snapshot should be suppressed")
	}
}
