package harness

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// Tests in this file cover epic #817 slice 1: a preserve-instruction accepted by
// CompactRun and threaded into the summarize/hybrid summarization prompt.

// compactInstructionProvider records every CompletionRequest it receives
// (slice copy of messages; strings are immutable) and returns scripted
// results, optionally gating a specific call index so tests can compact
// mid-run.
type compactInstructionProvider struct {
	mu       sync.Mutex
	calls    int
	requests []CompletionRequest
	results  []CompletionResult
	gate     func(idx int)
}

func (p *compactInstructionProvider) Complete(_ context.Context, req CompletionRequest) (CompletionResult, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	captured := CompletionRequest{
		Model:    req.Model,
		Messages: copyMessages(req.Messages),
	}
	p.requests = append(p.requests, captured)
	var result CompletionResult
	if idx < len(p.results) {
		result = p.results[idx]
	}
	gate := p.gate
	p.mu.Unlock()

	if gate != nil {
		gate(idx)
	}
	return result, nil
}

func (p *compactInstructionProvider) snapshot() []CompletionRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]CompletionRequest(nil), p.requests...)
}

func (p *compactInstructionProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// summarizationRequests returns the captured requests whose final message is
// the summarization prompt (i.e. calls made by the compaction summarizer,
// not by the run's step loop).
func summarizationRequests(reqs []CompletionRequest) []CompletionRequest {
	var out []CompletionRequest
	for _, r := range reqs {
		if len(r.Messages) == 0 {
			continue
		}
		last := r.Messages[len(r.Messages)-1]
		if last.Role == "user" && strings.HasPrefix(last.Content, "Please provide a concise summary") {
			out = append(out, r)
		}
	}
	return out
}

// startThreeStepGatedRun starts a run whose provider scripts three tool-call
// steps and then blocks the fourth LLM call on gateRelease, returning the
// runner and run ID once the fourth call is in flight. At that point
// state.messages holds user + 3x(assistant+tool) = 7 messages = 4 turns, so
// CompactRun with KeepLast=1 has a non-empty compaction zone.
func startThreeStepGatedRun(t *testing.T, prov *compactInstructionProvider, toolOutput string, gateRelease <-chan struct{}) (*Runner, string) {
	t.Helper()

	registry := NewRegistry()
	if err := registry.Register(ToolDefinition{
		Name:        "echo_json",
		Description: "echoes payload",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return toolOutput, nil
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel: "test",
		MaxSteps:     6,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Wait until the 4th LLM call (idx 3) is in flight and gated.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if prov.callCount() >= 4 {
			return runner, run.ID
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for step 4 gate (calls=%d)", prov.callCount())
	return nil, ""
}

// threeStepToolCallResults scripts three echo_json tool-call steps, a gated
// final answer, and a summarization result.
func threeStepToolCallResults() []CompletionResult {
	return []CompletionResult{
		{ToolCalls: []ToolCall{{ID: "call-1", Name: "echo_json", Arguments: `{"message":"s1"}`}}},
		{ToolCalls: []ToolCall{{ID: "call-2", Name: "echo_json", Arguments: `{"message":"s2"}`}}},
		{ToolCalls: []ToolCall{{ID: "call-3", Name: "echo_json", Arguments: `{"message":"s3"}`}}},
		{Content: "done"},
		{Content: "a compact summary"},
	}
}

func newGatedInstructionProvider(gateRelease <-chan struct{}) *compactInstructionProvider {
	return &compactInstructionProvider{
		results: threeStepToolCallResults(),
		gate: func(idx int) {
			if idx == 3 {
				<-gateRelease
			}
		},
	}
}

// TestCompactRun_InstructionReachesSummarizerPrompt_Summarize verifies that a
// preserve-instruction passed to CompactRun in summarize mode is appended to
// the provider-visible summarization prompt.
func TestCompactRun_InstructionReachesSummarizerPrompt_Summarize(t *testing.T) {
	t.Parallel()

	gateRelease := make(chan struct{})
	prov := newGatedInstructionProvider(gateRelease)
	runner, runID := startThreeStepGatedRun(t, prov, `{"echo":"ok"}`, gateRelease)

	_, err := runner.CompactRun(context.Background(), runID, CompactRunRequest{
		Mode:        "summarize",
		KeepLast:    1,
		Instruction: "keep the SQL schema",
	})
	if err != nil {
		t.Fatalf("CompactRun summarize: %v", err)
	}
	close(gateRelease)

	summaries := summarizationRequests(prov.snapshot())
	if len(summaries) != 1 {
		t.Fatalf("expected exactly 1 summarization call, got %d", len(summaries))
	}
	last := summaries[0].Messages[len(summaries[0].Messages)-1]
	if !strings.Contains(last.Content, "keep the SQL schema") {
		t.Errorf("summarization prompt missing preserve-instruction; got: %q", last.Content)
	}
}

// TestCompactRun_InstructionReachesSummarizerPrompt_Hybrid verifies the
// instruction also reaches the summarizer in hybrid mode, where only large
// tool outputs are summarized.
func TestCompactRun_InstructionReachesSummarizerPrompt_Hybrid(t *testing.T) {
	t.Parallel()

	gateRelease := make(chan struct{})
	prov := newGatedInstructionProvider(gateRelease)

	// Tool output above the 500-token hybrid threshold (~2000 runes).
	largeOutput := strings.Repeat("x", 2400)
	runner, runID := startThreeStepGatedRun(t, prov, largeOutput, gateRelease)

	_, err := runner.CompactRun(context.Background(), runID, CompactRunRequest{
		Mode:        "hybrid",
		KeepLast:    1,
		Instruction: "keep the failing test output",
	})
	if err != nil {
		t.Fatalf("CompactRun hybrid: %v", err)
	}
	close(gateRelease)

	summaries := summarizationRequests(prov.snapshot())
	if len(summaries) != 1 {
		t.Fatalf("expected exactly 1 summarization call, got %d", len(summaries))
	}
	last := summaries[0].Messages[len(summaries[0].Messages)-1]
	if !strings.Contains(last.Content, "keep the failing test output") {
		t.Errorf("hybrid summarization prompt missing preserve-instruction; got: %q", last.Content)
	}
}

// TestCompactRun_InstructionIgnoredInStripMode verifies strip mode never
// invokes the summarizer, so the instruction never reaches the provider.
func TestCompactRun_InstructionIgnoredInStripMode(t *testing.T) {
	t.Parallel()

	gateRelease := make(chan struct{})
	prov := newGatedInstructionProvider(gateRelease)
	runner, runID := startThreeStepGatedRun(t, prov, `{"echo":"ok"}`, gateRelease)

	callsBefore := prov.callCount()
	_, err := runner.CompactRun(context.Background(), runID, CompactRunRequest{
		Mode:        "strip",
		KeepLast:    1,
		Instruction: "keep the SQL schema",
	})
	if err != nil {
		t.Fatalf("CompactRun strip: %v", err)
	}
	close(gateRelease)

	if got := prov.callCount(); got != callsBefore {
		t.Errorf("strip mode issued %d new provider call(s); summarizer must not run in strip mode", got-callsBefore)
	}
	for _, r := range prov.snapshot() {
		for _, m := range r.Messages {
			if strings.Contains(m.Content, "keep the SQL schema") {
				t.Errorf("strip mode leaked instruction into a provider request: %q", m.Content)
			}
		}
	}
}

// TestCompactRun_EmptyInstructionIsNoOp verifies that an empty (or
// whitespace-only) instruction leaves the summarization prompt byte-identical
// to the fixed prompt used before this feature existed.
func TestCompactRun_EmptyInstructionIsNoOp(t *testing.T) {
	t.Parallel()

	const fixedPrompt = "Please provide a concise summary of this conversation so far, suitable for use as context in a continuation. Include key facts, decisions, and outputs. Be concise."

	for _, instruction := range []string{"", "   "} {
		instruction := instruction
		t.Run("instruction="+strings.TrimSpace(instruction), func(t *testing.T) {
			t.Parallel()

			gateRelease := make(chan struct{})
			prov := newGatedInstructionProvider(gateRelease)
			runner, runID := startThreeStepGatedRun(t, prov, `{"echo":"ok"}`, gateRelease)

			_, err := runner.CompactRun(context.Background(), runID, CompactRunRequest{
				Mode:        "summarize",
				KeepLast:    1,
				Instruction: instruction,
			})
			if err != nil {
				t.Fatalf("CompactRun summarize: %v", err)
			}
			close(gateRelease)

			summaries := summarizationRequests(prov.snapshot())
			if len(summaries) != 1 {
				t.Fatalf("expected exactly 1 summarization call, got %d", len(summaries))
			}
			last := summaries[0].Messages[len(summaries[0].Messages)-1]
			if last.Content != fixedPrompt {
				t.Errorf("empty instruction changed the summarization prompt:\n got: %q\nwant: %q", last.Content, fixedPrompt)
			}
		})
	}
}

// TestAutoCompact_SummarizationPromptHasNoInstruction is a regression guard:
// the auto-compaction path must keep sending the instruction-free prompt.
func TestAutoCompact_SummarizationPromptHasNoInstruction(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(ToolDefinition{
		Name:        "echo_json",
		Description: "echoes payload",
		Parameters:  map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return `{"echo":"ok"}`, nil
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	prov := &compactInstructionProvider{
		results: []CompletionResult{
			{ToolCalls: []ToolCall{{ID: "call-1", Name: "echo_json", Arguments: `{"message":"s1"}`}}},
			{Content: "auto summary"},
			{Content: "done"},
		},
	}

	runner := NewRunner(prov, registry, RunnerConfig{
		DefaultModel:         "test",
		MaxSteps:             4,
		AutoCompactEnabled:   true,
		ModelContextWindow:   100,
		AutoCompactThreshold: 0.80,
		AutoCompactKeepLast:  1,
		AutoCompactMode:      "summarize",
	})

	// 400 runes ≈ 100 tokens > 80% of the 100-token window → auto-compact fires.
	run, err := runner.StartRun(RunRequest{Prompt: strings.Repeat("x", 400)})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, ok := runner.GetRun(run.ID)
		if !ok {
			t.Fatal("run disappeared")
		}
		if state.Status == RunStatusCompleted || state.Status == RunStatusFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	state, ok := runner.GetRun(run.ID)
	if !ok {
		t.Fatal("run not found")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected completed, got %q", state.Status)
	}

	summaries := summarizationRequests(prov.snapshot())
	if len(summaries) == 0 {
		t.Fatal("expected auto-compaction to issue a summarization call, got none")
	}
	for _, r := range summaries {
		last := r.Messages[len(r.Messages)-1]
		if strings.Contains(last.Content, "Preserve especially") {
			t.Errorf("auto-compaction summarization prompt gained an instruction marker: %q", last.Content)
		}
	}
}
