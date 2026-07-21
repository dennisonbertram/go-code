package subagents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
	tools "go-agent-harness/internal/harness/tools"
)

// fakeSwarmManager implements tools.SubagentManager with controllable Wait
// semantics so ramp, cancellation, and aggregation behavior can be observed
// deterministically.
type fakeSwarmManager struct {
	mu          sync.Mutex
	started     []tools.SubagentRequest
	ids         []string
	chans       map[string]chan tools.SubagentResult
	seeded      map[string]tools.SubagentResult
	cancelled   []string
	releaseCh   chan struct{}
	released    bool
	startErr    error
	startErrAt  int // 0-based start index that fails with startErr; -1 disables
	startsTotal int
	log         *eventLog
}

func newFakeSwarmManager() *fakeSwarmManager {
	return &fakeSwarmManager{
		chans:      make(map[string]chan tools.SubagentResult),
		seeded:     make(map[string]tools.SubagentResult),
		releaseCh:  make(chan struct{}),
		startErrAt: -1,
	}
}

// seed registers a pre-existing subagent (for resume_agent_ids tests) with a
// wait channel like a started member's.
func (f *fakeSwarmManager) seed(id, runID, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seeded[id] = tools.SubagentResult{ID: id, RunID: runID, Status: status}
	f.chans[id] = make(chan tools.SubagentResult, 1)
}

func (f *fakeSwarmManager) Start(_ context.Context, req tools.SubagentRequest) (tools.SubagentResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil && (f.startErrAt < 0 || f.startErrAt == f.startsTotal) {
		f.startsTotal++
		return tools.SubagentResult{}, f.startErr
	}
	f.startsTotal++
	id := fmt.Sprintf("member-%d", f.startsTotal)
	f.started = append(f.started, req)
	f.ids = append(f.ids, id)
	f.chans[id] = make(chan tools.SubagentResult, 1)
	if f.log != nil {
		f.log.add("start:" + req.Prompt)
	}
	return tools.SubagentResult{ID: id, RunID: id + "-run", Status: string(harness.RunStatusRunning)}, nil
}

func (f *fakeSwarmManager) Wait(ctx context.Context, id string) (tools.SubagentResult, error) {
	f.mu.Lock()
	ch := f.chans[id]
	f.mu.Unlock()
	if ch == nil {
		return tools.SubagentResult{}, fmt.Errorf("unknown subagent %q", id)
	}
	select {
	case res := <-ch:
		return res, nil
	case <-f.releaseCh:
		// A per-member finish() may already be buffered; it wins over the
		// generic release result so tests stay deterministic.
		select {
		case res := <-ch:
			return res, nil
		default:
		}
		return tools.SubagentResult{ID: id, Status: string(harness.RunStatusCompleted), Output: "out-" + id}, nil
	case <-ctx.Done():
		return tools.SubagentResult{}, ctx.Err()
	}
}

func (f *fakeSwarmManager) CreateAndWait(ctx context.Context, req tools.SubagentRequest) (tools.SubagentResult, error) {
	res, err := f.Start(ctx, req)
	if err != nil {
		return tools.SubagentResult{}, err
	}
	return f.Wait(ctx, res.ID)
}

func (f *fakeSwarmManager) Get(_ context.Context, id string) (tools.SubagentResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, known := range f.ids {
		if known == id {
			return tools.SubagentResult{ID: id, Status: string(harness.RunStatusRunning)}, nil
		}
	}
	if res, ok := f.seeded[id]; ok {
		return res, nil
	}
	return tools.SubagentResult{}, fmt.Errorf("unknown subagent %q", id)
}

func (f *fakeSwarmManager) Cancel(_ context.Context, id string) error {
	f.mu.Lock()
	f.cancelled = append(f.cancelled, id)
	ch := f.chans[id]
	f.mu.Unlock()
	if ch != nil {
		select {
		case ch <- tools.SubagentResult{ID: id, Status: string(harness.RunStatusCancelled), Error: "cancelled"}:
		default:
		}
	}
	return nil
}

// release makes every current and future Wait return a completed result.
func (f *fakeSwarmManager) release() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.released {
		f.released = true
		close(f.releaseCh)
	}
}

