package workflow_test

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go-agent-harness/internal/workflow"
)

// =============================================================================
// Helpers (mockMgr, newEngine, waitForRun are in engine_test.go)
// =============================================================================

// newEngineWithBudget creates an engine with a default budget.
func newEngineWithBudget(t *testing.T, budget int) (*workflow.Engine, *mockMgr) {
	t.Helper()
	m := newMockMgr()
	e := workflow.NewEngine(workflow.EngineOptions{
		Subagents:      m,
		MaxConcurrency: 4,
		DefaultBudget:  budget,
	})
	return e, m
}

// collectEvents subscribes and drains all events (historical + live) into a slice.
func collectEvents(t *testing.T, eng *workflow.Engine, runID string) []workflow.Event {
	t.Helper()
	history, live, cancel, err := eng.Subscribe(runID)
	require.NoError(t, err)
	defer cancel()
	all := make([]workflow.Event, 0, len(history))
	all = append(all, history...)
	// Drain live with timeout
	timeout := time.After(500 * time.Millisecond)
drain:
	for {
		select {
		case ev, ok := <-live:
			if !ok {
				break drain
			}
			all = append(all, ev)
		case <-timeout:
			break drain
		}
	}
	return all
}

// failingMgr is a mock that can be configured to fail on specific calls.
type failingMgr struct {
	*mockMgr
	failOnCall int
	callCount  atomic.Int64
}

func (f *failingMgr) Create(ctx context.Context, req workflow.SubagentRequest) (workflow.SubagentResult, error) {
	if f.callCount.Add(1) == int64(f.failOnCall) {
		return workflow.SubagentResult{}, fmt.Errorf("injected failure on call %d", f.failOnCall)
	}
	return f.mockMgr.Create(ctx, req)
}

// =============================================================================
// Scenario 01: Simple agent call with label and phase
// =============================================================================

func TestComprehensive_01_SimpleAgent(t *testing.T) {
	eng, mgr := newEngine(t)

	eng.Register("simple-agent", func(ctx *workflow.Context) (any, error) {
		ctx.Phase("main")
		ctx.Log("starting work")
		r, err := ctx.Agent("process data", &workflow.AgentOpts{
			Label: "processor",
			Phase: "main",
		})
		if err != nil {
			return nil, err
		}
		ctx.Log("work complete")
		return map[string]any{"output": r.Output, "hasSchema": r.Schema != nil}, nil
	})

	run, err := eng.Start(context.Background(), "simple-agent", nil)
	require.NoError(t, err)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	final, err := eng.GetRun(run.ID)
	require.NoError(t, err)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
	assert.Contains(t, final.ResultJSON, "process data")

	calls := mgr.getCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "process data", calls[0].Prompt)

	// Verify events
	events := collectEvents(t, eng, run.ID)
	hasStarted, hasAgent, hasCompleted := false, false, false
	for _, ev := range events {
		switch ev.Type {
		case workflow.EventWorkflowStarted:
			hasStarted = true
		case workflow.EventWorkflowAgentCompleted:
			hasAgent = true
		case workflow.EventWorkflowCompleted:
			hasCompleted = true
		}
	}
	assert.True(t, hasStarted && hasAgent && hasCompleted, "missing expected events")
}

// =============================================================================
// Scenario 02: Parallel execution with barrier
// =============================================================================

