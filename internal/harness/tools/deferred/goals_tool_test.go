package deferred

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go-agent-harness/internal/goals"
)

func TestGoalToolsManageLifecycle(t *testing.T) {
	t.Parallel()

	if NewGoalTools(nil) != nil {
		t.Fatal("expected nil factory for nil manager")
	}

	manager := goals.NewManager(nil)
	tool := NewGoalTools(manager)()
	if tool.Definition.Name != "goals" {
		t.Fatalf("tool name = %q, want goals", tool.Definition.Name)
	}

	ctx := context.Background()
	result, err := tool.Handler(ctx, json.RawMessage(`{
		"action":"create",
		"id":"goal-1",
		"name":"Ship eval harness",
		"description":"adapter-first smoke",
		"verify_criteria":"regression and smoke pass",
		"progress_total":4,
		"progress_completed":1,
		"metadata":{"tier":"smoke"}
	}`))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var created map[string]any
	if err := json.Unmarshal([]byte(result), &created); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	if created["goal_id"] != "goal-1" {
		t.Fatalf("goal_id = %v, want goal-1", created["goal_id"])
	}

	if _, err := tool.Handler(ctx, json.RawMessage(`{"action":"get","id":"goal-1"}`)); err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := tool.Handler(ctx, json.RawMessage(`{
		"action":"update",
		"id":"goal-1",
		"status":"running",
		"progress_total":4,
		"progress_completed":2,
		"metadata":{"gate":"coverage"}
	}`)); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := tool.Handler(ctx, json.RawMessage(`{"action":"list","filter_status":"running","limit":10}`)); err != nil {
		t.Fatalf("list: %v", err)
	}

	if _, err := tool.Handler(ctx, json.RawMessage(`{"action":"create","id":"goal-ready","name":"Ready goal"}`)); err != nil {
		t.Fatalf("create ready goal: %v", err)
	}
	readyRaw, err := tool.Handler(ctx, json.RawMessage(`{"action":"ready"}`))
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	var ready map[string]any
	if err := json.Unmarshal([]byte(readyRaw), &ready); err != nil {
		t.Fatalf("unmarshal ready: %v", err)
	}
	if ready["count"].(float64) == 0 {
		t.Fatal("expected at least one ready goal")
	}
}

func TestGoalToolsRejectInvalidRequests(t *testing.T) {
	t.Parallel()

	tool := NewGoalTools(goals.NewManager(nil))()
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{name: "bad json", raw: json.RawMessage(`{`), want: "invalid goals request"},
		{name: "unknown action", raw: json.RawMessage(`{"action":"bogus"}`), want: "unknown action"},
		{name: "create missing name", raw: json.RawMessage(`{"action":"create"}`), want: "name is required"},
		{name: "get missing id", raw: json.RawMessage(`{"action":"get"}`), want: "id is required"},
		{name: "update missing id", raw: json.RawMessage(`{"action":"update"}`), want: "id is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tool.Handler(ctx, tc.raw)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want containing %q", err.Error(), tc.want)
			}
		})
	}
}
