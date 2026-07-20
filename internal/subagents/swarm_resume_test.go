package subagents

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
	tools "go-agent-harness/internal/harness/tools"
)

// eventLog records the ordering between manager starts and steer calls so
// scheduling order can be asserted deterministically.
type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) add(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *eventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

type steerCall struct{ runID, message string }

// fakeRunSteerer implements tools.RunSteerer, recording steered messages.
type fakeRunSteerer struct {
	mu    sync.Mutex
	calls []steerCall
	err   error
	log   *eventLog
}

func (f *fakeRunSteerer) SteerRun(runID, message string) error {
	if f.log != nil {
		f.log.add("steer:" + runID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, steerCall{runID: runID, message: message})
	return f.err
}

func (f *fakeRunSteerer) ParentRunID(string) (string, bool) { return "", false }

func (f *fakeRunSteerer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeRunSteerer) call(i int) steerCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[i]
}

func waitForSteerCount(t *testing.T, s *fakeRunSteerer, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.callCount() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("steer call count did not reach %d (got %d)", want, s.callCount())
}

func TestSwarmResumeHappyPath(t *testing.T) {
	t.Parallel()

	fake := newFakeSwarmManager()
	fake.seed("sub-1", "run-1", string(harness.RunStatusRunning))
	fake.release()
	steerer := &fakeRunSteerer{}
	swarm := NewSwarm(fake, WithSwarmSteerer(steerer))

	res := awaitSwarmRun(t, runSwarmAsync(swarm, context.Background(), SwarmRequest{
		PromptTemplate: "do {{item}}",
		Items:          []string{"a", "b"},
		ResumeAgentIDs: []string{"sub-1"},
	}))
	if res.err != nil {
		t.Fatalf("Run error = %v, want nil", res.err)
	}

	// The resumed subagent receives the first item's expanded prompt through
	// the messaging path (SteerRun on its run ID, resolved via Get).
	if steerer.callCount() != 1 {
		t.Fatalf("steer calls = %d, want 1", steerer.callCount())
	}
	call := steerer.call(0)
	if call.runID != "run-1" || call.message != "do a" {
		t.Fatalf("steer call = (%q, %q), want (%q, %q)", call.runID, call.message, "run-1", "do a")
	}

	// Only one new subagent is created, for the remaining item.
	if fake.startCount() != 1 {
		t.Fatalf("started %d new members, want 1", fake.startCount())
	}

	if res.report.Total != 2 || res.report.Completed != 2 || res.report.Failed != 0 {
		t.Fatalf("report counts = total:%d completed:%d failed:%d, want 2/2/0",
			res.report.Total, res.report.Completed, res.report.Failed)
	}

	// Report order: non-resumed item members first, resumed members last.
	newMember := res.report.Members[0]
	if newMember.Item != "b" || newMember.Resumed {
		t.Fatalf("member 0 = item:%q resumed:%v, want item b not resumed", newMember.Item, newMember.Resumed)
	}
	if newMember.Status != string(harness.RunStatusCompleted) {
		t.Fatalf("member 0 Status = %q, want completed", newMember.Status)
	}
	resumed := res.report.Members[1]
	if resumed.Item != "a" || !resumed.Resumed {
		t.Fatalf("member 1 = item:%q resumed:%v, want item a resumed", resumed.Item, resumed.Resumed)
	}
	if resumed.ID != "sub-1" {
		t.Fatalf("resumed member ID = %q, want sub-1", resumed.ID)
	}
	if resumed.Status != string(harness.RunStatusCompleted) {
		t.Fatalf("resumed member Status = %q, want completed", resumed.Status)
	}
	if resumed.Output != "out-sub-1" {
		t.Fatalf("resumed member Output = %q, want out-sub-1", resumed.Output)
	}
}

func TestSwarmResumeStatusCompatibility(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status  harness.RunStatus
		wantErr bool
	}{
		{harness.RunStatusRunning, false},
		{harness.RunStatusWaitingForUser, false},
		{harness.RunStatusQueued, true},
		{harness.RunStatusCompleted, true},
		{harness.RunStatusFailed, true},
		{harness.RunStatusCancelled, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			t.Parallel()
			fake := newFakeSwarmManager()
			fake.seed("sub-1", "run-1", string(tt.status))
			fake.release()
			steerer := &fakeRunSteerer{}
			swarm := NewSwarm(fake, WithSwarmSteerer(steerer))

			res := awaitSwarmRun(t, runSwarmAsync(swarm, context.Background(), SwarmRequest{
				PromptTemplate: "do {{item}}",
				Items:          []string{"a"},
				ResumeAgentIDs: []string{"sub-1"},
			}))
			if tt.wantErr {
				if res.err == nil {
					t.Fatalf("Run error = nil, want rejection for status %q", tt.status)
				}
				if !errors.Is(res.err, ErrInvalidSwarmRequest) {
					t.Fatalf("Run error = %v, want ErrInvalidSwarmRequest", res.err)
				}
				if !strings.Contains(res.err.Error(), string(tt.status)) {
					t.Fatalf("Run error = %q, want status %q mentioned", res.err.Error(), tt.status)
				}
				if steerer.callCount() != 0 || fake.startCount() != 0 {
					t.Fatalf("rejected request steered %d / started %d members, want 0/0",
						steerer.callCount(), fake.startCount())
				}
				return
			}
			if res.err != nil {
				t.Fatalf("Run error = %v, want nil for status %q", res.err, tt.status)
			}
			if steerer.callCount() != 1 {
				t.Fatalf("steer calls = %d, want 1", steerer.callCount())
			}
		})
	}
}