func TestComprehensive_02_ParallelBarrier(t *testing.T) {
	eng, _ := newEngine(t)

	var order []int
	var mu sync.Mutex

	eng.Register("parallel-barrier", func(ctx *workflow.Context) (any, error) {
		ctx.Phase("fanout")
		results, _ := ctx.Parallel([]func() (any, error){
			func() (any, error) {
				time.Sleep(50 * time.Millisecond)
				mu.Lock()
				order = append(order, 1)
				mu.Unlock()
				return "first", nil
			},
			func() (any, error) {
				mu.Lock()
				order = append(order, 2)
				mu.Unlock()
				return "second", nil
			},
			func() (any, error) {
				time.Sleep(30 * time.Millisecond)
				mu.Lock()
				order = append(order, 3)
				mu.Unlock()
				return "third", nil
			},
			func() (any, error) {
				mu.Lock()
				order = append(order, 4)
				mu.Unlock()
				return "fourth", nil
			},
			func() (any, error) {
				time.Sleep(20 * time.Millisecond)
				mu.Lock()
				order = append(order, 5)
				mu.Unlock()
				return "fifth", nil
			},
		})
		// All must complete before we reach here (barrier)
		assert.Len(t, order, 5, "all 5 thunks must complete before barrier lifts")
		assert.Len(t, results, 5)
		return results, nil
	})

	run, _ := eng.Start(context.Background(), "parallel-barrier", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
}

// =============================================================================
// Scenario 03: Pipeline no-barrier processing
// =============================================================================

func TestComprehensive_03_PipelineNoBarrier(t *testing.T) {
	eng, _ := newEngine(t)

	// Track which stage each item is in concurrently
	var stageProgress sync.Map

	eng.Register("pipeline-flow", func(ctx *workflow.Context) (any, error) {
		ctx.Phase("process")
		items := make([]any, 10)
		for i := range items {
			items[i] = i
		}

		results, _ := ctx.Pipeline(items,
			// Stage 1: mark entry
			func(prev any, item any, index int) (any, error) {
				stageProgress.Store(fmt.Sprintf("item_%d_stage1", index), time.Now())
				time.Sleep(10 * time.Millisecond)
				return fmt.Sprintf("s1_%d", item), nil
			},
			// Stage 2: transforms immediately after stage 1
			func(prev any, item any, index int) (any, error) {
				stageProgress.Store(fmt.Sprintf("item_%d_stage2", index), time.Now())
				return fmt.Sprintf("%s_s2", prev), nil
			},
			// Stage 3: final transform
			func(prev any, item any, index int) (any, error) {
				stageProgress.Store(fmt.Sprintf("item_%d_stage3", index), time.Now())
				return fmt.Sprintf("final_%s", prev), nil
			},
		)
		assert.Len(t, results, 10)
		// Verify all items reached all stages
		for i := 0; i < 10; i++ {
			_, ok1 := stageProgress.Load(fmt.Sprintf("item_%d_stage1", i))
			_, ok2 := stageProgress.Load(fmt.Sprintf("item_%d_stage2", i))
			_, ok3 := stageProgress.Load(fmt.Sprintf("item_%d_stage3", i))
			assert.True(t, ok1 && ok2 && ok3, "item %d did not complete all stages", i)
		}
		return results, nil
	})

	run, _ := eng.Start(context.Background(), "pipeline-flow", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)
}

// =============================================================================
// Scenario 04: Nested workflow invocation
// =============================================================================

func TestComprehensive_04_NestedWorkflows(t *testing.T) {
	eng, _ := newEngine(t)

	var callOrder []string
	var mu sync.Mutex

	eng.Register("leaf", func(ctx *workflow.Context) (any, error) {
		mu.Lock()
		callOrder = append(callOrder, "leaf")
		mu.Unlock()
		return "leaf-result", nil
	})

	eng.Register("middle", func(ctx *workflow.Context) (any, error) {
		mu.Lock()
		callOrder = append(callOrder, "middle-before")
		mu.Unlock()
		r, err := ctx.Workflow("leaf", nil)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		callOrder = append(callOrder, "middle-after")
		mu.Unlock()
		return map[string]any{"from_leaf": r}, nil
	})

	eng.Register("root", func(ctx *workflow.Context) (any, error) {
		mu.Lock()
		callOrder = append(callOrder, "root")
		mu.Unlock()
		r, err := ctx.Workflow("middle", nil)
		if err != nil {
			return nil, err
		}
		return r, nil
	})

	run, _ := eng.Start(context.Background(), "root", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	final, _ := eng.GetRun(run.ID)
	assert.Contains(t, final.ResultJSON, "leaf-result")

	mu.Lock()
	t.Logf("Call order: %v", callOrder)
	assert.Equal(t, "root", callOrder[0])
	assert.Equal(t, "middle-before", callOrder[1])
	assert.Equal(t, "leaf", callOrder[2])
	assert.Equal(t, "middle-after", callOrder[3])
	mu.Unlock()
}

// =============================================================================
// Scenario 05: Schema validation with structured output
// =============================================================================

func TestComprehensive_05_SchemaValidation(t *testing.T) {
	eng, _ := newEngine(t)

	eng.Register("schema-test", func(ctx *workflow.Context) (any, error) {
		schema := map[string]any{
			"type":     "object",
			"required": []any{"name", "count"},
			"properties": map[string]any{
				"name":  map[string]any{"type": "string"},
				"count": map[string]any{"type": "integer"},
			},
		}

		// Test: valid JSON matching schema
		parsed, err := workflow.ParseStructuredOutput(`{"name":"test","count":5}`, schema)
		if err != nil {
			return nil, fmt.Errorf("valid JSON should parse: %w", err)
		}
		m := parsed.(map[string]any)
		if m["name"] != "test" || m["count"] != float64(5) {
			return nil, fmt.Errorf("parsed values mismatch")
		}

		// Test: invalid JSON (missing required field)
		_, err = workflow.ParseStructuredOutput(`{"name":"test"}`, schema)
		if err == nil {
			return nil, fmt.Errorf("should fail on missing required field")
		}

		// Test: wrong type
		err = workflow.ValidateSchema(schema, map[string]any{"name": 123, "count": 5})
		if err == nil {
			return nil, fmt.Errorf("should fail on wrong type")
		}

		// Test: enum validation
		enumSchema := map[string]any{"type": "string", "enum": []any{"a", "b", "c"}}
		if err := workflow.ValidateSchema(enumSchema, "d"); err == nil {
			return nil, fmt.Errorf("should fail enum validation")
		}
		if err := workflow.ValidateSchema(enumSchema, "a"); err != nil {
			return nil, fmt.Errorf("should pass enum validation: %v", err)
		}

		// Test: nested objects
		nestedSchema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"inner": map[string]any{
					"type":       "object",
					"properties": map[string]any{"value": map[string]any{"type": "integer"}},
				},
			},
		}
		parsed, err = workflow.ParseStructuredOutput(`{"inner":{"value":42}}`, nestedSchema)
		if err != nil {
			return nil, fmt.Errorf("nested object should parse: %w", err)
		}

		// Test: array with items
		arraySchema := map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "integer"},
		}
		if err := workflow.ValidateSchema(arraySchema, []any{1.0, 2.0, 3.0}); err != nil {
			return nil, fmt.Errorf("array should validate: %w", err)
		}
		if err := workflow.ValidateSchema(arraySchema, []any{1.0, "bad"}); err == nil {
			return nil, fmt.Errorf("array with wrong type should fail")
		}

		// Test: additionalProperties: false
		strictSchema := map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           map[string]any{"allowed": map[string]any{"type": "string"}},
		}
		if err := workflow.ValidateSchema(strictSchema, map[string]any{"allowed": "ok", "extra": "nope"}); err == nil {
			return nil, fmt.Errorf("should reject additional property")
		}

		return map[string]any{"schema_tests": "all_passed"}, nil
	})

	run, _ := eng.Start(context.Background(), "schema-test", map[string]any{})
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	final, _ := eng.GetRun(run.ID)
	assert.Contains(t, final.ResultJSON, "all_passed")
}

