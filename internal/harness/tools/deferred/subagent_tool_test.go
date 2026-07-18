package deferred

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-agent-harness/internal/harness/tools"
)

// mockActivationTracker is a fake tools.ActivationTrackerInterface for
// testing that StartSubagentTool co-activates the rest of the
// subagent-lifecycle tool family once it actually spawns a subagent.
type mockActivationTracker struct {
	activated map[string][]string // runID -> tool names activated for it
}

func (m *mockActivationTracker) Activate(runID string, toolNames ...string) {
	if m.activated == nil {
		m.activated = make(map[string][]string)
	}
	m.activated[runID] = append(m.activated[runID], toolNames...)
}

func (m *mockActivationTracker) IsActive(runID string, toolName string) bool {
	for _, name := range m.activated[runID] {
		if name == toolName {
			return true
		}
	}
	return false
}

func TestStartSubagentTool_CreatesAndReturnsSubagentID(t *testing.T) {
	manager := &mockSubagentManager{
		result: tools.SubagentResult{
			ID:     "subagent-1",
			RunID:  "run-1",
			Status: "running",
		},
	}
	tool := StartSubagentTool(manager, "", nil)

	raw, _ := json.Marshal(map[string]any{
		"task":      "Handle refactor",
		"profile":   "full",
		"model":     "gpt-4.1-mini",
		"max_steps": 12,
	})
	got, err := tool.Handler(context.Background(), raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &payload))
	assert.Equal(t, "subagent-1", payload["subagent_id"])
	assert.Equal(t, "running", payload["status"])
	assert.Equal(t, "run-1", payload["run_id"])
	assert.Equal(t, 12, manager.lastReq.MaxSteps)
	assert.True(t, manager.startCalled)
}

func TestStartSubagentTool_SchemaAdvertisesAllowedTools(t *testing.T) {
	tool := StartSubagentTool(&mockSubagentManager{}, "", nil)
	props, ok := tool.Definition.Parameters["properties"].(map[string]any)
	require.True(t, ok, "expected schema properties map")
	_, ok = props["allowed_tools"]
	assert.True(t, ok, "expected start_subagent schema to advertise allowed_tools so parents can restrict a subagent's tools")
}

func TestStartSubagentTool_ForwardsAllowedToolsToManager(t *testing.T) {
	manager := &mockSubagentManager{
		result: tools.SubagentResult{ID: "subagent-1", RunID: "run-1", Status: "running"},
	}
	tool := StartSubagentTool(manager, "", nil)

	raw, _ := json.Marshal(map[string]any{
		"task":          "notify the parent only",
		"allowed_tools": []string{"notify_parent"},
	})
	_, err := tool.Handler(context.Background(), raw)
	require.NoError(t, err)
	assert.Equal(t, []string{"notify_parent"}, manager.lastReq.AllowedTools)
}

// TestStartSubagentTool_CoActivatesTheRestOfTheLifecycleFamily covers a real
// failure observed in live testing: find_tool activated start_subagent (plus
// unrelated tools that happened to share tags) but never wait_subagent or
// get_subagent, so a parent that successfully spawned a subagent had no way
// to check on it — it just kept calling start_subagent again, spawning
// duplicate subagents until it ran out of steps, occasionally hallucinating
// a fake `bash("wait ...")` command as a last resort. The moment
// start_subagent actually succeeds, the rest of the family it depends on
// (get_subagent, wait_subagent, cancel_subagent, message_subagent) must be
// activated too, regardless of what find_tool's keyword ranking surfaced.
func TestStartSubagentTool_CoActivatesTheRestOfTheLifecycleFamily(t *testing.T) {
	manager := &mockSubagentManager{
		result: tools.SubagentResult{ID: "subagent-1", RunID: "run-1", Status: "queued"},
	}
	tracker := &mockActivationTracker{}
	tool := StartSubagentTool(manager, "", tracker)

	ctx := context.WithValue(context.Background(), tools.ContextKeyRunMetadata, tools.RunMetadata{RunID: "run-parent-1"})
	raw, _ := json.Marshal(map[string]any{"task": "do something"})
	_, err := tool.Handler(ctx, raw)
	require.NoError(t, err)

	for _, name := range []string{"get_subagent", "wait_subagent", "cancel_subagent", "message_subagent"} {
		assert.True(t, tracker.IsActive("run-parent-1", name), "expected %q to be co-activated for the parent run", name)
	}
}

// TestStartSubagentTool_NilTrackerIsSafe verifies a nil tracker (e.g. a
// registry built without one configured) doesn't panic — co-activation is a
// best-effort improvement, not a hard dependency of start_subagent working.
func TestStartSubagentTool_NilTrackerIsSafe(t *testing.T) {
	manager := &mockSubagentManager{
		result: tools.SubagentResult{ID: "subagent-1", RunID: "run-1", Status: "queued"},
	}
	tool := StartSubagentTool(manager, "", nil)

	raw, _ := json.Marshal(map[string]any{"task": "do something"})
	_, err := tool.Handler(context.Background(), raw)
	require.NoError(t, err)
}

func TestGetSubagentTool_ReturnsSubagentStatus(t *testing.T) {
	manager := &mockSubagentManager{
		result: tools.SubagentResult{
			ID:     "subagent-1",
			Status: "running",
			Output: "working",
		},
	}
	tool := GetSubagentTool(manager)

	raw, _ := json.Marshal(map[string]any{"id": "subagent-1"})
	got, err := tool.Handler(context.Background(), raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &payload))
	assert.Equal(t, "subagent-1", payload["id"])
	assert.Equal(t, "running", payload["status"])
	assert.Equal(t, "working", payload["output"])
	assert.True(t, manager.getCalled)
}

func TestWaitSubagentTool_ReturnsTerminalSubagent(t *testing.T) {
	manager := &mockSubagentManager{
		result: tools.SubagentResult{
			ID:     "subagent-1",
			Status: "completed",
			Output: "done",
		},
	}
	tool := WaitSubagentTool(manager)

	raw, _ := json.Marshal(map[string]any{"id": "subagent-1"})
	got, err := tool.Handler(context.Background(), raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &payload))
	assert.Equal(t, "completed", payload["status"])
	assert.Equal(t, "done", payload["output"])
	assert.True(t, manager.waitCalled)
}

func TestCancelSubagentTool_CallsManagerCancel(t *testing.T) {
	manager := &mockSubagentManager{
		result: tools.SubagentResult{},
		err:    nil,
	}

	tool := CancelSubagentTool(manager)
	raw, _ := json.Marshal(map[string]any{"id": "subagent-1"})
	got, err := tool.Handler(context.Background(), raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &payload))
	assert.Equal(t, "cancelling", payload["status"])
	assert.True(t, manager.cancelCalled)
}
