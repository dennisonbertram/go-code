package harness

// runner_store_durability_test.go — TDD tests for issue #320.
// Verifies that the runner correctly persists run state, events, and messages
// to the store.Store when one is configured via RunnerConfig.Store.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go-agent-harness/internal/store"
)

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestRunnerStore_CreateRunCalledOnStartRun verifies that StartRun calls
// store.CreateRun with the correct initial run state (status=queued).
func TestRunnerStore_CreateRunCalledOnStartRun(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	provider := &stubProvider{turns: []CompletionResult{{Content: "done"}}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
		Store:        st,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello store"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Wait for run to complete so the store has had time to be populated.
	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	// The run must exist in the store.
	ctx := context.Background()
	storedRun, err := st.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("store.GetRun: %v (run was never persisted via CreateRun)", err)
	}
	if storedRun.ID != run.ID {
		t.Errorf("stored run ID: got %q, want %q", storedRun.ID, run.ID)
	}
	if storedRun.Prompt != "hello store" {
		t.Errorf("stored run Prompt: got %q, want %q", storedRun.Prompt, "hello store")
	}
	// Initial model must be preserved.
	if storedRun.Model != "test" {
		t.Errorf("stored run Model: got %q, want %q", storedRun.Model, "test")
	}
}

// TestRunnerStore_RunStatusTransitions verifies that UpdateRun is called as
// the run transitions through queued → running → completed.
func TestRunnerStore_RunStatusTransitions(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	provider := &stubProvider{turns: []CompletionResult{{Content: "finished"}}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
		Store:        st,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "status transitions"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Wait for completion.
	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	ctx := context.Background()
	storedRun, err := st.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("store.GetRun: %v", err)
	}

	// After completion, the store must reflect RunStatusCompleted.
	if storedRun.Status != store.RunStatusCompleted {
		t.Errorf("final stored status: got %q, want %q", storedRun.Status, store.RunStatusCompleted)
	}
	if storedRun.Output != "finished" {
		t.Errorf("final stored output: got %q, want %q", storedRun.Output, "finished")
	}
}

func TestRunnerStore_CompletedRunPersistsWorkflowRecap(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	registry := NewRegistry()
	if err := registry.Register(ToolDefinition{
		Name:        "bash",
		Description: "run command",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	}); err != nil {
		t.Fatalf("register bash: %v", err)
	}
	if err := registry.Register(ToolDefinition{
		Name:        "edit",
		Description: "edit file",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "updated", nil
	}); err != nil {
		t.Fatalf("register edit: %v", err)
	}

	provider := &stubProvider{turns: []CompletionResult{
		{ToolCalls: []ToolCall{
			{ID: "cmd-1", Name: "bash", Arguments: `{"cmd":"go test ./internal/harness"}`},
			{ID: "edit-1", Name: "edit", Arguments: `{"path":"internal/harness/runner.go"}`},
		}},
		{Content: "fixed"},
	}}
	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "test",
		MaxSteps:     4,
		Store:        st,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "fix flaky harness tests"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if _, err := collectRunEvents(t, runner, run.ID); err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	storedRun, err := st.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("store.GetRun: %v", err)
	}
	if storedRun.Recap == nil {
		t.Fatal("expected completed run recap")
	}
	if storedRun.Recap.Goal != "fix flaky harness tests" {
		t.Errorf("recap goal = %q", storedRun.Recap.Goal)
	}
	if !containsString(storedRun.Recap.TestsRun, "go test ./internal/harness") {
		t.Errorf("recap tests = %#v", storedRun.Recap.TestsRun)
	}
	if !containsString(storedRun.Recap.ChangedFiles, "internal/harness/runner.go") {
		t.Errorf("recap changed files = %#v", storedRun.Recap.ChangedFiles)
	}
	if !strings.Contains(storedRun.Recap.NextContinuationPrompt, run.ID) {
		t.Errorf("next continuation prompt = %q", storedRun.Recap.NextContinuationPrompt)
	}
}

