package deferred

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	tools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/goals"
)

// GoalCreator is the interface the tool needs from the goals manager.
type GoalCreator interface {
	Create(ctx context.Context, goal goals.Goal) (*goals.Goal, error)
	Get(ctx context.Context, id string) (*goals.Goal, error)
	Update(ctx context.Context, goal goals.Goal) (*goals.Goal, error)
	List(ctx context.Context, filter goals.GoalFilter) ([]goals.Goal, error)
	Ready(ctx context.Context) ([]goals.Goal, error)
}

// NewGoalTools returns a tool factory that creates goal management tools.
func NewGoalTools(mgr GoalCreator) func() tools.Tool {
	if mgr == nil {
		return nil
	}
	return func() tools.Tool {
		return tools.Tool{
			Definition: tools.Definition{
				Name:        "goals",
				Description: "Manage persistent, multi-session goals with dependency chains and progress tracking. Actions: create (with optional depends_on), get, update (status/progress/metadata), list (with status filter), ready (goals whose dependencies are all completed). Goals survive restarts and support verification criteria for definition-of-done enforcement.",
				Parameters: map[string]any{
					"type":     "object",
					"required": []any{"action"},
					"properties": map[string]any{
						"action": map[string]any{
							"type":        "string",
							"description": "Action: create, get, update, list, ready",
							"enum":        []any{"create", "get", "update", "list", "ready"},
						},
						"id": map[string]any{
							"type":        "string",
							"description": "Goal ID (required for get, update)",
						},
						"name": map[string]any{
							"type":        "string",
							"description": "Goal name (required for create)",
						},
						"description": map[string]any{
							"type":        "string",
							"description": "Goal description",
						},
						"verify_criteria": map[string]any{
							"type":        "string",
							"description": "How to verify completion (e.g., 'all tests pass')",
						},
						"status": map[string]any{
							"type":        "string",
							"enum":        []any{"pending", "running", "verifying", "completed", "failed", "cancelled"},
						},
						"depends_on": map[string]any{
							"type":  "array",
							"items": map[string]any{"type": "string"},
						},
						"progress_total":     map[string]any{"type": "integer"},
						"progress_completed": map[string]any{"type": "integer"},
						"metadata":           map[string]any{"type": "object"},
						"error":              map[string]any{"type": "string"},
						"result":             map[string]any{"type": "string"},
						"filter_status": map[string]any{
							"type": "string",
							"enum": []any{"pending", "running", "verifying", "completed", "failed", "cancelled"},
						},
						"limit": map[string]any{"type": "integer"},
					},
				},
				Tier: tools.TierDeferred,
				Tags: []string{"goals", "tasks", "missions", "dependencies", "progress"},
			},
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				return handleGoalAction(ctx, mgr, args)
			},
		}
	}
}

type goalRequest struct {
	Action            string            `json:"action"`
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Description       string            `json:"description"`
	VerifyCriteria    string            `json:"verify_criteria"`
	Status            string            `json:"status"`
	DependsOn         []string          `json:"depends_on"`
	ProgressTotal     int               `json:"progress_total"`
	ProgressCompleted int               `json:"progress_completed"`
	Metadata          map[string]string `json:"metadata"`
	Error             string            `json:"error"`
	Result            string            `json:"result"`
	FilterStatus      string            `json:"filter_status"`
	Limit             int               `json:"limit"`
}

func handleGoalAction(ctx context.Context, mgr GoalCreator, raw json.RawMessage) (string, error) {
	var req goalRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return "", fmt.Errorf("invalid goals request: %w", err)
	}

	switch req.Action {
	case "create":
		return goalCreate(ctx, mgr, req)
	case "get":
		return goalGet(ctx, mgr, req)
	case "update":
		return goalUpdate(ctx, mgr, req)
	case "list":
		return goalList(ctx, mgr, req)
	case "ready":
		return goalReady(ctx, mgr)
	default:
		return "", fmt.Errorf("unknown action %q", req.Action)
	}
}

func goalCreate(ctx context.Context, mgr GoalCreator, req goalRequest) (string, error) {
	if req.Name == "" {
		return "", fmt.Errorf("name is required for create action")
	}
	id := req.ID
	if id == "" {
		id = fmt.Sprintf("goal-%d", time.Now().UnixNano())
	}

	goal := goals.Goal{
		ID:             id,
		Name:           req.Name,
		Description:    req.Description,
		VerifyCriteria: req.VerifyCriteria,
		DependsOn:      req.DependsOn,
		Progress:       goals.Progress{Total: req.ProgressTotal, Completed: req.ProgressCompleted},
		Metadata:       req.Metadata,
	}
	if req.Status != "" {
		goal.Status = goals.Status(req.Status)
	}

	created, err := mgr.Create(ctx, goal)
	if err != nil {
		return "", fmt.Errorf("create goal: %w", err)
	}
	return jsonString(map[string]any{"action": "created", "goal_id": created.ID, "status": string(created.Status), "goal": created})
}

func goalGet(ctx context.Context, mgr GoalCreator, req goalRequest) (string, error) {
	if req.ID == "" {
		return "", fmt.Errorf("id is required for get action")
	}
	g, err := mgr.Get(ctx, req.ID)
	if err != nil {
		return "", fmt.Errorf("get goal: %w", err)
	}
	return jsonString(map[string]any{"action": "get", "goal": g})
}

func goalUpdate(ctx context.Context, mgr GoalCreator, req goalRequest) (string, error) {
	if req.ID == "" {
		return "", fmt.Errorf("id is required for update action")
	}
	existing, err := mgr.Get(ctx, req.ID)
	if err != nil {
		return "", fmt.Errorf("get goal for update: %w", err)
	}
	if req.Name != "" {
		existing.Name = req.Name
	}
	if req.Description != "" {
		existing.Description = req.Description
	}
	if req.VerifyCriteria != "" {
		existing.VerifyCriteria = req.VerifyCriteria
	}
	if req.Status != "" {
		existing.Status = goals.Status(req.Status)
	}
	if req.Error != "" {
		existing.Error = req.Error
	}
	if req.Result != "" {
		existing.Result = req.Result
	}
	if req.ProgressTotal > 0 || req.ProgressCompleted > 0 {
		existing.Progress = goals.Progress{Total: req.ProgressTotal, Completed: req.ProgressCompleted}
	}
	if req.Metadata != nil {
		if existing.Metadata == nil {
			existing.Metadata = make(map[string]string)
		}
		for k, v := range req.Metadata {
			existing.Metadata[k] = v
		}
	}
	if len(req.DependsOn) > 0 {
		existing.DependsOn = req.DependsOn
	}

	updated, err := mgr.Update(ctx, *existing)
	if err != nil {
		return "", fmt.Errorf("update goal: %w", err)
	}
	return jsonString(map[string]any{"action": "updated", "goal": updated})
}

func goalList(ctx context.Context, mgr GoalCreator, req goalRequest) (string, error) {
	filter := goals.GoalFilter{Limit: req.Limit}
	if req.FilterStatus != "" {
		filter.Status = goals.Status(req.FilterStatus)
	}
	results, err := mgr.List(ctx, filter)
	if err != nil {
		return "", fmt.Errorf("list goals: %w", err)
	}
	return jsonString(map[string]any{"action": "list", "goals": results, "count": len(results)})
}

func goalReady(ctx context.Context, mgr GoalCreator) (string, error) {
	ready, err := mgr.Ready(ctx)
	if err != nil {
		return "", fmt.Errorf("get ready goals: %w", err)
	}
	return jsonString(map[string]any{"action": "ready", "goals": ready, "count": len(ready)})
}

func jsonString(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
