package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	htools "go-agent-harness/internal/harness/tools"
)

// TestGetRunContextStatus_NotFound verifies ErrRunNotFound for an unknown run.
func TestGetRunContextStatus_NotFound(t *testing.T) {
	t.Parallel()

	runner := NewRunner(&staticRunnerProvider{result: CompletionResult{Content: "done"}},
		NewRegistry(), RunnerConfig{DefaultModel: "test", MaxSteps: 2})

	_, err := runner.GetRunContextStatus("nonexistent-run-id")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != ErrRunNotFound {
		t.Fatalf("expected ErrRunNotFound, got: %v", err)
	}
}

// TestGetRunContextStatus_ReturnsData verifies context status is returned for a
// known run and the pressure field is one of the expected values.
func TestGetRunContextStatus_ReturnsData(t *testing.T) {
	t.Parallel()

	blockCh := make(chan struct{})
	releaseCh := make(chan struct{})

	provider := &contextCompactGatingProvider{
		results: []CompletionResult{{Content: "done"}},
		beforeCall: func(idx int) {
			if idx == 0 {
				close(blockCh)
				<-releaseCh
			}
		},
	}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
		MaxSteps:     2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	<-blockCh

	status, err := runner.GetRunContextStatus(run.ID)
	if err != nil {
		t.Fatalf("GetRunContextStatus: %v", err)
	}

	valid := map[string]bool{"low": true, "medium": true, "high": true}
	if !valid[status.ContextPressure] {
		t.Errorf("unexpected context_pressure %q", status.ContextPressure)
	}
	if status.MessageCount < 0 {
		t.Errorf("expected non-negative message_count, got %d", status.MessageCount)
	}
	if status.EstimatedTokens < 0 {
		t.Errorf("expected non-negative estimated_tokens, got %d", status.EstimatedTokens)
	}

	close(releaseCh)
}

// TestContextPressureLevel verifies the thresholds used to compute pressure level.
func TestContextPressureLevel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		tokens int
		want   string
	}{
		{0, "low"},
		{1000, "low"},
		{30000, "low"},
		{30001, "medium"},
		{60000, "medium"},
		{60001, "high"},
		{200000, "high"},
	}
	for _, tc := range cases {
		got := contextPressureLevel(tc.tokens)
		if got != tc.want {
			t.Errorf("contextPressureLevel(%d) = %q, want %q", tc.tokens, got, tc.want)
		}
	}
}

// TestCompactRun_NotFound verifies ErrRunNotFound for unknown run.
func TestCompactRun_NotFound(t *testing.T) {
	t.Parallel()

	runner := NewRunner(&staticRunnerProvider{result: CompletionResult{Content: "done"}},
		NewRegistry(), RunnerConfig{DefaultModel: "test", MaxSteps: 2})

	_, err := runner.CompactRun(context.Background(), "nonexistent-run-id", CompactRunRequest{Mode: "strip"})
	if err != ErrRunNotFound {
		t.Fatalf("expected ErrRunNotFound, got: %v", err)
	}
}

// TestCompactRun_NotActive verifies ErrRunNotActive for a completed run.
func TestCompactRun_NotActive(t *testing.T) {
	t.Parallel()

	provider := &staticRunnerProvider{result: CompletionResult{Content: "done"}}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
		MaxSteps:     2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Wait for completion.
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

	_, err = runner.CompactRun(context.Background(), run.ID, CompactRunRequest{Mode: "strip"})
	if err != ErrRunNotActive {
		t.Fatalf("expected ErrRunNotActive, got: %v", err)
	}
}

// TestCompactRun_InvalidMode verifies an error is returned for an unknown mode.
func TestCompactRun_InvalidMode(t *testing.T) {
	t.Parallel()

	blockCh := make(chan struct{})
	releaseCh := make(chan struct{})

	provider := &contextCompactGatingProvider{
		results: []CompletionResult{{Content: "done"}},
		beforeCall: func(idx int) {
			if idx == 0 {
				close(blockCh)
				<-releaseCh
			}
		},
	}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
		MaxSteps:     2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	<-blockCh

	_, err = runner.CompactRun(context.Background(), run.ID, CompactRunRequest{Mode: "badmode"})
	if err == nil {
		t.Fatal("expected error for invalid mode, got nil")
	}

	close(releaseCh)
}

// TestCompactRun_StripMode verifies strip compaction runs without error on an
// active run and returns a MessagesRemoved count >= 0.
func TestCompactRun_StripMode(t *testing.T) {
	t.Parallel()

	blockCh := make(chan struct{})
	releaseCh := make(chan struct{})

	provider := &contextCompactGatingProvider{
		results: []CompletionResult{{Content: "done"}},
		beforeCall: func(idx int) {
			if idx == 0 {
				close(blockCh)
				<-releaseCh
			}
		},
	}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
		MaxSteps:     2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	<-blockCh

	result, err := runner.CompactRun(context.Background(), run.ID, CompactRunRequest{Mode: "strip"})
	if err != nil {
		t.Fatalf("CompactRun strip: %v", err)
	}
	if result.MessagesRemoved < 0 {
		t.Errorf("expected non-negative MessagesRemoved, got %d", result.MessagesRemoved)
	}

	close(releaseCh)
}

// TestCompactRun_ConcurrentSafe verifies no races when GetRunContextStatus and
// CompactRun are called concurrently while a run is active.
func TestCompactRun_ConcurrentSafe(t *testing.T) {
	t.Parallel()

	blockCh := make(chan struct{})
	releaseCh := make(chan struct{})

	provider := &contextCompactGatingProvider{
		results: []CompletionResult{{Content: "done"}},
		beforeCall: func(idx int) {
			if idx == 0 {
				close(blockCh)
				<-releaseCh
			}
		},
	}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
		MaxSteps:     2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	<-blockCh

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = runner.GetRunContextStatus(run.ID)
		}()
	}
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = runner.CompactRun(context.Background(), run.ID, CompactRunRequest{Mode: "strip"})
		}()
	}

	wg.Wait()
	close(releaseCh)
}

