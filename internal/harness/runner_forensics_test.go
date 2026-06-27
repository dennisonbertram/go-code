package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

// TestSchemaVersionOnAllEvents verifies that every event emitted by the runner
// carries schema_version == EventSchemaVersion in its payload.
func TestSchemaVersionOnAllEvents(t *testing.T) {
	t.Parallel()

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done"},
	}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            2,
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

	for _, evt := range events {
		sv, ok := evt.Payload["schema_version"]
		if !ok {
			t.Errorf("event %s (type=%s) missing schema_version in payload", evt.ID, evt.Type)
			continue
		}
		if sv != EventSchemaVersion {
			t.Errorf("event %s (type=%s) schema_version=%v, want %q", evt.ID, evt.Type, sv, EventSchemaVersion)
		}
	}
}

// TestConversationIDOnAllEvents verifies that every event carries a
// conversation_id field matching the run's ConversationID.
func TestConversationIDOnAllEvents(t *testing.T) {
	t.Parallel()

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done"},
	}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)
	runFinal, _ := runner.GetRun(run.ID)

	for _, evt := range events {
		cid, ok := evt.Payload["conversation_id"]
		if !ok {
			t.Errorf("event %s (type=%s) missing conversation_id", evt.ID, evt.Type)
			continue
		}
		if cid != runFinal.ConversationID {
			t.Errorf("event %s conversation_id=%v, want %q", evt.ID, cid, runFinal.ConversationID)
		}
	}
}