// finish completes a single member's Wait with the given result.
func (f *fakeSwarmManager) finish(id string, res tools.SubagentResult) {
	f.mu.Lock()
	ch := f.chans[id]
	f.mu.Unlock()
	if ch != nil {
		select {
		case ch <- res:
		default:
		}
	}
}

func (f *fakeSwarmManager) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

func (f *fakeSwarmManager) cancelCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cancelled)
}

// idForPrompt returns the member id whose start request carried the given
// prompt. Member ids are assigned in Start-call order, which is concurrent,
// so tests must resolve ids through the recorded prompts instead of guessing
// "member-N".
func (f *fakeSwarmManager) idForPrompt(prompt string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, req := range f.started {
		if req.Prompt == prompt {
			return f.ids[i]
		}
	}
	return ""
}

// manualTicker is a test ticker fired explicitly by the test.
type manualTicker struct{ ch chan time.Time }

func newManualTicker() *manualTicker { return &manualTicker{ch: make(chan time.Time)} }

func (m *manualTicker) Chan() <-chan time.Time { return m.ch }

func (m *manualTicker) Stop() {}

func (m *manualTicker) tick() { m.ch <- time.Now() }

func manualTickerFactory(mt *manualTicker) func(time.Duration) swarmTicker {
	return func(time.Duration) swarmTicker { return mt }
}

type swarmRunResult struct {
	report SwarmReport
	err    error
}

func runSwarmAsync(swarm *Swarm, ctx context.Context, req SwarmRequest) chan swarmRunResult {
	done := make(chan swarmRunResult, 1)
	go func() {
		report, err := swarm.Run(ctx, req)
		done <- swarmRunResult{report: report, err: err}
	}()
	return done
}

func awaitSwarmRun(t *testing.T, done chan swarmRunResult) swarmRunResult {
	t.Helper()
	select {
	case res := <-done:
		return res
	case <-time.After(10 * time.Second):
		t.Fatal("swarm Run did not return within 10s")
		return swarmRunResult{}
	}
}

func waitForStartCount(t *testing.T, f *fakeSwarmManager, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if f.startCount() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("start count did not reach %d (got %d)", want, f.startCount())
}

func swarmItems(n int) []string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf("item-%d", i)
	}
	return items
}

func TestSwarmRunValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     SwarmRequest
		wantErr string
	}{
		{
			name:    "template missing placeholder",
			req:     SwarmRequest{PromptTemplate: "review the code", Items: []string{"a"}},
			wantErr: "{{item}}",
		},
		{
			name:    "empty template",
			req:     SwarmRequest{PromptTemplate: "", Items: []string{"a"}},
			wantErr: "{{item}}",
		},
		{
			name:    "no items",
			req:     SwarmRequest{PromptTemplate: "do {{item}}", Items: nil},
			wantErr: "at least 1",
		},
		{
			name:    "too many items",
			req:     SwarmRequest{PromptTemplate: "do {{item}}", Items: swarmItems(SwarmMaxMembers + 1)},
			wantErr: "max",
		},
		{
			name:    "duplicate items expand to same prompt",
			req:     SwarmRequest{PromptTemplate: "do {{item}}", Items: []string{"a", "b", "a"}},
			wantErr: "same prompt",
		},
		{
			name:    "whitespace-only difference still duplicates",
			req:     SwarmRequest{PromptTemplate: "do {{item}}", Items: []string{"a", "a "}},
			wantErr: "same prompt",
		},
		{
			name:    "more resume_agent_ids than items",
			req:     SwarmRequest{PromptTemplate: "do {{item}}", Items: []string{"a"}, ResumeAgentIDs: []string{"s1", "s2"}},
			wantErr: "resume_agent_ids",
		},
		{
			name:    "duplicate resume_agent_ids",
			req:     SwarmRequest{PromptTemplate: "do {{item}}", Items: []string{"a", "b"}, ResumeAgentIDs: []string{"s1", "s1"}},
			wantErr: "duplicate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fake := newFakeSwarmManager()
			swarm := NewSwarm(fake)
			_, err := swarm.Run(context.Background(), tt.req)
			if err == nil {
				t.Fatalf("Run error = nil, want error containing %q", tt.wantErr)
			}
			if !errors.Is(err, ErrInvalidSwarmRequest) {
				t.Fatalf("Run error = %v, want ErrInvalidSwarmRequest", err)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Run error = %q, want substring %q", err.Error(), tt.wantErr)
			}
			if fake.startCount() != 0 {
				t.Fatalf("invalid request started %d members, want 0", fake.startCount())
			}
		})
	}
}