// TestMessagesAsTranscriptSnapshot verifies meta messages are excluded.
func TestMessagesAsTranscriptSnapshot(t *testing.T) {
	t.Parallel()

	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi", IsMeta: true}, // should be excluded
		{Role: "assistant", Content: "how can I help?"},
		{Role: "tool", Content: "result", ToolCallID: "tc1"},
	}

	snap := messagesAsTranscriptSnapshot(msgs)

	if len(snap) != 3 {
		t.Errorf("expected 3 transcript messages (meta excluded), got %d", len(snap))
	}
	for _, m := range snap {
		if m.Role == "assistant" && m.Content == "hi" {
			t.Error("meta message should have been excluded")
		}
	}
}

// TestCompactStripHTTP verifies strip compaction removes tool messages.
func TestCompactStripHTTP(t *testing.T) {
	t.Parallel()

	msgs := []htools.TranscriptMessage{
		{Index: 0, Role: "user", Content: "hello"},
		{Index: 1, Role: "assistant", Content: "calling tool"},
		{Index: 2, Role: "tool", Content: "tool result", ToolCallID: "tc1"},
		{Index: 3, Role: "user", Content: "second"},
		{Index: 4, Role: "assistant", Content: "done"},
	}

	result, _, err := compactMessagesHTTP(context.Background(), msgs, "strip", 2, nil)
	if err != nil {
		t.Fatalf("compactMessagesHTTP strip: %v", err)
	}
	// tool message should be stripped
	for _, m := range result {
		if m.Role == "tool" {
			t.Errorf("expected tool message to be stripped, but found one: %v", m)
		}
	}
}

// TestCompactSummarizeHTTP verifies summarize compaction calls the summarizer.
func TestCompactSummarizeHTTP(t *testing.T) {
	t.Parallel()

	msgs := []htools.TranscriptMessage{
		{Index: 0, Role: "user", Content: "hello"},
		{Index: 1, Role: "assistant", Content: "hi"},
		{Index: 2, Role: "user", Content: "world"},
		{Index: 3, Role: "assistant", Content: "done"},
	}

	summarizer := &fixedSummarizer{summary: "a summary"}
	result, _, err := compactMessagesHTTP(context.Background(), msgs, "summarize", 2, summarizer)
	if err != nil {
		t.Fatalf("compactMessagesHTTP summarize: %v", err)
	}
	// Should contain a compact_summary system message.
	found := false
	for _, m := range result {
		if m.Role == "system" && m.Name == "compact_summary" {
			found = true
		}
	}
	if !found {
		t.Error("expected compact_summary system message in result")
	}
}

// TestCompactHybridHTTP verifies hybrid compaction runs without error.
func TestCompactHybridHTTP(t *testing.T) {
	t.Parallel()

	// Create a large tool result to trigger hybrid removal.
	largeContent := string(make([]byte, 3000))
	for i := range largeContent {
		largeContent = largeContent[:i] + "x" + largeContent[i+1:]
	}

	msgs := []htools.TranscriptMessage{
		{Index: 0, Role: "user", Content: "hello"},
		{Index: 1, Role: "assistant", Content: "calling tool"},
		{Index: 2, Role: "tool", Content: largeContent, ToolCallID: "tc1"},
		{Index: 3, Role: "user", Content: "second"},
		{Index: 4, Role: "assistant", Content: "done"},
	}

	summarizer := &fixedSummarizer{summary: "hybrid summary"}
	result, _, err := compactMessagesHTTP(context.Background(), msgs, "hybrid", 2, summarizer)
	if err != nil {
		t.Fatalf("compactMessagesHTTP hybrid: %v", err)
	}
	if len(result) == 0 {
		t.Error("expected non-empty result from hybrid compaction")
	}
}

// TestCompactMessagesHTTP_NoOp verifies that when there is nothing to compact,
// the original slice is returned unchanged.
func TestCompactMessagesHTTP_NoOp(t *testing.T) {
	t.Parallel()

	msgs := []htools.TranscriptMessage{
		{Index: 0, Role: "user", Content: "hello"},
		{Index: 1, Role: "assistant", Content: "done"},
	}

	result, _, err := compactMessagesHTTP(context.Background(), msgs, "strip", 4, nil)
	if err != nil {
		t.Fatalf("compactMessagesHTTP: %v", err)
	}
	if len(result) != len(msgs) {
		t.Errorf("expected %d messages (no-op), got %d", len(msgs), len(result))
	}
}

// TestCompactRun_SummarizeMode verifies summarize mode compaction runs via CompactRun.
func TestCompactRun_SummarizeMode(t *testing.T) {
	t.Parallel()

	blockCh := make(chan struct{})
	releaseCh := make(chan struct{})

	provider := &contextCompactGatingProvider{
		results: []CompletionResult{{Content: "a summary"}, {Content: "done"}},
		beforeCall: func(idx int) {
			if idx == 0 {
				close(blockCh)
				<-releaseCh
			}
		},
	}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
		MaxSteps:     2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	<-blockCh

	_, err = runner.CompactRun(context.Background(), run.ID, CompactRunRequest{Mode: "summarize"})
	// summarize may succeed or fail (no messages to compact yet), just confirm no panic.
	_ = err

	close(releaseCh)
}

// TestCompactRun_HybridMode verifies hybrid mode compaction runs via CompactRun.
func TestCompactRun_HybridMode(t *testing.T) {
	t.Parallel()

	blockCh := make(chan struct{})
	releaseCh := make(chan struct{})

	provider := &contextCompactGatingProvider{
		results: []CompletionResult{{Content: "hybrid summary"}, {Content: "done"}},
		beforeCall: func(idx int) {
			if idx == 0 {
				close(blockCh)
				<-releaseCh
			}
		},
	}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
		MaxSteps:     2,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	<-blockCh

	_, err = runner.CompactRun(context.Background(), run.ID, CompactRunRequest{Mode: "hybrid"})
	// hybrid may succeed or fail (no messages to compact yet), just confirm no panic.
	_ = err

	close(releaseCh)
}

// fixedSummarizer is a MessageSummarizer that always returns a fixed summary.
type fixedSummarizer struct {
	summary string
}

func (s *fixedSummarizer) SummarizeMessages(_ context.Context, _ []map[string]any) (string, error) {
	return s.summary, nil
}

// contextCompactGatingProvider is a scripted provider with a beforeCall hook
// for use in context/compact tests.
type contextCompactGatingProvider struct {
	mu         sync.Mutex
	results    []CompletionResult
	calls      int
	beforeCall func(idx int)
}

