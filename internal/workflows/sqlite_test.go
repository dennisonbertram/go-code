package workflows

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteStoreRunStepAndEventRoundTrip(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "workflows.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 4, 5, 12, 0, 0, 123, time.UTC)
	run := &Run{
		ID:                  "workflow-run-1",
		WorkflowName:        "review",
		Status:              RunStatusRunning,
		CurrentStepID:       "step-1",
		CurrentCheckpointID: "checkpoint-1",
		InputJSON:           `{"ticket":"649"}`,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	run.Status = RunStatusCompleted
	run.CurrentStepID = ""
	run.OutputJSON = `{"ok":true}`
	run.UpdatedAt = now.Add(time.Minute)
	if err := store.UpdateRun(context.Background(), run); err != nil {
		t.Fatalf("UpdateRun: %v", err)
	}
	loadedRun, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if loadedRun.Status != RunStatusCompleted {
		t.Fatalf("loaded status = %q, want %q", loadedRun.Status, RunStatusCompleted)
	}
	if loadedRun.OutputJSON != run.OutputJSON {
		t.Fatalf("loaded output = %q, want %q", loadedRun.OutputJSON, run.OutputJSON)
	}
	if !loadedRun.UpdatedAt.Equal(run.UpdatedAt) {
		t.Fatalf("loaded updated_at = %s, want %s", loadedRun.UpdatedAt, run.UpdatedAt)
	}

	state1 := &StepState{
		WorkflowRunID: run.ID,
		StepID:        "step-1",
		Status:        StepStatusRunning,
		StartedAt:     now,
		UpdatedAt:     now,
	}
	state2 := &StepState{
		WorkflowRunID: run.ID,
		StepID:        "step-2",
		Status:        StepStatusCompleted,
		OutputJSON:    `{"done":true}`,
		StartedAt:     now.Add(time.Second),
		UpdatedAt:     now.Add(time.Second),
	}
	if err := store.UpsertStepState(context.Background(), state2); err != nil {
		t.Fatalf("UpsertStepState step2: %v", err)
	}
	if err := store.UpsertStepState(context.Background(), state1); err != nil {
		t.Fatalf("UpsertStepState step1: %v", err)
	}
	state1.Status = StepStatusCompleted
	state1.OutputJSON = `{"result":"first"}`
	state1.UpdatedAt = now.Add(2 * time.Second)
	if err := store.UpsertStepState(context.Background(), state1); err != nil {
		t.Fatalf("UpsertStepState update step1: %v", err)
	}

	states, err := store.ListStepStates(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListStepStates: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("state count = %d, want 2", len(states))
	}
	if states[0].StepID != "step-1" || states[0].Status != StepStatusCompleted {
		t.Fatalf("states[0] = %+v", states[0])
	}
	if states[1].StepID != "step-2" {
		t.Fatalf("states[1] = %+v", states[1])
	}

	event1 := &Event{
		WorkflowRunID: run.ID,
		Seq:           1,
		Type:          "workflow.started",
		Payload:       map[string]any{"workflow": "review"},
		Timestamp:     now,
	}
	event2 := &Event{
		WorkflowRunID: run.ID,
		Seq:           2,
		Type:          "workflow.completed",
		Payload:       map[string]any{"ok": true},
		Timestamp:     now.Add(3 * time.Second),
	}
	if err := store.AppendEvent(context.Background(), event1); err != nil {
		t.Fatalf("AppendEvent event1: %v", err)
	}
	if err := store.AppendEvent(context.Background(), event2); err != nil {
		t.Fatalf("AppendEvent event2: %v", err)
	}
	events, err := store.GetEvents(context.Background(), run.ID, 1)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(events) != 1 || events[0].Seq != 2 || events[0].Type != "workflow.completed" {
		t.Fatalf("events after seq 1 = %+v", events)
	}
	if events[0].Payload["ok"] != true {
		t.Fatalf("event payload = %#v", events[0].Payload)
	}

	if got := formatTime(now); got == "" {
		t.Fatal("formatTime returned empty string")
	}
	if parsed := parseTime(formatTime(now)); !parsed.Equal(now) {
		t.Fatalf("parseTime(formatTime(now)) = %s, want %s", parsed, now)
	}
}
