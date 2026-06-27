package workflow_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"go-agent-harness/internal/workflow"
)

type sourceNoopSubagents struct{}

func (sourceNoopSubagents) Create(context.Context, workflow.SubagentRequest) (workflow.SubagentResult, error) {
	return workflow.SubagentResult{ID: "subagent_1", Status: "completed", Output: "ok"}, nil
}

func (sourceNoopSubagents) Get(context.Context, string) (workflow.SubagentResult, error) {
	return workflow.SubagentResult{ID: "subagent_1", Status: "completed", Output: "ok"}, nil
}

type sourceRecordingSubagents struct {
	mu  sync.Mutex
	req workflow.SubagentRequest
}

func (s *sourceRecordingSubagents) Create(_ context.Context, req workflow.SubagentRequest) (workflow.SubagentResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.req = req
	return workflow.SubagentResult{ID: "subagent_1", Status: "completed", Output: "agent-output"}, nil
}

func (s *sourceRecordingSubagents) Get(context.Context, string) (workflow.SubagentResult, error) {
	return workflow.SubagentResult{ID: "subagent_1", Status: "completed", Output: "agent-output"}, nil
}

func (s *sourceRecordingSubagents) request() workflow.SubagentRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.req
}

type sourceQuestionResponder struct {
	mu  sync.Mutex
	req workflow.QuestionRequest
}

func (r *sourceQuestionResponder) AskWorkflowQuestion(_ context.Context, req workflow.QuestionRequest) (any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.req = req
	return "Continue", nil
}

func (r *sourceQuestionResponder) request() workflow.QuestionRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.req
}

func TestSourceManagerCreateWorkflowHotRegistersAndRunsFeedback(t *testing.T) {
	root := t.TempDir()
	engine := workflow.NewEngine(workflow.EngineOptions{Subagents: sourceNoopSubagents{}})
	manager, err := workflow.NewSourceManager(workflow.SourceManagerOptions{
		Engine:       engine,
		WorkflowDirs: []string{filepath.Join(root, "global"), filepath.Join(root, "workspace")},
		CacheDir:     filepath.Join(root, "cache"),
		ModuleRoot:   mustRepoRoot(t),
	})
	require.NoError(t, err)

	source := `package main

import sdk "go-agent-harness/pkg/workflowsdk"

func main() {
	sdk.Main(func(ctx *sdk.Context) (any, error) {
		_ = ctx.Phase("Inspect")
		_ = ctx.Feedback("finding", "found useful evidence", map[string]any{"file": "main.go"})
		return map[string]any{"ok": true, "args": ctx.Args}, nil
	})
}`

	bundle, err := manager.CreateWorkflow(context.Background(), workflow.CreateWorkflowRequest{
		Name:        "evidence-check",
		Description: "Checks evidence.",
		WhenToUse:   "Use when evidence needs validation.",
		Source:      source,
		Scope:       "workspace",
	})
	require.NoError(t, err)
	require.Equal(t, "evidence-check", bundle.Manifest.Name)
	require.NotEmpty(t, bundle.Hash)

	run, err := manager.Start(context.Background(), "evidence-check", map[string]any{"target": "x"})
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	final, events, err := manager.Wait(waitCtx, run.ID)
	require.NoError(t, err)
	require.Equal(t, workflow.RunStatusCompleted, final.Status)
	require.Contains(t, final.ResultJSON, `"ok":true`)

	var sawFinding bool
	for _, ev := range events {
		if ev.Type == workflow.EventWorkflowFinding {
			sawFinding = true
			require.Equal(t, "finding", ev.Payload["kind"])
			require.Equal(t, "found useful evidence", ev.Payload["message"])
		}
	}
	require.True(t, sawFinding, "expected workflow finding feedback event")
}

func TestSourceManagerCreateWorkflowRejectsDuplicateUntilOverwrite(t *testing.T) {
	root := t.TempDir()
	manager := newTestSourceManager(t, root, sourceNoopSubagents{}, nil)

	_, err := manager.CreateWorkflow(context.Background(), workflow.CreateWorkflowRequest{
		Name:        "daily-review",
		Description: "Review the day.",
		Source:      workflowSourceReturning(`map[string]any{"version": 1}`),
		Scope:       "workspace",
	})
	require.NoError(t, err)

	_, err = manager.CreateWorkflow(context.Background(), workflow.CreateWorkflowRequest{
		Name:        "daily-review",
		Description: "Review the day again.",
		Source:      workflowSourceReturning(`map[string]any{"version": 2}`),
		Scope:       "workspace",
	})
	require.ErrorContains(t, err, "already exists")

	_, err = manager.CreateWorkflow(context.Background(), workflow.CreateWorkflowRequest{
		Name:        "daily-review",
		Description: "Review the day again.",
		Source:      workflowSourceReturning(`map[string]any{"version": 2}`),
		Scope:       "workspace",
		Overwrite:   true,
	})
	require.NoError(t, err)

	final := runWorkflowToTerminal(t, manager, "daily-review", map[string]any{})
	require.Equal(t, workflow.RunStatusCompleted, final.Status)
	require.Contains(t, final.ResultJSON, `"version":2`)
}

