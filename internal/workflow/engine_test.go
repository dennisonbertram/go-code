package workflow_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go-agent-harness/internal/workflow"
)

// mockMgr implements workflow.SubagentManager for testing.
type mockMgr struct {
	mu      sync.Mutex
	results map[string]workflow.SubagentResult
	calls   []workflow.SubagentRequest
	counter atomic.Int64
	delay   time.Duration
}

func newMockMgr() *mockMgr {
	return &mockMgr{results: make(map[string]workflow.SubagentResult)}
}

func (m *mockMgr) Create(_ context.Context, req workflow.SubagentRequest) (workflow.SubagentResult, error) {
	m.mu.Lock()
	m.calls = append(m.calls, req)
	m.mu.Unlock()
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	id := fmt.Sprintf("agent_%d", m.counter.Add(1))
	r := workflow.SubagentResult{ID: id, Status: "completed", Output: fmt.Sprintf("result: %s", req.Prompt)}
	m.mu.Lock()
	m.results[id] = r
	m.mu.Unlock()
	return r, nil
}

func (m *mockMgr) Get(_ context.Context, id string) (workflow.SubagentResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.results[id]
	if !ok {
		return workflow.SubagentResult{}, fmt.Errorf("not found: %s", id)
	}
	return r, nil
}

func (m *mockMgr) getCalls() []workflow.SubagentRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]workflow.SubagentRequest, len(m.calls))
	copy(out, m.calls)
	return out
}

func newEngine(t *testing.T) (*workflow.Engine, *mockMgr) {
	t.Helper()
	m := newMockMgr()
	e := workflow.NewEngine(workflow.EngineOptions{Subagents: m, MaxConcurrency: 4})
	return e, m
}

func waitForRun(t *testing.T, eng *workflow.Engine, runID string, want workflow.RunStatus) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		run, err := eng.GetRun(runID)
		if err == nil && (run.Status == workflow.RunStatusCompleted || run.Status == workflow.RunStatusFailed) {
			if want != "" {
				assert.Equal(t, want, run.Status)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("run %s did not complete", runID)
}

func TestEngineStartComplete(t *testing.T) {
	eng, mgr := newEngine(t)
	eng.Register("test", func(ctx *workflow.Context) (any, error) {
		r, err := ctx.Agent("do work", nil)
		if err != nil { return nil, err }
		return r.Output, nil
	})
	run, err := eng.Start(context.Background(), "test", nil)
	require.NoError(t, err)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)
	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
	assert.Contains(t, final.ResultJSON, "result:")
	assert.Len(t, mgr.getCalls(), 1)
}

func TestEngineStartUnknown(t *testing.T) {
	eng, _ := newEngine(t)
	_, err := eng.Start(context.Background(), "unknown", nil)
	assert.Error(t, err)
}

func TestEngineScriptError(t *testing.T) {
	eng, _ := newEngine(t)
	eng.Register("fail", func(ctx *workflow.Context) (any, error) {
		return nil, fmt.Errorf("intentional")
	})
	run, _ := eng.Start(context.Background(), "fail", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusFailed)
	final, _ := eng.GetRun(run.ID)
	assert.Contains(t, final.Error, "intentional")
}

func TestEngineScriptPanic(t *testing.T) {
	eng, _ := newEngine(t)
	eng.Register("panic", func(ctx *workflow.Context) (any, error) {
		panic("boom")
	})
	run, _ := eng.Start(context.Background(), "panic", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusFailed)
	final, _ := eng.GetRun(run.ID)
	assert.Contains(t, final.Error, "panic")
}

func TestEngineList(t *testing.T) {
	eng, _ := newEngine(t)
	eng.Register("b", func(ctx *workflow.Context) (any, error) { return nil, nil })
	eng.Register("a", func(ctx *workflow.Context) (any, error) { return nil, nil })
	list := eng.List()
	require.Len(t, list, 2)
	assert.Equal(t, "a", list[0].Name)
}

