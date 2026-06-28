package deferred

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"go-agent-harness/internal/workflow"
)

type fakeWorkflowService struct {
	created workflow.CreateWorkflowRequest
	run     *workflow.Run
	events  []workflow.Event
}

func (f *fakeWorkflowService) List() []workflow.Meta { return nil }

func (f *fakeWorkflowService) Start(_ context.Context, name string, _ any) (*workflow.Run, error) {
	f.run = &workflow.Run{ID: "wf_1", WorkflowName: name, Status: workflow.RunStatusRunning}
	return f.run, nil
}

func (f *fakeWorkflowService) Resume(context.Context, string, any) (*workflow.Run, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *fakeWorkflowService) GetRun(string) (*workflow.Run, error) { return f.run, nil }

func (f *fakeWorkflowService) Subscribe(string) ([]workflow.Event, <-chan workflow.Event, func(), error) {
	return nil, nil, func() {}, nil
}

func (f *fakeWorkflowService) Wait(context.Context, string) (*workflow.Run, []workflow.Event, error) {
	f.run.Status = workflow.RunStatusCompleted
	f.run.ResultJSON = `{"ok":true}`
	return f.run, f.events, nil
}

func (f *fakeWorkflowService) CreateWorkflow(_ context.Context, req workflow.CreateWorkflowRequest) (*workflow.SourceBundle, error) {
	f.created = req
	return &workflow.SourceBundle{
		Manifest: workflow.SourceBundleManifest{Name: req.Name, Description: req.Description},
		Dir:      "/tmp/workflows/" + req.Name,
		Hash:     "abc123",
		Scope:    req.Scope,
	}, nil
}

func TestCreateWorkflowToolCreatesBundle(t *testing.T) {
	fake := &fakeWorkflowService{}
	tool := CreateWorkflowTool(fake)
	out, err := tool.Handler(context.Background(), json.RawMessage(`{
		"name": "daily-review",
		"description": "Review the day.",
		"source": "package main\nfunc main(){}",
		"scope": "workspace"
	}`))
	require.NoError(t, err)
	require.Contains(t, out, `"status":"created"`)
	require.Equal(t, "daily-review", fake.created.Name)
	require.Equal(t, "workspace", fake.created.Scope)
}

func TestRunWorkflowToolReturnsFeedbackWhenWaiting(t *testing.T) {
	fake := &fakeWorkflowService{
		events: []workflow.Event{{
			Seq:       1,
			RunID:     "wf_1",
			Type:      workflow.EventWorkflowFinding,
			Payload:   map[string]any{"kind": "finding", "message": "found issue"},
			Timestamp: time.Now().UTC(),
		}},
	}
	tool := RunWorkflowTool(fake)
	out, err := tool.Handler(context.Background(), json.RawMessage(`{
		"name": "daily-review",
		"args": {"target": "repo"},
		"wait": true
	}`))
	require.NoError(t, err)
	require.Contains(t, out, `"run_id":"wf_1"`)
	require.Contains(t, out, `"status":"completed"`)
	require.Contains(t, out, `"workflow.finding"`)
	require.Contains(t, out, `"found issue"`)
}