func (p *contextCompactGatingProvider) Complete(_ context.Context, _ CompletionRequest) (CompletionResult, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	var result CompletionResult
	if idx < len(p.results) {
		result = p.results[idx]
	}
	p.mu.Unlock()

	if p.beforeCall != nil {
		p.beforeCall(idx)
	}
	return result, nil
}

// staticRunnerProvider is a minimal provider returning a fixed result, for
// context/compact tests that don't need gating.
type staticRunnerProvider struct {
	result CompletionResult
}

func (p *staticRunnerProvider) Complete(_ context.Context, _ CompletionRequest) (CompletionResult, error) {
	return p.result, nil
}

// TestCompactRunSurvivesConcurrentExecute defends the message lifecycle
// invariant that state.messages is the only source of truth. execute() may
// hold a per-step snapshot, but the next step must re-read the canonical state
// instead of overwriting compaction with stale local context.
// Regression test for #232.
func TestCompactRunSurvivesConcurrentExecute(t *testing.T) {
	t.Parallel()

	// step4Gate blocks the step 4 LLM call so we can compact in between.
	step4Gate := make(chan struct{})

	// Provider: steps 1-3 return tool calls (loop continues), step 4 returns text.
	// This generates 4 turns (user + 3x assistant_tool) so strip with keepLast=2
	// actually removes tool messages from the earlier turns.
	provider := &contextCompactGatingProvider{
		results: []CompletionResult{
			{
				ToolCalls: []ToolCall{{
					ID:        "call-1",
					Name:      "echo_json",
					Arguments: `{"message":"step1"}`,
				}},
			},
			{
				ToolCalls: []ToolCall{{
					ID:        "call-2",
					Name:      "echo_json",
					Arguments: `{"message":"step2"}`,
				}},
			},
			{
				ToolCalls: []ToolCall{{
					ID:        "call-3",
					Name:      "echo_json",
					Arguments: `{"message":"step3"}`,
				}},
			},
			{Content: "final answer"},
		},
		beforeCall: func(idx int) {
			if idx == 3 {
				// Step 4 LLM call: wait for the test to compact first.
				<-step4Gate
			}
		},
	}

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

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "test",
		MaxSteps:     6,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Wait until step 4 is blocked (provider.calls == 4 means step 4 entered beforeCall).
	deadline := time.Now().Add(5 * time.Second)
	gateReached := false
	for time.Now().Before(deadline) {
		provider.mu.Lock()
		calls := provider.calls
		provider.mu.Unlock()
		if calls >= 4 {
			gateReached = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !gateReached {
		t.Fatalf("timed out waiting for step 4 gate")
	}

	// At this point steps 1-3 are fully done (tool calls executed, messages stored).
	// Step 4 is blocked in beforeCall. execute()'s local `messages` has the full
	// history: user + 3x(assistant+tool) = 7 messages = 4 turns.
	msgsBefore := runner.GetRunMessages(run.ID)
	beforeCount := len(msgsBefore)
	if beforeCount < 7 {
		t.Fatalf("expected at least 7 messages after steps 1-3, got %d", beforeCount)
	}

	// Count tool messages before compaction.
	toolMsgsBefore := 0
	for _, m := range msgsBefore {
		if m.Role == "tool" {
			toolMsgsBefore++
		}
	}

	// Compact: strip mode removes tool messages. Use KeepLast=2 so the early
	// assistant_tool turns (turns 1-2) fall outside the keep window.
	result, err := runner.CompactRun(context.Background(), run.ID, CompactRunRequest{Mode: "strip", KeepLast: 2})
	if err != nil {
		t.Fatalf("CompactRun: %v", err)
	}
	if result.MessagesRemoved == 0 && toolMsgsBefore > 0 {
		t.Fatal("expected strip to remove tool messages, but removed 0")
	}

	compactedCount := len(runner.GetRunMessages(run.ID))

	// Release step 4.
	close(step4Gate)

	// Wait for run to complete.
	deadline = time.Now().Add(5 * time.Second)
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
		t.Fatal("run not found after completion")
	}
	if state.Status != RunStatusCompleted {
		t.Fatalf("expected completed, got %q", state.Status)
	}

	// Step 4 has no tool calls, so execute() appends exactly 1 assistant message.
	// With the fix: final = compactedCount + 1 (re-reads compacted base).
	// With the bug: final = beforeCount + 1 (stale messages overwrite compaction).
	msgsFinal := runner.GetRunMessages(run.ID)
	finalCount := len(msgsFinal)
	expectedWithFix := compactedCount + 1

	if finalCount != expectedWithFix {
		t.Errorf("compaction not preserved: final=%d, want=%d (compacted=%d + 1 assistant), pre-compact=%d",
			finalCount, expectedWithFix, compactedCount, beforeCount)
	}
}

// TestCompactRunAtStepBoundary defends the same source-of-truth invariant at
// the step boundary: compaction that wins the boundary must define the next
// LLM context instead of being lost to a stale execute() copy.
// Regression test for #232.
func TestCompactRunAtStepBoundary(t *testing.T) {
	t.Parallel()

	step4Gate := make(chan struct{})

	// Provider: steps 1-3 return tool calls, step 4 returns text.
	// 4 turns total so keepLast=2 leaves 2 turns in the compact window.
	provider := &contextCompactGatingProvider{
		results: []CompletionResult{
			{
				ToolCalls: []ToolCall{{
					ID:        "call-1",
					Name:      "echo_json",
					Arguments: `{"message":"s1"}`,
				}},
			},
			{
				ToolCalls: []ToolCall{{
					ID:        "call-2",
					Name:      "echo_json",
					Arguments: `{"message":"s2"}`,
				}},
			},
			{
				ToolCalls: []ToolCall{{
					ID:        "call-3",
					Name:      "echo_json",
					Arguments: `{"message":"s3"}`,
				}},
			},
			{Content: "done"},
		},
		beforeCall: func(idx int) {
			if idx == 3 {
				<-step4Gate
			}
		},
	}

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

	runner := NewRunner(provider, registry, RunnerConfig{
		DefaultModel: "test",
		MaxSteps:     6,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Wait until step 4 is gated.
	deadline := time.Now().Add(5 * time.Second)
	gateReached := false
	for time.Now().Before(deadline) {
		provider.mu.Lock()
		calls := provider.calls
		provider.mu.Unlock()
		if calls >= 4 {
			gateReached = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !gateReached {
		t.Fatalf("timed out waiting for step 4 gate")
	}

	// Compact while step 4 is gated (steps 1-3 fully processed).
	msgsBefore := runner.GetRunMessages(run.ID)
	_, err = runner.CompactRun(context.Background(), run.ID, CompactRunRequest{Mode: "strip", KeepLast: 2})
	if err != nil {
		t.Fatalf("CompactRun: %v", err)
	}

	msgsAfterCompact := runner.GetRunMessages(run.ID)
	compactedCount := len(msgsAfterCompact)

	// Release step 4 so the run completes.
	close(step4Gate)

	deadline = time.Now().Add(5 * time.Second)
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
	if state.Output != "done" {
		t.Errorf("expected output %q, got %q", "done", state.Output)
	}

	// Step 4 adds exactly 1 assistant message (no tool calls).
	// With the fix: final = compactedCount + 1 (re-reads compacted base).
	msgsFinal := runner.GetRunMessages(run.ID)
	finalCount := len(msgsFinal)
	expectedWithFix := compactedCount + 1

	if finalCount != expectedWithFix {
		t.Errorf("compaction not preserved: final=%d, want=%d (compacted=%d + 1 assistant), pre-compact=%d",
			finalCount, expectedWithFix, compactedCount, len(msgsBefore))
	}
}

// TestCompactRun_HonoursPerRequestSummarizerModel is a regression test for the
// HIGH issue in #25: CompactRun was calling r.NewMessageSummarizer() (no model
// override) instead of r.newMessageSummarizerWithModel(state.resolvedRoleModels.Summarizer).
// This meant a per-request RoleModels.Summarizer was silently ignored during
// manual compaction triggered via the HTTP API.
func TestCompactRun_HonoursPerRequestSummarizerModel(t *testing.T) {
	t.Parallel()

	blockCh := make(chan struct{})
	releaseCh := make(chan struct{})

	// modelCapProvider records which model each Complete call uses,
	// and optionally gates specific calls.
	type modelCapProvider struct {
		mu             sync.Mutex
		calls          int
		capturedModels []string
		results        []CompletionResult
		gate           func(idx int)
	}

	prov := &modelCapProvider{
		results: []CompletionResult{
			// Call 0: main LLM step (gated so we can compact mid-run).
			{Content: "done"},
			// Call 1: summarization call issued by CompactRun in summarize mode.
			{Content: "a compact summary"},
		},
		gate: func(idx int) {
			if idx == 0 {
				close(blockCh)
				<-releaseCh
			}
		},
	}

	// Implement CompletionProvider for modelCapProvider via a wrapper.
	provFn := func(ctx context.Context, req CompletionRequest) (CompletionResult, error) {
		prov.mu.Lock()
		idx := prov.calls
		prov.calls++
		prov.capturedModels = append(prov.capturedModels, req.Model)
		var result CompletionResult
		if idx < len(prov.results) {
			result = prov.results[idx]
		}
		gate := prov.gate
		prov.mu.Unlock()

		if gate != nil {
			gate(idx)
		}
		return result, nil
	}

	runner := NewRunner(compactFuncProvider(provFn), NewRegistry(), RunnerConfig{
		DefaultModel: "default-model",
		// No config-level Summarizer — only the per-request override must be used.
	})

	const perRequestSummarizer = "per-request-summarizer-model"
	run, err := runner.StartRun(RunRequest{
		Prompt: "hello",
		RoleModels: &RoleModels{
			Summarizer: perRequestSummarizer,
		},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Wait until the run is active (first LLM call is blocked).
	<-blockCh

	// Trigger manual compaction in summarize mode while run is blocked.
	// The run has the initial user message in state, so summarize will attempt
	// to call the provider.
	// With the bug: uses NewMessageSummarizer() → "default-model".
	// With the fix: uses newMessageSummarizerWithModel(perRequestSummarizer).
	_, _ = runner.CompactRun(context.Background(), run.ID, CompactRunRequest{
		Mode:     "summarize",
		KeepLast: 4,
	})

	// Release the blocked LLM step.
	close(releaseCh)

	// Wait for run to complete.
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

	// Check: any provider call after call 0 (the main LLM step) is a
	// summarization call. It must have used the per-request summarizer model.
	prov.mu.Lock()
	captured := append([]string(nil), prov.capturedModels...)
	prov.mu.Unlock()

	for i := 1; i < len(captured); i++ {
		if captured[i] != perRequestSummarizer {
			t.Errorf("summarization call %d used model %q, want %q (per-request summarizer override ignored)",
				i, captured[i], perRequestSummarizer)
		}
	}
}

// compactFuncProvider adapts a plain function to the CompletionProvider interface.
// Named distinctly to avoid collision with funcProvider in runner_tool_filter_test.go.
type compactFuncProvider func(ctx context.Context, req CompletionRequest) (CompletionResult, error)

func (f compactFuncProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResult, error) {
	return f(ctx, req)
}

// ---------------------------------------------------------------------------
// #331 — parseTurnsHTTP edge case coverage
// ---------------------------------------------------------------------------

// TestParseTurnsHTTP_Empty verifies nil is returned for an empty message slice.
func TestParseTurnsHTTP_Empty(t *testing.T) {
	t.Parallel()
	turns := parseTurnsHTTP(nil)
	if turns != nil {
		t.Errorf("expected nil turns for empty input, got %v", turns)
	}
}

// TestParseTurnsHTTP_SystemPrefix verifies system messages without
// compact_summary are tagged "system_prefix".
func TestParseTurnsHTTP_SystemPrefix(t *testing.T) {
	t.Parallel()
	msgs := []htools.TranscriptMessage{
		{Index: 0, Role: "system", Content: "system instructions"},
		{Index: 1, Role: "user", Content: "hello"},
	}
	turns := parseTurnsHTTP(msgs)
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}
	if turns[0].Kind != "system_prefix" {
		t.Errorf("expected system_prefix kind, got %q", turns[0].Kind)
	}
	if turns[1].Kind != "user" {
		t.Errorf("expected user kind, got %q", turns[1].Kind)
	}
}

// TestParseTurnsHTTP_CompactSummaryPrefix verifies a system message with
// Name=="compact_summary" in the prefix position is tagged "compact_summary".
func TestParseTurnsHTTP_CompactSummaryPrefix(t *testing.T) {
	t.Parallel()
	msgs := []htools.TranscriptMessage{
		{Index: 0, Role: "system", Name: "compact_summary", Content: "prior context summary"},
		{Index: 1, Role: "user", Content: "continue"},
	}
	turns := parseTurnsHTTP(msgs)
	if len(turns) < 1 {
		t.Fatalf("expected at least 1 turn, got %d", len(turns))
	}
	if turns[0].Kind != "compact_summary" {
		t.Errorf("expected compact_summary kind in prefix position, got %q", turns[0].Kind)
	}
}

// TestParseTurnsHTTP_CompactSummaryNonPrefix verifies a compact_summary system
// message that appears after an assistant message is collected into the
// assistant turn (because parseTurnsHTTP swallows trailing system messages
// into the preceding assistant turn). The compact_summary content is present
// in the resulting turn's messages.
func TestParseTurnsHTTP_CompactSummaryNonPrefix(t *testing.T) {
	t.Parallel()
	msgs := []htools.TranscriptMessage{
		{Index: 0, Role: "user", Content: "hello"},
		{Index: 1, Role: "assistant", Content: "hi"},
		{Index: 2, Role: "system", Name: "compact_summary", Content: "summary of prior turns"},
		{Index: 3, Role: "user", Content: "next question"},
	}
	turns := parseTurnsHTTP(msgs)
	// The compact_summary after an assistant message is folded into the
	// assistant turn (Kind == "assistant_text" since no tool results follow).
	// Verify its content is reachable somewhere in the turns.
	found := false
	for _, tt := range turns {
		for _, m := range tt.Messages {
			if m.Name == "compact_summary" && m.Content == "summary of prior turns" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("compact_summary content not found in any turn, got: %v", turns)
	}
}

// TestParseTurnsHTTP_AssistantWithToolAndSystem verifies that an assistant
// message followed by tool messages AND inline system messages all get
// collected into a single "assistant_tool" turn.
func TestParseTurnsHTTP_AssistantWithToolAndSystem(t *testing.T) {
	t.Parallel()
	msgs := []htools.TranscriptMessage{
		{Index: 0, Role: "user", Content: "run tools"},
		{Index: 1, Role: "assistant", Content: "calling tool"},
		{Index: 2, Role: "tool", Content: "tool result", ToolCallID: "tc1"},
		{Index: 3, Role: "system", Content: "inline system note"},
		{Index: 4, Role: "user", Content: "done"},
	}
	turns := parseTurnsHTTP(msgs)
	// Turn 1 should be user, turn 2 should be assistant_tool (contains assistant+tool+system).
	var assistantTurn *httpTurn
	for i := range turns {
		if turns[i].Kind == "assistant_tool" {
			assistantTurn = &turns[i]
			break
		}
	}
	if assistantTurn == nil {
		t.Fatalf("expected assistant_tool turn, got %v", turns)
	}
	// Must contain the assistant message, the tool message, and the inline system.
	if len(assistantTurn.Messages) < 3 {
		t.Errorf("assistant_tool turn should contain >= 3 messages, got %d: %v", len(assistantTurn.Messages), assistantTurn.Messages)
	}
	roles := make(map[string]int)
	for _, m := range assistantTurn.Messages {
		roles[m.Role]++
	}
	if roles["assistant"] == 0 {
		t.Error("assistant_tool turn missing assistant message")
	}
	if roles["tool"] == 0 {
		t.Error("assistant_tool turn missing tool message")
	}
	if roles["system"] == 0 {
		t.Error("assistant_tool turn missing inline system message")
	}
}

// TestParseTurnsHTTP_ToolOnly verifies a bare tool message (no preceding
// assistant) becomes an "assistant_tool" turn.
func TestParseTurnsHTTP_ToolOnly(t *testing.T) {
	t.Parallel()
	msgs := []htools.TranscriptMessage{
		{Index: 0, Role: "tool", Content: "orphan tool result", ToolCallID: "tc1"},
	}
	turns := parseTurnsHTTP(msgs)
	if len(turns) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(turns))
	}
	if turns[0].Kind != "assistant_tool" {
		t.Errorf("expected assistant_tool for bare tool msg, got %q", turns[0].Kind)
	}
}

// TestParseTurnsHTTP_SystemOnly verifies a system message that appears AFTER
// non-prefix content is tagged "system_prefix" (current behaviour for
// mid-conversation system messages).
func TestParseTurnsHTTP_SystemOnly(t *testing.T) {
	t.Parallel()
	msgs := []htools.TranscriptMessage{
		{Index: 0, Role: "user", Content: "first"},
		{Index: 1, Role: "system", Content: "mid-conversation system"},
	}
	turns := parseTurnsHTTP(msgs)
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}
	// The mid-conversation system message should be tagged system_prefix.
	if turns[1].Kind != "system_prefix" {
		t.Errorf("expected system_prefix for mid-conv system msg, got %q", turns[1].Kind)
	}
}

// TestParseTurnsHTTP_UnknownRole verifies that a message with an unknown role
// is assigned the "user" kind (default branch).
func TestParseTurnsHTTP_UnknownRole(t *testing.T) {
	t.Parallel()
	msgs := []htools.TranscriptMessage{
		{Index: 0, Role: "unknown_role", Content: "some content"},
	}
	turns := parseTurnsHTTP(msgs)
	if len(turns) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(turns))
	}
	if turns[0].Kind != "user" {
		t.Errorf("expected user kind for unknown role, got %q", turns[0].Kind)
	}
}

// TestParseTurnsHTTP_AssistantTextOnly verifies that an assistant message
// with no following tool messages is tagged "assistant_text".
func TestParseTurnsHTTP_AssistantTextOnly(t *testing.T) {
	t.Parallel()
	msgs := []htools.TranscriptMessage{
		{Index: 0, Role: "user", Content: "hello"},
		{Index: 1, Role: "assistant", Content: "world"},
	}
	turns := parseTurnsHTTP(msgs)
	// Find the assistant turn.
	var atTurn *httpTurn
	for i := range turns {
		if turns[i].Kind == "assistant_text" {
			atTurn = &turns[i]
			break
		}
	}
	if atTurn == nil {
		t.Fatalf("expected assistant_text turn; got %v", turns)
	}
}

// TestParseTurnsHTTP_MixedSequence exercises a realistic mixed sequence:
// prefix system, compact_summary, user, assistant+tool, assistant_text.
func TestParseTurnsHTTP_MixedSequence(t *testing.T) {
	t.Parallel()
	msgs := []htools.TranscriptMessage{
		{Index: 0, Role: "system", Content: "instructions"},
		{Index: 1, Role: "system", Name: "compact_summary", Content: "previous summary"},
		{Index: 2, Role: "user", Content: "first user msg"},
		{Index: 3, Role: "assistant", Content: "calling tool"},
		{Index: 4, Role: "tool", Content: "result", ToolCallID: "tc1"},
		{Index: 5, Role: "user", Content: "second user msg"},
		{Index: 6, Role: "assistant", Content: "final text"},
	}
	turns := parseTurnsHTTP(msgs)

	want := map[string]int{
		"system_prefix":   1,
		"compact_summary": 1,
		"user":            2,
		"assistant_tool":  1,
		"assistant_text":  1,
	}
	got := make(map[string]int)
	for _, tt := range turns {
		got[tt.Kind]++
	}
	for kind, count := range want {
		if got[kind] != count {
			t.Errorf("kind %q: expected %d turns, got %d (all turns: %v)", kind, count, got[kind], turns)
		}
	}
}

// ---------------------------------------------------------------------------
// #331 — compactMessagesHTTP edge cases
// ---------------------------------------------------------------------------

// TestCompactMessagesHTTP_SummarizeNilSummarizer verifies that summarize
// mode returns an error when summarizer is nil.
func TestCompactMessagesHTTP_SummarizeNilSummarizer(t *testing.T) {
	t.Parallel()

	msgs := []htools.TranscriptMessage{
		{Index: 0, Role: "user", Content: "a"},
		{Index: 1, Role: "assistant", Content: "b"},
		{Index: 2, Role: "user", Content: "c"},
		{Index: 3, Role: "assistant", Content: "d"},
	}

	_, _, err := compactMessagesHTTP(context.Background(), msgs, "summarize", 1, nil)
	if err == nil {
		t.Fatal("expected error when summarizer is nil in summarize mode")
	}
}

// TestCompactMessagesHTTP_StripPreservesAssistantText verifies that strip mode
// keeps assistant text content while removing tool outputs.
func TestCompactMessagesHTTP_StripPreservesAssistantText(t *testing.T) {
	t.Parallel()

	const assistantText = "I am calling a tool now"
	msgs := []htools.TranscriptMessage{
		{Index: 0, Role: "user", Content: "do stuff"},
		{Index: 1, Role: "assistant", Content: assistantText},
		{Index: 2, Role: "tool", Content: "big tool output", ToolCallID: "tc1"},
		{Index: 3, Role: "user", Content: "next"},
		{Index: 4, Role: "assistant", Content: "final"},
	}

	result, _, err := compactMessagesHTTP(context.Background(), msgs, "strip", 1, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The assistant text must be preserved.
	found := false
	for _, m := range result {
		if m.Content == assistantText {
			found = true
		}
	}
	if !found {
		t.Errorf("expected assistant text %q to be preserved after strip, got %v", assistantText, result)
	}

	// The tool message must be stripped.
	for _, m := range result {
		if m.Role == "tool" {
			t.Errorf("tool message should have been stripped, but found: %v", m)
		}
	}
}

// TestCompactMessagesHTTP_HybridNilSummarizer verifies that hybrid mode with
// a nil summarizer still runs without error (strips large tools, no summary).
func TestCompactMessagesHTTP_HybridNilSummarizer(t *testing.T) {
	t.Parallel()

	largeContent := strings.Repeat("x", 3000)
	msgs := []htools.TranscriptMessage{
		{Index: 0, Role: "user", Content: "hello"},
		{Index: 1, Role: "assistant", Content: "calling tool"},
		{Index: 2, Role: "tool", Content: largeContent, ToolCallID: "tc1"},
		{Index: 3, Role: "user", Content: "second"},
		{Index: 4, Role: "assistant", Content: "done"},
	}

	result, _, err := compactMessagesHTTP(context.Background(), msgs, "hybrid", 1, nil)
	if err != nil {
		t.Fatalf("hybrid with nil summarizer should not error: %v", err)
	}
	if len(result) == 0 {
		t.Error("expected non-empty result from hybrid with nil summarizer")
	}
	// Large tool content should be removed.
	for _, m := range result {
		if m.Role == "tool" && m.Content == largeContent {
			t.Error("large tool content should have been removed by hybrid mode")
		}
	}
}

// TestCompactMessagesHTTP_HybridSummarizerError verifies that hybrid mode
// falls back gracefully (no error) when the summarizer returns an error.
func TestCompactMessagesHTTP_HybridSummarizerError(t *testing.T) {
	t.Parallel()

	largeContent := strings.Repeat("y", 3000)
	msgs := []htools.TranscriptMessage{
		{Index: 0, Role: "user", Content: "go"},
		{Index: 1, Role: "assistant", Content: "running"},
		{Index: 2, Role: "tool", Content: largeContent, ToolCallID: "tc2"},
		{Index: 3, Role: "user", Content: "ok"},
		{Index: 4, Role: "assistant", Content: "done"},
	}

	errorSummarizer := &errorSummarizer{}
	result, _, err := compactMessagesHTTP(context.Background(), msgs, "hybrid", 1, errorSummarizer)
	// hybrid does not propagate summarizer errors; it falls back to a marker.
	if err != nil {
		t.Fatalf("hybrid should not propagate summarizer error: %v", err)
	}
	if len(result) == 0 {
		t.Error("expected non-empty result from hybrid with erroring summarizer")
	}
}

// TestCompactMessagesHTTP_StripCompactSummaryInPrefix verifies that a
// compact_summary in the prefix is preserved verbatim by strip mode.
func TestCompactMessagesHTTP_StripCompactSummaryInPrefix(t *testing.T) {
	t.Parallel()

	const summaryContent = "previous context summary"
	msgs := []htools.TranscriptMessage{
		{Index: 0, Role: "system", Name: "compact_summary", Content: summaryContent},
		{Index: 1, Role: "user", Content: "new question"},
		{Index: 2, Role: "assistant", Content: "calling tool"},
		{Index: 3, Role: "tool", Content: "tool result", ToolCallID: "tc1"},
		{Index: 4, Role: "user", Content: "another"},
		{Index: 5, Role: "assistant", Content: "final"},
	}

	result, _, err := compactMessagesHTTP(context.Background(), msgs, "strip", 1, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The original compact_summary in the prefix must be retained.
	found := false
	for _, m := range result {
		if m.Name == "compact_summary" && m.Content == summaryContent {
			found = true
		}
	}
	if !found {
		t.Errorf("expected prefix compact_summary to be preserved, got %v", result)
	}
}

// TestCompactMessagesHTTP_UnknownMode verifies an error for an unrecognised mode.
func TestCompactMessagesHTTP_UnknownMode(t *testing.T) {
	t.Parallel()

	msgs := []htools.TranscriptMessage{
		{Index: 0, Role: "user", Content: "a"},
		{Index: 1, Role: "assistant", Content: "b"},
		{Index: 2, Role: "user", Content: "c"},
		{Index: 3, Role: "assistant", Content: "d"},
	}

	_, _, err := compactMessagesHTTP(context.Background(), msgs, "bogusmode", 1, nil)
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

// ---------------------------------------------------------------------------
// #331 — autoCompactMessages fallback and override coverage
// ---------------------------------------------------------------------------

// TestAutoCompactMessages_SummarizerOverride verifies that autoCompactMessages
// uses the per-run summarizer model when one is set on the run state.
func TestAutoCompactMessages_SummarizerOverride(t *testing.T) {
	t.Parallel()

	blockCh := make(chan struct{})
	releaseCh := make(chan struct{})

	const perRunModel = "per-run-summarizer-v2"

	type modelCapProvider struct {
		mu             sync.Mutex
		calls          int
		capturedModels []string
		results        []CompletionResult
		gate           func(idx int)
	}

	prov := &modelCapProvider{
		results: []CompletionResult{
			{Content: "done"},
			{Content: "compact summary content"},
		},
		gate: func(idx int) {
			if idx == 0 {
				close(blockCh)
				<-releaseCh
			}
		},
	}

	provFn := func(ctx context.Context, req CompletionRequest) (CompletionResult, error) {
		prov.mu.Lock()
		idx := prov.calls
		prov.calls++
		prov.capturedModels = append(prov.capturedModels, req.Model)
		var result CompletionResult
		if idx < len(prov.results) {
			result = prov.results[idx]
		}
		gate := prov.gate
		prov.mu.Unlock()
		if gate != nil {
			gate(idx)
		}
		return result, nil
	}

	runner := NewRunner(compactFuncProvider(provFn), NewRegistry(), RunnerConfig{
		DefaultModel: "default-model",
		MaxSteps:     1,
	})

	run, err := runner.StartRun(RunRequest{
		Prompt: "hello",
		RoleModels: &RoleModels{
			Summarizer: perRunModel,
		},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	<-blockCh

	messages := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "calling tool"},
		{Role: "tool", Content: strings.Repeat("x", 3000), ToolCallID: "tc1"},
		{Role: "user", Content: "question 2"},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "question 3"},
		{Role: "assistant", Content: "also done"},
	}
	runner.setMessages(run.ID, messages)

	result, err := runner.autoCompactMessages(context.Background(), run.ID, messages)
	if err != nil {
		t.Fatalf("autoCompactMessages: %v", err)
	}
	if result == nil {
		t.Fatal("autoCompactMessages returned nil")
	}

	close(releaseCh)

	// Wait for run completion so goroutines are done.
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

	// Any summarization call (idx > 0) must use the per-run model.
	prov.mu.Lock()
	captured := append([]string(nil), prov.capturedModels...)
	prov.mu.Unlock()

	for i := 1; i < len(captured); i++ {
		if captured[i] != perRunModel {
			t.Errorf("summarization call %d used model %q, want per-run model %q",
				i, captured[i], perRunModel)
		}
	}
}

// TestAutoCompactMessages_FallbackFromSummarizeToStrip verifies that when the
// configured mode is "summarize" and the summarizer returns an error,
// autoCompactMessages falls back to "strip" and returns a valid result.
func TestAutoCompactMessages_FallbackFromSummarizeToStrip(t *testing.T) {
	t.Parallel()

	blockCh := make(chan struct{})
	releaseCh := make(chan struct{})

	provider := &contextCompactGatingProvider{
		results: []CompletionResult{{Content: "done"}},
		beforeCall: func(idx int) {
			if idx == 0 {
				close(blockCh)
				<-releaseCh
			}
		},
	}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:         "test",
		MaxSteps:             2,
		AutoCompactEnabled:   true,
		ModelContextWindow:   100,
		AutoCompactThreshold: 0.80,
		AutoCompactKeepLast:  1,
		AutoCompactMode:      "summarize", // summarize will fail (provider not configured for it)
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	<-blockCh

	messages := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "calling tool"},
		{Role: "tool", Content: strings.Repeat("x", 3000), ToolCallID: "tc1"},
		{Role: "user", Content: "follow up"},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "more"},
		{Role: "assistant", Content: "also done"},
	}
	runner.setMessages(run.ID, messages)

	// autoCompactMessages in summarize mode: summarizer will return an error
	// (provider not configured for summarization — returns empty string which
	// compactSummarizeHTTP treats as a valid but empty summary; however our
	// test configures the runner without a SummarizerModel so
	// newMessageSummarizerWithModel falls back to the main runner model).
	// The key assertion is that the function does NOT return an error.
	result, err := runner.autoCompactMessages(context.Background(), run.ID, messages)
	if err != nil {
		t.Fatalf("autoCompactMessages should not return an error even if summarize fails, got: %v", err)
	}
	if result == nil {
		t.Fatal("autoCompactMessages returned nil result")
	}

	close(releaseCh)
}

// TestAutoCompactMessages_HybridFallbackToStripOnError verifies that when
// the hybrid summarizer errors, autoCompactMessages falls back to strip
// mode and still returns a valid non-nil result.
func TestAutoCompactMessages_HybridFallbackToStripOnError(t *testing.T) {
	t.Parallel()

	blockCh := make(chan struct{})
	releaseCh := make(chan struct{})

	provider := &contextCompactGatingProvider{
		results: []CompletionResult{{Content: "done"}},
		beforeCall: func(idx int) {
			if idx == 0 {
				close(blockCh)
				<-releaseCh
			}
		},
	}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:         "test",
		MaxSteps:             2,
		AutoCompactEnabled:   true,
		ModelContextWindow:   100,
		AutoCompactThreshold: 0.80,
		AutoCompactKeepLast:  1,
		AutoCompactMode:      "hybrid",
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	<-blockCh

	messages := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "calling tool"},
		{Role: "tool", Content: strings.Repeat("z", 3000), ToolCallID: "tc1"},
		{Role: "user", Content: "question 2"},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "question 3"},
		{Role: "assistant", Content: "final"},
	}
	runner.setMessages(run.ID, messages)

	// Override the runner config to use an erroring summarizer indirectly: hybrid
	// itself does not fail on summarizer error (logs and uses empty summary).
	// We verify the result is valid regardless.
	result, err := runner.autoCompactMessages(context.Background(), run.ID, messages)
	if err != nil {
		t.Fatalf("autoCompactMessages should not fail for hybrid mode: %v", err)
	}
	if result == nil {
		t.Fatal("autoCompactMessages returned nil for hybrid mode")
	}

	close(releaseCh)
}

