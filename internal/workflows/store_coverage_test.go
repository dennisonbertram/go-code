package workflows

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestEngineDefinitionSubscribeAndFailure(t *testing.T) {
	t.Parallel()

	engine := NewEngine(Options{
		Definitions: []Definition{{
			Name:        "bad-flow",
			Description: "unsupported step",
			Steps: []StepDefinition{{
				ID:   "bad",
				Type: StepType("unsupported"),
			}},
		}},
		Store: NewMemoryStore(),
		Now:   time.Now,
	})
	if def, ok := engine.GetDefinition("bad-flow"); !ok || def.Name != "bad-flow" {
		t.Fatalf("GetDefinition = %#v, %v", def, ok)
	}

	run, err := engine.Start("bad-flow", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	history, stream, cancel, err := engine.Subscribe(run.ID)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	cancel()
	if history == nil {
		t.Fatal("expected non-nil history")
	}
	if _, ok := <-stream; ok {
		t.Fatal("expected canceled subscription channel to close")
	}

	finalRun, _, err := waitForWorkflowRun(engine, run.ID)
	if err != nil {
		t.Fatalf("waitForWorkflowRun: %v", err)
	}
	if finalRun.Status != RunStatusFailed {
		t.Fatalf("status = %q, want %q", finalRun.Status, RunStatusFailed)
	}
	if finalRun.Error == "" {
		t.Fatal("expected failure error")
	}
}

func TestWorkflowSQLiteStoreRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "workflows.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	run := &Run{
		ID:            "workflow-run-1",
		WorkflowName:  "smoke",
		Status:        RunStatusRunning,
		CurrentStepID: "step-1",
		InputJSON:     `{"task":"smoke"}`,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	run.Status = RunStatusCompleted
	run.OutputJSON = `{"ok":true}`
	run.UpdatedAt = now.Add(time.Minute)
	if err := store.UpdateRun(ctx, run); err != nil {
		t.Fatalf("UpdateRun: %v", err)
	}
	got, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.Status != RunStatusCompleted {
		t.Fatalf("status = %q, want %q", got.Status, RunStatusCompleted)
	}

	state := &StepState{
		WorkflowRunID: run.ID,
		StepID:        "step-1",
		Status:        StepStatusCompleted,
		OutputJSON:    `{"done":true}`,
		StartedAt:     now,
		UpdatedAt:     now.Add(time.Second),
	}
	if err := store.UpsertStepState(ctx, state); err != nil {
		t.Fatalf("UpsertStepState: %v", err)
	}
	states, err := store.ListStepStates(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListStepStates: %v", err)
	}
	if len(states) != 1 || states[0].StepID != "step-1" {
		t.Fatalf("states = %#v, want step-1", states)
	}

	for _, event := range []*Event{
		{WorkflowRunID: run.ID, Seq: 1, Type: "workflow.started", Payload: map[string]any{"workflow": "smoke"}, Timestamp: now},
		{WorkflowRunID: run.ID, Seq: 2, Type: "workflow.completed", Payload: map[string]any{"ok": true}, Timestamp: now.Add(time.Second)},
	} {
		if err := store.AppendEvent(ctx, event); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
	events, err := store.GetEvents(ctx, run.ID, 1)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(events) != 1 || events[0].Seq != 2 {
		t.Fatalf("events = %#v, want only seq 2", events)
	}
}

func TestMemoryStoreGetEventsFiltersAfterSeq(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()
	for _, event := range []*Event{
		{WorkflowRunID: "run-1", Seq: 1, Type: "one"},
		{WorkflowRunID: "run-1", Seq: 2, Type: "two"},
	} {
		if err := store.AppendEvent(ctx, event); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
	events, err := store.GetEvents(ctx, "run-1", 1)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(events) != 1 || events[0].Seq != 2 {
		t.Fatalf("events = %#v, want only seq 2", events)
	}
}
