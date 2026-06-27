package deferred

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go-agent-harness/internal/goals"
	tools "go-agent-harness/internal/harness/tools"
)

func TestGoalToolsNilManager(t *testing.T) {
	t.Parallel()

	if NewGoalTools(nil) != nil {
		t.Fatal("NewGoalTools(nil) should return nil")
	}
}

func TestGoalToolsCRUDListAndReady(t *testing.T) {
	t.Parallel()

	manager := goals.NewManager(nil)
	factory := NewGoalTools(manager)
	if factory == nil {
		t.Fatal("NewGoalTools returned nil factory")
	}
	tool := factory()
	if tool.Definition.Name != "goals" {
		t.Fatalf("tool name = %q, want goals", tool.Definition.Name)
	}
	if tool.Definition.Tier != tools.TierDeferred {
		t.Fatalf("tool tier = %q, want %q", tool.Definition.Tier, tools.TierDeferred)
	}
	if tool.Handler == nil {
		t.Fatal("tool handler is nil")
	}

	created := callGoalTool(t, tool, map[string]any{
		"action":             "create",
		"id":                 "goal-parent",
		"name":               "Parent",
		"description":        "Parent goal",
		"verify_criteria":    "tests pass",
		"progress_total":     4,
		"progress_completed": 1,
		"metadata":           map[string]string{"ticket": "649"},
	})
	if created["action"] != "created" {
		t.Fatalf("create action = %v", created["action"])
	}
	if created["goal_id"] != "goal-parent" {
		t.Fatalf("goal_id = %v", created["goal_id"])
	}

	callGoalTool(t, tool, map[string]any{
		"action":     "create",
		"id":         "goal-child",
		"name":       "Child",
		"depends_on": []string{"goal-parent"},
	})

	got := callGoalTool(t, tool, map[string]any{"action": "get", "id": "goal-parent"})
	goal := got["goal"].(map[string]any)
	if goal["name"] != "Parent" {
		t.Fatalf("get goal name = %v", goal["name"])
	}

	updated := callGoalTool(t, tool, map[string]any{
		"action":             "update",
		"id":                 "goal-parent",
		"status":             "completed",
		"progress_total":     4,
		"progress_completed": 4,
		"metadata":           map[string]string{"gate": "green"},
		"result":             "done",
	})
	updatedGoal := updated["goal"].(map[string]any)
	if updatedGoal["status"] != "completed" {
		t.Fatalf("updated status = %v", updatedGoal["status"])
	}
	metadata := updatedGoal["metadata"].(map[string]any)
	if metadata["ticket"] != "649" || metadata["gate"] != "green" {
		t.Fatalf("merged metadata = %#v", metadata)
	}

	listed := callGoalTool(t, tool, map[string]any{
		"action":        "list",
		"filter_status": "pending",
		"limit":         5,
	})
	if listed["action"] != "list" || listed["count"].(float64) != 1 {
		t.Fatalf("list response = %#v", listed)
	}

	ready := callGoalTool(t, tool, map[string]any{"action": "ready"})
	if ready["action"] != "ready" || ready["count"].(float64) != 1 {
		t.Fatalf("ready response = %#v", ready)
	}
	readyGoals := ready["goals"].([]any)
	readyGoal := readyGoals[0].(map[string]any)
	if readyGoal["id"] != "goal-child" {
		t.Fatalf("ready goal id = %v, want goal-child", readyGoal["id"])
	}
}

func TestGoalToolsValidationErrors(t *testing.T) {
	t.Parallel()

	tool := NewGoalTools(goals.NewManager(nil))()
	cases := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{name: "invalid json", raw: `{`, wantErr: "invalid goals request"},
		{name: "unknown action", raw: `{"action":"delete"}`, wantErr: `unknown action "delete"`},
		{name: "create missing name", raw: `{"action":"create"}`, wantErr: "name is required"},
		{name: "get missing id", raw: `{"action":"get"}`, wantErr: "id is required"},
		{name: "update missing id", raw: `{"action":"update"}`, wantErr: "id is required"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := tool.Handler(context.Background(), json.RawMessage(tc.raw))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func callGoalTool(t *testing.T, tool tools.Tool, payload map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	out, err := tool.Handler(context.Background(), raw)
	if err != nil {
		t.Fatalf("goal tool %v: %v", payload["action"], err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("unmarshal response %q: %v", out, err)
	}
	return decoded
}