// =============================================================================
// Scenario 06: Budget tracking and exhaustion
// =============================================================================

func TestComprehensive_06_BudgetTracking(t *testing.T) {
	eng, _ := newEngineWithBudget(t, 100)

	eng.Register("budget-test", func(ctx *workflow.Context) (any, error) {
		// Initial budget state
		if ctx.Budget.Total != 100 {
			return nil, fmt.Errorf("expected budget total 100, got %d", ctx.Budget.Total)
		}
		if ctx.Budget.Remaining() != 100 {
			return nil, fmt.Errorf("expected remaining 100, got %d", ctx.Budget.Remaining())
		}

		ctx.Budget.Spend(30)
		if ctx.Budget.Spent() != 30 {
			return nil, fmt.Errorf("expected spent 30, got %d", ctx.Budget.Spent())
		}
		if ctx.Budget.Remaining() != 70 {
			return nil, fmt.Errorf("expected remaining 70, got %d", ctx.Budget.Remaining())
		}

		ctx.Budget.Spend(50)
		if ctx.Budget.Remaining() != 20 {
			return nil, fmt.Errorf("expected remaining 20, got %d", ctx.Budget.Remaining())
		}

		// Overspend
		ctx.Budget.Spend(40)
		if ctx.Budget.Remaining() != 0 {
			return nil, fmt.Errorf("expected remaining 0 after overspend, got %d", ctx.Budget.Remaining())
		}

		// Clone shares parent
		child := ctx.Budget.Clone()
		child.Spend(10)
		if ctx.Budget.Spent() != 130 {
			return nil, fmt.Errorf("parent should reflect child spend: spent=%d", ctx.Budget.Spent())
		}

		return map[string]any{
			"spent":     ctx.Budget.Spent(),
			"remaining": ctx.Budget.Remaining(),
		}, nil
	})

	run, _ := eng.Start(context.Background(), "budget-test", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	final, _ := eng.GetRun(run.ID)
	assert.Contains(t, final.ResultJSON, `"spent":130`)
	assert.Contains(t, final.ResultJSON, `"remaining":0`)
}

// =============================================================================
// Scenario 07: Budget unlimited (total=0)
// =============================================================================

func TestComprehensive_07_BudgetUnlimited(t *testing.T) {
	eng, _ := newEngineWithBudget(t, 0) // 0 = unlimited

	eng.Register("unlimited-budget", func(ctx *workflow.Context) (any, error) {
		if ctx.Budget.Total != 0 {
			return nil, fmt.Errorf("expected total=0 for unlimited, got %d", ctx.Budget.Total)
		}
		rem := ctx.Budget.Remaining()
		// Remaining should be MaxInt-ish
		if rem < 1_000_000_000 {
			return nil, fmt.Errorf("unlimited budget should have huge remaining, got %d", rem)
		}
		ctx.Budget.Spend(999999)
		if ctx.Budget.Remaining() < 1_000_000_000 {
			return nil, fmt.Errorf("unlimited budget should stay huge after spending, got %d", ctx.Budget.Remaining())
		}
		return map[string]any{"unlimited": true}, nil
	})

	run, _ := eng.Start(context.Background(), "unlimited-budget", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	final, _ := eng.GetRun(run.ID)
	assert.Contains(t, final.ResultJSON, `"unlimited":true`)
}

// =============================================================================
// Scenario 08: Resume after failure
// =============================================================================

func TestComprehensive_08_ResumeAfterFailure(t *testing.T) {
	eng, mgr := newEngine(t)

	var attempts atomic.Int64

	eng.Register("resume-test", func(ctx *workflow.Context) (any, error) {
		attempt := attempts.Add(1)
		ctx.Log(fmt.Sprintf("attempt %d", attempt))

		if attempt == 1 {
			// First attempt: call agent then fail
			r, err := ctx.Agent("initial work", nil)
			if err != nil {
				return nil, err
			}
			ctx.Log(fmt.Sprintf("agent said: %s", r.Output))
			return nil, fmt.Errorf("simulated transient failure")
		}

		if attempt == 2 {
			// Second attempt: check that we have fresh args
			ctx.Log("resumed successfully")
			r, err := ctx.Agent("recovery work", nil)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"attempt": attempt,
				"output":  r.Output,
				"status":  "recovered",
			}, nil
		}

		return nil, fmt.Errorf("unexpected attempt %d", attempt)
	})

	// First run - should fail
	run, err := eng.Start(context.Background(), "resume-test", nil)
	require.NoError(t, err)
	waitForRun(t, eng, run.ID, workflow.RunStatusFailed)

	failed, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusFailed, failed.Status)
	assert.Contains(t, failed.Error, "transient failure")

	callsBefore := len(mgr.getCalls())
	assert.Equal(t, 1, callsBefore, "should have 1 agent call before resume")

	// Resume
	_, err = eng.Resume(context.Background(), run.ID, map[string]any{"resumed": true})
	require.NoError(t, err)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
	assert.Contains(t, final.ResultJSON, "recovered")
	assert.Contains(t, final.ResultJSON, "recovery work")

	// Should have 2 agent calls total
	assert.Equal(t, 2, len(mgr.getCalls()), "should have 2 agent calls after resume")
}

