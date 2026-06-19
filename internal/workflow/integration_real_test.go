package workflow_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/provider/anthropic"
	"go-agent-harness/internal/workflow"
)

// =============================================================================
// Real Provider Setup (DeepSeek via Anthropic-compatible API)
// =============================================================================

func deepseekAPIKey() string {
	key := os.Getenv("DEEPSEEK_API_KEY")
	if key == "" {
		key = os.Getenv("ANTHROPIC_AUTH_TOKEN")
	}
	return key
}

func deepseekBaseURL() string {
	url := os.Getenv("ANTHROPIC_BASE_URL")
	if url == "" {
		url = "https://api.deepseek.com/anthropic"
	}
	return url
}

func newRealProvider(t *testing.T) harness.Provider {
	t.Helper()
	key := deepseekAPIKey()
	if key == "" {
		t.Skip("DEEPSEEK_API_KEY not set")
	}
	client, err := anthropic.NewClient(anthropic.Config{
		APIKey:  key,
		BaseURL: deepseekBaseURL(),
		Model:   "deepseek-chat",
	})
	require.NoError(t, err)
	return client
}

// realSubagentManager wraps a real provider to implement workflow.SubagentManager.
type realSubagentMgr struct {
	provider harness.Provider
	mu       sync.Mutex
	results  map[string]workflow.SubagentResult
	counter  atomic.Int64
}

func newRealSubagentMgr(provider harness.Provider) *realSubagentMgr {
	return &realSubagentMgr{
		provider: provider,
		results:  make(map[string]workflow.SubagentResult),
	}
}

func (m *realSubagentMgr) Create(ctx context.Context, req workflow.SubagentRequest) (workflow.SubagentResult, error) {
	result, err := m.provider.Complete(ctx, harness.CompletionRequest{
		Model: "deepseek-chat",
		Messages: []harness.Message{
			{Role: "user", Content: req.Prompt},
		},
	})
	if err != nil {
		return workflow.SubagentResult{}, err
	}

	id := fmt.Sprintf("agent_%d", m.counter.Add(1))
	r := workflow.SubagentResult{
		ID:     id,
		Status: "completed",
		Output: result.Content,
	}
	m.mu.Lock()
	m.results[id] = r
	m.mu.Unlock()
	return r, nil
}

func (m *realSubagentMgr) Get(_ context.Context, id string) (workflow.SubagentResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.results[id]
	if !ok {
		return workflow.SubagentResult{}, fmt.Errorf("agent %q not found", id)
	}
	return r, nil
}

// =============================================================================
// Integration Test Helpers
// =============================================================================

func newRealEngine(t *testing.T) (*workflow.Engine, *realSubagentMgr) {
	t.Helper()
	provider := newRealProvider(t)
	mgr := newRealSubagentMgr(provider)
	eng := workflow.NewEngine(workflow.EngineOptions{
		Subagents:      mgr,
		MaxConcurrency: 4,
	})
	return eng, mgr
}

// =============================================================================
// POC 1: Simple real inference agent call
// =============================================================================

func TestReal_01_SimpleAgentCall(t *testing.T) {
	eng, _ := newRealEngine(t)

	eng.Register("simple", func(ctx *workflow.Context) (any, error) {
		r, err := ctx.Agent("Reply with exactly the word 'HELLO' in uppercase and nothing else.", nil)
		if err != nil {
			return nil, err
		}
		return map[string]any{"response": r.Output}, nil
	})

	run, err := eng.Start(context.Background(), "simple", nil)
	require.NoError(t, err)

	// Wait for real inference (can take a few seconds)
	waitForWorkflow(t, eng, run.ID, 30*time.Second)

	final, err := eng.GetRun(run.ID)
	require.NoError(t, err)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
	assert.Contains(t, strings.ToUpper(final.ResultJSON), "HELLO")
}

// =============================================================================
// POC 2: Multi-turn agent conversation
// =============================================================================