func TestSwarmResumeUnknownIDRejected(t *testing.T) {
	t.Parallel()

	fake := newFakeSwarmManager()
	steerer := &fakeRunSteerer{}
	swarm := NewSwarm(fake, WithSwarmSteerer(steerer))

	_, err := swarm.Run(context.Background(), SwarmRequest{
		PromptTemplate: "do {{item}}",
		Items:          []string{"a"},
		ResumeAgentIDs: []string{"ghost"},
	})
	if err == nil {
		t.Fatal("Run error = nil, want unknown resume id rejection")
	}
	if !errors.Is(err, ErrInvalidSwarmRequest) {
		t.Fatalf("Run error = %v, want ErrInvalidSwarmRequest", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("Run error = %q, want the unknown id mentioned", err.Error())
	}
	if steerer.callCount() != 0 || fake.startCount() != 0 {
		t.Fatalf("rejected request steered %d / started %d members, want 0/0",
			steerer.callCount(), fake.startCount())
	}
}

func TestSwarmResumeMissingSteererRejected(t *testing.T) {
	t.Parallel()

	fake := newFakeSwarmManager()
	fake.seed("sub-1", "run-1", string(harness.RunStatusRunning))
	swarm := NewSwarm(fake) // no WithSwarmSteerer

	_, err := swarm.Run(context.Background(), SwarmRequest{
		PromptTemplate: "do {{item}}",
		Items:          []string{"a"},
		ResumeAgentIDs: []string{"sub-1"},
	})
	if err == nil {
		t.Fatal("Run error = nil, want missing steerer rejection")
	}
	if !strings.Contains(err.Error(), "steerer") {
		t.Fatalf("Run error = %q, want steerer mentioned", err.Error())
	}
	if fake.startCount() != 0 {
		t.Fatalf("rejected request started %d members, want 0", fake.startCount())
	}
}

func TestSwarmResumeScheduledBeforeNewItems(t *testing.T) {
	t.Parallel()

	log := &eventLog{}
	fake := newFakeSwarmManager()
	fake.log = log
	fake.seed("sub-1", "run-1", string(harness.RunStatusRunning))
	steerer := &fakeRunSteerer{log: log}
	mt := newManualTicker()
	swarm := NewSwarm(fake,
		WithSwarmSteerer(steerer),
		WithSwarmMaxConcurrency(1),
		withSwarmTickerFactory(manualTickerFactory(mt)),
	)

	done := runSwarmAsync(swarm, context.Background(), SwarmRequest{
		PromptTemplate: "do {{item}}",
		Items:          []string{"a", "b", "c"},
		ResumeAgentIDs: []string{"sub-1"},
	})

	// With cap 1 the resume occupies the only slot first.
	waitForSteerCount(t, steerer, 1)
	time.Sleep(50 * time.Millisecond)
	if got := fake.startCount(); got != 0 {
		t.Fatalf("new members started before the resume completed: %d, want 0", got)
	}

	fake.finish("sub-1", tools.SubagentResult{ID: "sub-1", Status: string(harness.RunStatusCompleted), Output: "resumed-done"})
	waitForStartCount(t, fake, 1)
	fake.finish(fake.idForPrompt("do b"), tools.SubagentResult{Status: string(harness.RunStatusCompleted)})
	waitForStartCount(t, fake, 2)
	fake.finish(fake.idForPrompt("do c"), tools.SubagentResult{Status: string(harness.RunStatusCompleted)})

	res := awaitSwarmRun(t, done)
	if res.err != nil {
		t.Fatalf("Run error = %v, want nil", res.err)
	}

	wantOrder := []string{"steer:run-1", "start:do b", "start:do c"}
	gotOrder := log.snapshot()
	if len(gotOrder) != len(wantOrder) {
		t.Fatalf("event order = %v, want %v", gotOrder, wantOrder)
	}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Fatalf("event order = %v, want %v", gotOrder, wantOrder)
		}
	}

	// Report order: new item members first (item order), resumed last.
	if res.report.Members[0].Item != "b" || res.report.Members[1].Item != "c" {
		t.Fatalf("report items = %q, %q, ..., want b, c first",
			res.report.Members[0].Item, res.report.Members[1].Item)
	}
	last := res.report.Members[2]
	if last.Item != "a" || !last.Resumed || last.ID != "sub-1" {
		t.Fatalf("report member 2 = item:%q resumed:%v id:%q, want a/true/sub-1",
			last.Item, last.Resumed, last.ID)
	}
	if last.Output != "resumed-done" {
		t.Fatalf("resumed member Output = %q, want resumed-done", last.Output)
	}
}

