package workflows

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteStorePersistsRunsStepsAndEvents(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "workflows.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	now := time.Date(2026, 6, 26, 12, 0, 0, 123, time.UTC)
	run := &Run{
		ID:                  "wf-run",
		WorkflowName:        "coverage",
		Status:              RunStatusRunning,
		CurrentStepID:       "step-1",
		CurrentCheckpointID: "checkpoint-1",
		InputJSON:           `{"target":"repo"}`,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	run.Status = RunStatusCompleted
	run.CurrentStepID = ""
	run.OutputJSON = `{"ok":true}`
	run.UpdatedAt = now.Add(time.Second)
	if err := store.UpdateRun(ctx, run); err != nil {
		t.Fatalf("UpdateRun: %v", err)
	}

	loaded, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if loaded.Status != RunStatusCompleted || loaded.OutputJSON != `{"ok":true}` {
		t.Fatalf("loaded run = %+v", loaded)
	}

	if err := store.UpsertStepState(ctx, &StepState{
		WorkflowRunID: run.ID,
		StepID:        "step-1",
		Status:        StepStatusRunning,
		StartedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("UpsertStepState running: %v", err)
	}
	if err := store.UpsertStepState(ctx, &StepState{
		WorkflowRunID: run.ID,
		StepID:        "step-1",
		Status:        StepStatusCompleted,
		OutputJSON:    `{"result":"done"}`,
		StartedAt:     now,
		UpdatedAt:     now.Add(time.Second),
	}); err != nil {
		t.Fatalf("UpsertStepState completed: %v", err)
	}
	states, err := store.ListStepStates(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListStepStates: %v", err)
	}
	if len(states) != 1 || states[0].Status != StepStatusCompleted {
		t.Fatalf("states = %+v", states)
	}

	if err := store.AppendEvent(ctx, &Event{
		WorkflowRunID: run.ID,
		Seq:           1,
		Type:          "workflow.started",
		Payload:       map[string]any{"workflow": "coverage"},
		Timestamp:     now,
	}); err != nil {
		t.Fatalf("AppendEvent 1: %v", err)
	}
	if err := store.AppendEvent(ctx, &Event{
		WorkflowRunID: run.ID,
		Seq:           2,
		Type:          "workflow.completed",
		Payload:       map[string]any{"workflow": "coverage"},
		Timestamp:     now.Add(time.Second),
	}); err != nil {
		t.Fatalf("AppendEvent 2: %v", err)
	}
	events, err := store.GetEvents(ctx, run.ID, 1)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != "workflow.completed" {
		t.Fatalf("events = %+v", events)
	}
}
