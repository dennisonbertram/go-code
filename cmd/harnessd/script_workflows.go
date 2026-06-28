package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	htools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/subagents"
	scriptworkflow "go-agent-harness/internal/workflow"
)

type scriptWorkflowServiceRef struct {
	mu  sync.RWMutex
	svc scriptworkflow.SourceService
}

func (r *scriptWorkflowServiceRef) Set(svc scriptworkflow.SourceService) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.svc = svc
}

func (r *scriptWorkflowServiceRef) service() (scriptworkflow.SourceService, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.svc == nil {
		return nil, fmt.Errorf("script workflow service is not configured")
	}
	return r.svc, nil
}

func (r *scriptWorkflowServiceRef) Reload(ctx context.Context) error {
	r.mu.RLock()
	svc := r.svc
	r.mu.RUnlock()
	if svc == nil {
		return nil
	}
	reloader, ok := svc.(interface {
		Load(context.Context) error
	})
	if !ok {
		return nil
	}
	return reloader.Load(ctx)
}

func (r *scriptWorkflowServiceRef) List() []scriptworkflow.Meta {
	svc, err := r.service()
	if err != nil {
		return nil
	}
	return svc.List()
}

func (r *scriptWorkflowServiceRef) Start(ctx context.Context, name string, args any) (*scriptworkflow.Run, error) {
	svc, err := r.service()
	if err != nil {
		return nil, err
	}
	return svc.Start(ctx, name, args)
}

func (r *scriptWorkflowServiceRef) Resume(ctx context.Context, runID string, args any) (*scriptworkflow.Run, error) {
	svc, err := r.service()
	if err != nil {
		return nil, err
	}
	return svc.Resume(ctx, runID, args)
}

func (r *scriptWorkflowServiceRef) GetRun(runID string) (*scriptworkflow.Run, error) {
	svc, err := r.service()
	if err != nil {
		return nil, err
	}
	return svc.GetRun(runID)
}

func (r *scriptWorkflowServiceRef) Subscribe(runID string) ([]scriptworkflow.Event, <-chan scriptworkflow.Event, func(), error) {
	svc, err := r.service()
	if err != nil {
		return nil, nil, nil, err
	}
	return svc.Subscribe(runID)
}

func (r *scriptWorkflowServiceRef) Wait(ctx context.Context, runID string) (*scriptworkflow.Run, []scriptworkflow.Event, error) {
	svc, err := r.service()
	if err != nil {
		return nil, nil, err
	}
	return svc.Wait(ctx, runID)
}

func (r *scriptWorkflowServiceRef) CreateWorkflow(ctx context.Context, req scriptworkflow.CreateWorkflowRequest) (*scriptworkflow.SourceBundle, error) {
	svc, err := r.service()
	if err != nil {
		return nil, err
	}
	return svc.CreateWorkflow(ctx, req)
}

type scriptSubagentAdapter struct {
	manager subagents.Manager
}

func (a scriptSubagentAdapter) Create(ctx context.Context, req scriptworkflow.SubagentRequest) (scriptworkflow.SubagentResult, error) {
	if a.manager == nil {
		return scriptworkflow.SubagentResult{}, fmt.Errorf("subagent manager is not configured")
	}
	item, err := a.manager.Create(ctx, subagents.Request{
		Prompt:        req.Prompt,
		Model:         req.Model,
		ProviderName:  req.Provider,
		AllowedTools:  append([]string(nil), req.AllowedTools...),
		ProfileName:   req.Profile,
		Isolation:     subagents.IsolationMode(req.Isolation),
		CleanupPolicy: subagents.CleanupPolicy(req.CleanupPolicy),
		MaxSteps:      req.MaxSteps,
		MaxCostUSD:    req.MaxCostUSD,
	})
	if err != nil {
		return scriptworkflow.SubagentResult{}, err
	}
	return subagentResultFrom(item), nil
}

func (a scriptSubagentAdapter) Get(ctx context.Context, id string) (scriptworkflow.SubagentResult, error) {
	if a.manager == nil {
		return scriptworkflow.SubagentResult{}, fmt.Errorf("subagent manager is not configured")
	}
	item, err := a.manager.Get(ctx, id)
	if err != nil {
		return scriptworkflow.SubagentResult{}, err
	}
	return subagentResultFrom(item), nil
}

func subagentResultFrom(item subagents.Subagent) scriptworkflow.SubagentResult {
	return scriptworkflow.SubagentResult{
		ID:     item.ID,
		Status: string(item.Status),
		Output: item.Output,
		Error:  item.Error,
	}
}

type workflowQuestionResponder struct {
	broker  htools.AskUserQuestionBroker
	timeout time.Duration
}

func (r workflowQuestionResponder) AskWorkflowQuestion(ctx context.Context, req scriptworkflow.QuestionRequest) (any, error) {
	if r.broker == nil {
		return nil, fmt.Errorf("workflow question broker is not configured")
	}
	options := make([]htools.AskUserQuestionOption, 0, len(req.Choices))
	for _, choice := range req.Choices {
		if len(options) >= 4 {
			break
		}
		label := choice.Label
		if label == "" {
			continue
		}
		desc := choice.Description
		if desc == "" {
			desc = choice.Label
		}
		options = append(options, htools.AskUserQuestionOption{Label: label, Description: desc})
	}
	if len(options) == 0 {
		options = append(options,
			htools.AskUserQuestionOption{Label: "Continue", Description: "Continue the workflow."},
			htools.AskUserQuestionOption{Label: "Stop", Description: "Stop and report the blocker."},
		)
	}
	if len(options) == 1 {
		options = append(options, htools.AskUserQuestionOption{Label: "Other", Description: "Use another answer."})
	}
	answers, _, err := r.broker.Ask(ctx, htools.AskUserQuestionRequest{
		RunID:  req.RunID,
		CallID: req.CallID,
		Questions: []htools.AskUserQuestion{{
			Question:    req.Prompt,
			Header:      "Workflow",
			Options:     options,
			MultiSelect: false,
		}},
		Timeout: r.timeout,
	})
	if err != nil {
		return nil, err
	}
	return answers[req.Prompt], nil
}
