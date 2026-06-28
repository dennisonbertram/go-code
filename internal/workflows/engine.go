package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"go-agent-harness/internal/checkpoints"
	"go-agent-harness/internal/harness"
)

type ToolExecutor interface {
	Execute(ctx context.Context, name string, args json.RawMessage) (string, error)
}

type RunEngine interface {
	StartRun(req harness.RunRequest) (harness.Run, error)
	GetRun(runID string) (harness.Run, bool)
	Subscribe(runID string) ([]harness.Event, <-chan harness.Event, func(), error)
}

type Options struct {
	Definitions []Definition
	Runner      RunEngine
	Tools       ToolExecutor
	Checkpoints *checkpoints.Service
	Store       Store
	Now         func() time.Time
}

type Engine struct {
	defs        map[string]Definition
	runner      RunEngine
	tools       ToolExecutor
	checkpoints *checkpoints.Service
	store       Store
	now         func() time.Time

	mu        sync.Mutex
	subs      map[string]map[chan Event]struct{}
	eventSeqs map[string]int64
}

func NewEngine(opts Options) *Engine {
	defs := make(map[string]Definition, len(opts.Definitions))
	for _, def := range opts.Definitions {
		defs[def.Name] = def
	}
	if opts.Store == nil {
		opts.Store = NewMemoryStore()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Engine{
		defs:        defs,
		runner:      opts.Runner,
		tools:       opts.Tools,
		checkpoints: opts.Checkpoints,
		store:       opts.Store,
		now:         opts.Now,
		subs:        make(map[string]map[chan Event]struct{}),
		eventSeqs:   make(map[string]int64),
	}
}

func (e *Engine) RegisterDefinition(def Definition) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.defs[def.Name] = def
}