func TestSourceManagerCreateWorkflowCompileFailureDoesNotActivate(t *testing.T) {
	root := t.TempDir()
	manager := newTestSourceManager(t, root, sourceNoopSubagents{}, nil)

	_, err := manager.CreateWorkflow(context.Background(), workflow.CreateWorkflowRequest{
		Name:        "broken-workflow",
		Description: "Does not compile.",
		Source:      "package main\nfunc main() {",
		Scope:       "workspace",
	})
	require.ErrorContains(t, err, "build workflow")

	_, err = manager.Start(context.Background(), "broken-workflow", map[string]any{})
	require.ErrorContains(t, err, "not found")
}

func TestSourceManagerCreateWorkflowValidatesName(t *testing.T) {
	root := t.TempDir()
	manager := newTestSourceManager(t, root, sourceNoopSubagents{}, nil)

	_, err := manager.CreateWorkflow(context.Background(), workflow.CreateWorkflowRequest{
		Name:        "Not Kebab",
		Description: "Invalid name.",
		Source:      workflowSourceReturning(`"ok"`),
		Scope:       "workspace",
	})
	require.ErrorContains(t, err, "must be kebab-case")
}

func TestSourceManagerRunWorkflowFailsOnInvalidProtocolAfterResult(t *testing.T) {
	root := t.TempDir()
	manager := newTestSourceManager(t, root, sourceNoopSubagents{}, nil)

	source := `package main

import "fmt"

func main() {
	fmt.Println(` + "`" + `{"type":"result","result":{"ok":true}}` + "`" + `)
	fmt.Println(` + "`" + `{"type":"log","args":{"message":"late side effect"}}` + "`" + `)
}`
	_, err := manager.CreateWorkflow(context.Background(), workflow.CreateWorkflowRequest{
		Name:        "bad-protocol",
		Description: "Emits a protocol message after result.",
		Source:      source,
		Scope:       "workspace",
	})
	require.NoError(t, err)

	final := runWorkflowToTerminal(t, manager, "bad-protocol", map[string]any{})
	require.Equal(t, workflow.RunStatusFailed, final.Status)
	require.Contains(t, final.Error, "message after terminal result")
}

func TestSourceManagerRunWorkflowFailsOnTimeout(t *testing.T) {
	root := t.TempDir()
	manager := newTestSourceManager(t, root, sourceNoopSubagents{}, nil)

	source := `package main

import "time"

func main() {
	time.Sleep(3 * time.Second)
}`
	_, err := manager.CreateWorkflow(context.Background(), workflow.CreateWorkflowRequest{
		Name:           "sleepy-workflow",
		Description:    "Sleeps too long.",
		Source:         source,
		Scope:          "workspace",
		TimeoutSeconds: 1,
	})
	require.NoError(t, err)

	final := runWorkflowToTerminal(t, manager, "sleepy-workflow", map[string]any{})
	require.Equal(t, workflow.RunStatusFailed, final.Status)
	require.Contains(t, final.Error, "timed out")
}

func TestSourceManagerRunWorkflowFailsOnProcessExit(t *testing.T) {
	root := t.TempDir()
	manager := newTestSourceManager(t, root, sourceNoopSubagents{}, nil)

	source := `package main

import "os"

func main() {
	os.Exit(7)
}`
	_, err := manager.CreateWorkflow(context.Background(), workflow.CreateWorkflowRequest{
		Name:        "exit-workflow",
		Description: "Exits nonzero.",
		Source:      source,
		Scope:       "workspace",
	})
	require.NoError(t, err)

	final := runWorkflowToTerminal(t, manager, "exit-workflow", map[string]any{})
	require.Equal(t, workflow.RunStatusFailed, final.Status)
	require.Contains(t, final.Error, "exited")

	resumed, err := manager.Resume(context.Background(), final.ID, map[string]any{"retry": true})
	require.NoError(t, err)
	waitCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resumedFinal, _, err := manager.Wait(waitCtx, resumed.ID)
	require.NoError(t, err)
	require.Equal(t, workflow.RunStatusFailed, resumedFinal.Status)
}