func TestSwarmRunValidationAcceptsMaxItems(t *testing.T) {
	t.Parallel()

	fake := newFakeSwarmManager()
	fake.release()
	swarm := NewSwarm(fake)
	res := awaitSwarmRun(t, runSwarmAsync(swarm, context.Background(), SwarmRequest{
		PromptTemplate: "do {{item}}",
		Items:          swarmItems(SwarmMaxMembers),
	}))
	if res.err != nil {
		t.Fatalf("Run with %d items error = %v, want nil", SwarmMaxMembers, res.err)
	}
	if res.report.Total != SwarmMaxMembers {
		t.Fatalf("report Total = %d, want %d", res.report.Total, SwarmMaxMembers)
	}
	if fake.startCount() != SwarmMaxMembers {
		t.Fatalf("started %d members, want %d", fake.startCount(), SwarmMaxMembers)
	}
}

func TestResolveSwarmMaxConcurrency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want int
	}{
		{name: "unset defaults to 128", env: nil, want: SwarmMaxMembers},
		{name: "explicit value", env: map[string]string{SwarmMaxConcurrencyEnv: "8"}, want: 8},
		{name: "zero falls back to default", env: map[string]string{SwarmMaxConcurrencyEnv: "0"}, want: SwarmMaxMembers},
		{name: "negative falls back to default", env: map[string]string{SwarmMaxConcurrencyEnv: "-3"}, want: SwarmMaxMembers},
		{name: "garbage falls back to default", env: map[string]string{SwarmMaxConcurrencyEnv: "abc"}, want: SwarmMaxMembers},
		{name: "above cap clamped to 128", env: map[string]string{SwarmMaxConcurrencyEnv: "999"}, want: SwarmMaxMembers},
		{name: "whitespace trimmed", env: map[string]string{SwarmMaxConcurrencyEnv: " 4 "}, want: 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			getenv := func(key string) string { return tt.env[key] }
			if got := resolveSwarmMaxConcurrency(getenv); got != tt.want {
				t.Fatalf("resolveSwarmMaxConcurrency(%v) = %d, want %d", tt.env, got, tt.want)
			}
		})
	}
}

func TestSwarmRunAggregatesReport(t *testing.T) {
	t.Parallel()

	fake := newFakeSwarmManager()
	fake.release()
	swarm := NewSwarm(fake)
	items := []string{"alpha", "beta", "gamma"}
	res := awaitSwarmRun(t, runSwarmAsync(swarm, context.Background(), SwarmRequest{
		PromptTemplate: "Review {{item}} carefully",
		Items:          items,
		Model:          "gpt-swarm",
		MaxSteps:       7,
		ProfileName:    "explorer",
		AllowedTools:   []string{"read"},
	}))
	if res.err != nil {
		t.Fatalf("Run error = %v, want nil", res.err)
	}

	report := res.report
	if report.Total != 3 || report.Completed != 3 || report.Failed != 0 || report.Cancelled != 0 {
		t.Fatalf("report counts = total:%d completed:%d failed:%d cancelled:%d, want 3/3/0/0",
			report.Total, report.Completed, report.Failed, report.Cancelled)
	}
	if len(report.Members) != len(items) {
		t.Fatalf("report has %d members, want %d", len(report.Members), len(items))
	}
	for i, item := range items {
		m := report.Members[i]
		if m.Item != item {
			t.Errorf("member %d Item = %q, want %q (deterministic item order)", i, m.Item, item)
		}
		if m.Prompt != "Review "+item+" carefully" {
			t.Errorf("member %d Prompt = %q, want expanded template", i, m.Prompt)
		}
		if m.ID == "" {
			t.Errorf("member %d ID is empty, want subagent id", i)
		}
		if m.Status != string(harness.RunStatusCompleted) {
			t.Errorf("member %d Status = %q, want completed", i, m.Status)
		}
		if m.Output == "" {
			t.Errorf("member %d Output is empty, want member output", i)
		}
	}

	// Member requests must carry the expanded prompt and the profile/model
	// overrides. Start-call order is concurrent, so compare as a set.
	if fake.startCount() != len(items) {
		t.Fatalf("started %d members, want %d", fake.startCount(), len(items))
	}
	fake.mu.Lock()
	startedReqs := append([]tools.SubagentRequest(nil), fake.started...)
	fake.mu.Unlock()
	seenPrompts := make(map[string]bool, len(startedReqs))
	for _, req := range startedReqs {
		seenPrompts[req.Prompt] = true
		if req.Model != "gpt-swarm" || req.MaxSteps != 7 || req.ProfileName != "explorer" {
			t.Errorf("started request overrides = model:%q steps:%d profile:%q, want gpt-swarm/7/explorer",
				req.Model, req.MaxSteps, req.ProfileName)
		}
		if len(req.AllowedTools) != 1 || req.AllowedTools[0] != "read" {
			t.Errorf("started request AllowedTools = %v, want [read]", req.AllowedTools)
		}
	}
	for _, item := range items {
		want := "Review " + item + " carefully"
		if !seenPrompts[want] {
			t.Errorf("no member started with prompt %q; got %v", want, seenPrompts)
		}
	}
}