// TestConversationIDStableAcrossContinue verifies that when a run is continued,
// the conversation_id in events from both runs is identical.
func TestConversationIDStableAcrossContinue(t *testing.T) {
	t.Parallel()

	prov := &continuationProvider{
		turns: []CompletionResult{
			{Content: "first response"},
			{Content: "second response"},
		},
	}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            4,
	})

	run1, err := runner.StartRun(RunRequest{Prompt: "initial"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatusCont(t, runner, run1.ID, RunStatusCompleted, RunStatusFailed)

	run2, err := runner.ContinueRun(run1.ID, "follow up")
	if err != nil {
		t.Fatalf("ContinueRun: %v", err)
	}
	waitForStatusCont(t, runner, run2.ID, RunStatusCompleted, RunStatusFailed)

	events1 := collectEvents(t, runner, run1.ID)
	events2 := collectEvents(t, runner, run2.ID)

	// Extract conversation_id from first run's events.
	var convID1 string
	for _, evt := range events1 {
		if cid, ok := evt.Payload["conversation_id"].(string); ok && cid != "" {
			convID1 = cid
			break
		}
	}
	if convID1 == "" {
		t.Fatal("run1 events have no conversation_id")
	}

	// Verify all run2 events have the same conversation_id.
	for _, evt := range events2 {
		cid, ok := evt.Payload["conversation_id"].(string)
		if !ok {
			t.Errorf("run2 event %s (type=%s) missing conversation_id", evt.ID, evt.Type)
			continue
		}
		if cid != convID1 {
			t.Errorf("run2 event %s conversation_id=%q, want %q (same as run1)", evt.ID, cid, convID1)
		}
	}

	// Verify run2's run.started event includes previous_run_id.
	for _, evt := range events2 {
		if evt.Type == EventRunStarted {
			prevID, ok := evt.Payload["previous_run_id"].(string)
			if !ok || prevID == "" {
				t.Errorf("run2 run.started event missing previous_run_id")
			} else if prevID != run1.ID {
				t.Errorf("previous_run_id=%q, want %q", prevID, run1.ID)
			}
			break
		}
	}
}

type recordedLedgerEntry struct {
	Timestamp time.Time      `json:"ts"`
	Seq       uint64         `json:"seq"`
	Type      string         `json:"type"`
	Data      map[string]any `json:"data,omitempty"`
}

func normalizePayloadForJSON(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	if payload == nil {
		return nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(data, &normalized); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return normalized
}

func loadRecordedLedger(t *testing.T, rolloutDir, runID string) []recordedLedgerEntry {
	t.Helper()

	entries, err := readRecordedLedger(rolloutDir, runID)
	if err != nil {
		t.Fatalf("read rollout ledger: %v", err)
	}
	return entries
}

func readRecordedLedger(rolloutDir, runID string) ([]recordedLedgerEntry, error) {
	path := filepath.Join(rolloutDir, time.Now().UTC().Format("2006-01-02"), runID+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var entries []recordedLedgerEntry
	for scanner.Scan() {
		var entry recordedLedgerEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// TestEventLedgerInvariant_JSONLMatchesInMemoryHistory defends the recorder
// invariant that the JSONL rollout is a faithful ordered ledger mirror of the
// canonical in-memory event history, even when non-terminal emits happen from
// multiple goroutines while the run is active.
func TestEventLedgerInvariant_JSONLMatchesInMemoryHistory(t *testing.T) {
	t.Parallel()

	rolloutDir := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})

	provider := &contextCompactGatingProvider{
		results: []CompletionResult{{Content: "done"}},
		beforeCall: func(idx int) {
			if idx == 0 {
				close(started)
				<-release
			}
		},
	}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     1,
		RolloutDir:   rolloutDir,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "ledger invariant"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	<-started

	const extraEvents = 24
	var wg sync.WaitGroup
	for i := 0; i < extraEvents; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			runner.emit(run.ID, EventType("test.concurrent"), map[string]any{
				"marker": fmt.Sprintf("event-%02d", i),
			})
		}(i)
	}
	wg.Wait()

	close(release)
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	history := collectEvents(t, runner, run.ID)
	var entries []recordedLedgerEntry
	deadline := time.Now().Add(2 * time.Second)
	for {
		var err error
		entries, err = readRecordedLedger(rolloutDir, run.ID)
		if err == nil && len(entries) >= len(history) {
			break
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("read rollout ledger: %v", err)
			}
			t.Fatalf("rollout ledger length mismatch: got %d entries, want %d", len(entries), len(history))
		}
		time.Sleep(10 * time.Millisecond)
	}

	if len(entries) != len(history) {
		t.Fatalf("rollout ledger length mismatch: got %d entries, want %d", len(entries), len(history))
	}

	for i := range history {
		if entries[i].Seq != uint64(i) {
			t.Fatalf("entry %d seq=%d, want %d", i, entries[i].Seq, i)
		}
		if entries[i].Type != string(history[i].Type) {
			t.Fatalf("entry %d type=%q, want %q", i, entries[i].Type, history[i].Type)
		}
		wantPayload := normalizePayloadForJSON(t, history[i].Payload)
		if !reflect.DeepEqual(entries[i].Data, wantPayload) {
			t.Fatalf("entry %d payload mismatch:\n got: %#v\nwant: %#v", i, entries[i].Data, wantPayload)
		}
	}
}

// TestStepInAllEventsAfterRunStarted verifies that a "step" field is present
// on all events emitted after run.started.
func TestStepInAllEventsAfterRunStarted(t *testing.T) {
	t.Parallel()

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done"},
	}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	events := collectEvents(t, runner, run.ID)

	sawRunStarted := false
	for _, evt := range events {
		if evt.Type == EventRunStarted {
			sawRunStarted = true
			// run.started itself gets step=0 (before the loop)
			continue
		}
		if !sawRunStarted {
			continue
		}
		if _, ok := evt.Payload["step"]; !ok {
			t.Errorf("event %s (type=%s) after run.started missing step field", evt.ID, evt.Type)
		}
	}
}