func TestReal_02_MultiTurnAgent(t *testing.T) {
	eng, _ := newRealEngine(t)

	eng.Register("multi-turn", func(ctx *workflow.Context) (any, error) {
		// Turn 1: Ask for a list
		r1, err := ctx.Agent("List exactly 3 colors, one per line, no other text.", nil)
		if err != nil {
			return nil, err
		}
		colors := r1.Output

		// Turn 2: Count them
		r2, err := ctx.Agent(fmt.Sprintf(
			"Here is a list:\n%s\n\nReply with ONLY the number of items in this list.",
			colors,
		), nil)
		if err != nil {
			return nil, err
		}

		return map[string]any{
			"colors": colors,
			"count":  r2.Output,
		}, nil
	})

	run, _ := eng.Start(context.Background(), "multi-turn", nil)
	waitForWorkflow(t, eng, run.ID, 30*time.Second)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
	assert.Contains(t, final.ResultJSON, "3")
}

// =============================================================================
// POC 3: Parallel real inference calls
// =============================================================================

func TestReal_03_ParallelInference(t *testing.T) {
	eng, _ := newRealEngine(t)

	eng.Register("parallel", func(ctx *workflow.Context) (any, error) {
		results, _ := ctx.Parallel([]func() (any, error){
			func() (any, error) {
				r, err := ctx.Agent("Reply with only the word 'ALPHA'.", nil)
				if err != nil {
					return nil, err
				}
				return r.Output, nil
			},
			func() (any, error) {
				r, err := ctx.Agent("Reply with only the word 'BETA'.", nil)
				if err != nil {
					return nil, err
				}
				return r.Output, nil
			},
			func() (any, error) {
				r, err := ctx.Agent("Reply with only the word 'GAMMA'.", nil)
				if err != nil {
					return nil, err
				}
				return r.Output, nil
			},
		})
		return map[string]any{"results": results}, nil
	})

	run, _ := eng.Start(context.Background(), "parallel", nil)
	waitForWorkflow(t, eng, run.ID, 30*time.Second)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
	assert.Contains(t, strings.ToUpper(final.ResultJSON), "ALPHA")
	assert.Contains(t, strings.ToUpper(final.ResultJSON), "BETA")
	assert.Contains(t, strings.ToUpper(final.ResultJSON), "GAMMA")
}

// =============================================================================
// POC 4: Pipeline with real agents in stages
// =============================================================================

func TestReal_04_PipelineStages(t *testing.T) {
	eng, _ := newRealEngine(t)

	type result struct{ Output string }

	eng.Register("pipeline", func(ctx *workflow.Context) (any, error) {
		items := []any{"cat", "dog"}

		results, _ := ctx.Pipeline(items,
			func(prev any, item any, index int) (any, error) {
				r, err := ctx.Agent(
					fmt.Sprintf("Translate the word '%s' to Spanish. Reply with ONLY the Spanish word, no other text.", item),
					&workflow.AgentOpts{Label: fmt.Sprintf("translate-%v", item)},
				)
				if err != nil {
					return nil, err
				}
				return r.Output, nil
			},
			func(prev any, item any, index int) (any, error) {
				prevStr, _ := prev.(string)
				r, err := ctx.Agent(
					fmt.Sprintf("Convert '%s' to UPPERCASE. Reply with ONLY the uppercase word.", prevStr),
					&workflow.AgentOpts{Label: fmt.Sprintf("upper-%v", prevStr)},
				)
				if err != nil {
					return nil, err
				}
				return r.Output, nil
			},
		)
		return map[string]any{"results": results}, nil
	})

	run, _ := eng.Start(context.Background(), "pipeline", nil)
	waitForWorkflow(t, eng, run.ID, 30*time.Second)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
	// Should have Spanish translations in uppercase
	resultStr := strings.ToUpper(final.ResultJSON)
	assert.True(t, strings.Contains(resultStr, "GATO") || strings.Contains(resultStr, "PERRO"))
}

// =============================================================================
// POC 5: Schema-validated structured output
// =============================================================================