func (e *Engine) ListDefinitions() []Definition {
	out := make([]Definition, 0, len(e.defs))
	for _, def := range e.defs {
		out = append(out, def)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (e *Engine) GetDefinition(name string) (Definition, bool) {
	def, ok := e.defs[name]
	return def, ok
}

func (e *Engine) Start(name string, input map[string]any) (Run, error) {
	def, ok := e.defs[name]
	if !ok {
		return Run{}, fmt.Errorf("workflow %q not found", name)
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return Run{}, err
	}
	run := Run{
		ID:            "workflow_" + uuid.NewString(),
		WorkflowName:  def.Name,
		Status:        RunStatusRunning,
		CurrentStepID: firstStepID(def),
		InputJSON:     string(inputJSON),
		CreatedAt:     e.now().UTC(),
		UpdatedAt:     e.now().UTC(),
	}
	if err := e.store.CreateRun(context.Background(), &run); err != nil {
		return Run{}, err
	}
	e.emit(run.ID, "workflow.started", map[string]any{"workflow": def.Name})
	go e.execute(run.ID)
	return run, nil
}

func (e *Engine) StartDefinition(def Definition, input map[string]any) (Run, error) {
	e.RegisterDefinition(def)
	return e.Start(def.Name, input)
}

func (e *Engine) GetRun(runID string) (Run, []StepState, error) {
	run, err := e.store.GetRun(context.Background(), runID)
	if err != nil {
		return Run{}, nil, err
	}
	states, err := e.store.ListStepStates(context.Background(), runID)
	if err != nil {
		return Run{}, nil, err
	}
	return *run, states, nil
}

func (e *Engine) Subscribe(runID string) ([]Event, <-chan Event, func(), error) {
	history, err := e.store.GetEvents(context.Background(), runID, -1)
	if err != nil {
		return nil, nil, nil, err
	}
	ch := make(chan Event, 16)
	e.mu.Lock()
	if _, ok := e.subs[runID]; !ok {
		e.subs[runID] = make(map[chan Event]struct{})
	}
	e.subs[runID][ch] = struct{}{}
	e.mu.Unlock()
	cancel := func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		delete(e.subs[runID], ch)
		close(ch)
	}
	return history, ch, cancel, nil
}

func (e *Engine) ResumeRun(ctx context.Context, runID string, payload map[string]any) error {
	if e.checkpoints == nil {
		return fmt.Errorf("checkpoint service is not configured")
	}
	record, ok, err := e.checkpoints.PendingByWorkflowRun(ctx, runID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("workflow run %q has no pending checkpoint", runID)
	}
	return e.checkpoints.Resume(ctx, record.ID, payload)
}

func (e *Engine) execute(runID string) {
	run, err := e.store.GetRun(context.Background(), runID)
	if err != nil {
		return
	}
	def := e.defs[run.WorkflowName]
	stepIndex := indexSteps(def)

	branchTargetStepID := ""
	for stepID := run.CurrentStepID; stepID != ""; {
		step, ok := stepIndex[stepID]
		if !ok {
			e.failRun(run, stepID, fmt.Errorf("unknown workflow step %q", stepID))
			return
		}
		e.emit(run.ID, "workflow.step.started", map[string]any{"step_id": step.ID, "type": step.Type})
		state := StepState{
			WorkflowRunID: run.ID,
			StepID:        step.ID,
			Status:        StepStatusRunning,
			StartedAt:     e.now().UTC(),
			UpdatedAt:     e.now().UTC(),
		}
		_ = e.store.UpsertStepState(context.Background(), &state)

		contextData, err := e.runtimeData(run.ID, run.InputJSON)
		if err != nil {
			e.failRun(run, stepID, err)
			return
		}

		var (
			outputJSON string
			nextStepID string
		)
		switch step.Type {
		case StepTypeTool:
			outputJSON, err = e.executeToolStep(step, contextData)
			nextStepID = e.nextStepID(def, step)
			if branchTargetStepID == step.ID && strings.TrimSpace(step.Next) == "" {
				nextStepID = ""
			}
		case StepTypeRun:
			outputJSON, err = e.executeRunStep(step, contextData)
			nextStepID = e.nextStepID(def, step)
			if branchTargetStepID == step.ID && strings.TrimSpace(step.Next) == "" {
				nextStepID = ""
			}
		case StepTypeCheckpoint:
			outputJSON, err = e.executeCheckpointStep(run, step)
			nextStepID = e.nextStepID(def, step)
			if branchTargetStepID == step.ID && strings.TrimSpace(step.Next) == "" {
				nextStepID = ""
			}
		case StepTypeBranch:
			nextStepID, err = e.executeBranchStep(step, contextData)
			outputJSON = ""
			branchTargetStepID = nextStepID
		default:
			err = fmt.Errorf("unsupported workflow step type %q", step.Type)
		}
		if err != nil {
			state.Status = StepStatusFailed
			state.Error = err.Error()
			state.UpdatedAt = e.now().UTC()
			_ = e.store.UpsertStepState(context.Background(), &state)
			e.failRun(run, step.ID, err)
			return
		}

		if outputJSON != "" {
			state.OutputJSON = outputJSON
		}
		state.Status = StepStatusCompleted
		state.UpdatedAt = e.now().UTC()
		_ = e.store.UpsertStepState(context.Background(), &state)
		e.emit(run.ID, "workflow.step.completed", map[string]any{"step_id": step.ID})

		run.CurrentStepID = nextStepID
		if outputJSON != "" {
			run.OutputJSON = outputJSON
		}
		run.UpdatedAt = e.now().UTC()
		if nextStepID == "" {
			run.Status = RunStatusCompleted
			_ = e.store.UpdateRun(context.Background(), run)
			e.emit(run.ID, "workflow.completed", map[string]any{"workflow": run.WorkflowName})
			return
		}
		if branchTargetStepID == step.ID {
			branchTargetStepID = ""
		}
		if branchTargetStepID == nextStepID && step.Type != StepTypeBranch {
			branchTargetStepID = ""
		}
		_ = e.store.UpdateRun(context.Background(), run)
		stepID = nextStepID
	}
}

func (e *Engine) executeToolStep(step StepDefinition, data map[string]any) (string, error) {
	if e.tools == nil {
		return "", fmt.Errorf("tool executor is not configured")
	}
	args, err := resolveValue(step.Args, data)
	if err != nil {
		return "", err
	}
	rawArgs, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	output, err := e.tools.Execute(context.Background(), step.Tool, rawArgs)
	if err != nil {
		return "", err
	}
	return normalizeOutputJSON(output)
}

func (e *Engine) executeRunStep(step StepDefinition, data map[string]any) (string, error) {
	if e.runner == nil {
		return "", fmt.Errorf("run engine is not configured")
	}
	if step.Run == nil {
		return "", fmt.Errorf("run step %q is missing run config", step.ID)
	}
	prompt, err := resolveString(step.Run.Prompt, data)
	if err != nil {
		return "", err
	}
	model, err := resolveString(step.Run.Model, data)
	if err != nil {
		return "", err
	}
	childRun, err := e.runner.StartRun(harness.RunRequest{
		Prompt: prompt,
		Model:  model,
	})
	if err != nil {
		return "", err
	}
	history, stream, cancel, err := e.runner.Subscribe(childRun.ID)
	if err != nil {
		return "", err
	}
	defer cancel()
	completed := false
	for _, event := range history {
		if harness.IsTerminalEvent(event.Type) {
			completed = true
			break
		}
	}
	if !completed {
		for event := range stream {
			if harness.IsTerminalEvent(event.Type) {
				completed = true
				break
			}
		}
	}
	if !completed {
		return "", fmt.Errorf("run step %q did not reach terminal state", step.ID)
	}
	loadedRun, ok := e.runner.GetRun(childRun.ID)
	if !ok {
		loadedRun = childRun
	}
	raw, err := json.Marshal(map[string]any{
		"run_id": loadedRun.ID,
		"status": loadedRun.Status,
		"output": loadedRun.Output,
		"error":  loadedRun.Error,
	})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (e *Engine) executeCheckpointStep(run *Run, step StepDefinition) (string, error) {
	if e.checkpoints == nil {
		return "", fmt.Errorf("checkpoint service is not configured")
	}
	payloadJSON := ""
	schemaJSON := ""
	if step.Checkpoint != nil && step.Checkpoint.SuspendPayload != nil {
		raw, err := json.Marshal(step.Checkpoint.SuspendPayload)
		if err != nil {
			return "", err
		}
		payloadJSON = string(raw)
	}
	if step.Checkpoint != nil && step.Checkpoint.ResumeSchema != nil {
		raw, err := json.Marshal(step.Checkpoint.ResumeSchema)
		if err != nil {
			return "", err
		}
		schemaJSON = string(raw)
	}
	record, err := e.checkpoints.Create(context.Background(), checkpoints.CreateRequest{
		Kind:           checkpoints.KindExternalResume,
		RunID:          run.ID,
		WorkflowRunID:  run.ID,
		CallID:         step.ID,
		SuspendPayload: payloadJSON,
		ResumeSchema:   schemaJSON,
	})
	if err != nil {
		return "", err
	}
	run.Status = RunStatusWaitingForCheckpoint
	run.CurrentCheckpointID = record.ID
	run.UpdatedAt = e.now().UTC()
	_ = e.store.UpdateRun(context.Background(), run)
	e.emit(run.ID, "workflow.suspended", map[string]any{"checkpoint_id": record.ID, "step_id": step.ID})

	result, err := e.checkpoints.Wait(context.Background(), record.ID)
	if err != nil {
		return "", err
	}
	run.Status = RunStatusRunning
	run.UpdatedAt = e.now().UTC()
	_ = e.store.UpdateRun(context.Background(), run)
	e.emit(run.ID, "workflow.resumed", map[string]any{"checkpoint_id": record.ID, "step_id": step.ID})
	raw, err := json.Marshal(result.Payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (e *Engine) executeBranchStep(step StepDefinition, data map[string]any) (string, error) {
	value := fmt.Sprint(resolvePath(data, step.Field))
	if next, ok := step.Cases[value]; ok {
		return next, nil
	}
	return step.Default, nil
}

func (e *Engine) nextStepID(def Definition, step StepDefinition) string {
	if strings.TrimSpace(step.Next) != "" {
		return step.Next
	}
	for index, candidate := range def.Steps {
		if candidate.ID == step.ID && index+1 < len(def.Steps) {
			return def.Steps[index+1].ID
		}
	}
	return ""
}

func (e *Engine) runtimeData(runID, inputJSON string) (map[string]any, error) {
	data := map[string]any{
		"inputs": map[string]any{},
		"steps":  map[string]any{},
	}
	if strings.TrimSpace(inputJSON) != "" {
		inputs := map[string]any{}
		if err := json.Unmarshal([]byte(inputJSON), &inputs); err != nil {
			return nil, err
		}
		data["inputs"] = inputs
	}
	stepStates, err := e.store.ListStepStates(context.Background(), runID)
	if err != nil {
		return nil, err
	}
	steps := data["steps"].(map[string]any)
	for _, state := range stepStates {
		if state.Status != StepStatusCompleted || strings.TrimSpace(state.OutputJSON) == "" {
			continue
		}
		var output any
		if err := json.Unmarshal([]byte(state.OutputJSON), &output); err != nil {
			output = map[string]any{"text": state.OutputJSON}
		}
		steps[state.StepID] = map[string]any{"output": output}
	}
	return data, nil
}

func (e *Engine) failRun(run *Run, stepID string, err error) {
	run.Status = RunStatusFailed
	run.CurrentStepID = stepID
	run.Error = err.Error()
	run.UpdatedAt = e.now().UTC()
	_ = e.store.UpdateRun(context.Background(), run)
	e.emit(run.ID, "workflow.failed", map[string]any{"step_id": stepID, "error": err.Error()})
}

func (e *Engine) emit(runID, eventType string, payload map[string]any) {
	e.mu.Lock()
	e.eventSeqs[runID]++
	seq := e.eventSeqs[runID]
	event := Event{
		Seq:           seq,
		WorkflowRunID: runID,
		Type:          eventType,
		Payload:       payload,
		Timestamp:     e.now().UTC(),
	}
	_ = e.store.AppendEvent(context.Background(), &event)
	for ch := range e.subs[runID] {
		select {
		case ch <- event:
		default:
		}
	}
	e.mu.Unlock()
}

func firstStepID(def Definition) string {
	if len(def.Steps) == 0 {
		return ""
	}
	return def.Steps[0].ID
}

func indexSteps(def Definition) map[string]StepDefinition {
	index := make(map[string]StepDefinition, len(def.Steps))
	for _, step := range def.Steps {
		index[step.ID] = step
	}
	return index
}

func normalizeOutputJSON(output string) (string, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return "", nil
	}
	var parsed any
	if json.Unmarshal([]byte(trimmed), &parsed) == nil {
		return trimmed, nil
	}
	raw, err := json.Marshal(map[string]any{"text": output})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func resolveValue(value any, data map[string]any) (any, error) {
	switch typed := value.(type) {
	case nil:
		return map[string]any{}, nil
	case string:
		return resolveString(typed, data)
	case map[string]any:
		resolved := make(map[string]any, len(typed))
		for key, item := range typed {
			value, err := resolveValue(item, data)
			if err != nil {
				return nil, err
			}
			resolved[key] = value
		}
		return resolved, nil
	case []any:
		resolved := make([]any, 0, len(typed))
		for _, item := range typed {
			value, err := resolveValue(item, data)
			if err != nil {
				return nil, err
			}
			resolved = append(resolved, value)
		}
		return resolved, nil
	default:
		return typed, nil
	}
}

func resolveString(template string, data map[string]any) (string, error) {
	if !strings.Contains(template, "{{") {
		return template, nil
	}
	result := template
	for {
		start := strings.Index(result, "{{")
		if start < 0 {
			break
		}
		end := strings.Index(result[start:], "}}")
		if end < 0 {
			break
		}
		end += start
		expr := strings.TrimSpace(result[start+2 : end])
		value := fmt.Sprint(resolvePath(data, expr))
		result = result[:start] + value + result[end+2:]
	}
	return result, nil
}

func resolvePath(root any, path string) any {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	current := root
	for _, part := range strings.Split(path, ".") {
		asMap, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = asMap[part]
	}
	return current
}