func TestSwarmRampConcurrency(t *testing.T) {
	t.Parallel()

	fake := newFakeSwarmManager()
	mt := newManualTicker()
	swarm := NewSwarm(fake,
		WithSwarmMaxConcurrency(10),
		withSwarmTickerFactory(manualTickerFactory(mt)),
	)

	done := runSwarmAsync(swarm, context.Background(), SwarmRequest{
		PromptTemplate: "do {{item}}",
		Items:          swarmItems(10),
	})

	// Initial burst: exactly 5 in-flight members before the first tick.
	waitForStartCount(t, fake, 5)
	time.Sleep(50 * time.Millisecond)
	if got := fake.startCount(); got != 5 {
		t.Fatalf("in-flight before first tick = %d, want 5", got)
	}

	// Each ramp tick allows exactly one more concurrent member.
	mt.tick()
	waitForStartCount(t, fake, 6)
	time.Sleep(50 * time.Millisecond)
	if got := fake.startCount(); got != 6 {
		t.Fatalf("in-flight after first tick = %d, want 6", got)
	}

	mt.tick()
	waitForStartCount(t, fake, 7)
	time.Sleep(50 * time.Millisecond)
	if got := fake.startCount(); got != 7 {
		t.Fatalf("in-flight after second tick = %d, want 7", got)
	}

	fake.release()
	res := awaitSwarmRun(t, done)
	if res.err != nil {
		t.Fatalf("Run error = %v, want nil", res.err)
	}
	if res.report.Completed != 10 {
		t.Fatalf("report Completed = %d, want 10", res.report.Completed)
	}
	if fake.startCount() != 10 {
		t.Fatalf("started %d members, want 10", fake.startCount())
	}
}

func TestSwarmRampAllowsReplacementAfterCompletion(t *testing.T) {
	t.Parallel()

	fake := newFakeSwarmManager()
	mt := newManualTicker()
	swarm := NewSwarm(fake,
		WithSwarmMaxConcurrency(3),
		withSwarmTickerFactory(manualTickerFactory(mt)),
	)

	done := runSwarmAsync(swarm, context.Background(), SwarmRequest{
		PromptTemplate: "do {{item}}",
		Items:          swarmItems(6),
	})

	// Cap of 3 is below the initial ramp of 5, so only 3 start.
	waitForStartCount(t, fake, 3)
	time.Sleep(50 * time.Millisecond)
	if got := fake.startCount(); got != 3 {
		t.Fatalf("in-flight with cap 3 = %d, want 3", got)
	}

	// Completing a member frees a slot even without a tick. Member ids are
	// assigned in concurrent Start-call order, so resolve via the prompt.
	firstID := fake.idForPrompt("do item-0")
	if firstID == "" {
		t.Fatal("no member started for item-0")
	}
	fake.finish(firstID, tools.SubagentResult{ID: firstID, Status: string(harness.RunStatusCompleted), Output: "first"})
	waitForStartCount(t, fake, 4)

	fake.release()
	res := awaitSwarmRun(t, done)
	if res.err != nil {
		t.Fatalf("Run error = %v, want nil", res.err)
	}
	if res.report.Completed != 6 {
		t.Fatalf("report Completed = %d, want 6", res.report.Completed)
	}
	if res.report.Members[0].Output != "first" {
		t.Fatalf("member 0 Output = %q, want %q", res.report.Members[0].Output, "first")
	}
}