func TestReal_05_SchemaValidatedOutput(t *testing.T) {
	eng, _ := newRealEngine(t)

	schema := map[string]any{
		"type":     "object",
		"required": []any{"name", "age", "city"},
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
			"age":  map[string]any{"type": "integer"},
			"city": map[string]any{"type": "string"},
		},
	}

	eng.Register("schema", func(ctx *workflow.Context) (any, error) {
		r, err := ctx.Agent(
			`Create a JSON object with fields: name (string), age (integer), city (string).
Use made-up values. Reply with ONLY valid JSON, no markdown, no other text.`,
			&workflow.AgentOpts{Schema: schema},
		)
		if err != nil {
			return nil, err
		}
		return r.Schema, nil
	})

	run, _ := eng.Start(context.Background(), "schema", nil)
	waitForWorkflow(t, eng, run.ID, 30*time.Second)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
}

// =============================================================================
// POC 6: Nested workflow execution
// =============================================================================

func TestReal_06_NestedWorkflows(t *testing.T) {
	eng, _ := newRealEngine(t)

	eng.Register("inner", func(ctx *workflow.Context) (any, error) {
		r, err := ctx.Agent("Reply with ONLY the number 42.", nil)
		if err != nil {
			return nil, err
		}
		return r.Output, nil
	})

	eng.Register("outer", func(ctx *workflow.Context) (any, error) {
		innerResult, err := ctx.Workflow("inner", nil)
		if err != nil {
			return nil, err
		}
		return map[string]any{"inner_output": innerResult}, nil
	})

	run, _ := eng.Start(context.Background(), "outer", nil)
	waitForWorkflow(t, eng, run.ID, 30*time.Second)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
	assert.Contains(t, final.ResultJSON, "42")
}

// =============================================================================
// POC 7: Budget tracking with real usage
// =============================================================================

func TestReal_07_BudgetTracking(t *testing.T) {
	eng, _ := newRealEngine(t)

	eng.Register("budget", func(ctx *workflow.Context) (any, error) {
		initial := ctx.Budget.Remaining()

		ctx.Budget.Spend(100)
		afterSpend := ctx.Budget.Remaining()

		r, err := ctx.Agent("Reply with only the word 'OK'.", nil)
		if err != nil {
			return nil, err
		}

		return map[string]any{
			"initial":     initial,
			"after_spend": afterSpend,
			"remaining":   ctx.Budget.Remaining(),
			"spent":       ctx.Budget.Spent(),
			"got":         r.Output,
		}, nil
	})

	run, _ := eng.Start(context.Background(), "budget", nil)
	waitForWorkflow(t, eng, run.ID, 30*time.Second)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
	assert.Contains(t, final.ResultJSON, `"spent":100`)
}

// =============================================================================
// POC 8: Multi-phase workflow
// =============================================================================

func TestReal_08_MultiPhaseWorkflow(t *testing.T) {
	eng, _ := newRealEngine(t)

	eng.Register("phases", func(ctx *workflow.Context) (any, error) {
		ctx.Phase("analyze")
		ctx.Log("Starting analysis phase")
		r1, err := ctx.Agent("What is 2+2? Reply with only the number.", &workflow.AgentOpts{Phase: "analyze"})
		if err != nil {
			return nil, err
		}

		ctx.Phase("verify")
		ctx.Log("Starting verification phase")
		r2, err := ctx.Agent("Is the answer 4 correct? Reply with only YES or NO.", &workflow.AgentOpts{Phase: "verify"})
		if err != nil {
			return nil, err
		}

		return map[string]any{
			"answer":   r1.Output,
			"verified": r2.Output,
		}, nil
	})

	run, _ := eng.Start(context.Background(), "phases", nil)
	waitForWorkflow(t, eng, run.ID, 30*time.Second)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
	assert.Contains(t, final.ResultJSON, "4")
}

// =============================================================================
// POC 9: Error handling and recovery
// =============================================================================

func TestReal_09_ErrorHandling(t *testing.T) {
	eng, _ := newRealEngine(t)

	eng.Register("errors", func(ctx *workflow.Context) (any, error) {
		// Test: pipeline with items where one "fails"
		results, _ := ctx.Pipeline(
			[]any{"hello", "world"},
			func(prev any, item any, index int) (any, error) {
				r, err := ctx.Agent(
					fmt.Sprintf("If the word is '%s', reply with its length as a number. Otherwise reply with ERROR.", item),
					nil,
				)
				if err != nil {
					return nil, err
				}
				return r.Output, nil
			},
		)
		// Both should succeed since both are simple words
		assert.Len(t, results, 2)
		return map[string]any{"result_count": len(results)}, nil
	})

	run, _ := eng.Start(context.Background(), "errors", nil)
	waitForWorkflow(t, eng, run.ID, 30*time.Second)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
}