func TestAgent(t *testing.T) {
	eng, mgr := newEngine(t)
	eng.Register("agent", func(ctx *workflow.Context) (any, error) {
		r, err := ctx.Agent("hello", &workflow.AgentOpts{Label: "x"})
		if err != nil { return nil, err }
		return r.Output, nil
	})
	run, _ := eng.Start(context.Background(), "agent", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)
	assert.Equal(t, "hello", mgr.getCalls()[0].Prompt)
}

func TestAgentWithModel(t *testing.T) {
	eng, mgr := newEngine(t)
	eng.Register("m", func(ctx *workflow.Context) (any, error) {
		_, err := ctx.Agent("x", &workflow.AgentOpts{Model: "gpt-5"})
		return nil, err
	})
	run, _ := eng.Start(context.Background(), "m", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)
	assert.Equal(t, "gpt-5", mgr.getCalls()[0].Model)
}

func TestAgentWithIsolation(t *testing.T) {
	eng, mgr := newEngine(t)
	eng.Register("iso", func(ctx *workflow.Context) (any, error) {
		_, err := ctx.Agent("x", &workflow.AgentOpts{Isolation: "worktree"})
		return nil, err
	})
	run, _ := eng.Start(context.Background(), "iso", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)
	assert.Equal(t, "worktree", mgr.getCalls()[0].Isolation)
}

func TestParallel(t *testing.T) {
	eng, _ := newEngine(t)
	var order []int
	var mu sync.Mutex
	eng.Register("p", func(ctx *workflow.Context) (any, error) {
		results, _ := ctx.Parallel([]func() (any, error){
			func() (any, error) { mu.Lock(); order = append(order, 0); mu.Unlock(); return "a", nil },
			func() (any, error) { mu.Lock(); order = append(order, 1); mu.Unlock(); return "b", nil },
			func() (any, error) { mu.Lock(); order = append(order, 2); mu.Unlock(); return "c", nil },
		})
		return results, nil
	})
	run, _ := eng.Start(context.Background(), "p", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)
	assert.Len(t, order, 3)
}

func TestParallelErrorsNil(t *testing.T) {
	eng, _ := newEngine(t)
	eng.Register("pe", func(ctx *workflow.Context) (any, error) {
		results, _ := ctx.Parallel([]func() (any, error){
			func() (any, error) { return "ok", nil },
			func() (any, error) { return nil, fmt.Errorf("fail") },
		})
		assert.Nil(t, results[1])
		return results, nil
	})
	run, _ := eng.Start(context.Background(), "pe", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)
}

func TestPipeline(t *testing.T) {
	eng, _ := newEngine(t)
	eng.Register("pl", func(ctx *workflow.Context) (any, error) {
		results, _ := ctx.Pipeline(
			[]any{"a", "b"},
			func(prev any, item any, index int) (any, error) {
				return fmt.Sprintf("%s_1", item), nil
			},
			func(prev any, item any, index int) (any, error) {
				return fmt.Sprintf("%s_2", prev), nil
			},
		)
		return results, nil
	})
	run, _ := eng.Start(context.Background(), "pl", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)
	final, _ := eng.GetRun(run.ID)
	assert.Contains(t, final.ResultJSON, "a_1_2")
}

func TestPipelineErrorDropsItem(t *testing.T) {
	eng, _ := newEngine(t)
	eng.Register("ple", func(ctx *workflow.Context) (any, error) {
		results, _ := ctx.Pipeline(
			[]any{"a", "b"},
			func(prev any, item any, index int) (any, error) {
				if item == "b" { return nil, fmt.Errorf("fail") }
				return item, nil
			},
			func(prev any, item any, index int) (any, error) {
				return fmt.Sprintf("ok_%v", prev), nil
			},
		)
		assert.Nil(t, results[1])
		return results, nil
	})
	run, _ := eng.Start(context.Background(), "ple", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)
}

func TestPhaseAndLog(t *testing.T) {
	eng, _ := newEngine(t)
	eng.Register("ph", func(ctx *workflow.Context) (any, error) {
		ctx.Phase("main")
		ctx.Log("hello")
		return "done", nil
	})
	run, _ := eng.Start(context.Background(), "ph", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)
	history, _, cancel, _ := eng.Subscribe(run.ID)
	defer cancel()
	hasPhase, hasLog := false, false
	for _, ev := range history {
		if ev.Type == workflow.EventWorkflowPhaseStarted { hasPhase = true }
		if ev.Type == workflow.EventWorkflowLog { hasLog = true }
	}
	assert.True(t, hasPhase && hasLog)
}

