package deferred

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go-agent-harness/internal/goals"
)

func TestGoalToolHandlerCoversLifecycleActions(t *testing.T) {
	t.Parallel()

	mgr := goals.NewManager(nil)
	factory := NewGoalTools(mgr)
	if factory == nil {
		t.Fatal("NewGoalTools returned nil")
	}
	tool := factory()
	ctx := context.Background()

	createOut, err := tool.Handler(ctx, json.RawMessage(`{
		"action":"create",
		"id":"goal-coverage",
		"name":"Cover goal tool",
		"description":"Exercise handler paths",
		"verify_criteria":"tests pass",
		"depends_on":["goal-dependency"],
		"progress_total":4,
		"progress_completed":1,
		"metadata":{"source":"coverage"}
	}`))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(createOut, `"action":"created"`) {
		t.Fatalf("create output = %s", createOut)
	}

	getOut, err := tool.Handler(ctx, json.RawMessage(`{"action":"get","id":"goal-coverage"}`))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(getOut, `"goal-coverage"`) {
		t.Fatalf("get output = %s", getOut)
	}

	updateOut, err := tool.Handler(ctx, json.RawMessage(`{
		"action":"update",
		"id":"goal-coverage",
		"status":"running",
		"progress_total":4,
		"progress_completed":2,
		"metadata":{"phase":"test"}
	}`))
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !strings.Contains(updateOut, `"action":"updated"`) {
		t.Fatalf("update output = %s", updateOut)
	}

	listOut, err := tool.Handler(ctx, json.RawMessage(`{"action":"list","filter_status":"running","limit":10}`))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(listOut, `"count":1`) {
		t.Fatalf("list output = %s", listOut)
	}

	if _, err := mgr.Create(ctx, goals.Goal{
		ID:     "goal-ready",
		Name:   "Ready goal",
		Status: goals.StatusPending,
	}); err != nil {
		t.Fatalf("seed ready goal: %v", err)
	}
	readyOut, err := tool.Handler(ctx, json.RawMessage(`{"action":"ready"}`))
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	if !strings.Contains(readyOut, `"action":"ready"`) {
		t.Fatalf("ready output = %s", readyOut)
	}
}

func TestGoalToolsNilManager(t *testing.T) {
	t.Parallel()

	if factory := NewGoalTools(nil); factory != nil {
		t.Fatal("expected nil factory for nil goal manager")
	}
}