// TestCorrelationFieldsInRollout verifies that schema_version and
// conversation_id appear in the JSONL rollout file.
func TestCorrelationFieldsInRollout(t *testing.T) {
	t.Parallel()

	rolloutDir := t.TempDir()
	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done"},
	}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            2,
		RolloutDir:          rolloutDir,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	// Find the rollout file.
	dateDir := filepath.Join(rolloutDir, time.Now().UTC().Format("2006-01-02"))
	jsonlPath := filepath.Join(dateDir, run.ID+".jsonl")
	f, err := os.Open(jsonlPath)
	if err != nil {
		t.Fatalf("open rollout file: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		var entry struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("line %d: parse JSONL: %v", lineNum, err)
		}
		if entry.Data == nil {
			continue
		}
		if _, ok := entry.Data["schema_version"]; !ok {
			t.Errorf("line %d: missing schema_version in rollout data", lineNum)
		}
		if _, ok := entry.Data["conversation_id"]; !ok {
			t.Errorf("line %d: missing conversation_id in rollout data", lineNum)
		}
	}
	if lineNum == 0 {
		t.Fatal("rollout file is empty")
	}
}

// TestSubscribeCancelConcurrentEmit verifies that rapidly subscribing and
// cancelling while events are being emitted does not panic (send on closed channel).
func TestSubscribeCancelConcurrentEmit(t *testing.T) {
	t.Parallel()

	// Use a blocking provider so the run stays active while we hammer subscribe/cancel.
	blocker := make(chan struct{})
	prov := &blockingProvider{blocker: blocker}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "concurrent test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Wait until the run is actually running.
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

	// Spin up goroutines that subscribe, cancel, and emit concurrently.
	var wg sync.WaitGroup
	const n = 20
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, _, cancel, err := runner.Subscribe(run.ID)
			if err != nil {
				return
			}
			// Emit an event while the subscription is live, then immediately cancel.
			runner.emit(run.ID, EventType("test.concurrent"), map[string]any{"i": 1})
			cancel()
		}()
	}
	wg.Wait()

	// Unblock the provider so the run completes cleanly.
	close(blocker)
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)
}

// TestContinueRunPreservesSourceStatus verifies that the source run remains
// in Completed status after ContinueRun (not mutated to Running).
func TestContinueRunPreservesSourceStatus(t *testing.T) {
	t.Parallel()

	prov := &continuationProvider{
		turns: []CompletionResult{
			{Content: "first"},
			{Content: "second"},
		},
	}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            4,
	})

	run1, err := runner.StartRun(RunRequest{Prompt: "initial"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run1.ID, RunStatusCompleted, RunStatusFailed)

	_, err = runner.ContinueRun(run1.ID, "follow up")
	if err != nil {
		t.Fatalf("ContinueRun: %v", err)
	}

	// The source run must still be Completed.
	run1Final, _ := runner.GetRun(run1.ID)
	if run1Final.Status != RunStatusCompleted {
		t.Errorf("source run status = %s, want %s", run1Final.Status, RunStatusCompleted)
	}

	// A second ContinueRun should fail.
	_, err = runner.ContinueRun(run1.ID, "second follow up")
	if err == nil {
		t.Error("expected error on second ContinueRun, got nil")
	}
}