func TestBudget(t *testing.T) {
	b := &workflow.Budget{Total: 100}
	assert.Equal(t, 100, b.Remaining())
	b.Spend(30)
	assert.Equal(t, 70, b.Remaining())
}

func TestBudgetUnlimited(t *testing.T) {
	b := &workflow.Budget{Total: 0}
	assert.True(t, b.Remaining() > 1_000_000)
}

func TestBudgetClone(t *testing.T) {
	parent := &workflow.Budget{Total: 100}
	parent.Spend(20)
	child := parent.Clone()
	child.Spend(30)
	assert.Equal(t, 50, parent.Remaining())
}

func TestSchemaValid(t *testing.T) {
	schema := map[string]any{"type": "object", "required": []any{"x"}, "properties": map[string]any{"x": map[string]any{"type": "string"}}}
	assert.NoError(t, workflow.ValidateSchema(schema, map[string]any{"x": "hi"}))
}

func TestSchemaInvalid(t *testing.T) {
	schema := map[string]any{"type": "object", "required": []any{"x"}, "properties": map[string]any{"x": map[string]any{"type": "string"}}}
	assert.Error(t, workflow.ValidateSchema(schema, map[string]any{"y": "hi"}))
}

func TestSchemaEnum(t *testing.T) {
	assert.NoError(t, workflow.ValidateSchema(map[string]any{"enum": []any{1, 2}}, float64(1)))
	assert.Error(t, workflow.ValidateSchema(map[string]any{"enum": []any{1, 2}}, float64(3)))
}

func TestParseStructuredOutput(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"v": map[string]any{"type": "integer"}}}
	parsed, err := workflow.ParseStructuredOutput(`{"v":42}`, schema)
	require.NoError(t, err)
	assert.Equal(t, float64(42), parsed.(map[string]any)["v"])
}

func TestParseStructuredOutputMarkdown(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "integer"}}}
	parsed, err := workflow.ParseStructuredOutput("```json\n{\"x\":99}\n```", schema)
	require.NoError(t, err)
	assert.Equal(t, float64(99), parsed.(map[string]any)["x"])
}

func TestArgs(t *testing.T) {
	eng, _ := newEngine(t)
	eng.Register("args", func(ctx *workflow.Context) (any, error) {
		return ctx.Args.(map[string]any)["key"], nil
	})
	run, _ := eng.Start(context.Background(), "args", map[string]any{"key": "val"})
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)
	final, _ := eng.GetRun(run.ID)
	assert.Contains(t, final.ResultJSON, "val")
}

func TestNestedWorkflow(t *testing.T) {
	eng, _ := newEngine(t)
	eng.Register("inner", func(ctx *workflow.Context) (any, error) { return "in", nil })
	eng.Register("outer", func(ctx *workflow.Context) (any, error) {
		r, err := ctx.Workflow("inner", nil)
		if err != nil { return nil, err }
		return map[string]any{"x": r}, nil
	})
	run, _ := eng.Start(context.Background(), "outer", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)
	final, _ := eng.GetRun(run.ID)
	assert.Contains(t, final.ResultJSON, "in")
}

func TestNestedWorkflowNotFound(t *testing.T) {
	eng, _ := newEngine(t)
	eng.Register("bad", func(ctx *workflow.Context) (any, error) {
		_, err := ctx.Workflow("nope", nil)
		return nil, err
	})
	run, _ := eng.Start(context.Background(), "bad", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusFailed)
}

