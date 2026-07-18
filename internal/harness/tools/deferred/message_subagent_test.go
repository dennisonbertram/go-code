package deferred

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-agent-harness/internal/harness/tools"
)

// mockRunSteerer is a fake tools.RunSteerer for testing message_subagent and
// notify_parent without a real *harness.Runner.
type mockRunSteerer struct {
	parentByRunID map[string]string // runID -> parentRunID, absent means no parent recorded

	steerErr error
	steered  []steerCall
}

type steerCall struct {
	runID   string
	message string
}

func (m *mockRunSteerer) SteerRun(runID, message string) error {
	m.steered = append(m.steered, steerCall{runID: runID, message: message})
	return m.steerErr
}

func (m *mockRunSteerer) ParentRunID(runID string) (string, bool) {
	id, ok := m.parentByRunID[runID]
	return id, ok
}

func TestMessageSubagentTool_SendsMessageToTheSubagentsRun(t *testing.T) {
	manager := &mockSubagentManager{
		result: tools.SubagentResult{ID: "subagent-1", RunID: "run-child-1", Status: "running"},
	}
	steerer := &mockRunSteerer{}
	tool := MessageSubagentTool(manager, steerer)

	raw, _ := json.Marshal(map[string]any{"id": "subagent-1", "message": "please prioritize the auth bug"})
	got, err := tool.Handler(context.Background(), raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &payload))
	assert.Equal(t, "subagent-1", payload["id"])
	assert.Equal(t, "run-child-1", payload["run_id"])
	assert.Equal(t, "sent", payload["status"])

	require.Len(t, steerer.steered, 1)
	assert.Equal(t, "run-child-1", steerer.steered[0].runID)
	assert.Equal(t, "please prioritize the auth bug", steerer.steered[0].message)
	assert.True(t, manager.getCalled)
}

func TestMessageSubagentTool_RequiresID(t *testing.T) {
	tool := MessageSubagentTool(&mockSubagentManager{}, &mockRunSteerer{})
	raw, _ := json.Marshal(map[string]any{"message": "hi"})
	_, err := tool.Handler(context.Background(), raw)
	require.Error(t, err)
}

func TestMessageSubagentTool_RequiresMessage(t *testing.T) {
	tool := MessageSubagentTool(&mockSubagentManager{}, &mockRunSteerer{})
	raw, _ := json.Marshal(map[string]any{"id": "subagent-1"})
	_, err := tool.Handler(context.Background(), raw)
	require.Error(t, err)
}

func TestMessageSubagentTool_PropagatesLookupError(t *testing.T) {
	manager := &mockSubagentManager{err: assert.AnError}
	tool := MessageSubagentTool(manager, &mockRunSteerer{})
	raw, _ := json.Marshal(map[string]any{"id": "subagent-missing", "message": "hi"})
	_, err := tool.Handler(context.Background(), raw)
	require.Error(t, err)
}

func TestMessageSubagentTool_PropagatesSteerError(t *testing.T) {
	manager := &mockSubagentManager{result: tools.SubagentResult{ID: "subagent-1", RunID: "run-child-1"}}
	steerer := &mockRunSteerer{steerErr: assert.AnError}
	tool := MessageSubagentTool(manager, steerer)
	raw, _ := json.Marshal(map[string]any{"id": "subagent-1", "message": "hi"})
	_, err := tool.Handler(context.Background(), raw)
	require.Error(t, err)
}

func TestNotifyParentTool_SendsMessageToTheRecordedParent(t *testing.T) {
	steerer := &mockRunSteerer{parentByRunID: map[string]string{"run-child-1": "run-parent-1"}}
	tool := NotifyParentTool(steerer)

	ctx := context.WithValue(context.Background(), tools.ContextKeyRunMetadata, tools.RunMetadata{RunID: "run-child-1"})
	raw, _ := json.Marshal(map[string]any{"message": "auth subtask done, tests green"})
	got, err := tool.Handler(ctx, raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &payload))
	assert.Equal(t, "run-parent-1", payload["parent_run_id"])
	assert.Equal(t, "sent", payload["status"])

	require.Len(t, steerer.steered, 1)
	assert.Equal(t, "run-parent-1", steerer.steered[0].runID)
	assert.Equal(t, "auth subtask done, tests green", steerer.steered[0].message)
}

func TestNotifyParentTool_ErrorsWhenNoParentIsRecorded(t *testing.T) {
	steerer := &mockRunSteerer{} // no parent recorded for any run
	tool := NotifyParentTool(steerer)

	ctx := context.WithValue(context.Background(), tools.ContextKeyRunMetadata, tools.RunMetadata{RunID: "run-top-level"})
	raw, _ := json.Marshal(map[string]any{"message": "hi"})
	_, err := tool.Handler(ctx, raw)
	require.Error(t, err)
	assert.Empty(t, steerer.steered)
}

func TestNotifyParentTool_ErrorsWhenNoRunContext(t *testing.T) {
	tool := NotifyParentTool(&mockRunSteerer{})
	raw, _ := json.Marshal(map[string]any{"message": "hi"})
	_, err := tool.Handler(context.Background(), raw)
	require.Error(t, err)
}

func TestNotifyParentTool_RequiresMessage(t *testing.T) {
	steerer := &mockRunSteerer{parentByRunID: map[string]string{"run-child-1": "run-parent-1"}}
	tool := NotifyParentTool(steerer)
	ctx := context.WithValue(context.Background(), tools.ContextKeyRunMetadata, tools.RunMetadata{RunID: "run-child-1"})
	raw, _ := json.Marshal(map[string]any{})
	_, err := tool.Handler(ctx, raw)
	require.Error(t, err)
}
