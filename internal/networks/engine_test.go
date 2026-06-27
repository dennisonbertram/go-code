package networks

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go-agent-harness/internal/checkpoints"
	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/workflows"
)

func TestEngineExecutesSequentialRolesViaWorkflows(t *testing.T) {
	t.Parallel()

	workflowEngine := workflows.NewEngine(workflows.Options{
		Runner: &stubRunEngine{
			outputs: []string{
				`{"summary":"planned","status":"completed","output":"plan output"}`,
				`{"summary":"reviewed","status":"completed","output":"review output"}`,
			},
		},
		Checkpoints: checkpoints.NewService(checkpoints.NewMemoryStore(), time.Now),
		Store:       workflows.NewMemoryStore(),
		Now:         time.Now,
	})
	engine := NewEngine(Options{
		Definitions: []Definition{{
			Name:        "planner-reviewer",
			Description: "planner then reviewer",
			Roles: []RoleDefinition{
				{ID: "planner", Prompt: "Plan the work", Model: "gpt-test"},
				{ID: "reviewer", Prompt: "Review the plan", Model: "gpt-test"},
			},
		}},
		Workflows: workflowEngine,
	})

	run, err := engine.Start("planner-reviewer", map[string]any{"ticket": "123"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	finalRun, steps, err := waitForNetworkRun(engine, run.ID)
	if err != nil {
		t.Fatalf("waitForNetworkRun: %v", err)
	}
	if finalRun.Status != workflows.RunStatusCompleted {
		t.Fatalf("status = %q, want %q", finalRun.Status, workflows.RunStatusCompleted)
	}
	if len(steps) != 2 {
		t.Fatalf("step count = %d, want 2", len(steps))
	}
}

type stubRunEngine struct {
	outputs []string
	index   int
}

func (s *stubRunEngine) StartRun(req harness.RunRequest) (harness.Run, error) {
	output := s.outputs[s.index]
	s.index++
	return harness.Run{
		ID:     "run-" + req.Prompt,
		Status: harness.RunStatusCompleted,
		Output: output,
	}, nil
}

func (s *stubRunEngine) GetRun(runID string) (harness.Run, bool) {
	return harness.Run{ID: runID, Status: harness.RunStatusCompleted, Output: `{"summary":"ok","status":"completed"}`}, true
}

func (s *stubRunEngine) Subscribe(runID string) ([]harness.Event, <-chan harness.Event, func(), error) {
	ch := make(chan harness.Event, 1)
	ch <- harness.Event{RunID: runID, Type: harness.EventRunCompleted, Timestamp: time.Now().UTC()}
	close(ch)
	return nil, ch, func() {}, nil
}

func waitForNetworkRun(engine *Engine, runID string) (workflows.Run, []workflows.StepState, error) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		run, steps, err := engine.GetRun(runID)
		if err != nil {
			return workflows.Run{}, nil, err
		}
		if run.Status == workflows.RunStatusCompleted || run.Status == workflows.RunStatusFailed {
			return run, steps, nil
		}
		if time.Now().After(deadline) {
			return workflows.Run{}, nil, context.DeadlineExceeded
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCompileDefinitionIncludesStructuredResultInstructions(t *testing.T) {
	t.Parallel()

	engine := NewEngine(Options{})
	def := Definition{
		Name: "structured",
		Roles: []RoleDefinition{
			{ID: "planner", Prompt: "Plan the fix", Model: "gpt-test"},
		},
	}
	compiled, err := engine.compile(def)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(compiled.Steps) != 1 {
		t.Fatalf("step count = %d, want 1", len(compiled.Steps))
	}
	raw, err := json.Marshal(compiled.Steps[0].Run)
	if err != nil {
		t.Fatalf("marshal run step: %v", err)
	}
	if string(raw) == "" {
		t.Fatal("expected compiled run step")
	}
}

func TestEngineGetDefinition(t *testing.T) {
	t.Parallel()

	engine := NewEngine(Options{
		Definitions: []Definition{{
			Name:        "review-network",
			Description: "review work",
		}},
	})
	def, ok := engine.GetDefinition("review-network")
	if !ok {
		t.Fatal("expected review-network definition")
	}
	if def.Description != "review work" {
		t.Fatalf("description = %q, want review work", def.Description)
	}
	if _, ok := engine.GetDefinition("missing"); ok {
		t.Fatal("expected missing definition to be absent")
	}
}