func TestConcurrencyCap(t *testing.T) {
	var cur, maxC atomic.Int64
	mgr := newMockMgr()
	mgr.delay = 50 * time.Millisecond

	// Wrap to track concurrent agent calls at the SubagentManager level.
	// This is where the semaphore gates access, so it accurately measures
	// the concurrency cap enforced by Agent().
	inner := workflow.SubagentManager(mgr)
	tracker := &concurrencyTrackingMgr{inner: inner, cur: &cur, maxC: &maxC}

	eng := workflow.NewEngine(workflow.EngineOptions{Subagents: tracker, MaxConcurrency: 2})
	eng.Register("cc", func(ctx *workflow.Context) (any, error) {
		tasks := make([]func() (any, error), 4)
		for i := range tasks {
			idx := i
			tasks[i] = func() (any, error) {
				_, err := ctx.Agent(fmt.Sprintf("task-%d", idx), nil)
				return "x", err
			}
		}
		results, _ := ctx.Parallel(tasks)
		return results, nil
	})
	run, _ := eng.Start(context.Background(), "cc", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)
	assert.LessOrEqual(t, maxC.Load(), int64(2), "agent calls capped at MaxConcurrency=2")
	assert.Len(t, mgr.getCalls(), 4)
}

// concurrencyTrackingMgr wraps SubagentManager to track concurrent Create calls.
type concurrencyTrackingMgr struct {
	inner workflow.SubagentManager
	cur   *atomic.Int64
	maxC  *atomic.Int64
}

func (m *concurrencyTrackingMgr) Create(ctx context.Context, req workflow.SubagentRequest) (workflow.SubagentResult, error) {
	c := m.cur.Add(1)
	for {
		o := m.maxC.Load()
		if c <= o || m.maxC.CompareAndSwap(o, c) {
			break
		}
	}
	defer m.cur.Add(-1)
	return m.inner.Create(ctx, req)
}

func (m *concurrencyTrackingMgr) Get(ctx context.Context, id string) (workflow.SubagentResult, error) {
	return m.inner.Get(ctx, id)
}

func TestResume(t *testing.T) {
	eng, _ := newEngine(t)
	var calls atomic.Int64
	eng.Register("resume", func(ctx *workflow.Context) (any, error) {
		if calls.Add(1) == 1 { return nil, fmt.Errorf("fail") }
		return "ok", nil
	})
	run, _ := eng.Start(context.Background(), "resume", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusFailed)
	_, err := eng.Resume(context.Background(), run.ID, nil)
	require.NoError(t, err)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)
}

func TestResumeNotFailed(t *testing.T) {
	eng, _ := newEngine(t)
	eng.Register("ok", func(ctx *workflow.Context) (any, error) { return nil, nil })
	run, _ := eng.Start(context.Background(), "ok", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)
	_, err := eng.Resume(context.Background(), run.ID, nil)
	assert.Error(t, err)
}

func TestSubscribe(t *testing.T) {
	eng, _ := newEngine(t)
	eng.Register("sub", func(ctx *workflow.Context) (any, error) {
		ctx.Log("msg")
		ctx.Agent("work", nil)
		return "ok", nil
	})
	run, _ := eng.Start(context.Background(), "sub", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)
	history, _, cancel, _ := eng.Subscribe(run.ID)
	defer cancel()
	hasLog := false
	for _, ev := range history {
		if ev.Type == workflow.EventWorkflowLog { hasLog = true }
	}
	assert.True(t, hasLog)
}

func TestPatternAdversarialVerify(t *testing.T) {
	eng, mgr := newEngine(t)
	eng.Register("review", func(ctx *workflow.Context) (any, error) {
		ctx.Phase("Review")
		results, _ := ctx.Pipeline(
			[]any{"bugs", "perf"},
			func(prev any, item any, index int) (any, error) {
				r, err := ctx.Agent(fmt.Sprintf("find %s", item), &workflow.AgentOpts{Label: "r:" + item.(string)})
				if err != nil { return nil, err }
				return r.Output, nil
			},
		)
		return results, nil
	})
	run, _ := eng.Start(context.Background(), "review", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)
	assert.Len(t, mgr.getCalls(), 2)
}

func TestPatternLoopUntilCount(t *testing.T) {
	eng, mgr := newEngine(t)
	eng.Register("loop", func(ctx *workflow.Context) (any, error) {
		var bugs []string
		for len(bugs) < 3 {
			r, err := ctx.Agent("find bug", nil)
			if err != nil { return nil, err }
			bugs = append(bugs, r.Output)
		}
		return bugs, nil
	})
	run, _ := eng.Start(context.Background(), "loop", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)
	assert.Len(t, mgr.getCalls(), 3)
}