func TestSwarmResumeSteerFailureIsCaptured(t *testing.T) {
	t.Parallel()

	fake := newFakeSwarmManager()
	fake.seed("sub-1", "run-1", string(harness.RunStatusRunning))
	fake.release()
	steerer := &fakeRunSteerer{err: errors.New("steering buffer full")}
	swarm := NewSwarm(fake, WithSwarmSteerer(steerer))

	res := awaitSwarmRun(t, runSwarmAsync(swarm, context.Background(), SwarmRequest{
		PromptTemplate: "do {{item}}",
		Items:          []string{"a", "b"},
		ResumeAgentIDs: []string{"sub-1"},
	}))
	if res.err != nil {
		t.Fatalf("Run error = %v, want nil (steer failures live in the report)", res.err)
	}
	if res.report.Completed != 1 || res.report.Failed != 1 {
		t.Fatalf("report counts = completed:%d failed:%d, want 1/1", res.report.Completed, res.report.Failed)
	}
	resumed := res.report.Members[1]
	if resumed.Status != string(harness.RunStatusFailed) {
		t.Fatalf("resumed member Status = %q, want failed", resumed.Status)
	}
	if !strings.Contains(resumed.Error, "steering buffer full") {
		t.Fatalf("resumed member Error = %q, want steering buffer full", resumed.Error)
	}
	if resumed.ID != "sub-1" || !resumed.Resumed {
		t.Fatalf("resumed member = id:%q resumed:%v, want sub-1/true", resumed.ID, resumed.Resumed)
	}
	if res.report.Members[0].Status != string(harness.RunStatusCompleted) {
		t.Fatalf("new member Status = %q, want completed", res.report.Members[0].Status)
	}
}

func TestSwarmCancellationCancelsResumedMembers(t *testing.T) {
	t.Parallel()

	fake := newFakeSwarmManager()
	fake.seed("sub-1", "run-1", string(harness.RunStatusRunning))
	steerer := &fakeRunSteerer{}
	mt := newManualTicker()
	swarm := NewSwarm(fake, WithSwarmSteerer(steerer), withSwarmTickerFactory(manualTickerFactory(mt)))

	ctx, cancel := context.WithCancel(context.Background())
	done := runSwarmAsync(swarm, ctx, SwarmRequest{
		PromptTemplate: "do {{item}}",
		Items:          []string{"a", "b", "c"},
		ResumeAgentIDs: []string{"sub-1"},
	})

	waitForSteerCount(t, steerer, 1)
	waitForStartCount(t, fake, 2)
	cancel()

	res := awaitSwarmRun(t, done)
	if !errors.Is(res.err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", res.err)
	}
	// The resumed member is cancelled through the manager like any started member.
	if got := fake.cancelCount(); got != 3 {
		t.Fatalf("manager Cancel calls = %d, want 3 (resume + 2 new)", got)
	}
	if res.report.Cancelled != 3 {
		t.Fatalf("report Cancelled = %d, want 3", res.report.Cancelled)
	}
	resumed := res.report.Members[2]
	if resumed.Status != string(harness.RunStatusCancelled) || resumed.ID != "sub-1" || !resumed.Resumed {
		t.Fatalf("resumed member = status:%q id:%q resumed:%v, want cancelled/sub-1/true",
			resumed.Status, resumed.ID, resumed.Resumed)
	}
}
