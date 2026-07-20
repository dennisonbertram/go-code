package harness

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// applyConfigRecordingProvider records the model of every completion request
// and serves scripted results by call index. Call 0 can optionally block until
// released so tests can apply config while a run is in flight.
type applyConfigRecordingProvider struct {
	mu      sync.Mutex
	results []CompletionResult
	calls   int
	models  []string

	blockFirstCall bool
	blocked        chan struct{}
	release        chan struct{}
}

func (p *applyConfigRecordingProvider) Complete(_ context.Context, req CompletionRequest) (CompletionResult, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.models = append(p.models, req.Model)
	var result CompletionResult
	if idx < len(p.results) {
		result = p.results[idx]
	} else {
		result = CompletionResult{Content: "done"}
	}
	block := p.blockFirstCall && idx == 0
	blockedCh := p.blocked
	releaseCh := p.release
	p.mu.Unlock()

	if block {
		close(blockedCh)
		<-releaseCh
	}
	return result, nil
}

func (p *applyConfigRecordingProvider) recordedModels() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.models...)
}

// waitRunTerminal polls until the run reaches a terminal status or the deadline passes.
func waitRunTerminal(t *testing.T, runner *Runner, runID string) Run {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		run, ok := runner.GetRun(runID)
		if !ok {
			t.Fatalf("run %s disappeared", runID)
		}
		switch run.Status {
		case RunStatusCompleted, RunStatusFailed, RunStatusCancelled:
			return run
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach a terminal status in time", runID)
	return Run{}
}

// TestApplyConfig_NewRunsObserveNewModel verifies that a run started after
// ApplyConfig resolves the newly applied DefaultModel, while a run started
// before used the original one.
func TestApplyConfig_NewRunsObserveNewModel(t *testing.T) {
	t.Parallel()

	provider := &applyConfigRecordingProvider{
		results: []CompletionResult{{Content: "done"}, {Content: "done"}},
	}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{DefaultModel: "model-a"})

	before, err := runner.StartRun(RunRequest{Prompt: "first"})
	if err != nil {
		t.Fatalf("StartRun before apply: %v", err)
	}
	if run := waitRunTerminal(t, runner, before.ID); run.Status != RunStatusCompleted {
		t.Fatalf("pre-apply run status: got %s, want completed (%s)", run.Status, run.Error)
	}

	runner.ApplyConfig(RunnerConfig{DefaultModel: "model-b"})

	after, err := runner.StartRun(RunRequest{Prompt: "second"})
	if err != nil {
		t.Fatalf("StartRun after apply: %v", err)
	}
	if run := waitRunTerminal(t, runner, after.ID); run.Status != RunStatusCompleted {
		t.Fatalf("post-apply run status: got %s, want completed (%s)", run.Status, run.Error)
	}

	models := provider.recordedModels()
	if len(models) != 2 {
		t.Fatalf("provider calls: got %d, want 2", len(models))
	}
	if models[0] != "model-a" {
		t.Errorf("pre-apply run model: got %q, want %q", models[0], "model-a")
	}
	if models[1] != "model-b" {
		t.Errorf("post-apply run model: got %q, want %q", models[1], "model-b")
	}
}