func TestSourceManagerRunWorkflowIncludesBoundedStderrOnProcessExit(t *testing.T) {
	root := t.TempDir()
	manager := newTestSourceManager(t, root, sourceNoopSubagents{}, nil)

	source := `package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprint(os.Stderr, "child stderr diagnostic")
	os.Exit(7)
}`
	_, err := manager.CreateWorkflow(context.Background(), workflow.CreateWorkflowRequest{
		Name:        "stderr-workflow",
		Description: "Writes stderr and exits nonzero.",
		Source:      source,
		Scope:       "workspace",
	})
	require.NoError(t, err)

	final := runWorkflowToTerminal(t, manager, "stderr-workflow", map[string]any{})
	require.Equal(t, workflow.RunStatusFailed, final.Status)
	require.Contains(t, final.Error, "child stderr diagnostic")
}

func TestSourceManagerNestedWorkflowRPC(t *testing.T) {
	root := t.TempDir()
	manager := newTestSourceManager(t, root, sourceNoopSubagents{}, nil)

	_, err := manager.CreateWorkflow(context.Background(), workflow.CreateWorkflowRequest{
		Name:        "child-workflow",
		Description: "Returns a child result.",
		Source:      workflowSourceReturning(`map[string]any{"child": true}`),
		Scope:       "workspace",
	})
	require.NoError(t, err)

	parent := `package main

import sdk "go-agent-harness/pkg/workflowsdk"

func main() {
	sdk.Main(func(ctx *sdk.Context) (any, error) {
		child, err := ctx.Workflow("child-workflow", map[string]any{"from": "parent"})
		if err != nil {
			return nil, err
		}
		return map[string]any{"child": child}, nil
	})
}`
	_, err = manager.CreateWorkflow(context.Background(), workflow.CreateWorkflowRequest{
		Name:        "parent-workflow",
		Description: "Calls a nested workflow.",
		Source:      parent,
		Scope:       "workspace",
	})
	require.NoError(t, err)

	final := runWorkflowToTerminal(t, manager, "parent-workflow", map[string]any{})
	require.Equal(t, workflow.RunStatusCompleted, final.Status)
	require.Contains(t, final.ResultJSON, `"child":true`)
}

func TestSourceManagerAgentRPCForwardsOptions(t *testing.T) {
	root := t.TempDir()
	subagents := &sourceRecordingSubagents{}
	manager := newTestSourceManager(t, root, subagents, nil)

	source := `package main

import sdk "go-agent-harness/pkg/workflowsdk"

func main() {
	sdk.Main(func(ctx *sdk.Context) (any, error) {
		res, err := ctx.Agent("inspect repository", &sdk.AgentOpts{
			Model: "gpt-5-nano",
			Provider: "openai",
			Profile: "reviewer",
			AllowedTools: []string{"read", "grep"},
			Isolation: "worktree",
			CleanupPolicy: "keep",
			AgentType: "review",
			MaxSteps: 3,
			MaxCostUSD: 0.25,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"output": res.Output}, nil
	})
}`
	_, err := manager.CreateWorkflow(context.Background(), workflow.CreateWorkflowRequest{
		Name:        "agent-options",
		Description: "Calls a subagent with options.",
		Source:      source,
		Scope:       "workspace",
	})
	require.NoError(t, err)

	final := runWorkflowToTerminal(t, manager, "agent-options", map[string]any{})
	require.Equal(t, workflow.RunStatusCompleted, final.Status)
	req := subagents.request()
	require.Equal(t, "inspect repository", req.Prompt)
	require.Equal(t, "gpt-5-nano", req.Model)
	require.Equal(t, "openai", req.Provider)
	require.Equal(t, "reviewer", req.Profile)
	require.Equal(t, []string{"read", "grep"}, req.AllowedTools)
	require.Equal(t, "worktree", req.Isolation)
	require.Equal(t, "keep", req.CleanupPolicy)
	require.Equal(t, "review", req.AgentType)
	require.Equal(t, 3, req.MaxSteps)
	require.Equal(t, 0.25, req.MaxCostUSD)
}