// =============================================================================
// POC 10: Event streaming during real execution
// =============================================================================

func TestReal_10_EventStreaming(t *testing.T) {
	eng, _ := newRealEngine(t)

	eng.Register("events", func(ctx *workflow.Context) (any, error) {
		ctx.Log("about to call agent")
		r, err := ctx.Agent("Say exactly 'EVENT_TEST_PASSED'.", &workflow.AgentOpts{Label: "test-agent"})
		if err != nil {
			return nil, err
		}
		ctx.Log(fmt.Sprintf("agent said: %s", r.Output))
		return r.Output, nil
	})

	run, _ := eng.Start(context.Background(), "events", nil)

	// Subscribe immediately to capture all events
	history, live, cancel, err := eng.Subscribe(run.ID)
	require.NoError(t, err)
	defer cancel()

	// Collect events
	var allEvents []workflow.Event
	allEvents = append(allEvents, history...)
	timeout := time.After(30 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-live:
			if !ok {
				break loop
			}
			allEvents = append(allEvents, ev)
			if ev.Type == workflow.EventWorkflowCompleted || ev.Type == workflow.EventWorkflowFailed {
				break loop
			}
		case <-timeout:
			break loop
		}
	}

	// Verify event types
	foundTypes := map[workflow.EventType]bool{}
	for _, ev := range allEvents {
		foundTypes[ev.Type] = true
	}
	assert.True(t, foundTypes[workflow.EventWorkflowStarted], "should have started event")
	assert.True(t, foundTypes[workflow.EventWorkflowAgentStarted], "should have agent started event")
	assert.True(t, foundTypes[workflow.EventWorkflowAgentCompleted], "should have agent completed event")
	assert.True(t, foundTypes[workflow.EventWorkflowCompleted], "should have completed event")
	assert.True(t, foundTypes[workflow.EventWorkflowLog], "should have log events")
}

// =============================================================================
// POC 11: Resume after simulation failure
// =============================================================================

func TestReal_11_ResumeWorkflow(t *testing.T) {
	eng, _ := newRealEngine(t)

	var callCount atomic.Int64

	eng.Register("resume-test", func(ctx *workflow.Context) (any, error) {
		count := callCount.Add(1)
		if count == 1 {
			return nil, fmt.Errorf("simulated transient failure on attempt 1")
		}
		// Second attempt: do real work
		r, err := ctx.Agent("Reply with only the word 'RECOVERED'.", nil)
		if err != nil {
			return nil, err
		}
		return map[string]any{"result": r.Output, "attempt": count}, nil
	})

	// First run fails
	run, _ := eng.Start(context.Background(), "resume-test", nil)
	waitForWorkflow(t, eng, run.ID, 30*time.Second)
	failed, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusFailed, failed.Status)

	// Resume
	_, err := eng.Resume(context.Background(), run.ID, map[string]any{"retry": true})
	require.NoError(t, err)
	waitForWorkflow(t, eng, run.ID, 30*time.Second)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
	assert.Contains(t, strings.ToUpper(final.ResultJSON), "RECOVERED")
}

// =============================================================================
// POC 12: Concurrent agent fan-out
// =============================================================================

func TestReal_12_ConcurrentFanOut(t *testing.T) {
	eng, _ := newRealEngine(t)

	eng.Register("fanout", func(ctx *workflow.Context) (any, error) {
		tasks := make([]func() (any, error), 5)
		questions := []string{
			"What is 1+1? Reply with only the number.",
			"What is 2+2? Reply with only the number.",
			"What is 3+3? Reply with only the number.",
			"What is 4+4? Reply with only the number.",
			"What is 5+5? Reply with only the number.",
		}
		for i, q := range questions {
			query := q
			tasks[i] = func() (any, error) {
				r, err := ctx.Agent(query, nil)
				if err != nil {
					return nil, err
				}
				return r.Output, nil
			}
		}
		results, _ := ctx.Parallel(tasks)
		return map[string]any{"answers": results, "count": len(results)}, nil
	})

	run, _ := eng.Start(context.Background(), "fanout", nil)
	waitForWorkflow(t, eng, run.ID, 30*time.Second)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
	assert.Contains(t, final.ResultJSON, `"count":5`)
}