func TestSwarmMaxConcurrencyEnvCap(t *testing.T) {
	// Not parallel: t.Setenv forbids parallel tests.
	t.Setenv(SwarmMaxConcurrencyEnv, "2")
	fake := newFakeSwarmManager()
	mt := newManualTicker()
	swarm := NewSwarm(fake, withSwarmTickerFactory(manualTickerFactory(mt)))

	done := runSwarmAsync(swarm, context.Background(), SwarmRequest{
		PromptTemplate: "do {{item}}",
		Items:          swarmItems(4),
	})

	// Env cap of 2 overrides the default initial ramp of 5.
	waitForStartCount(t, fake, 2)

	// Ticks must not grow the allowance past the env cap.
	for i := 0; i < 3; i++ {
		mt.tick()
	}
	time.Sleep(50 * time.Millisecond)
	if got := fake.startCount(); got != 2 {
		t.Fatalf("in-flight after ticks with env cap 2 = %d, want 2", got)
	}

	fake.release()
	res := awaitSwarmRun(t, done)
	if res.err != nil {
		t.Fatalf("Run error = %v, want nil", res.err)
	}
	if res.report.Completed != 4 {
		t.Fatalf("report Completed = %d, want 4", res.report.Completed)
	}
}

func TestSwarmCancellationCancelsAllMembers(t *testing.T) {
	t.Parallel()

	fake := newFakeSwarmManager()
	mt := newManualTicker()
	swarm := NewSwarm(fake, withSwarmTickerFactory(manualTickerFactory(mt)))

	ctx, cancel := context.WithCancel(context.Background())
	done := runSwarmAsync(swarm, ctx, SwarmRequest{
		PromptTemplate: "do {{item}}",
		Items:          swarmItems(6),
	})

	waitForStartCount(t, fake, 5)
	cancel()

	res := awaitSwarmRun(t, done)
	if !errors.Is(res.err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", res.err)
	}

	// Every started member must be cancelled through the manager.
	if got := fake.cancelCount(); got != 5 {
		t.Fatalf("manager Cancel calls = %d, want 5 (one per started member)", got)
	}

	// Every member in the report must be in a terminal state; the member that
	// never started is reported cancelled with no id.
	if len(res.report.Members) != 6 {
		t.Fatalf("report has %d members, want 6", len(res.report.Members))
	}
	for i, m := range res.report.Members {
		if m.Status != string(harness.RunStatusCancelled) {
			t.Errorf("member %d Status = %q, want cancelled", i, m.Status)
		}
		if i < 5 && m.ID == "" {
			t.Errorf("started member %d has empty ID", i)
		}
	}
	if res.report.Members[5].ID != "" {
		t.Errorf("unstarted member ID = %q, want empty", res.report.Members[5].ID)
	}
	if res.report.Cancelled != 6 || res.report.Completed != 0 || res.report.Failed != 0 {
		t.Fatalf("report counts = completed:%d failed:%d cancelled:%d, want 0/0/6",
			res.report.Completed, res.report.Failed, res.report.Cancelled)
	}
}

func TestSwarmMemberFailureDoesNotAbortCohort(t *testing.T) {
	t.Parallel()

	fake := newFakeSwarmManager()
	mt := newManualTicker()
	swarm := NewSwarm(fake, withSwarmTickerFactory(manualTickerFactory(mt)))

	done := runSwarmAsync(swarm, context.Background(), SwarmRequest{
		PromptTemplate: "do {{item}}",
		Items:          swarmItems(3),
	})

	waitForStartCount(t, fake, 3)
	failID := fake.idForPrompt("do item-1")
	if failID == "" {
		t.Fatal("no member started for item-1")
	}
	fake.finish(failID, tools.SubagentResult{ID: failID, Status: string(harness.RunStatusFailed), Error: "boom"})
	fake.release()

	res := awaitSwarmRun(t, done)
	if res.err != nil {
		t.Fatalf("Run error = %v, want nil (member failures live in the report)", res.err)
	}
	if res.report.Completed != 2 || res.report.Failed != 1 {
		t.Fatalf("report counts = completed:%d failed:%d, want 2/1", res.report.Completed, res.report.Failed)
	}
	failed := res.report.Members[1]
	if failed.Status != string(harness.RunStatusFailed) || failed.Error != "boom" {
		t.Fatalf("member 1 = status:%q error:%q, want failed/boom", failed.Status, failed.Error)
	}
	if res.report.Members[0].Status != string(harness.RunStatusCompleted) || res.report.Members[2].Status != string(harness.RunStatusCompleted) {
		t.Fatalf("members 0 and 2 must complete, got %q and %q", res.report.Members[0].Status, res.report.Members[2].Status)
	}
}

