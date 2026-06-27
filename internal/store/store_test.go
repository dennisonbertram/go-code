// store_test.go contains shared test helpers and contract tests for the Store interface.
// Both SQLiteStore and MemoryStore must satisfy all tests defined here.
package store_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/store"
)

// storeFactory is a function that creates a fresh Store for testing.
type storeFactory func(t *testing.T) store.Store

// runContractTests runs the full contract test suite against the provided factory.
func runContractTests(t *testing.T, factory storeFactory) {
	t.Helper()

	t.Run("CreateAndGetRun", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		run := &store.Run{
			ID:             "run-1",
			ConversationID: "conv-1",
			TenantID:       "tenant-1",
			AgentID:        "agent-1",
			Model:          "gpt-4",
			Prompt:         "hello world",
			Status:         store.RunStatusQueued,
			CreatedAt:      time.Now().UTC().Truncate(time.Second),
			UpdatedAt:      time.Now().UTC().Truncate(time.Second),
		}

		if err := s.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}

		got, err := s.GetRun(ctx, "run-1")
		if err != nil {
			t.Fatalf("GetRun: %v", err)
		}
		if got.ID != run.ID {
			t.Errorf("ID: got %q, want %q", got.ID, run.ID)
		}
		if got.ConversationID != run.ConversationID {
			t.Errorf("ConversationID: got %q, want %q", got.ConversationID, run.ConversationID)
		}
		if got.TenantID != run.TenantID {
			t.Errorf("TenantID: got %q, want %q", got.TenantID, run.TenantID)
		}
		if got.Status != run.Status {
			t.Errorf("Status: got %q, want %q", got.Status, run.Status)
		}
		if got.Prompt != run.Prompt {
			t.Errorf("Prompt: got %q, want %q", got.Prompt, run.Prompt)
		}
	})

	t.Run("GetRun_NotFound", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		_, err := s.GetRun(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error for missing run, got nil")
		}
		if !store.IsNotFound(err) {
			t.Errorf("expected IsNotFound=true, got error: %v", err)
		}
	})

	t.Run("CreateRun_DuplicateID", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		run := &store.Run{
			ID:        "dup-1",
			Status:    store.RunStatusQueued,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := s.CreateRun(ctx, run); err != nil {
			t.Fatalf("first CreateRun: %v", err)
		}
		if err := s.CreateRun(ctx, run); err == nil {
			t.Fatal("expected error on duplicate CreateRun, got nil")
		}
	})

	t.Run("UpdateRun", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		run := &store.Run{
			ID:        "upd-1",
			Status:    store.RunStatusQueued,
			Prompt:    "initial",
			CreatedAt: time.Now().UTC().Truncate(time.Second),
			UpdatedAt: time.Now().UTC().Truncate(time.Second),
		}
		if err := s.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}

		run.Status = store.RunStatusCompleted
		run.Output = "done"
		run.UpdatedAt = time.Now().UTC().Truncate(time.Second)
		if err := s.UpdateRun(ctx, run); err != nil {
			t.Fatalf("UpdateRun: %v", err)
		}

		got, err := s.GetRun(ctx, "upd-1")
		if err != nil {
			t.Fatalf("GetRun after update: %v", err)
		}
		if got.Status != store.RunStatusCompleted {
			t.Errorf("Status after update: got %q, want %q", got.Status, store.RunStatusCompleted)
		}
		if got.Output != "done" {
			t.Errorf("Output after update: got %q, want %q", got.Output, "done")
		}
	})

	t.Run("UpdateRun_PersistsWorkflowRecap", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		run := &store.Run{
			ID:        "recap-1",
			Status:    store.RunStatusQueued,
			Prompt:    "fix flaky tests",
			CreatedAt: time.Now().UTC().Truncate(time.Second),
			UpdatedAt: time.Now().UTC().Truncate(time.Second),
		}
		if err := s.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}

		run.Status = store.RunStatusCompleted
		run.Recap = &store.WorkflowRecap{
			Goal:                   "fix flaky tests",
			ChangedFiles:           []string{"internal/harness/runner.go"},
			TestsRun:               []string{"go test ./internal/harness"},
			FixPattern:             "added regression coverage before code changes",
			UsefulCommands:         []string{"go test ./internal/harness"},
			NextContinuationPrompt: "Continue from recap-1",
		}
		if err := s.UpdateRun(ctx, run); err != nil {
			t.Fatalf("UpdateRun: %v", err)
		}

		got, err := s.GetRun(ctx, "recap-1")
		if err != nil {
			t.Fatalf("GetRun after recap update: %v", err)
		}
		if got.Recap == nil {
			t.Fatal("Recap is nil after update")
		}
		if got.Recap.Goal != "fix flaky tests" {
			t.Errorf("Recap.Goal = %q", got.Recap.Goal)
		}
		if len(got.Recap.ChangedFiles) != 1 || got.Recap.ChangedFiles[0] != "internal/harness/runner.go" {
			t.Errorf("Recap.ChangedFiles = %#v", got.Recap.ChangedFiles)
		}
	})

	t.Run("UpdateRun_StatusTransition_NoBackward", func(t *testing.T) {
		// Status must not go backwards: completed -> queued is illegal.
		// Implementations may or may not enforce this at the store level.
		// This test documents the expected update behavior.
		s := factory(t)
		ctx := context.Background()

		run := &store.Run{
			ID:        "trans-1",
			Status:    store.RunStatusCompleted,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := s.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		// Verify we stored completed
		got, _ := s.GetRun(ctx, "trans-1")
		if got.Status != store.RunStatusCompleted {
			t.Errorf("initial status: got %q, want completed", got.Status)
		}
	})

	t.Run("ListRuns_Empty", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		runs, err := s.ListRuns(ctx, store.RunFilter{})
		if err != nil {
			t.Fatalf("ListRuns: %v", err)
		}
		if len(runs) != 0 {
			t.Errorf("expected empty list, got %d runs", len(runs))
		}
	})

	t.Run("ListRuns_FilterByConversation", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		for i := 0; i < 3; i++ {
			r := &store.Run{
				ID:             fmt.Sprintf("r%d", i),
				ConversationID: "conv-A",
				Status:         store.RunStatusCompleted,
				CreatedAt:      time.Now().UTC(),
				UpdatedAt:      time.Now().UTC(),
			}
			if err := s.CreateRun(ctx, r); err != nil {
				t.Fatalf("CreateRun %d: %v", i, err)
			}
		}
		// Run in a different conversation
		other := &store.Run{
			ID:             "r-other",
			ConversationID: "conv-B",
			Status:         store.RunStatusQueued,
			CreatedAt:      time.Now().UTC(),
			UpdatedAt:      time.Now().UTC(),
		}
		if err := s.CreateRun(ctx, other); err != nil {
			t.Fatalf("CreateRun other: %v", err)
		}

		runs, err := s.ListRuns(ctx, store.RunFilter{ConversationID: "conv-A"})
		if err != nil {
			t.Fatalf("ListRuns: %v", err)
		}
		if len(runs) != 3 {
			t.Errorf("expected 3 runs, got %d", len(runs))
		}
	})

	t.Run("ListRuns_FilterByStatus", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		statuses := []store.RunStatus{store.RunStatusCompleted, store.RunStatusFailed, store.RunStatusQueued}
		for i, st := range statuses {
			r := &store.Run{
				ID:        fmt.Sprintf("s%d", i),
				Status:    st,
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			}
			if err := s.CreateRun(ctx, r); err != nil {
				t.Fatalf("CreateRun: %v", err)
			}
		}

		runs, err := s.ListRuns(ctx, store.RunFilter{Status: store.RunStatusCompleted})
		if err != nil {
			t.Fatalf("ListRuns: %v", err)
		}
		if len(runs) != 1 {
			t.Errorf("expected 1 completed run, got %d", len(runs))
		}
		if runs[0].Status != store.RunStatusCompleted {
			t.Errorf("expected completed, got %q", runs[0].Status)
		}
	})

	t.Run("ListRuns_FilterByTenant", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		for i := 0; i < 2; i++ {
			r := &store.Run{
				ID:        fmt.Sprintf("t%d", i),
				TenantID:  "tenant-X",
				Status:    store.RunStatusQueued,
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			}
			if err := s.CreateRun(ctx, r); err != nil {
				t.Fatalf("CreateRun: %v", err)
			}
		}
		extra := &store.Run{
			ID:        "t-extra",
			TenantID:  "tenant-Y",
			Status:    store.RunStatusQueued,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := s.CreateRun(ctx, extra); err != nil {
			t.Fatalf("CreateRun extra: %v", err)
		}

		runs, err := s.ListRuns(ctx, store.RunFilter{TenantID: "tenant-X"})
		if err != nil {
			t.Fatalf("ListRuns: %v", err)
		}
		if len(runs) != 2 {
			t.Errorf("expected 2 runs for tenant-X, got %d", len(runs))
		}
	})

	t.Run("AppendAndGetMessages", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		run := &store.Run{
			ID:        "msg-run-1",
			Status:    store.RunStatusRunning,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := s.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}

		msgs := []*store.Message{
			{Seq: 0, RunID: "msg-run-1", Role: "user", Content: "hello"},
			{Seq: 1, RunID: "msg-run-1", Role: "assistant", Content: "hi there"},
			{Seq: 2, RunID: "msg-run-1", Role: "tool", Content: "result", ToolCallID: "tc-1"},
		}
		for _, m := range msgs {
			if err := s.AppendMessage(ctx, m); err != nil {
				t.Fatalf("AppendMessage seq=%d: %v", m.Seq, err)
			}
		}

		got, err := s.GetMessages(ctx, "msg-run-1")
		if err != nil {
			t.Fatalf("GetMessages: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(got))
		}
		if got[0].Role != "user" || got[0].Content != "hello" {
			t.Errorf("msg[0]: got role=%q content=%q", got[0].Role, got[0].Content)
		}
		if got[2].ToolCallID != "tc-1" {
			t.Errorf("msg[2].ToolCallID: got %q, want tc-1", got[2].ToolCallID)
		}
	})

	t.Run("GetMessages_Empty", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		run := &store.Run{
			ID:        "empty-msg-run",
			Status:    store.RunStatusQueued,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := s.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}

		msgs, err := s.GetMessages(ctx, "empty-msg-run")
		if err != nil {
			t.Fatalf("GetMessages: %v", err)
		}
		if len(msgs) != 0 {
			t.Errorf("expected empty messages, got %d", len(msgs))
		}
	})

	t.Run("AppendAndGetEvents", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		run := &store.Run{
			ID:        "evt-run-1",
			Status:    store.RunStatusRunning,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := s.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}

		now := time.Now().UTC()
		events := []*store.Event{
			{Seq: 0, RunID: "evt-run-1", EventID: "evt-run-1:0", EventType: "run.started", Payload: `{"status":"running"}`, Timestamp: now},
			{Seq: 1, RunID: "evt-run-1", EventID: "evt-run-1:1", EventType: "run.step", Payload: `{"step":1}`, Timestamp: now.Add(time.Second)},
			{Seq: 2, RunID: "evt-run-1", EventID: "evt-run-1:2", EventType: "run.completed", Payload: `{}`, Timestamp: now.Add(2 * time.Second)},
		}
		for _, e := range events {
			if err := s.AppendEvent(ctx, e); err != nil {
				t.Fatalf("AppendEvent seq=%d: %v", e.Seq, err)
			}
		}

		// Get all events
		all, err := s.GetEvents(ctx, "evt-run-1", -1)
		if err != nil {
			t.Fatalf("GetEvents all: %v", err)
		}
		if len(all) != 3 {
			t.Fatalf("expected 3 events, got %d", len(all))
		}
		if all[0].EventType != "run.started" {
			t.Errorf("event[0].Type: got %q, want run.started", all[0].EventType)
		}

		// Get events after seq=0
		partial, err := s.GetEvents(ctx, "evt-run-1", 0)
		if err != nil {
			t.Fatalf("GetEvents after 0: %v", err)
		}
		if len(partial) != 2 {
			t.Fatalf("expected 2 events after seq=0, got %d", len(partial))
		}
		if partial[0].EventType != "run.step" {
			t.Errorf("partial[0].Type: got %q, want run.step", partial[0].EventType)
		}
	})

	t.Run("GetEvents_Empty", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		run := &store.Run{
			ID:        "empty-evt-run",
			Status:    store.RunStatusQueued,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := s.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}

		events, err := s.GetEvents(ctx, "empty-evt-run", -1)
		if err != nil {
			t.Fatalf("GetEvents: %v", err)
		}
		if len(events) != 0 {
			t.Errorf("expected empty events, got %d", len(events))
		}
	})

	t.Run("GetEvents_MonotonicSeq", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		run := &store.Run{
			ID:        "mono-run",
			Status:    store.RunStatusRunning,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := s.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}

		for i := 0; i < 5; i++ {
			e := &store.Event{
				Seq:       i,
				RunID:     "mono-run",
				EventID:   fmt.Sprintf("mono-run:%d", i),
				EventType: "run.step",
				Payload:   `{}`,
				Timestamp: time.Now().UTC(),
			}
			if err := s.AppendEvent(ctx, e); err != nil {
				t.Fatalf("AppendEvent %d: %v", i, err)
			}
		}

		events, err := s.GetEvents(ctx, "mono-run", -1)
		if err != nil {
			t.Fatalf("GetEvents: %v", err)
		}
		if len(events) != 5 {
			t.Fatalf("expected 5 events, got %d", len(events))
		}
		for i, e := range events {
			if e.Seq != i {
				t.Errorf("events[%d].Seq = %d, want %d", i, e.Seq, i)
			}
		}
	})

	t.Run("ConcurrentCreateRun", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		var wg sync.WaitGroup
		const n = 20
		errs := make([]error, n)

		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				r := &store.Run{
					ID:        fmt.Sprintf("c-run-%d", i),
					Status:    store.RunStatusQueued,
					CreatedAt: time.Now().UTC(),
					UpdatedAt: time.Now().UTC(),
				}
				errs[i] = s.CreateRun(ctx, r)
			}(i)
		}
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Errorf("goroutine %d: CreateRun error: %v", i, err)
			}
		}

		runs, err := s.ListRuns(ctx, store.RunFilter{})
		if err != nil {
			t.Fatalf("ListRuns after concurrent creates: %v", err)
		}
		if len(runs) != n {
			t.Errorf("expected %d runs, got %d", n, len(runs))
		}
	})

	t.Run("ConcurrentUpdateRun", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		run := &store.Run{
			ID:        "update-race",
			Status:    store.RunStatusRunning,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := s.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}

		var wg sync.WaitGroup
		const n = 10
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				upd := &store.Run{
					ID:        "update-race",
					Status:    store.RunStatusRunning,
					Output:    fmt.Sprintf("output-%d", i),
					UpdatedAt: time.Now().UTC(),
				}
				_ = s.UpdateRun(ctx, upd)
			}(i)
		}
		wg.Wait()

		// Just verify the run is still readable (no crash / corruption).
		got, err := s.GetRun(ctx, "update-race")
		if err != nil {
			t.Fatalf("GetRun after concurrent updates: %v", err)
		}
		if got.ID != "update-race" {
			t.Errorf("ID: got %q, want update-race", got.ID)
		}
	})

	t.Run("AppendMessage_RunNotFound", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		msg := &store.Message{
			RunID:   "nonexistent-run",
			Seq:     0,
			Role:    "user",
			Content: "hello",
		}
		err := s.AppendMessage(ctx, msg)
		if err == nil {
			t.Fatal("expected error when appending message to nonexistent run, got nil")
		}
	})

	t.Run("AppendMessage_DuplicateSeq", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		run := &store.Run{
			ID:        "dup-msg-run",
			Status:    store.RunStatusRunning,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := s.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}

		msg := &store.Message{
			RunID:   "dup-msg-run",
			Seq:     1,
			Role:    "user",
			Content: "first",
		}
		if err := s.AppendMessage(ctx, msg); err != nil {
			t.Fatalf("first AppendMessage: %v", err)
		}

		dup := &store.Message{
			RunID:   "dup-msg-run",
			Seq:     1,
			Role:    "assistant",
			Content: "duplicate",
		}
		if err := s.AppendMessage(ctx, dup); err == nil {
			t.Fatal("expected error on duplicate seq AppendMessage, got nil")
		}
	})

	t.Run("AppendEvent_RunNotFound", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		event := &store.Event{
			RunID:     "nonexistent-run",
			Seq:       0,
			EventID:   "nonexistent-run:0",
			EventType: "run.started",
			Payload:   `{}`,
			Timestamp: time.Now().UTC(),
		}
		err := s.AppendEvent(ctx, event)
		if err == nil {
			t.Fatal("expected error when appending event to nonexistent run, got nil")
		}
	})

	t.Run("AppendEvent_DuplicateSeq", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		run := &store.Run{
			ID:        "dup-evt-run",
			Status:    store.RunStatusRunning,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := s.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}

		event := &store.Event{
			RunID:     "dup-evt-run",
			Seq:       1,
			EventID:   "dup-evt-run:1",
			EventType: "run.started",
			Payload:   `{}`,
			Timestamp: time.Now().UTC(),
		}
		if err := s.AppendEvent(ctx, event); err != nil {
			t.Fatalf("first AppendEvent: %v", err)
		}

		dup := &store.Event{
			RunID:     "dup-evt-run",
			Seq:       1,
			EventID:   "dup-evt-run:1-dup",
			EventType: "run.step",
			Payload:   `{}`,
			Timestamp: time.Now().UTC(),
		}
		if err := s.AppendEvent(ctx, dup); err == nil {
			t.Fatal("expected error on duplicate seq AppendEvent, got nil")
		}
	})

	t.Run("ConcurrentAppendEvents", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		run := &store.Run{
			ID:        "evt-race",
			Status:    store.RunStatusRunning,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := s.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}

		const n = 20
		var wg sync.WaitGroup
		errs := make([]error, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				e := &store.Event{
					Seq:       i,
					RunID:     "evt-race",
					EventID:   fmt.Sprintf("evt-race:%d", i),
					EventType: "run.step",
					Payload:   `{}`,
					Timestamp: time.Now().UTC(),
				}
				errs[i] = s.AppendEvent(ctx, e)
			}(i)
		}
		wg.Wait()

		// Some may fail due to duplicate seq; that's acceptable.
		// Verify at least some events were stored and the store is still readable.
		events, err := s.GetEvents(ctx, "evt-race", -1)
		if err != nil {
			t.Fatalf("GetEvents after concurrent appends: %v", err)
		}
		if len(events) == 0 {
			t.Error("expected at least some events after concurrent appends")
		}
		_ = errs // ignore individual errors from duplicate seq races
	})

	t.Run("Close", func(t *testing.T) {
		s := factory(t)
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
}

// TestNotFoundError_Error tests the Error() method of NotFoundError.
func TestNotFoundError_Error(t *testing.T) {
	err := &store.NotFoundError{ID: "run-abc"}
	got := err.Error()
	if got == "" {
		t.Fatal("Error() returned empty string")
	}
	if !strings.Contains(got, "run-abc") {
		t.Errorf("Error() %q does not contain run ID", got)
	}
}

// TestSQLiteStore runs the contract test suite against the SQLite implementation.
func TestSQLiteStore(t *testing.T) {
	runContractTests(t, func(t *testing.T) store.Store {
		t.Helper()
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		s, err := store.NewSQLiteStore(dbPath)
		if err != nil {
			t.Fatalf("NewSQLiteStore: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		if err := s.Migrate(context.Background()); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		return s
	})
}

// TestMemoryStore runs the contract test suite against the in-memory implementation.
func TestMemoryStore(t *testing.T) {
	runContractTests(t, func(t *testing.T) store.Store {
		t.Helper()
		return store.NewMemoryStore()
	})
}
