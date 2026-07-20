package harness

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// blockingSummarizeProvider blocks the main step LLM call on releaseMain and the
// compaction summarizer call on releaseSummarize. This lets a test drive the
// race window between CompactRun and stepSetMessages.
type blockingSummarizeProvider struct {
	mu               sync.Mutex
	mainOnce         sync.Once
	summarizeOnce    sync.Once
	enteredMain      chan struct{}
	releaseMain      chan struct{}
	enteredSummarize chan struct{}
	releaseSummarize chan struct{}
}

func (p *blockingSummarizeProvider) Complete(_ context.Context, req CompletionRequest) (CompletionResult, error) {
	// The summarizer appends a user message with this exact prompt.
	const summarizePrompt = "Please provide a concise summary of this conversation so far"
	last := ""
	if n := len(req.Messages); n > 0 {
		last = req.Messages[n-1].Content
	}
	isSummarize := strings.Contains(last, summarizePrompt)

	if isSummarize {
		p.summarizeOnce.Do(func() { close(p.enteredSummarize) })
		<-p.releaseSummarize
		return CompletionResult{Content: "compact summary"}, nil
	}

	p.mainOnce.Do(func() { close(p.enteredMain) })
	<-p.releaseMain
	return CompletionResult{Content: "done"}, nil
}

// TestCompactRunStepSetMessagesRace verifies that a step-engine write via
// stepSetMessages is not silently lost when CompactRun runs concurrently.
// CompactRun is held in the summarizer while the test appends a message and
// calls stepSetMessages; the helper must block until compaction finishes,
// then write, preserving the appended message.
func TestCompactRunStepSetMessagesRace(t *testing.T) {
	t.Parallel()

	provider := &blockingSummarizeProvider{
		enteredMain:      make(chan struct{}),
		releaseMain:      make(chan struct{}),
		enteredSummarize: make(chan struct{}),
		releaseSummarize: make(chan struct{}),
	}

	runner := NewRunner(provider, NewRegistry(), RunnerConfig{
		DefaultModel: "test",
		MaxSteps:     10,
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Wait until the step engine is blocked in its first LLM call.
	select {
	case <-provider.enteredMain:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for step LLM call")
	}

	// Seed the transcript with enough turns that summarize mode with KeepLast=1
	// actually compacts (i.e. calls the summarizer and removes messages).
	seedMessages := []Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "first user"},
		{Role: "assistant", Content: "first assistant"},
		{Role: "user", Content: "second user"},
		{Role: "assistant", Content: "second assistant"},
		{Role: "user", Content: "third user"},
		{Role: "assistant", Content: "third assistant"},
	}
	runner.stepSetMessages(run.ID, seedMessages)

	// Start CompactRun in the background. It will acquire compactMu.Lock, read
	// state.messages, and then block in the summarizer.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type compactResult struct {
		res CompactRunResult
		err error
	}
	compactCh := make(chan compactResult, 1)
	go func() {
		res, err := runner.CompactRun(ctx, run.ID, CompactRunRequest{Mode: "summarize", KeepLast: 1})
		compactCh <- compactResult{res: res, err: err}
	}()

	// Wait until CompactRun is inside the summarizer (widening the race window).
	select {
	case <-provider.enteredSummarize:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for CompactRun summarizer")
	}

	// Append a message while CompactRun is in progress. stepSetMessages must
	// block on compactMu.RLock until CompactRun finishes, then write.
	appended := "appended during compaction"
	current := runner.GetRunMessages(run.ID)
	appendedMessages := append(current, Message{Role: "assistant", Content: appended})
	stepDone := make(chan struct{})
	go func() {
		runner.stepSetMessages(run.ID, appendedMessages)
		close(stepDone)
	}()

	// Widen the window: give stepSetMessages time to hit the lock, then release
	// the summarizer so CompactRun can finish and the blocked writer can proceed.
	time.Sleep(100 * time.Millisecond)
	close(provider.releaseSummarize)

	// Wait for the step write to complete.
	select {
	case <-stepDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for stepSetMessages")
	}

	// Wait for CompactRun.
	var compactRes CompactRunResult
	var compactErr error
	select {
	case cr := <-compactCh:
		compactRes = cr.res
		compactErr = cr.err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for CompactRun")
	}
	if compactErr != nil {
		t.Fatalf("CompactRun: %v", compactErr)
	}
	if compactRes.MessagesRemoved <= 0 {
		t.Fatalf("expected CompactRun to remove messages, got %d", compactRes.MessagesRemoved)
	}

	// The appended message must survive. With the bug, CompactRun would write
	// its stale compacted snapshot over the step append and the message would
	// be lost.
	final := runner.GetRunMessages(run.ID)
	found := false
	for _, m := range final {
		if m.Role == "assistant" && m.Content == appended {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("appended message was lost; final messages: %+v", final)
	}

	// The original transcript must also be preserved (not replaced by a compacted
	// summary). The final state should be the seeded messages plus the append.
	if len(final) != len(seedMessages)+1 {
		t.Fatalf("unexpected final message count: got %d, want %d", len(final), len(seedMessages)+1)
	}
}