func TestSwarmMemberStartFailureIsCaptured(t *testing.T) {
	t.Parallel()

	fake := newFakeSwarmManager()
	fake.startErr = errors.New("start boom")
	fake.startErrAt = 1
	fake.release()
	swarm := NewSwarm(fake)

	res := awaitSwarmRun(t, runSwarmAsync(swarm, context.Background(), SwarmRequest{
		PromptTemplate: "do {{item}}",
		Items:          swarmItems(3),
	}))
	if res.err != nil {
		t.Fatalf("Run error = %v, want nil (start failures live in the report)", res.err)
	}
	if res.report.Completed != 2 || res.report.Failed != 1 {
		t.Fatalf("report counts = completed:%d failed:%d, want 2/1", res.report.Completed, res.report.Failed)
	}
	// The second Start call fails; which item that is depends on concurrent
	// start order, so assert the failure shape rather than the position.
	failedCount := 0
	for _, m := range res.report.Members {
		if m.Status == string(harness.RunStatusFailed) {
			failedCount++
			if !strings.Contains(m.Error, "start boom") {
				t.Fatalf("failed member Error = %q, want start boom", m.Error)
			}
			if m.ID != "" {
				t.Fatalf("failed member ID = %q, want empty (never started)", m.ID)
			}
		}
	}
	if failedCount != 1 {
		t.Fatalf("failed members = %d, want exactly 1", failedCount)
	}
}

func TestSwarmMembersExcludeAgentSwarmFromToolSet(t *testing.T) {
	t.Parallel()

	fake := newFakeSwarmManager()
	fake.release()
	swarm := NewSwarm(fake)
	res := awaitSwarmRun(t, runSwarmAsync(swarm, context.Background(), SwarmRequest{
		PromptTemplate: "do {{item}}",
		Items:          []string{"a", "b"},
	}))
	if res.err != nil {
		t.Fatalf("Run error = %v, want nil", res.err)
	}
	// Swarm members must never receive agent_swarm in their own tool set
	// (nested swarms are out of scope), regardless of profile tool grants.
	for i, req := range fake.started {
		found := false
		for _, name := range req.DeniedTools {
			if name == "agent_swarm" {
				found = true
			}
		}
		if !found {
			t.Errorf("member %d DeniedTools = %v, want agent_swarm excluded", i, req.DeniedTools)
		}
	}
}

func TestSwarmRunWithInlineManager(t *testing.T) {
	t.Parallel()

	inlineRunner := newTestRunner(t.TempDir(), "inline done")
	mgr, err := NewManager(Options{InlineRunner: inlineRunner})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	swarm := NewSwarm(NewInlineManager(mgr))

	res := awaitSwarmRun(t, runSwarmAsync(swarm, context.Background(), SwarmRequest{
		PromptTemplate: "say {{item}}",
		Items:          []string{"a", "b", "c"},
	}))
	if res.err != nil {
		t.Fatalf("Run error = %v, want nil", res.err)
	}
	if res.report.Completed != 3 || res.report.Failed != 0 {
		t.Fatalf("report counts = completed:%d failed:%d, want 3/0", res.report.Completed, res.report.Failed)
	}
	for i, m := range res.report.Members {
		if m.Status != string(harness.RunStatusCompleted) {
			t.Errorf("member %d Status = %q, want completed", i, m.Status)
		}
		if m.Output != "inline done" {
			t.Errorf("member %d Output = %q, want %q", i, m.Output, "inline done")
		}
		// The member must be a real subagent known to the manager.
		sa, err := mgr.Get(context.Background(), m.ID)
		if err != nil {
			t.Errorf("manager Get(%q): %v", m.ID, err)
			continue
		}
		if sa.Status != harness.RunStatusCompleted {
			t.Errorf("manager status for member %d = %q, want completed", i, sa.Status)
		}
	}
}