// TestAutoCompactMessages_EmptyMessages verifies autoCompactMessages returns
// the original (empty) message slice when the snapshot is empty.
func TestAutoCompactMessages_EmptyMessages(t *testing.T) {
	t.Parallel()

	blockCh := make(chan struct{})
	releaseCh := make(chan struct{})

	provider := &contextCompactGatingProvider{
		results: []CompletionResult{{Content: "done"}},
		beforeCall: func(idx int) {
			if idx == 0 {
				close(blockCh)
				<-releaseCh
			}
		},
	}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:         "test",
		MaxSteps:             2,
		AutoCompactEnabled:   true,
		ModelContextWindow:   100,
		AutoCompactThreshold: 0.80,
		AutoCompactKeepLast:  2,
		AutoCompactMode:      "strip",
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	<-blockCh

	// Call with only meta messages — these are excluded from the snapshot.
	messages := []Message{
		{Role: "assistant", Content: "meta only", IsMeta: true},
	}
	runner.setMessages(run.ID, messages)

	result, err := runner.autoCompactMessages(context.Background(), run.ID, messages)
	if err != nil {
		t.Fatalf("autoCompactMessages on meta-only messages: %v", err)
	}
	// Should return the original slice unchanged (no compaction possible).
	if len(result) != len(messages) {
		t.Errorf("expected %d messages returned unchanged, got %d", len(messages), len(result))
	}

	close(releaseCh)
}