// TestApplyConfig_NewRunsObserveNewMaxSteps verifies that the applied
// MaxSteps caps runs started after the apply.
func TestApplyConfig_NewRunsObserveNewMaxSteps(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	registerNoopTool(t, registry, "loop_tool")

	// Provider always returns a tool call, so the run only stops at the step limit.
	provider := &applyConfigRecordingProvider{
		results: []CompletionResult{
			{ToolCalls: []ToolCall{{ID: "c1", Name: "loop_tool", Arguments: `{}`}}},
			{ToolCalls: []ToolCall{{ID: "c2", Name: "loop_tool", Arguments: `{}`}}},
			{ToolCalls: []ToolCall{{ID: "c3", Name: "loop_tool", Arguments: `{}`}}},
			{ToolCalls: []ToolCall{{ID: "c4", Name: "loop_tool", Arguments: `{}`}}},
			{ToolCalls: []ToolCall{{ID: "c5", Name: "loop_tool", Arguments: `{}`}}},
		},
	}
	runner := NewRunner(provider, registry, RunnerConfig{DefaultModel: "test", MaxSteps: 10})

	runner.ApplyConfig(RunnerConfig{DefaultModel: "test", MaxSteps: 2})

	run, err := runner.StartRun(RunRequest{Prompt: "loop"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	final := waitRunTerminal(t, runner, run.ID)
	if final.Status != RunStatusFailed {
		t.Fatalf("run status: got %s, want failed (max steps)", final.Status)
	}
	if got := len(provider.recordedModels()); got != 2 {
		t.Errorf("provider calls: got %d, want 2 (MaxSteps=2 from applied config)", got)
	}
}

// TestApplyConfig_InFlightRunKeepsSnapshot is the core isolation contract:
// a run that started before ApplyConfig keeps its original config snapshot
// for its remaining steps, while runs started after observe the new config.
// The observable probes are the per-step auto-compaction check (flipped on by
// the applied config) and the model passed to the provider.
func TestApplyConfig_InFlightRunKeepsSnapshot(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	registerNoopTool(t, registry, "step_tool")

	provider := &applyConfigRecordingProvider{
		blockFirstCall: true,
		blocked:        make(chan struct{}),
		release:        make(chan struct{}),
		results: []CompletionResult{
			{ToolCalls: []ToolCall{{ID: "c1", Name: "step_tool", Arguments: `{}`}}},
			{Content: "done"},
			{Content: "done"},
		},
	}
	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel:       "model-a",
		MaxSteps:           10,
		AutoCompactEnabled: false,
	})

	inflight, err := runner.StartRun(RunRequest{Prompt: strings.Repeat("x", 400)})
	if err != nil {
		t.Fatalf("StartRun inflight: %v", err)
	}
	<-provider.blocked // first LLM call of the in-flight run is in progress

	// Apply a config that turns auto-compaction on with a hair trigger
	// (400-rune prompt ~= 100 tokens >> 10-token window) and swaps the model.
	runner.ApplyConfig(RunnerConfig{
		DefaultModel:         "model-b",
		MaxSteps:             10,
		AutoCompactEnabled:   true,
		ModelContextWindow:   10,
		AutoCompactThreshold: 0.01,
		AutoCompactKeepLast:  2,
		AutoCompactMode:      "strip",
	})

	close(provider.release) // let the in-flight run finish its remaining steps
	if run := waitRunTerminal(t, runner, inflight.ID); run.Status != RunStatusCompleted {
		t.Fatalf("in-flight run status: got %s, want completed (%s)", run.Status, run.Error)
	}

	// The in-flight run must not compact on its post-apply steps and must
	// keep using the model resolved from the original config.
	for _, e := range runner.getEvents(inflight.ID) {
		if e.Type == EventAutoCompactStarted {
			t.Error("in-flight run emitted auto_compact.started after ApplyConfig; snapshot isolation violated")
		}
	}
	for i, m := range provider.recordedModels() {
		if m != "model-a" {
			t.Errorf("in-flight run LLM call %d model: got %q, want %q", i, m, "model-a")
		}
	}

	// A run started after ApplyConfig uses the new model and compacts.
	after, err := runner.StartRun(RunRequest{Prompt: strings.Repeat("x", 400)})
	if err != nil {
		t.Fatalf("StartRun after apply: %v", err)
	}
	if run := waitRunTerminal(t, runner, after.ID); run.Status != RunStatusCompleted {
		t.Fatalf("post-apply run status: got %s, want completed (%s)", run.Status, run.Error)
	}

	compacted := false
	for _, e := range runner.getEvents(after.ID) {
		if e.Type == EventAutoCompactStarted {
			compacted = true
		}
	}
	if !compacted {
		t.Error("post-apply run did not emit auto_compact.started; applied config not in effect")
	}
	models := provider.recordedModels()
	if got := models[len(models)-1]; got != "model-b" {
		t.Errorf("post-apply run model: got %q, want %q", got, "model-b")
	}
}

// TestApplyConfig_NormalizesDefaultsLikeNewRunner verifies ApplyConfig applies
// the same zero-value normalization as NewRunner, so an applied config behaves
// exactly like a construction-time one.
func TestApplyConfig_NormalizesDefaultsLikeNewRunner(t *testing.T) {
	t.Parallel()

	runner := NewRunner(&applyConfigRecordingProvider{}, NewRegistry(), RunnerConfig{DefaultModel: "before"})
	runner.ApplyConfig(RunnerConfig{DefaultModel: "after"})

	cfg := runner.snapshotConfig()
	if cfg.DefaultModel != "after" {
		t.Errorf("DefaultModel: got %q, want %q", cfg.DefaultModel, "after")
	}
	if cfg.AutoCompactMode != "hybrid" {
		t.Errorf("AutoCompactMode: got %q, want %q", cfg.AutoCompactMode, "hybrid")
	}
	if cfg.AutoCompactThreshold != 0.80 {
		t.Errorf("AutoCompactThreshold: got %f, want 0.80", cfg.AutoCompactThreshold)
	}
	if cfg.AutoCompactKeepLast != 8 {
		t.Errorf("AutoCompactKeepLast: got %d, want 8", cfg.AutoCompactKeepLast)
	}
	if cfg.ModelContextWindow != 128000 {
		t.Errorf("ModelContextWindow: got %d, want 128000", cfg.ModelContextWindow)
	}
	if cfg.AskUserTimeout != 5*time.Minute {
		t.Errorf("AskUserTimeout: got %v, want 5m", cfg.AskUserTimeout)
	}
	if cfg.HookFailureMode != HookFailureModeFailClosed {
		t.Errorf("HookFailureMode: got %q, want %q", cfg.HookFailureMode, HookFailureModeFailClosed)
	}
}

// TestApplyConfig_ConcurrentWithRunStarts_RaceFree hammers ApplyConfig
// concurrently with run starts and executions. It is only meaningful under
// `go test -race`; a single unsynchronized r.config read fails it.
func TestApplyConfig_ConcurrentWithRunStarts_RaceFree(t *testing.T) {
	t.Parallel()

	provider := &applyConfigRecordingProvider{}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "model-a",
		MaxSteps:     1,
	})

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				run, err := runner.StartRun(RunRequest{Prompt: "race"})
				if err != nil {
					continue // runner may be shutting down at test end
				}
				_, _ = runner.GetRun(run.ID)
			}
		}()
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				model := "model-b"
				if (i+g)%2 == 0 {
					model = "model-c"
				}
				runner.ApplyConfig(RunnerConfig{
					DefaultModel:         model,
					MaxSteps:             1 + (i % 3),
					AutoCompactEnabled:   i%2 == 0,
					ModelContextWindow:   1000,
					AutoCompactThreshold: 0.9,
					TraceToolDecisions:   i%2 == 1,
					CaptureReasoning:     i%3 == 0,
				})
			}
		}(g)
	}
	wg.Wait()
}
