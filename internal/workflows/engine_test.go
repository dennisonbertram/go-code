package workflows

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go-agent-harness/internal/checkpoints"
	"go-agent-harness/internal/harness"
)

func TestEngineExecutesToolCheckpointBranchFlow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	checkpointSvc := checkpoints.NewService(checkpoints.NewMemoryStore(), func() time.Time { return now })
	engine := NewEngine(Options{
		Definitions: []Definition{{
			Name:        "review-flow",
			Description: "review flow",
			Steps: []StepDefinition{
				{
					ID:   "choose",
					Type: StepTypeTool,
					Tool: "choose_path",
				},
				{
					ID:   "approval",
					Type: StepTypeCheckpoint,
					Checkpoint: &CheckpointStep{
						SuspendPayload: map[string]any{"message": "Need approval"},
					},
				},
				{
					ID:      "branch",
					Type:    StepTypeBranch,
					Field:   "steps.choose.output.choice",
					Cases:   map[string]string{"docs": "docs"},
					Default: "fallback",
				},
				{
					ID:   "docs",
					Type: StepTypeTool,
					Tool: "record_docs",
				},
				{
					ID:   "fallback",
					Type: StepTypeTool,
					Tool: "record_fallback",
				},
			},
		}},
		Tools: toolExecutorFunc(func(ctx context.Context, name string, args json.RawMessage) (string, error) {
			switch name {
			case "choose_path":
				return `{"choice":"docs"}`, nil
			case "record_docs":
				return `{"result":"docs-path"}`, nil
			case "record_fallback":
				return `{"result":"fallback-path"}`, nil
			default:
				return "", nil
			}
		}),
		Checkpoints: checkpointSvc,
		Store:       NewMemoryStore(),
		Now:         func() time.Time { return now },
	})

	run, err := engine.Start("review-flow", map[string]any{"ticket": "123"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	var pending checkpoints.Record
	deadline := time.Now().Add(2 * time.Second)
	for {
		record, ok, err := checkpointSvc.PendingByWorkflowRun(context.Background(), run.ID)
		if err != nil {
			t.Fatalf("PendingByWorkflowRun: %v", err)
		}
		if ok {
			pending = record
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for workflow checkpoint")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := engine.ResumeRun(context.Background(), run.ID, map[string]any{"approved": true}); err != nil {
		t.Fatalf("ResumeRun: %v", err)
	}

	finalRun, stepStates, err := waitForWorkflowRun(engine, run.ID)
	if err != nil {
		t.Fatalf("waitForWorkflowRun: %v", err)
	}
	if finalRun.Status != RunStatusCompleted {
		t.Fatalf("status = %q, want %q", finalRun.Status, RunStatusCompleted)
	}
	if finalRun.CurrentCheckpointID != pending.ID {
		t.Fatalf("current checkpoint id = %q, want %q", finalRun.CurrentCheckpointID, pending.ID)
	}

	outputs := make(map[string]string, len(stepStates))
	for _, step := range stepStates {
		outputs[step.StepID] = step.OutputJSON
	}
	if outputs["docs"] == "" {
		t.Fatal("expected docs step output")
	}
	if outputs["fallback"] != "" {
		t.Fatalf("expected fallback step to be skipped, got %q", outputs["fallback"])
	}
}

func TestEngineExecutesRunStep(t *testing.T) {
	t.Parallel()

	engine := NewEngine(Options{
		Definitions: []Definition{{
			Name:        "run-flow",
			Description: "run flow",
			Steps: []StepDefinition{{
				ID:   "run",
				Type: StepTypeRun,
				Run: &RunStep{
					Prompt: "hello",
					Model:  "gpt-test",
				},
			}},
		}},
		Runner: &stubRunEngine{
			run: harness.Run{ID: "child-run", Status: harness.RunStatusCompleted, Output: "done"},
		},
		Store: NewMemoryStore(),
		Now:   time.Now,
	})

	run, err := engine.Start("run-flow", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	finalRun, stepStates, err := waitForWorkflowRun(engine, run.ID)
	if err != nil {
		t.Fatalf("waitForWorkflowRun: %v", err)
	}
	if finalRun.Status != RunStatusCompleted {
		t.Fatalf("status = %q, want %q", finalRun.Status, RunStatusCompleted)
	}
	if len(stepStates) != 1 {
		t.Fatalf("step state count = %d, want 1", len(stepStates))
	}
	if stepStates[0].StepID != "run" {
		t.Fatalf("step id = %q, want run", stepStates[0].StepID)
	}
	if stepStates[0].OutputJSON == "" {
		t.Fatal("expected run step output")
	}
}

func TestEngineDefinitionSubscribeAndFailurePaths(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	engine := NewEngine(Options{
		Definitions: []Definition{{
			Name: "bad-flow",
			Steps: []StepDefinition{{
				ID:   "bad",
				Type: StepType("unsupported"),
			}},
		}},
		Store: NewMemoryStore(),
		Now:   func() time.Time { return now },
	})
	def, ok := engine.GetDefinition("bad-flow")
	if !ok || def.Name != "bad-flow" {
		t.Fatalf("GetDefinition = (%+v, %v), want bad-flow true", def, ok)
	}

	run, err := engine.Start("bad-flow", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	history, live, cancel, err := engine.Subscribe(run.ID)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()
	if len(history) == 0 {
		t.Fatal("expected workflow.started history")
	}

	finalRun, _, err := waitForWorkflowRun(engine, run.ID)
	if err != nil {
		t.Fatalf("waitForWorkflowRun: %v", err)
	}
	if finalRun.Status != RunStatusFailed {
		t.Fatalf("status = %q, want failed", finalRun.Status)
	}
	if finalRun.Error == "" {
		t.Fatal("expected failure error")
	}

	select {
	case ev := <-live:
		if ev.WorkflowRunID != run.ID {
			t.Fatalf("live event run id = %q, want %q", ev.WorkflowRunID, run.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for live workflow event")
	}
}

type toolExecutorFunc func(ctx context.Context, name string, args json.RawMessage) (string, error)

func (f toolExecutorFunc) Execute(ctx context.Context, name string, args json.RawMessage) (string, error) {
	return f(ctx, name, args)
}

type stubRunEngine struct {
	run harness.Run
}

func (s *stubRunEngine) StartRun(req harness.RunRequest) (harness.Run, error) {
	s.run.Prompt = req.Prompt
	s.run.Model = req.Model
	return s.run, nil
}

func (s *stubRunEngine) GetRun(runID string) (harness.Run, bool) {
	return s.run, s.run.ID == runID
}

func (s *stubRunEngine) Subscribe(runID string) ([]harness.Event, <-chan harness.Event, func(), error) {
	ch := make(chan harness.Event, 1)
	ch <- harness.Event{RunID: runID, Type: harness.EventRunCompleted, Timestamp: time.Now().UTC()}
	close(ch)
	return nil, ch, func() {}, nil
}

func waitForWorkflowRun(engine *Engine, runID string) (Run, []StepState, error) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		run, steps, err := engine.GetRun(runID)
		if err != nil {
			return Run{}, nil, err
		}
		if run.Status == RunStatusCompleted || run.Status == RunStatusFailed {
			return run, steps, nil
		}
		if time.Now().After(deadline) {
			return Run{}, nil, context.DeadlineExceeded
		}
		time.Sleep(10 * time.Millisecond)
	}
}