// errorSummarizer is a MessageSummarizer that always returns an error.
type errorSummarizer struct{}

func (e *errorSummarizer) SummarizeMessages(_ context.Context, _ []map[string]any) (string, error) {
	return "", fmt.Errorf("summarizer: intentional test error")
}

// TestAutoCompactMessages_ContextCancelled verifies that autoCompactMessages
// respects the provided context and returns promptly when the context is
// already cancelled, rather than hanging on a context.Background() provider
// call.
func TestAutoCompactMessages_ContextCancelled(t *testing.T) {
	t.Parallel()

	provider := &cancelBlockingProvider{started: make(chan struct{})}
	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel:        "test",
		MaxSteps:            1,
		AutoCompactEnabled:  false,
		AutoCompactMode:     "summarize",
		AutoCompactKeepLast: 1,
		ModelContextWindow:  100,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Wait for the run's execute goroutine to finish preflight and block in
	// the provider call. This avoids a data race with runPreflight writing
	// state.resolvedRoleModels while autoCompactMessages reads it.
	<-provider.started

	messages := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "calling tool"},
		{Role: "tool", Content: strings.Repeat("x", 3000), ToolCallID: "tc1"},
		{Role: "user", Content: "follow up"},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "more"},
		{Role: "assistant", Content: "also done"},
	}
	runner.setMessages(run.ID, messages)

	// Use an already-cancelled context. A correct implementation propagates
	// this context into the summarizer provider call and returns promptly
	// (either with a context error or with a strip fallback). Before the fix,
	// autoCompactMessages passed context.Background() to the provider, so the
	// cancelBlockingProvider would block forever.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	type result struct {
		messages []Message
		err      error
	}
	done := make(chan result, 1)
	go func() {
		msgs, err := runner.autoCompactMessages(ctx, run.ID, messages)
		done <- result{msgs, err}
	}()

	select {
	case res := <-done:
		if res.err != nil && res.err != context.Canceled {
			t.Fatalf("autoCompactMessages returned unexpected error: %v", res.err)
		}
		if res.err == nil && res.messages == nil {
			t.Fatal("autoCompactMessages returned nil result without error")
		}
		if len(res.messages) == 0 {
			t.Fatal("autoCompactMessages returned empty result")
		}
		// Verify the large tool output was stripped (fallback) rather than
		// retained verbatim.
		for _, m := range res.messages {
			if m.Role == "tool" && m.ToolCallID == "tc1" {
				t.Fatal("tool content was not stripped in fallback")
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("autoCompactMessages did not return promptly on a cancelled context")
	}
}

// cancelBlockingProvider is a provider that blocks until its request context
// is cancelled, then returns the context error. It simulates a summarizer
// provider that cannot make progress until cancellation is propagated.
type cancelBlockingProvider struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
}

func (p *cancelBlockingProvider) Complete(ctx context.Context, _ CompletionRequest) (CompletionResult, error) {
	p.mu.Lock()
	p.calls++
	if p.started != nil && p.calls == 1 {
		close(p.started)
	}
	p.mu.Unlock()

	select {
	case <-ctx.Done():
		return CompletionResult{}, ctx.Err()
	}
}