// =============================================================================
// POC 13: Code generation workflow
// =============================================================================

func TestReal_13_CodeGeneration(t *testing.T) {
	eng, _ := newRealEngine(t)

	eng.Register("code-gen", func(ctx *workflow.Context) (any, error) {
		r, err := ctx.Agent(
			"Write a Go function named 'Add' that takes two integers and returns their sum. "+
				"Reply with ONLY the function code, no explanation, no markdown fences.",
			nil,
		)
		if err != nil {
			return nil, err
		}
		code := r.Output
		// Verify it contains expected Go syntax
		hasFunc := strings.Contains(code, "func") && strings.Contains(code, "Add")
		hasReturn := strings.Contains(code, "return") || strings.Contains(code, "int")
		return map[string]any{
			"code":          code,
			"has_func":      hasFunc,
			"has_return":    hasReturn,
			"looks_like_go": hasFunc && hasReturn,
		}, nil
	})

	run, _ := eng.Start(context.Background(), "code-gen", nil)
	waitForWorkflow(t, eng, run.ID, 30*time.Second)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
	assert.Contains(t, final.ResultJSON, `"looks_like_go":true`)
}

// =============================================================================
// POC 14: Chain-of-thought reasoning
// =============================================================================

func TestReal_14_ChainOfThought(t *testing.T) {
	eng, _ := newRealEngine(t)

	eng.Register("cot", func(ctx *workflow.Context) (any, error) {
		// Step 1: Ask model to reason step by step
		r1, err := ctx.Agent(
			"If a train travels at 60 mph for 2.5 hours, how far does it go? "+
				"Think step by step, then end with 'ANSWER: <number> miles'.",
			nil,
		)
		if err != nil {
			return nil, err
		}

		// Step 2: Verify the answer
		r2, err := ctx.Agent(
			fmt.Sprintf("Here is a reasoning:\n%s\n\nExtract just the final numeric answer (the number after ANSWER:). Reply with ONLY the number.", r1.Output),
			nil,
		)
		if err != nil {
			return nil, err
		}

		return map[string]any{
			"reasoning": r1.Output,
			"answer":    r2.Output,
		}, nil
	})

	run, _ := eng.Start(context.Background(), "cot", nil)
	waitForWorkflow(t, eng, run.ID, 30*time.Second)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
	assert.Contains(t, final.ResultJSON, "150")
}

// =============================================================================
// POC 15: Classification task
// =============================================================================

func TestReal_15_ClassificationTask(t *testing.T) {
	eng, _ := newRealEngine(t)

	eng.Register("classify", func(ctx *workflow.Context) (any, error) {
		r, err := ctx.Agent(
			"Classify this text as positive, negative, or neutral: 'I absolutely loved the new update, it made everything faster and smoother!'\n"+
				"Reply with ONLY one word: positive, negative, or neutral.",
			nil,
		)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"sentiment": strings.TrimSpace(r.Output),
		}, nil
	})

	run, _ := eng.Start(context.Background(), "classify", nil)
	waitForWorkflow(t, eng, run.ID, 30*time.Second)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
	assert.Contains(t, strings.ToLower(final.ResultJSON), "positive")
}

// =============================================================================
// POC 16: Summarization pipeline
// =============================================================================