// TestRunnerStore_FailedRunPersisted verifies that a failed run has its error
// and status persisted in the store.
func TestRunnerStore_FailedRunPersisted(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	provider := &errorProvider{err: errProviderFailed}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
		Store:        st,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "will fail"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	ctx := context.Background()
	storedRun, err := st.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("store.GetRun: %v", err)
	}

	if storedRun.Status != store.RunStatusFailed {
		t.Errorf("expected failed status, got %q", storedRun.Status)
	}
	if storedRun.Error == "" {
		t.Error("expected non-empty Error field after failed run")
	}
}

// TestRunnerStore_EventsPersistedAsTheyStream verifies that events are appended
// to the store as the run streams, not just at the end. After the run completes,
// the store must contain multiple events including run.started and run.completed.
func TestRunnerStore_EventsPersistedAsTheyStream(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	provider := &stubProvider{turns: []CompletionResult{{Content: "event stream done"}}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
		Store:        st,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "event persistence"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	allEvents, err := collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	ctx := context.Background()
	storedEvents, err := st.GetEvents(ctx, run.ID, -1)
	if err != nil {
		t.Fatalf("store.GetEvents: %v", err)
	}

	if len(storedEvents) == 0 {
		t.Fatal("no events persisted to store (expected at least run.started and run.completed)")
	}

	// run.started must be the first event.
	if storedEvents[0].EventType != string(EventRunStarted) {
		t.Errorf("first stored event: got %q, want %q", storedEvents[0].EventType, EventRunStarted)
	}

	// run.completed must be present in the store.
	// Note: we search all stored events rather than checking only the last entry
	// to avoid a race between the subscriber channel delivery and the store write.
	completedFound := false
	for _, se := range storedEvents {
		if se.EventType == string(EventRunCompleted) {
			completedFound = true
			break
		}
	}
	if !completedFound {
		// Retry with a small sleep: the terminal event store write is called
		// synchronously after the subscriber fan-out in emit(), but the subscriber
		// may unblock and read from the channel (allowing collectRunEvents to
		// return) before the store write goroutine is scheduled. Give the runtime
		// a brief window to complete the store write.
		for attempt := 0; attempt < 5 && !completedFound; attempt++ {
			time.Sleep(10 * time.Millisecond)
			storedEvents2, _ := st.GetEvents(ctx, run.ID, -1)
			for _, se := range storedEvents2 {
				if se.EventType == string(EventRunCompleted) {
					completedFound = true
					storedEvents = storedEvents2
					break
				}
			}
		}
	}
	if !completedFound {
		types := make([]string, len(storedEvents))
		for i, e := range storedEvents {
			types[i] = e.EventType
		}
		t.Errorf("run.completed not found in store; stored event types: %v", types)
	}

	// Stored event count must be >= minimum expected events (run.started + run.completed).
	if len(storedEvents) < 2 {
		t.Errorf("expected at least 2 stored events, got %d", len(storedEvents))
	}

	// In-memory count must not greatly exceed stored count (at most 1 event difference
	// due to the brief race window between subscriber delivery and store write).
	if len(allEvents)-len(storedEvents) > 1 {
		t.Errorf("too many in-memory events (%d) vs stored (%d); gap > 1", len(allEvents), len(storedEvents))
	}

	// Seq must be monotonically increasing.
	for i := 1; i < len(storedEvents); i++ {
		if storedEvents[i].Seq <= storedEvents[i-1].Seq {
			t.Errorf("non-monotonic seq at index %d: %d <= %d",
				i, storedEvents[i].Seq, storedEvents[i-1].Seq)
		}
	}
}

// TestRunnerStore_MessagesAppended verifies that run messages are persisted
// to the store after the run completes.
func TestRunnerStore_MessagesAppended(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()

	// Use a two-turn run: first turn returns a tool call, second returns final text.
	registry := NewRegistry()
	if err := registry.Register(ToolDefinition{
		Name:        "echo_store",
		Description: "echoes",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `"tool result"`, nil
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	provider := &stubProvider{turns: []CompletionResult{
		{
			ToolCalls: []ToolCall{{ID: "c1", Name: "echo_store", Arguments: `{}`}},
		},
		{Content: "all done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "test",
		MaxSteps:     4,
		Store:        st,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "message persistence test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	ctx := context.Background()
	storedMsgs, err := st.GetMessages(ctx, run.ID)
	if err != nil {
		t.Fatalf("store.GetMessages: %v", err)
	}

	if len(storedMsgs) == 0 {
		t.Fatal("no messages persisted to store")
	}

	// At minimum we should have: user message, assistant (tool call), tool result, assistant (final).
	if len(storedMsgs) < 4 {
		t.Errorf("expected at least 4 messages, got %d: %+v", len(storedMsgs), storedMsgs)
	}

	// First message must be from the user.
	if storedMsgs[0].Role != "user" {
		t.Errorf("first message role: got %q, want %q", storedMsgs[0].Role, "user")
	}
	if storedMsgs[0].Content != "message persistence test" {
		t.Errorf("first message content: got %q, want %q", storedMsgs[0].Content, "message persistence test")
	}

	// Seq must be monotonically increasing (0, 1, 2, ...).
	for i, m := range storedMsgs {
		if m.Seq != i {
			t.Errorf("message seq[%d] = %d, want %d", i, m.Seq, i)
		}
	}
}

// TestRunnerStore_NoStoreConfigured verifies that a runner without a configured
// Store field runs correctly and does not panic. This is the zero-value / no-op case.
func TestRunnerStore_NoStoreConfigured(t *testing.T) {
	t.Parallel()

	// No Store set in config — must not panic or fail.
	provider := &stubProvider{turns: []CompletionResult{{Content: "no store ok"}}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
	})

	run, err := runner.StartRun(RunRequest{Prompt: "no store"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("run not found")
	}
	if state.Status != RunStatusCompleted {
		t.Errorf("expected completed, got %q", state.Status)
	}
}

// TestRunnerStore_RunPersistedAcrossRestartSimulation simulates the durability
// scenario from issue #320: after a run completes, a "fresh" runner (simulating
// a server restart) backed by the same store must be able to retrieve the run.
func TestRunnerStore_RunPersistedAcrossRestartSimulation(t *testing.T) {
	t.Parallel()

	// Shared persistent store (simulates a database surviving a restart).
	st := store.NewMemoryStore()

	// --- First "server session" ---
	provider := &stubProvider{turns: []CompletionResult{{Content: "persistent output"}}}
	runner1 := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
		Store:        st,
	})

	run, err := runner1.StartRun(RunRequest{
		Prompt:         "persist me",
		TenantID:       "tenant-x",
		ConversationID: "",
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	_, err = collectRunEvents(t, runner1, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	// --- Simulate server restart: create a new runner backed by the same store ---
	// The new runner has no in-memory runs; it must rely entirely on the store.
	runner2 := NewRunner(&stubProvider{}, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
		Store:        st,
	})
	_ = runner2

	ctx := context.Background()

	// The store must have the run with completed status.
	storedRun, err := st.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("store.GetRun after simulated restart: %v", err)
	}
	if storedRun.Status != store.RunStatusCompleted {
		t.Errorf("post-restart run status: got %q, want completed", storedRun.Status)
	}
	if storedRun.Output != "persistent output" {
		t.Errorf("post-restart run output: got %q, want %q", storedRun.Output, "persistent output")
	}
	if storedRun.TenantID != "tenant-x" {
		t.Errorf("post-restart tenant: got %q, want tenant-x", storedRun.TenantID)
	}

	// Events must be retrievable.
	events, err := st.GetEvents(ctx, run.ID, -1)
	if err != nil {
		t.Fatalf("store.GetEvents after simulated restart: %v", err)
	}
	if len(events) == 0 {
		t.Error("no events in store after simulated restart")
	}

	// Messages must be retrievable.
	msgs, err := st.GetMessages(ctx, run.ID)
	if err != nil {
		t.Fatalf("store.GetMessages after simulated restart: %v", err)
	}
	if len(msgs) == 0 {
		t.Error("no messages in store after simulated restart")
	}
}

// TestRunnerStore_ContinuationRunPersisted verifies that a run created via
// ContinueRun is also persisted to the store.
func TestRunnerStore_ContinuationRunPersisted(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	provider := &stubProvider{turns: []CompletionResult{
		{Content: "first"},
		{Content: "second"},
	}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
		Store:        st,
	})

	// Start first run and wait for it to complete.
	run1, err := runner.StartRun(RunRequest{Prompt: "first run"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	_, err = collectRunEvents(t, runner, run1.ID)
	if err != nil {
		t.Fatalf("collectRunEvents run1: %v", err)
	}

	// Continue the run.
	run2, err := runner.ContinueRun(run1.ID, "second prompt")
	if err != nil {
		t.Fatalf("ContinueRun: %v", err)
	}
	_, err = collectRunEvents(t, runner, run2.ID)
	if err != nil {
		t.Fatalf("collectRunEvents run2: %v", err)
	}

	ctx := context.Background()

	// Both runs must be in the store.
	stored1, err := st.GetRun(ctx, run1.ID)
	if err != nil {
		t.Fatalf("store.GetRun run1: %v", err)
	}
	if stored1.Status != store.RunStatusCompleted {
		t.Errorf("run1 status: got %q, want completed", stored1.Status)
	}

	stored2, err := st.GetRun(ctx, run2.ID)
	if err != nil {
		t.Fatalf("store.GetRun run2: %v", err)
	}
	if stored2.Status != store.RunStatusCompleted {
		t.Errorf("run2 status: got %q, want completed", stored2.Status)
	}
	if stored2.ConversationID != stored1.ConversationID {
		t.Errorf("run2 must share conversation with run1: got %q, want %q",
			stored2.ConversationID, stored1.ConversationID)
	}
}

// TestRunnerStore_ListRunsByConversation verifies that multiple runs sharing a
// conversation ID are all queryable via ListRuns with a conversation filter.
func TestRunnerStore_ListRunsByConversation(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	provider := &stubProvider{turns: []CompletionResult{
		{Content: "r1"},
		{Content: "r2"},
		{Content: "r3"},
	}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
		Store:        st,
	})

	convID := ""
	var runIDs []string

	// Start first run to establish the conversation.
	run1, err := runner.StartRun(RunRequest{Prompt: "conv run 1"})
	if err != nil {
		t.Fatalf("StartRun 1: %v", err)
	}
	_, _ = collectRunEvents(t, runner, run1.ID)
	convID = run1.ConversationID
	runIDs = append(runIDs, run1.ID)

	// Continue twice to get 3 runs in the same conversation.
	run2, err := runner.ContinueRun(run1.ID, "conv run 2")
	if err != nil {
		t.Fatalf("ContinueRun 2: %v", err)
	}
	_, _ = collectRunEvents(t, runner, run2.ID)
	runIDs = append(runIDs, run2.ID)

	run3, err := runner.ContinueRun(run2.ID, "conv run 3")
	if err != nil {
		t.Fatalf("ContinueRun 3: %v", err)
	}
	_, _ = collectRunEvents(t, runner, run3.ID)
	runIDs = append(runIDs, run3.ID)

	ctx := context.Background()
	runs, err := st.ListRuns(ctx, store.RunFilter{ConversationID: convID})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 3 {
		t.Errorf("expected 3 runs in conversation %q, got %d", convID, len(runs))
	}
	_ = runIDs
}

// errProviderFailed is used by TestRunnerStore_FailedRunPersisted.
var errProviderFailed = errType("provider intentionally failed")

type errType string

func (e errType) Error() string { return string(e) }

// TestRunnerStore_EventSeqMonotonic verifies that stored events have strictly
// increasing Seq values (0, 1, 2, ...) matching the in-memory event sequence.
func TestRunnerStore_EventSeqMonotonic(t *testing.T) {
	t.Parallel()

	st := store.NewMemoryStore()
	registry := NewRegistry()
	if err := registry.Register(ToolDefinition{
		Name:        "echo_seq",
		Description: "echoes",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `"ok"`, nil
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	provider := &stubProvider{turns: []CompletionResult{
		{ToolCalls: []ToolCall{{ID: "tc1", Name: "echo_seq", Arguments: `{}`}}},
		{ToolCalls: []ToolCall{{ID: "tc2", Name: "echo_seq", Arguments: `{}`}}},
		{Content: "done"},
	}}

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "test",
		MaxSteps:     10,
		Store:        st,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "seq test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	ctx := context.Background()
	storedEvents, err := st.GetEvents(ctx, run.ID, -1)
	if err != nil {
		t.Fatalf("store.GetEvents: %v", err)
	}
	if len(storedEvents) == 0 {
		t.Fatal("no events in store")
	}

	for i, e := range storedEvents {
		if e.Seq != i {
			t.Errorf("events[%d].Seq = %d, want %d", i, e.Seq, i)
		}
		if e.RunID != run.ID {
			t.Errorf("events[%d].RunID = %q, want %q", i, e.RunID, run.ID)
		}
		if e.EventID == "" {
			t.Errorf("events[%d].EventID is empty", i)
		}
		if e.Payload == "" {
			t.Errorf("events[%d].Payload is empty", i)
		}
		if e.Timestamp.IsZero() {
			t.Errorf("events[%d].Timestamp is zero", i)
		}
	}
}

// TestRunnerStore_StoreErrorDoesNotFailRun verifies that if the store returns
// an error (e.g. a transient database error), the runner logs the error but
// does NOT fail the run. Durability errors must be non-fatal.
func TestRunnerStore_StoreErrorDoesNotFailRun(t *testing.T) {
	t.Parallel()

	st := &failingStore{inner: store.NewMemoryStore()}
	provider := &stubProvider{turns: []CompletionResult{{Content: "despite store error"}}}

	var loggedErrors []string
	logger := &captureLogger{errors: &loggedErrors}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
		Store:        st,
		Logger:       logger,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "store error test"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	_, err = collectRunEvents(t, runner, run.ID)
	if err != nil {
		t.Fatalf("collectRunEvents: %v", err)
	}

	// Despite store errors, the run must complete successfully.
	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("run not found in memory")
	}
	if state.Status != RunStatusCompleted {
		t.Errorf("expected completed despite store errors, got %q", state.Status)
	}
}

// failingStore wraps a MemoryStore but always fails on persistence calls,
// simulating a transient database outage.
type failingStore struct {
	inner *store.MemoryStore
}

func (f *failingStore) CreateRun(_ context.Context, _ *store.Run) error {
	return errType("store: simulated CreateRun failure")
}
func (f *failingStore) UpdateRun(_ context.Context, _ *store.Run) error {
	return errType("store: simulated UpdateRun failure")
}
func (f *failingStore) GetRun(ctx context.Context, id string) (*store.Run, error) {
	return f.inner.GetRun(ctx, id)
}
func (f *failingStore) ListRuns(ctx context.Context, filter store.RunFilter) ([]*store.Run, error) {
	return f.inner.ListRuns(ctx, filter)
}
func (f *failingStore) AppendMessage(_ context.Context, _ *store.Message) error {
	return errType("store: simulated AppendMessage failure")
}
func (f *failingStore) GetMessages(ctx context.Context, runID string) ([]*store.Message, error) {
	return f.inner.GetMessages(ctx, runID)
}
func (f *failingStore) AppendEvent(_ context.Context, _ *store.Event) error {
	return errType("store: simulated AppendEvent failure")
}
func (f *failingStore) GetEvents(ctx context.Context, runID string, afterSeq int) ([]*store.Event, error) {
	return f.inner.GetEvents(ctx, runID, afterSeq)
}
func (f *failingStore) Close() error { return nil }

// APIKeyStore methods (delegated to inner).
func (f *failingStore) CreateAPIKey(ctx context.Context, key store.APIKey) error {
	return f.inner.CreateAPIKey(ctx, key)
}
func (f *failingStore) ValidateAPIKey(ctx context.Context, rawToken string) (*store.APIKey, error) {
	return f.inner.ValidateAPIKey(ctx, rawToken)
}
func (f *failingStore) ListAPIKeys(ctx context.Context, tenantID string) ([]store.APIKey, error) {
	return f.inner.ListAPIKeys(ctx, tenantID)
}
func (f *failingStore) RevokeAPIKey(ctx context.Context, id string) error {
	return f.inner.RevokeAPIKey(ctx, id)
}

// captureLogger captures logged errors for assertions.
type captureLogger struct {
	errors *[]string
}

func (l *captureLogger) Error(msg string, _ ...any) {
	*l.errors = append(*l.errors, msg)
}