func TestSourceManagerQuestionEmitsEventAndUsesResponder(t *testing.T) {
	root := t.TempDir()
	responder := &sourceQuestionResponder{}
	manager := newTestSourceManager(t, root, sourceNoopSubagents{}, responder)

	source := `package main

import sdk "go-agent-harness/pkg/workflowsdk"

func main() {
	sdk.Main(func(ctx *sdk.Context) (any, error) {
		answer, err := ctx.Question("Continue?", []sdk.QuestionOption{{Label: "Continue", Description: "Keep going."}})
		if err != nil {
			return nil, err
		}
		return map[string]any{"answer": answer}, nil
	})
}`
	_, err := manager.CreateWorkflow(context.Background(), workflow.CreateWorkflowRequest{
		Name:        "question-workflow",
		Description: "Asks a question.",
		Source:      source,
		Scope:       "workspace",
	})
	require.NoError(t, err)

	run, err := manager.Start(context.Background(), "question-workflow", map[string]any{})
	require.NoError(t, err)
	waitCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	final, events, err := manager.Wait(waitCtx, run.ID)
	require.NoError(t, err)
	require.Equal(t, workflow.RunStatusCompleted, final.Status)
	require.Contains(t, final.ResultJSON, `"answer":"Continue"`)

	req := responder.request()
	require.Equal(t, "Continue?", req.Prompt)
	require.Len(t, req.Choices, 1)

	var sawQuestion bool
	for _, ev := range events {
		if ev.Type == workflow.EventWorkflowQuestion {
			sawQuestion = true
			require.Equal(t, "question", ev.Payload["kind"])
			require.Equal(t, true, ev.Payload["requires_response"])
		}
	}
	require.True(t, sawQuestion, "expected workflow question event")
}

func TestSourceManagerLoadDiscoversSkillBundledWorkflows(t *testing.T) {
	root := t.TempDir()
	skillWorkflowDir := filepath.Join(root, "skills", "review", "workflows", "skill-review")
	require.NoError(t, os.MkdirAll(skillWorkflowDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillWorkflowDir, "workflow.json"), []byte(`{
  "name": "skill-review",
  "description": "Skill bundled review workflow.",
  "version": 1,
  "language": "go",
  "entrypoint": "main.go",
  "when_to_use": "Use from the review skill."
}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(skillWorkflowDir, "main.go"), []byte(`package main

import sdk "go-agent-harness/pkg/workflowsdk"

func main() {
	sdk.Main(func(ctx *sdk.Context) (any, error) {
		_ = ctx.Log("skill workflow running")
		return "done", nil
	})
}`), 0o644))

	engine := workflow.NewEngine(workflow.EngineOptions{Subagents: sourceNoopSubagents{}})
	manager, err := workflow.NewSourceManager(workflow.SourceManagerOptions{
		Engine:     engine,
		SkillDirs:  []string{filepath.Join(root, "skills")},
		CacheDir:   filepath.Join(root, "cache"),
		ModuleRoot: mustRepoRoot(t),
	})
	require.NoError(t, err)
	require.NoError(t, manager.Load(context.Background()))

	var found bool
	for _, meta := range manager.List() {
		if meta.Name == "skill-review" {
			found = true
			require.Equal(t, "Skill bundled review workflow.", meta.Description)
			require.Equal(t, "Use from the review skill.", meta.WhenToUse)
		}
	}
	require.True(t, found, "expected skill-bundled workflow to be registered")
}

func newTestSourceManager(t *testing.T, root string, subagents workflow.SubagentManager, questions workflow.QuestionResponder) *workflow.SourceManager {
	t.Helper()
	engine := workflow.NewEngine(workflow.EngineOptions{Subagents: subagents, QuestionResponder: questions})
	manager, err := workflow.NewSourceManager(workflow.SourceManagerOptions{
		Engine:       engine,
		WorkflowDirs: []string{filepath.Join(root, "global"), filepath.Join(root, "workspace")},
		SkillDirs:    []string{filepath.Join(root, "skills")},
		CacheDir:     filepath.Join(root, "cache"),
		ModuleRoot:   mustRepoRoot(t),
	})
	require.NoError(t, err)
	return manager
}

func workflowSourceReturning(expr string) string {
	return `package main

import sdk "go-agent-harness/pkg/workflowsdk"

func main() {
	sdk.Main(func(ctx *sdk.Context) (any, error) {
		return ` + expr + `, nil
	})
}`
}

func runWorkflowToTerminal(t *testing.T, manager *workflow.SourceManager, name string, args map[string]any) *workflow.Run {
	t.Helper()
	run, err := manager.Start(context.Background(), name, args)
	require.NoError(t, err)
	waitCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	final, _, err := manager.Wait(waitCtx, run.ID)
	require.NoError(t, err)
	return final
}

func mustRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	for {
		raw, err := os.ReadFile(filepath.Join(wd, "go.mod"))
		if err == nil && strings.Contains(string(raw), "module go-agent-harness") {
			return wd
		}
		parent := filepath.Dir(wd)
		require.NotEqual(t, wd, parent, "repo root not found")
		wd = parent
	}
}