func TestReal_16_SummarizationPipeline(t *testing.T) {
	eng, _ := newRealEngine(t)

	longText := "Artificial intelligence has transformed the technology landscape. " +
		"Machine learning models can now process natural language, recognize images, " +
		"and make predictions with remarkable accuracy. Deep learning, a subset of ML, " +
		"uses neural networks with many layers to learn hierarchical representations. " +
		"These advances have enabled applications like autonomous vehicles, medical diagnosis, " +
		"and real-time language translation. However, challenges remain in areas like bias, " +
		"interpretability, and energy consumption."

	eng.Register("summarize", func(ctx *workflow.Context) (any, error) {
		results, _ := ctx.Pipeline(
			[]any{longText},
			func(prev any, item any, index int) (any, error) {
				text := item.(string)
				r, err := ctx.Agent(
					fmt.Sprintf("Summarize this text in EXACTLY one sentence:\n\n%s", text),
					&workflow.AgentOpts{Label: "summarize"},
				)
				if err != nil {
					return nil, err
				}
				return r.Output, nil
			},
			func(prev any, item any, index int) (any, error) {
				summary := prev.(string)
				r, err := ctx.Agent(
					fmt.Sprintf("Extract the single most important keyword from this summary. Reply with ONLY the keyword:\n\n%s", summary),
					&workflow.AgentOpts{Label: "keyword"},
				)
				if err != nil {
					return nil, err
				}
				return map[string]any{"summary": summary, "keyword": r.Output}, nil
			},
		)
		return results[0], nil
	})

	run, _ := eng.Start(context.Background(), "summarize", nil)
	waitForWorkflow(t, eng, run.ID, 30*time.Second)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
}

// =============================================================================
// POC 17: Adversarial verify pattern
// =============================================================================

func TestReal_17_AdversarialVerify(t *testing.T) {
	eng, _ := newRealEngine(t)

	eng.Register("verify", func(ctx *workflow.Context) (any, error) {
		ctx.Phase("generate")
		// Generate a claim
		r1, err := ctx.Agent(
			"State a simple mathematical fact as a single sentence. "+
				"Make it a claim that can be verified true or false. "+
				"Start with 'CLAIM: '",
			&workflow.AgentOpts{Phase: "generate"},
		)
		if err != nil {
			return nil, err
		}

		ctx.Phase("verify")
		// Verify the claim
		r2, err := ctx.Agent(
			fmt.Sprintf("Here is a claim:\n%s\n\nIs this claim TRUE or FALSE? Reply with ONLY 'TRUE' or 'FALSE'.", r1.Output),
			&workflow.AgentOpts{Phase: "verify"},
		)
		if err != nil {
			return nil, err
		}

		return map[string]any{
			"claim":  r1.Output,
			"verdict": r2.Output,
		}, nil
	})

	run, _ := eng.Start(context.Background(), "verify", nil)
	waitForWorkflow(t, eng, run.ID, 30*time.Second)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
	// The model should give a verdict (TRUE or FALSE) — accept either
	resultUpper := strings.ToUpper(final.ResultJSON)
	assert.True(t,
		strings.Contains(resultUpper, "TRUE") || strings.Contains(resultUpper, "FALSE"),
		"should contain a boolean verdict")
}

// =============================================================================
// POC 18: Translation task
// =============================================================================

func TestReal_18_TranslationTask(t *testing.T) {
	eng, _ := newRealEngine(t)

	eng.Register("translate", func(ctx *workflow.Context) (any, error) {
		r, err := ctx.Agent(
			"Translate 'Hello, how are you?' to French. Reply with ONLY the French translation.",
			nil,
		)
		if err != nil {
			return nil, err
		}
		return map[string]any{"french": r.Output}, nil
	})

	run, _ := eng.Start(context.Background(), "translate", nil)
	waitForWorkflow(t, eng, run.ID, 30*time.Second)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
	// "Bonjour" or "Salut" should appear (French greeting)
	resultUpper := strings.ToUpper(final.ResultJSON)
	assert.True(t,
		strings.Contains(resultUpper, "BONJOUR") ||
			strings.Contains(resultUpper, "SALUT") ||
			strings.Contains(resultUpper, "COMMENT"),
		"should contain French greeting")
}

// =============================================================================
// POC 19: Workflow save/resume persistence
// =============================================================================