// TestEmitDoesNotMutateCallerPayload verifies that emit() clones the payload
// map and does not modify the caller's copy.
func TestEmitDoesNotMutateCallerPayload(t *testing.T) {
	t.Parallel()

	prov := &stubProvider{turns: []CompletionResult{{Content: "done"}}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "payload test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	// Emit with a known payload and verify it is not mutated.
	original := map[string]any{"my_key": "my_value"}
	runner.emit(run.ID, EventType("test.payload"), original)

	if _, ok := original["schema_version"]; ok {
		t.Error("emit() mutated the caller's payload: found injected schema_version")
	}
	if _, ok := original["conversation_id"]; ok {
		t.Error("emit() mutated the caller's payload: found injected conversation_id")
	}
	if _, ok := original["step"]; ok {
		t.Error("emit() mutated the caller's payload: found injected step")
	}
}

// TestSubscribePayloadIsolation verifies that mutating an event payload received
// via Subscribe (either from history or from the live channel) does not corrupt
// the runner's stored forensic event history.
func TestSubscribePayloadIsolation(t *testing.T) {
	t.Parallel()

	prov := &stubProvider{turns: []CompletionResult{{Content: "done"}}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "isolation test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	// First subscription: mutate every event's payload from history.
	history1, _, cancel1, err := runner.Subscribe(run.ID)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	cancel1()

	for i := range history1 {
		history1[i].Payload["__tamper__"] = true
	}

	// Second subscription: verify the stored events were NOT affected.
	history2, _, cancel2, err := runner.Subscribe(run.ID)
	if err != nil {
		t.Fatalf("Subscribe (2): %v", err)
	}
	cancel2()

	for i, ev := range history2 {
		if _, ok := ev.Payload["__tamper__"]; ok {
			t.Errorf("event[%d] (type=%s): stored payload was tampered by first subscriber", i, ev.Type)
		}
	}
}

// TestDeepPayloadCloneIsolation verifies that nested map/slice values in event
// payloads are deep-copied, so mutating a nested structure obtained via
// Subscribe does not corrupt stored forensic history or other subscriber copies.
func TestDeepPayloadCloneIsolation(t *testing.T) {
	t.Parallel()

	// Use a blocking provider so the run stays active while we emit our
	// nested-structure test event.
	blocker := make(chan struct{})
	prov := &blockingProvider{blocker: blocker}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "deep clone test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Wait until the run is actually running.
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

	// Emit an event with nested structures while the run is still active.
	runner.emit(run.ID, EventType("test.nested"), map[string]any{
		"tags":   []any{"alpha", "beta"},
		"nested": map[string]any{"inner": "original"},
	})

	// Subscriber 1: mutate nested values.
	history1, _, cancel1, err := runner.Subscribe(run.ID)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	cancel1()

	// Find our test event.
	var testEvent1 *Event
	for i := range history1 {
		if history1[i].Type == "test.nested" {
			testEvent1 = &history1[i]
			break
		}
	}
	if testEvent1 == nil {
		// Unblock before fatal so goroutines can clean up.
		close(blocker)
		t.Fatal("test.nested event not found in history1")
	}

	// Mutate the nested map and slice from subscriber 1's copy.
	if nested, ok := testEvent1.Payload["nested"].(map[string]any); ok {
		nested["inner"] = "TAMPERED"
		nested["extra"] = "INJECTED"
	}
	if tags, ok := testEvent1.Payload["tags"].([]any); ok && len(tags) > 0 {
		tags[0] = "TAMPERED_TAG"
	}

	// Subscriber 2: verify the stored events are unaffected.
	history2, _, cancel2, err := runner.Subscribe(run.ID)
	if err != nil {
		close(blocker)
		t.Fatalf("Subscribe (2): %v", err)
	}
	cancel2()

	var testEvent2 *Event
	for i := range history2 {
		if history2[i].Type == "test.nested" {
			testEvent2 = &history2[i]
			break
		}
	}
	if testEvent2 == nil {
		close(blocker)
		t.Fatal("test.nested event not found in history2")
	}

	// Check nested map was not corrupted.
	if nested, ok := testEvent2.Payload["nested"].(map[string]any); ok {
		if nested["inner"] != "original" {
			t.Errorf("nested.inner was corrupted: got %v, want %q", nested["inner"], "original")
		}
		if _, exists := nested["extra"]; exists {
			t.Error("nested map has injected 'extra' key from subscriber 1")
		}
	} else {
		t.Error("test event missing nested map")
	}

	// Check slice was not corrupted.
	if tags, ok := testEvent2.Payload["tags"].([]any); ok {
		if len(tags) == 0 || tags[0] != "alpha" {
			t.Errorf("tags[0] was corrupted: got %v, want %q", tags[0], "alpha")
		}
	} else {
		t.Error("test event missing tags slice")
	}

	// Let the run finish cleanly.
	close(blocker)
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)
}

// TestDeepClonePayloadUnit tests the deepClonePayload helper directly for
// correctness with nested structures and nil/empty inputs.
func TestDeepClonePayloadUnit(t *testing.T) {
	t.Parallel()

	t.Run("nil input", func(t *testing.T) {
		if got := deepClonePayload(nil); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("nil map value preserved", func(t *testing.T) {
		// Regression test: deepCloneValue must NOT drop map keys whose value is nil.
		// A nil value is semantically distinct from a missing key.
		orig := map[string]any{
			"present": "hello",
			"nilval":  nil,
		}
		cloned := deepClonePayload(orig)
		if _, ok := cloned["nilval"]; !ok {
			t.Error("nil-valued map key was silently dropped by deepClonePayload")
		}
		if cloned["nilval"] != nil {
			t.Errorf("nil-valued map key should remain nil, got %v", cloned["nilval"])
		}
	})

	t.Run("nested structures", func(t *testing.T) {
		orig := map[string]any{
			"str": "hello",
			"num": float64(42),
			"nested": map[string]any{
				"a": "b",
				"deep": map[string]any{
					"level": float64(3),
				},
			},
			"list": []any{"x", float64(1), map[string]any{"in_list": true}},
		}

		cloned := deepClonePayload(orig)

		// Mutate original nested structures.
		orig["nested"].(map[string]any)["a"] = "CHANGED"
		orig["nested"].(map[string]any)["deep"].(map[string]any)["level"] = float64(999)
		orig["list"].([]any)[0] = "CHANGED"
		orig["list"].([]any)[2].(map[string]any)["in_list"] = false

		// Cloned must be unaffected.
		if cloned["nested"].(map[string]any)["a"] != "b" {
			t.Error("nested.a was aliased")
		}
		if cloned["nested"].(map[string]any)["deep"].(map[string]any)["level"] != float64(3) {
			t.Error("nested.deep.level was aliased")
		}
		if cloned["list"].([]any)[0] != "x" {
			t.Error("list[0] was aliased")
		}
		if cloned["list"].([]any)[2].(map[string]any)["in_list"] != true {
			t.Error("list[2].in_list was aliased")
		}
	})
}

// TestPostTerminalEventsDropped verifies that events emitted after the terminal
// event (run.completed / run.failed) are silently dropped and do not appear in
// the forensic event history.
func TestPostTerminalEventsDropped(t *testing.T) {
	t.Parallel()

	prov := &stubProvider{turns: []CompletionResult{{Content: "done"}}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "terminal test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	// Collect the event count before injecting post-terminal events.
	eventsBefore := collectEvents(t, runner, run.ID)
	countBefore := len(eventsBefore)
	if countBefore == 0 {
		t.Fatal("expected at least one event before post-terminal emission")
	}

	// Emit several events after the run is already terminal.
	for i := 0; i < 5; i++ {
		runner.emit(run.ID, EventType("test.post_terminal"), map[string]any{"seq": i})
	}

	// The stored event count must not have increased.
	eventsAfter := collectEvents(t, runner, run.ID)
	if len(eventsAfter) != countBefore {
		t.Errorf("post-terminal events leaked into history: before=%d after=%d",
			countBefore, len(eventsAfter))
	}

	// Verify no post-terminal event type appears.
	for _, ev := range eventsAfter {
		if ev.Type == "test.post_terminal" {
			t.Errorf("post-terminal event found in history: %+v", ev)
		}
	}
}

// TestRecorderNotCalledAfterTerminal verifies that the rollout recorder is not
// invoked on events that arrive after the terminal event (ensures no
// record-after-close panic).  This exercises the atomic detach path in emit().
func TestRecorderNotCalledAfterTerminal(t *testing.T) {
	t.Parallel()

	rolloutDir := t.TempDir()
	prov := &stubProvider{turns: []CompletionResult{{Content: "done"}}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            2,
		RolloutDir:          rolloutDir,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "recorder test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	// Hammering emit() post-terminal must not panic (write on closed file).
	// The race detector will also flag any data race if the recorder is
	// accessed unsafely.
	for i := 0; i < 20; i++ {
		runner.emit(run.ID, EventType("test.post_terminal"), map[string]any{"i": i})
	}
}

// TestEmitNestedPayloadCallerMutationIsolation verifies that if a caller
// mutates a nested map or slice inside the payload AFTER calling emit(),
// the stored forensic event is not affected (deep-copy isolation, #228).
func TestEmitNestedPayloadCallerMutationIsolation(t *testing.T) {
	t.Parallel()

	blocker := make(chan struct{})
	prov := &blockingProvider{blocker: blocker}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "deep-clone caller mutation test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Wait until running.
	deadline := time.Now().Add(2 * time.Second)
	for {
		r, _ := runner.GetRun(run.ID)
		if r.Status == RunStatusRunning {
			break
		}
		if time.Now().After(deadline) {
			close(blocker)
			t.Fatal("timed out waiting for running status")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Build a payload with nested mutable structures.
	innerMap := map[string]any{"key": "original_value"}
	tags := []any{"tag1", "tag2"}
	payload := map[string]any{
		"nested": innerMap,
		"tags":   tags,
	}
	runner.emit(run.ID, EventType("test.caller_mutation"), payload)

	// Now mutate the caller's nested structures AFTER emit() has returned.
	innerMap["key"] = "MUTATED_BY_CALLER"
	innerMap["injected"] = "NEW_KEY"
	tags[0] = "MUTATED_TAG"

	// Subscribe and verify stored event is unaffected.
	history, _, cancel, err := runner.Subscribe(run.ID)
	if err != nil {
		close(blocker)
		t.Fatalf("Subscribe: %v", err)
	}
	cancel()

	var found *Event
	for i := range history {
		if history[i].Type == "test.caller_mutation" {
			found = &history[i]
			break
		}
	}

	close(blocker)
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	if found == nil {
		t.Fatal("test.caller_mutation event not found in history")
	}

	// Nested map must not have been mutated.
	if nested, ok := found.Payload["nested"].(map[string]any); ok {
		if nested["key"] != "original_value" {
			t.Errorf("nested.key was aliased: got %v, want %q", nested["key"], "original_value")
		}
		if _, exists := nested["injected"]; exists {
			t.Error("nested map has injected key from caller mutation")
		}
	} else {
		t.Error("stored event missing nested map")
	}

	// Slice must not have been mutated.
	if storedTags, ok := found.Payload["tags"].([]any); ok {
		if len(storedTags) == 0 || storedTags[0] != "tag1" {
			t.Errorf("tags[0] was aliased: got %v, want %q", storedTags[0], "tag1")
		}
	} else {
		t.Error("stored event missing tags slice")
	}
}

// TestMessageExportMutationIsolation verifies that callers cannot corrupt
// runner state by mutating the ToolCalls slice returned from GetRunMessages()
// or ConversationMessages(). Regression test for issue #231.
func TestMessageExportMutationIsolation(t *testing.T) {
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
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            4,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	// --- Test GetRunMessages isolation ---
	msgs1 := runner.GetRunMessages(run.ID)
	if msgs1 == nil {
		t.Fatal("GetRunMessages returned nil")
	}

	// Find the assistant message with ToolCalls.
	var assistantIdx int = -1
	for i, m := range msgs1 {
		if len(m.ToolCalls) > 0 {
			assistantIdx = i
			break
		}
	}
	if assistantIdx < 0 {
		t.Fatal("expected at least one message with ToolCalls")
	}

	originalID := msgs1[assistantIdx].ToolCalls[0].ID
	originalLen := len(msgs1[assistantIdx].ToolCalls)

	// Mutate the returned slice: modify existing ToolCall ID.
	msgs1[assistantIdx].ToolCalls[0].ID = "CORRUPTED"

	// Mutate the returned slice: append a new ToolCall.
	msgs1[assistantIdx].ToolCalls = append(msgs1[assistantIdx].ToolCalls, ToolCall{
		ID:   "injected",
		Name: "evil_tool",
	})

	// Second call must return pristine state.
	msgs2 := runner.GetRunMessages(run.ID)
	if msgs2 == nil {
		t.Fatal("second GetRunMessages returned nil")
	}

	for i, m := range msgs2 {
		if len(m.ToolCalls) > 0 {
			if m.ToolCalls[0].ID != originalID {
				t.Errorf("GetRunMessages: ToolCalls[0].ID corrupted: got %q, want %q",
					m.ToolCalls[0].ID, originalID)
			}
			if len(m.ToolCalls) != originalLen {
				t.Errorf("GetRunMessages: ToolCalls length changed: got %d, want %d (msg index %d)",
					len(m.ToolCalls), originalLen, i)
			}
			break
		}
	}

	// --- Test ConversationMessages isolation ---
	runFinal, _ := runner.GetRun(run.ID)
	convMsgs1, ok := runner.ConversationMessages(runFinal.ConversationID)
	if !ok {
		t.Fatal("ConversationMessages returned false")
	}

	// Find assistant message with ToolCalls in conversation messages.
	assistantIdx = -1
	for i, m := range convMsgs1 {
		if len(m.ToolCalls) > 0 {
			assistantIdx = i
			break
		}
	}
	if assistantIdx < 0 {
		t.Fatal("ConversationMessages: expected at least one message with ToolCalls")
	}

	convOriginalID := convMsgs1[assistantIdx].ToolCalls[0].ID
	convOriginalLen := len(convMsgs1[assistantIdx].ToolCalls)

	// Mutate returned conversation messages.
	convMsgs1[assistantIdx].ToolCalls[0].ID = "CONV_CORRUPTED"
	convMsgs1[assistantIdx].ToolCalls = append(convMsgs1[assistantIdx].ToolCalls, ToolCall{
		ID:   "conv_injected",
		Name: "evil_conv_tool",
	})

	// Second call must return pristine state.
	convMsgs2, ok := runner.ConversationMessages(runFinal.ConversationID)
	if !ok {
		t.Fatal("second ConversationMessages returned false")
	}

	for i, m := range convMsgs2 {
		if len(m.ToolCalls) > 0 {
			if m.ToolCalls[0].ID != convOriginalID {
				t.Errorf("ConversationMessages: ToolCalls[0].ID corrupted: got %q, want %q",
					m.ToolCalls[0].ID, convOriginalID)
			}
			if len(m.ToolCalls) != convOriginalLen {
				t.Errorf("ConversationMessages: ToolCalls length changed: got %d, want %d (msg index %d)",
					len(m.ToolCalls), convOriginalLen, i)
			}
			break
		}
	}
}

// waitForStatus polls GetRun until one of the target statuses is reached.
func waitForStatus(t *testing.T, r *Runner, runID string, targets ...RunStatus) RunStatus {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		run, ok := r.GetRun(runID)
		if !ok {
			t.Fatalf("run %q not found", runID)
		}
		for _, target := range targets {
			if run.Status == target {
				return run.Status
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for status %v, last status: %s", targets, run.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// collectEvents returns all events for a run via Subscribe.
func collectEvents(t *testing.T, r *Runner, runID string) []Event {
	t.Helper()
	history, _, cancel, err := r.Subscribe(runID)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	cancel()
	return history
}

// TestAccountingStructPointerFieldIsolation verifies that CompletionUsage
// structs inserted into event payloads by recordAccounting() are converted to
// map[string]any before storage, so that pointer fields (*int) are not shared
// across event payload copies delivered to different subscribers.
// Regression test for issue #233.
func TestAccountingStructPointerFieldIsolation(t *testing.T) {
	t.Parallel()

	// Build a CompletionResult whose Usage has non-nil pointer fields.
	cached := 42
	reasoning := 7
	usage := &CompletionUsage{
		PromptTokens:       100,
		CompletionTokens:   50,
		TotalTokens:        150,
		CachedPromptTokens: &cached,
		ReasoningTokens:    &reasoning,
	}

	prov := &stubProvider{turns: []CompletionResult{
		{Content: "done", Usage: usage},
	}}
	runner := NewRunner(prov, NewRegistry(), RunnerConfig{
		DefaultModel:        "test-model",
		DefaultSystemPrompt: "You are helpful.",
		MaxSteps:            2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "accounting isolation test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitForStatus(t, runner, run.ID, RunStatusCompleted, RunStatusFailed)

	// Collect events via first subscription.
	history1, _, cancel1, err := runner.Subscribe(run.ID)
	if err != nil {
		t.Fatalf("Subscribe (1): %v", err)
	}
	cancel1()

	// Find the usage.delta event in the first subscription.
	var usageDeltaIdx1 int = -1
	for i, ev := range history1 {
		if ev.Type == EventUsageDelta {
			usageDeltaIdx1 = i
			break
		}
	}
	if usageDeltaIdx1 < 0 {
		t.Fatal("no usage.delta event found in first subscription")
	}

	// Assert cumulative_usage is a map[string]any (not a CompletionUsage struct).
	// Before the fix, deepCloneValue returns the struct as-is, so the type
	// assertion to map[string]any will fail.
	cumUsage1, ok := history1[usageDeltaIdx1].Payload["cumulative_usage"].(map[string]any)
	if !ok {
		t.Fatalf("cumulative_usage is not map[string]any: got %T — struct was stored by reference instead of converted to map",
			history1[usageDeltaIdx1].Payload["cumulative_usage"])
	}

	// Assert turn_usage is also a map[string]any.
	_, ok = history1[usageDeltaIdx1].Payload["turn_usage"].(map[string]any)
	if !ok {
		t.Fatalf("turn_usage is not map[string]any: got %T — struct was stored by reference instead of converted to map",
			history1[usageDeltaIdx1].Payload["turn_usage"])
	}

	// Verify the expected values are present in the map.
	if pt, ok := cumUsage1["prompt_tokens"].(float64); !ok || int(pt) != 100 {
		t.Errorf("cumulative_usage.prompt_tokens: got %v (%T), want 100", cumUsage1["prompt_tokens"], cumUsage1["prompt_tokens"])
	}
	if ct, ok := cumUsage1["completion_tokens"].(float64); !ok || int(ct) != 50 {
		t.Errorf("cumulative_usage.completion_tokens: got %v, want 50", cumUsage1["completion_tokens"])
	}
	if cp, ok := cumUsage1["cached_prompt_tokens"].(float64); !ok || int(cp) != 42 {
		t.Errorf("cumulative_usage.cached_prompt_tokens: got %v, want 42", cumUsage1["cached_prompt_tokens"])
	}

	// Mutate the map obtained from the first subscription and verify that
	// the second subscription's payload is unaffected.
	cumUsage1["prompt_tokens"] = float64(9999)
	cumUsage1["cached_prompt_tokens"] = float64(9999)

	// Collect events via second subscription.
	history2, _, cancel2, err := runner.Subscribe(run.ID)
	if err != nil {
		t.Fatalf("Subscribe (2): %v", err)
	}
	cancel2()

	var usageDeltaIdx2 int = -1
	for i, ev := range history2 {
		if ev.Type == EventUsageDelta {
			usageDeltaIdx2 = i
			break
		}
	}
	if usageDeltaIdx2 < 0 {
		t.Fatal("no usage.delta event found in second subscription")
	}

	cumUsage2, ok := history2[usageDeltaIdx2].Payload["cumulative_usage"].(map[string]any)
	if !ok {
		t.Fatalf("cumulative_usage (sub2) is not map[string]any: got %T",
			history2[usageDeltaIdx2].Payload["cumulative_usage"])
	}

	// The second subscriber's copy must not have been corrupted by the mutation above.
	if pt, ok := cumUsage2["prompt_tokens"].(float64); !ok || int(pt) != 100 {
		t.Errorf("second subscriber cumulative_usage.prompt_tokens corrupted: got %v, want 100", cumUsage2["prompt_tokens"])
	}
	if cp, ok := cumUsage2["cached_prompt_tokens"].(float64); !ok || int(cp) != 42 {
		t.Errorf("second subscriber cumulative_usage.cached_prompt_tokens corrupted: got %v, want 42", cumUsage2["cached_prompt_tokens"])
	}
}