// =============================================================================
// Scenario 09: Multi-phase workflow with agents in each phase
// =============================================================================

func TestComprehensive_09_MultiPhaseWorkflow(t *testing.T) {
	eng, mgr := newEngine(t)

	eng.Register("multi-phase", func(ctx *workflow.Context) (any, error) {
		ctx.Phase("analyze")
		ctx.Log("analyzing requirements")
		r1, err := ctx.Agent("analyze the task", &workflow.AgentOpts{Phase: "analyze"})
		if err != nil {
			return nil, err
		}

		ctx.Phase("implement")
		ctx.Log("implementing solution")
		r2, err := ctx.Agent("implement the fix", &workflow.AgentOpts{Phase: "implement"})
		if err != nil {
			return nil, err
		}

		ctx.Phase("verify")
		ctx.Log("verifying implementation")
		r3, err := ctx.Agent("verify the fix", &workflow.AgentOpts{Phase: "verify"})
		if err != nil {
			return nil, err
		}

		return map[string]any{
			"analyze":     r1.Output,
			"implement":   r2.Output,
			"verify":      r3.Output,
			"phases_done": 3,
		}, nil
	})

	run, _ := eng.Start(context.Background(), "multi-phase", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	final, _ := eng.GetRun(run.ID)
	assert.Contains(t, final.ResultJSON, `"phases_done":3`)

	calls := mgr.getCalls()
	assert.Len(t, calls, 3)
	assert.Equal(t, "analyze the task", calls[0].Prompt)
	assert.Equal(t, "implement the fix", calls[1].Prompt)
	assert.Equal(t, "verify the fix", calls[2].Prompt)

	// Verify phase events
	events := collectEvents(t, eng, run.ID)
	phaseCount := 0
	for _, ev := range events {
		if ev.Type == workflow.EventWorkflowPhaseStarted {
			phaseCount++
		}
	}
	assert.GreaterOrEqual(t, phaseCount, 3, "should have at least 3 phase events")
}

// =============================================================================
// Scenario 10: Error propagation and handling
// =============================================================================

func TestComprehensive_10_ErrorPropagation(t *testing.T) {
	eng, mgr := newEngine(t)

	eng.Register("error-test", func(ctx *workflow.Context) (any, error) {
		// Test 1: Pipeline with some items erroring
		results, _ := ctx.Pipeline(
			[]any{1, 2, 3, 4, 5},
			func(prev any, item any, index int) (any, error) {
				if item.(int)%2 == 0 {
					return nil, fmt.Errorf("even numbers fail")
				}
				return item, nil
			},
			func(prev any, item any, index int) (any, error) {
				return fmt.Sprintf("ok_%v", prev), nil
			},
		)
		// Odd items pass, even items become nil
		assert.NotNil(t, results[0]) // item 1 is odd
		assert.Nil(t, results[1])    // item 2 is even
		assert.NotNil(t, results[2]) // item 3 is odd

		// Test 2: Parallel with errors becomes nil
		parResults, _ := ctx.Parallel([]func() (any, error){
			func() (any, error) { return "ok", nil },
			func() (any, error) { return nil, fmt.Errorf("failure") },
			func() (any, error) { panic("unexpected") },
		})
		assert.NotNil(t, parResults[0])
		assert.Nil(t, parResults[1])
		assert.Nil(t, parResults[2])

		// Test 3: Agent that fails is returned as error (not nil result)
		_, err := ctx.Agent("should work", nil)
		if err != nil {
			return nil, fmt.Errorf("agent should succeed: %w", err)
		}

		return map[string]any{"errors_handled": true}, nil
	})

	run, _ := eng.Start(context.Background(), "error-test", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	final, _ := eng.GetRun(run.ID)
	assert.Contains(t, final.ResultJSON, "errors_handled")

	// Should have 1 agent call (the successful one)
	assert.Len(t, mgr.getCalls(), 1)
}

// =============================================================================
// Scenario 11: Concurrency stress test
// =============================================================================

func TestComprehensive_11_ConcurrencyStress(t *testing.T) {
	eng, mgr := newEngine(t)
	mgr.delay = 5 * time.Millisecond // small delay to force interleaving

	eng.Register("stress-test", func(ctx *workflow.Context) (any, error) {
		ctx.Phase("stress")

		// Launch 20 parallel thunks that each spawn an agent
		thunks := make([]func() (any, error), 20)
		for i := range thunks {
			idx := i
			thunks[i] = func() (any, error) {
				r, err := ctx.Agent(fmt.Sprintf("task-%d", idx), &workflow.AgentOpts{
					Label: fmt.Sprintf("task-%d", idx),
				})
				if err != nil {
					return nil, err
				}
				return r.Output, nil
			}
		}
		results, _ := ctx.Parallel(thunks)

		// Count non-nil results
		successCount := 0
		for _, r := range results {
			if r != nil {
				successCount++
			}
		}
		return map[string]any{
			"total":   len(results),
			"success": successCount,
		}, nil
	})

	run, _ := eng.Start(context.Background(), "stress-test", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
	assert.Contains(t, final.ResultJSON, `"total":20`)
	assert.Contains(t, final.ResultJSON, `"success":20`)

	// All 20 agent calls should have been made
	assert.Len(t, mgr.getCalls(), 20)
}

// =============================================================================
// Scenario 12: Adversarial verify pattern (parallel find → pipeline verify)
// =============================================================================

func TestComprehensive_12_AdversarialVerify(t *testing.T) {
	eng, mgr := newEngine(t)

	eng.Register("adversarial", func(ctx *workflow.Context) (any, error) {
		ctx.Phase("Find")
		ctx.Log("searching for issues...")

		// Parallel: find issues from different angles
		findResults, _ := ctx.Parallel([]func() (any, error){
			func() (any, error) {
				r, err := ctx.Agent("find bugs", &workflow.AgentOpts{Label: "find:bugs", Phase: "Find"})
				if err != nil {
					return nil, err
				}
				return map[string]any{"source": "bugs", "finding": r.Output}, nil
			},
			func() (any, error) {
				r, err := ctx.Agent("find perf issues", &workflow.AgentOpts{Label: "find:perf", Phase: "Find"})
				if err != nil {
					return nil, err
				}
				return map[string]any{"source": "perf", "finding": r.Output}, nil
			},
			func() (any, error) {
				r, err := ctx.Agent("find security issues", &workflow.AgentOpts{Label: "find:security", Phase: "Find"})
				if err != nil {
					return nil, err
				}
				return map[string]any{"source": "security", "finding": r.Output}, nil
			},
		})

		// Filter non-nil findings
		var findings []any
		for _, r := range findResults {
			if r != nil {
				findings = append(findings, r)
			}
		}
		ctx.Log(fmt.Sprintf("found %d issues to verify", len(findings)))

		ctx.Phase("Verify")
		// Pipeline: each finding goes through verification stages
		verifiedResults, _ := ctx.Pipeline(
			findings,
			// Stage 1: confirm finding is real
			func(prev any, item any, index int) (any, error) {
				m := item.(map[string]any)
				r, err := ctx.Agent(
					fmt.Sprintf("verify: %s", m["finding"]),
					&workflow.AgentOpts{Label: fmt.Sprintf("verify:%s", m["source"]), Phase: "Verify"},
				)
				if err != nil {
					return nil, err
				}
				m["verified"] = true
				m["verification"] = r.Output
				return m, nil
			},
			// Stage 2: assess severity
			func(prev any, item any, index int) (any, error) {
				m := prev.(map[string]any)
				r, err := ctx.Agent(
					fmt.Sprintf("assess severity of: %s", m["finding"]),
					&workflow.AgentOpts{Label: fmt.Sprintf("severity:%s", m["source"]), Phase: "Verify"},
				)
				if err != nil {
					return nil, err
				}
				m["severity"] = r.Output
				return m, nil
			},
		)

		return map[string]any{
			"findings_found":    len(findings),
			"findings_verified": len(verifiedResults),
		}, nil
	})

	run, _ := eng.Start(context.Background(), "adversarial", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
	assert.Contains(t, final.ResultJSON, `"findings_found":3`)

	// 3 find agents + 3 verify agents + 3 severity agents = 9 total
	calls := mgr.getCalls()
	assert.Len(t, calls, 9)
}

// =============================================================================
// Scenario 13: Args propagation through nested workflows
// =============================================================================

func TestComprehensive_13_ArgsPropagation(t *testing.T) {
	eng, _ := newEngine(t)

	eng.Register("args-child", func(ctx *workflow.Context) (any, error) {
		m, ok := ctx.Args.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("child expected map args")
		}
		return map[string]any{
			"child_got_key":   m["key"],
			"child_got_extra": m["extra"],
		}, nil
	})

	eng.Register("args-parent", func(ctx *workflow.Context) (any, error) {
		m, ok := ctx.Args.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("parent expected map args")
		}
		// Pass modified args to child
		childResult, err := ctx.Workflow("args-child", map[string]any{
			"key":   m["key"],
			"extra": "added_by_parent",
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"parent_got": m["key"],
			"child":      childResult,
		}, nil
	})

	run, _ := eng.Start(context.Background(), "args-parent", map[string]any{"key": "original_value"})
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	final, _ := eng.GetRun(run.ID)
	assert.Contains(t, final.ResultJSON, "original_value")
	assert.Contains(t, final.ResultJSON, "added_by_parent")
}

// =============================================================================
// Scenario 14: Engine list and metadata
// =============================================================================

func TestComprehensive_14_EngineListAndMeta(t *testing.T) {
	eng, _ := newEngine(t)

	names := []string{"zebra", "alpha", "gamma", "beta"}
	for _, name := range names {
		n := name
		eng.Register(n, func(ctx *workflow.Context) (any, error) {
			return n, nil
		})
	}

	list := eng.List()
	require.Len(t, list, len(names))

	// Verify sorted order
	sorted := make([]string, len(list))
	for i, m := range list {
		sorted[i] = m.Name
	}
	assert.True(t, sort.StringsAreSorted(sorted), "workflow list should be sorted: %v", sorted)

	// Verify all names present
	for _, name := range names {
		found := false
		for _, m := range list {
			if m.Name == name {
				found = true
				break
			}
		}
		assert.True(t, found, "workflow %q should be in list", name)
	}
}

// =============================================================================
// Scenario 15: Pipeline with single item and single stage
// =============================================================================

func TestComprehensive_15_PipelineSingleItem(t *testing.T) {
	eng, _ := newEngine(t)

	eng.Register("pipeline-single", func(ctx *workflow.Context) (any, error) {
		results, _ := ctx.Pipeline(
			[]any{"solo"},
			func(prev any, item any, index int) (any, error) {
				return fmt.Sprintf("processed_%v", item), nil
			},
		)
		assert.Len(t, results, 1)
		assert.Equal(t, "processed_solo", results[0])
		return results, nil
	})

	run, _ := eng.Start(context.Background(), "pipeline-single", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	final, _ := eng.GetRun(run.ID)
	assert.Contains(t, final.ResultJSON, "processed_solo")
}

// =============================================================================
// Scenario 16: Event subscription during execution
// =============================================================================

func TestComprehensive_16_EventStreaming(t *testing.T) {
	eng, _ := newEngine(t)

	eng.Register("event-stream", func(ctx *workflow.Context) (any, error) {
		ctx.Phase("init")
		ctx.Log("before agent")
		r, err := ctx.Agent("do work", &workflow.AgentOpts{Label: "worker"})
		if err != nil {
			return nil, err
		}
		ctx.Log(fmt.Sprintf("after agent: %s", r.Output))
		ctx.Phase("cleanup")
		ctx.Log("done")
		return "ok", nil
	})

	// Subscribe BEFORE starting to catch all events
	run, _ := eng.Start(context.Background(), "event-stream", nil)

	// Wait for completion then check events
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	events := collectEvents(t, eng, run.ID)

	// Count event types
	eventCounts := map[workflow.EventType]int{}
	for _, ev := range events {
		eventCounts[ev.Type]++
	}

	assert.Greater(t, eventCounts[workflow.EventWorkflowStarted], 0, "should have started event")
	assert.Greater(t, eventCounts[workflow.EventWorkflowAgentStarted], 0, "should have agent started event")
	assert.Greater(t, eventCounts[workflow.EventWorkflowAgentCompleted], 0, "should have agent completed event")
	assert.Greater(t, eventCounts[workflow.EventWorkflowLog], 0, "should have log events")
	assert.Greater(t, eventCounts[workflow.EventWorkflowPhaseStarted], 0, "should have phase events")
	assert.Greater(t, eventCounts[workflow.EventWorkflowCompleted], 0, "should have completed event")

	t.Logf("Event counts: %v", eventCounts)
}

// =============================================================================
// Scenario 17: GetRun returns copy (no race)
// =============================================================================

func TestComprehensive_17_GetRunReturnsCopy(t *testing.T) {
	eng, _ := newEngine(t)

	eng.Register("copy-test", func(ctx *workflow.Context) (any, error) {
		r, err := ctx.Agent("work", nil)
		if err != nil {
			return nil, err
		}
		return r.Output, nil
	})

	run, _ := eng.Start(context.Background(), "copy-test", nil)

	// Rapidly call GetRun while the workflow is running
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := eng.GetRun(run.ID)
			if err == nil && r != nil {
				// Just accessing the fields should not race
				_ = r.Status
				_ = r.WorkflowName
				_ = r.ResultJSON
			}
		}()
	}
	wg.Wait()

	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)
}

// =============================================================================
// Scenario 18: Large pipeline with many items
// =============================================================================

func TestComprehensive_18_LargePipeline(t *testing.T) {
	eng, _ := newEngine(t)

	const itemCount = 50

	eng.Register("large-pipeline", func(ctx *workflow.Context) (any, error) {
		items := make([]any, itemCount)
		for i := range items {
			items[i] = i
		}

		results, _ := ctx.Pipeline(items,
			func(prev any, item any, index int) (any, error) {
				time.Sleep(1 * time.Millisecond)
				return item.(int) * 2, nil
			},
			func(prev any, item any, index int) (any, error) {
				return prev.(int) + 1, nil
			},
			func(prev any, item any, index int) (any, error) {
				return prev.(int) * 10, nil
			},
		)

		assert.Len(t, results, itemCount)
		// Verify first and last
		assert.Equal(t, 10, results[0])            // (0*2+1)*10 = 10
		assert.Equal(t, 990, results[itemCount-1]) // (49*2+1)*10 = 990
		return map[string]any{"count": len(results)}, nil
	})

	run, _ := eng.Start(context.Background(), "large-pipeline", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	final, _ := eng.GetRun(run.ID)
	assert.Contains(t, final.ResultJSON, fmt.Sprintf(`"count":%d`, itemCount))
}

// =============================================================================
// Scenario 19: Mixed parallel and pipeline composition
// =============================================================================

func TestComprehensive_19_MixedParallelPipeline(t *testing.T) {
	eng, mgr := newEngine(t)

	eng.Register("mixed", func(ctx *workflow.Context) (any, error) {
		ctx.Phase("fan-out")
		// Parallel: each dimension does its own pipeline
		compositeResults, _ := ctx.Parallel([]func() (any, error){
			func() (any, error) {
				// Pipeline for dimension A
				results, _ := ctx.Pipeline(
					[]any{"a1", "a2"},
					func(prev any, item any, index int) (any, error) {
						r, err := ctx.Agent(fmt.Sprintf("process-a-%v", item), &workflow.AgentOpts{Label: fmt.Sprintf("a-%d", index)})
						if err != nil {
							return nil, err
						}
						return r.Output, nil
					},
				)
				return results, nil
			},
			func() (any, error) {
				// Pipeline for dimension B
				results, _ := ctx.Pipeline(
					[]any{"b1", "b2"},
					func(prev any, item any, index int) (any, error) {
						r, err := ctx.Agent(fmt.Sprintf("process-b-%v", item), &workflow.AgentOpts{Label: fmt.Sprintf("b-%d", index)})
						if err != nil {
							return nil, err
						}
						return r.Output, nil
					},
				)
				return results, nil
			},
		})

		// Flatten results
		var allResults []string
		for _, r := range compositeResults {
			if r != nil {
				for _, item := range r.([]any) {
					if item != nil {
						allResults = append(allResults, item.(string))
					}
				}
			}
		}
		return map[string]any{
			"composite_count": len(compositeResults),
			"total_results":   len(allResults),
		}, nil
	})

	run, _ := eng.Start(context.Background(), "mixed", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	final, _ := eng.GetRun(run.ID)
	assert.Contains(t, final.ResultJSON, `"composite_count":2`)
	assert.Contains(t, final.ResultJSON, `"total_results":4`)

	// 2 pipelines × 2 items each = 4 agent calls
	assert.Len(t, mgr.getCalls(), 4)
}

// =============================================================================
// Scenario 20: Workflow with agent using all opts
// =============================================================================

func TestComprehensive_20_AgentAllOpts(t *testing.T) {
	eng, mgr := newEngine(t)

	eng.Register("all-opts", func(ctx *workflow.Context) (any, error) {
		r, err := ctx.Agent("complex task", &workflow.AgentOpts{
			Label:     "my-label",
			Phase:     "my-phase",
			Model:     "claude-opus-4-8",
			Isolation: "worktree",
			AgentType: "code-reviewer",
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"output": r.Output,
			"error":  r.Error,
		}, nil
	})

	run, _ := eng.Start(context.Background(), "all-opts", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	calls := mgr.getCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "complex task", calls[0].Prompt)
	assert.Equal(t, "claude-opus-4-8", calls[0].Model)
	assert.Equal(t, "worktree", calls[0].Isolation)
	assert.Equal(t, "code-reviewer", calls[0].AgentType)
}

// =============================================================================
// Scenario 21: Loop until dry (no new findings)
// =============================================================================

func TestComprehensive_21_LoopUntilDry(t *testing.T) {
	eng, mgr := newEngine(t)

	eng.Register("loop-dry", func(ctx *workflow.Context) (any, error) {
		ctx.Phase("search")
		var allFindings []string
		dryRounds := 0

		for dryRounds < 2 {
			ctx.Log(fmt.Sprintf("search round, dry=%d", dryRounds))
			r, err := ctx.Agent("search for issues", &workflow.AgentOpts{Label: "searcher"})
			if err != nil {
				return nil, err
			}

			// Simulate: first 3 rounds find something, then it goes dry
			finding := r.Output
			if len(allFindings) < 3 {
				allFindings = append(allFindings, finding)
				ctx.Log(fmt.Sprintf("found: %s", finding))
				dryRounds = 0
			} else {
				dryRounds++
				ctx.Log(fmt.Sprintf("dry round %d/2", dryRounds))
			}
		}

		return map[string]any{
			"findings": len(allFindings),
			"dry":      dryRounds >= 2,
		}, nil
	})

	run, _ := eng.Start(context.Background(), "loop-dry", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	final, _ := eng.GetRun(run.ID)
	assert.Contains(t, final.ResultJSON, `"dry":true`)

	// 3 finding rounds + 2 dry rounds = 5 agent calls
	calls := mgr.getCalls()
	assert.GreaterOrEqual(t, len(calls), 5)
}

// =============================================================================
// Scenario 22: Nested budget tracking across workflows
// =============================================================================

func TestComprehensive_22_NestedBudget(t *testing.T) {
	eng, _ := newEngineWithBudget(t, 200)

	eng.Register("budget-child", func(ctx *workflow.Context) (any, error) {
		initial := ctx.Budget.Remaining()
		ctx.Budget.Spend(30)
		return map[string]any{
			"child_initial": initial,
			"child_spent":   ctx.Budget.Spent(),
		}, nil
	})

	eng.Register("budget-parent", func(ctx *workflow.Context) (any, error) {
		ctx.Budget.Spend(50)
		parentSpentAfterOwn := ctx.Budget.Spent()

		childResult, err := ctx.Workflow("budget-child", nil)
		if err != nil {
			return nil, err
		}

		// Child's spending should be reflected in parent
		return map[string]any{
			"parent_spent_after_own":   parentSpentAfterOwn,
			"parent_spent_after_child": ctx.Budget.Spent(),
			"parent_remaining":         ctx.Budget.Remaining(),
			"child":                    childResult,
		}, nil
	})

	run, _ := eng.Start(context.Background(), "budget-parent", nil)
	waitForRun(t, eng, run.ID, workflow.RunStatusCompleted)

	final, _ := eng.GetRun(run.ID)
	assert.Contains(t, final.ResultJSON, `"parent_spent_after_child":80`) // 50 + 30 from child
	assert.Contains(t, final.ResultJSON, `"parent_remaining":120`)
}