func TestReal_19_WorkflowSaveResume(t *testing.T) {
	eng, _ := newRealEngine(t)

	var state atomic.Value
	state.Store(map[string]any{"step": 0})

	eng.Register("save-resume", func(ctx *workflow.Context) (any, error) {
		s := state.Load().(map[string]any)
		var step int
		switch v := s["step"].(type) {
		case float64:
			step = int(v)
		case int:
			step = v
		default:
			step = 0
		}

		ctx.Log(fmt.Sprintf("Executing step %d", step))

		if step == 0 {
			// Step 0: Do real work
			r, err := ctx.Agent("Reply with only 'STEP_0_DONE'.", nil)
			if err != nil {
				return nil, err
			}
			state.Store(map[string]any{"step": 1, "step0_result": r.Output})
			return nil, fmt.Errorf("checkpoint after step 0") // deliberate pause
		}

		if step == 1 {
			// Step 1: Continue with saved state
			r, err := ctx.Agent("Reply with only 'STEP_1_DONE'.", nil)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"step0": s["step0_result"],
				"step1": r.Output,
				"all_done": true,
			}, nil
		}

		return nil, fmt.Errorf("unknown step: %d", step)
	})

	// First run
	run, _ := eng.Start(context.Background(), "save-resume", nil)
	waitForWorkflow(t, eng, run.ID, 30*time.Second)
	failed, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusFailed, failed.Status)
	assert.Contains(t, failed.Error, "checkpoint")

	// Resume with saved state
	_, err := eng.Resume(context.Background(), run.ID, nil)
	require.NoError(t, err)
	waitForWorkflow(t, eng, run.ID, 30*time.Second)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
	assert.Contains(t, final.ResultJSON, `"all_done":true`)
}

// =============================================================================
// POC 20: Full CI/CD simulation
// =============================================================================

func TestReal_20_FullCICDSimulation(t *testing.T) {
	eng, _ := newRealEngine(t)

	eng.Register("cicd", func(ctx *workflow.Context) (any, error) {
		ctx.Phase("build")
		ctx.Log("Building application...")
		r1, err := ctx.Agent(
			"Simulate a build step. Reply with: BUILD_SUCCESS:{random 3-digit number}",
			&workflow.AgentOpts{Phase: "build"},
		)
		if err != nil {
			return nil, err
		}

		ctx.Phase("test")
		ctx.Log("Running tests...")
		r2, err := ctx.Agent(
			"Simulate a test run. Reply with: TESTS_PASSED:{number between 10 and 50}",
			&workflow.AgentOpts{Phase: "test"},
		)
		if err != nil {
			return nil, err
		}

		ctx.Phase("deploy")
		ctx.Log("Deploying...")
		r3, err := ctx.Agent(
			"Simulate a deployment. Reply with: DEPLOY_SUCCESS:{any version string like v1.2.3}",
			&workflow.AgentOpts{Phase: "deploy"},
		)
		if err != nil {
			return nil, err
		}

		return map[string]any{
			"build":  r1.Output,
			"test":   r2.Output,
			"deploy": r3.Output,
			"pipeline_complete": true,
		}, nil
	})

	run, _ := eng.Start(context.Background(), "cicd", nil)
	waitForWorkflow(t, eng, run.ID, 30*time.Second)

	final, _ := eng.GetRun(run.ID)
	assert.Equal(t, workflow.RunStatusCompleted, final.Status)
	assert.Contains(t, final.ResultJSON, `"pipeline_complete":true`)
	assert.Contains(t, strings.ToUpper(final.ResultJSON), "BUILD_SUCCESS")
	assert.Contains(t, strings.ToUpper(final.ResultJSON), "TESTS_PASSED")
	assert.Contains(t, strings.ToUpper(final.ResultJSON), "DEPLOY_SUCCESS")
}

// =============================================================================
// Helper: wait for workflow completion with timeout
// =============================================================================

func waitForWorkflow(t *testing.T, eng *workflow.Engine, runID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		run, err := eng.GetRun(runID)
		if err == nil && (run.Status == workflow.RunStatusCompleted || run.Status == workflow.RunStatusFailed) {
			t.Logf("Workflow %s status: %s (after %v)", runID, run.Status, time.Since(deadline.Add(-timeout)))
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("workflow %s did not complete within %v", runID, timeout)
}
