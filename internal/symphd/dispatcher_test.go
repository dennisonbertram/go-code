package symphd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-agent-harness/internal/workspace"
)

// ---------------------------------------------------------------------------
// Mock workspace
// ---------------------------------------------------------------------------

type mockWorkspace struct {
	mu           sync.Mutex
	provisioned  bool
	destroyed    bool
	destroyCount int
	provideErr   error
	path         string
	harnessURL   string
}

func (m *mockWorkspace) Provision(_ context.Context, _ workspace.Options) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.provideErr != nil {
		return m.provideErr
	}
	m.provisioned = true
	return nil
}

func (m *mockWorkspace) HarnessURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.harnessURL
}

func (m *mockWorkspace) WorkspacePath() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.path
}

func (m *mockWorkspace) Destroy(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.destroyed = true
	m.destroyCount++
	return nil
}

// ---------------------------------------------------------------------------
// Mock harness client
// ---------------------------------------------------------------------------

type mockHarnessClient struct {
	startFunc  func(ctx context.Context, prompt, path string) (string, error)
	statusFunc func(ctx context.Context, runID string) (string, error)
}

func (m *mockHarnessClient) StartRun(ctx context.Context, prompt, path string) (string, error) {
	return m.startFunc(ctx, prompt, path)
}

func (m *mockHarnessClient) RunStatus(ctx context.Context, runID string) (string, error) {
	return m.statusFunc(ctx, runID)
}

// ---------------------------------------------------------------------------
// Mock tracker
// ---------------------------------------------------------------------------

type mockTracker struct {
	mu       sync.Mutex
	issues   map[int]*TrackedIssue
	started  []int
	complete []int
	failed   []int
	reset    []int
}

func newMockTracker(issues ...*TrackedIssue) *mockTracker {
	m := &mockTracker{issues: make(map[int]*TrackedIssue)}
	for _, issue := range issues {
		m.issues[issue.Number] = issue
	}
	return m
}

func (m *mockTracker) Poll(_ context.Context) error { return nil }

func (m *mockTracker) Candidates() []*TrackedIssue {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*TrackedIssue
	for _, i := range m.issues {
		if i.ClaimState == ClaimStateClaimed {
			cp := *i
			out = append(out, &cp)
		}
	}
	return out
}

func (m *mockTracker) Claim(number int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.issues[number]
	if !ok {
		return fmt.Errorf("not found: %d", number)
	}
	i.ClaimState = ClaimStateClaimed
	return nil
}

func (m *mockTracker) Start(number int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.issues[number]
	if !ok {
		return fmt.Errorf("not found: %d", number)
	}
	if i.ClaimState != ClaimStateClaimed {
		return fmt.Errorf("issue #%d is %s, cannot start", number, i.ClaimState)
	}
	i.ClaimState = ClaimStateRunning
	m.started = append(m.started, number)
	return nil
}

func (m *mockTracker) Complete(number int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.issues[number]
	if !ok {
		return fmt.Errorf("not found: %d", number)
	}
	if i.ClaimState != ClaimStateRunning {
		return fmt.Errorf("issue #%d is %s, cannot complete", number, i.ClaimState)
	}
	i.ClaimState = ClaimStateDone
	m.complete = append(m.complete, number)
	return nil
}

func (m *mockTracker) Fail(number int, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.issues[number]
	if !ok {
		return fmt.Errorf("not found: %d", number)
	}
	if i.ClaimState != ClaimStateRunning {
		return fmt.Errorf("issue #%d is %s, cannot fail: %s", number, i.ClaimState, reason)
	}
	i.ClaimState = ClaimStateFailed
	m.failed = append(m.failed, number)
	return nil
}

func (m *mockTracker) Issues() []*TrackedIssue {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*TrackedIssue, 0, len(m.issues))
	for _, i := range m.issues {
		cp := *i
		out = append(out, &cp)
	}
	return out
}

func (m *mockTracker) Reset(number int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	issue, ok := m.issues[number]
	if !ok {
		return fmt.Errorf("issue %d not found", number)
	}
	if issue.ClaimState != ClaimStateFailed {
		return fmt.Errorf("issue %d is not in Failed state", number)
	}
	issue.ClaimState = ClaimStateUnclaimed
	issue.Attempts++
	m.reset = append(m.reset, number)
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func claimedIssue(n int) *TrackedIssue {
	return &TrackedIssue{
		Number:     n,
		Title:      fmt.Sprintf("Issue %d", n),
		Body:       fmt.Sprintf("Body for issue %d", n),
		ClaimState: ClaimStateClaimed,
	}
}

func fastDispatchConfig() DispatchConfig {
	return DispatchConfig{
		MaxConcurrent: 2,
		StallTimeout:  200 * time.Millisecond,
		HarnessURL:    "http://localhost:8080",
		PollInterval:  10 * time.Millisecond,
	}
}

// newHealthzServer starts a test HTTP server that returns 200 on /healthz.
// The caller must call Close() when done.
func newHealthzServer() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return httptest.NewServer(mux)
}

// wsFactory returns a WorkspaceFactory that always returns ws.
func wsFactory(ws workspace.Workspace) WorkspaceFactory {
	return func() workspace.Workspace { return ws }
}

// clFactory returns a HarnessClientFactory that always returns cl regardless of URL.
func clFactory(cl HarnessClient) HarnessClientFactory {
	return func(_ string) HarnessClient { return cl }
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestNewDispatcher verifies constructor sets fields correctly.
func TestNewDispatcher(t *testing.T) {
	ws := &mockWorkspace{}
	tr := newMockTracker()
	cl := &mockHarnessClient{}
	cfg := DispatchConfig{MaxConcurrent: 3, StallTimeout: time.Minute, HarnessURL: "http://example.com"}
	d := NewDispatcher(cfg, wsFactory(ws), tr, clFactory(cl))

	if d == nil {
		t.Fatal("NewDispatcher returned nil")
	}
	if cap(d.sem) != 3 {
		t.Errorf("semaphore capacity = %d, want 3", cap(d.sem))
	}
	if d.config.StallTimeout != time.Minute {
		t.Errorf("StallTimeout = %v, want 1m", d.config.StallTimeout)
	}
	if d.results == nil {
		t.Error("results channel is nil")
	}
	if d.running == nil {
		t.Error("running map is nil")
	}
}

// TestNewDispatcher_Defaults verifies zero-value fields receive sensible defaults.
func TestNewDispatcher_Defaults(t *testing.T) {
	ws := &mockWorkspace{}
	tr := newMockTracker()
	cl := &mockHarnessClient{}
	d := NewDispatcher(DispatchConfig{}, wsFactory(ws), tr, clFactory(cl))

	if d.config.StallTimeout != 5*time.Minute {
		t.Errorf("default StallTimeout = %v, want 5m", d.config.StallTimeout)
	}
	if d.config.PollInterval != 5*time.Second {
		t.Errorf("default PollInterval = %v, want 5s", d.config.PollInterval)
	}
	if cap(d.sem) != 1 {
		t.Errorf("default semaphore capacity = %d, want 1", cap(d.sem))
	}
}

// TestDispatcher_Dispatch_Success verifies a happy-path dispatch:
// workspace provisioned, run started, polled to "completed", tracker updated.
func TestDispatcher_Dispatch_Success(t *testing.T) {
	srv := newHealthzServer()
	defer srv.Close()

	issue := claimedIssue(42)
	tr := newMockTracker(issue)
	ws := &mockWorkspace{path: "/tmp/ws-42", harnessURL: srv.URL}

	cl := &mockHarnessClient{
		startFunc: func(_ context.Context, prompt, path string) (string, error) {
			if path != "/tmp/ws-42" {
				return "", fmt.Errorf("unexpected workspace path: %q", path)
			}
			return "run-42", nil
		},
		statusFunc: func(_ context.Context, runID string) (string, error) {
			return "completed", nil
		},
	}

	cfg := fastDispatchConfig()
	d := NewDispatcher(cfg, wsFactory(ws), tr, clFactory(cl))

	ctx := context.Background()
	if err := d.Dispatch(ctx, issue); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}

	result := <-d.Results()

	if !result.Success {
		t.Errorf("expected Success=true, got error: %v", result.Error)
	}
	if result.IssueNumber != 42 {
		t.Errorf("IssueNumber = %d, want 42", result.IssueNumber)
	}
	if result.Duration <= 0 {
		t.Error("Duration should be positive")
	}

	// Verify workspace was provisioned.
	if !ws.provisioned {
		t.Error("workspace was not provisioned")
	}

	// Verify tracker transitions.
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.started) != 1 || tr.started[0] != 42 {
		t.Errorf("tracker.started = %v, want [42]", tr.started)
	}
	if len(tr.complete) != 1 || tr.complete[0] != 42 {
		t.Errorf("tracker.complete = %v, want [42]", tr.complete)
	}
	if len(tr.failed) != 0 {
		t.Errorf("tracker.failed = %v, want []", tr.failed)
	}
}

// TestDispatcher_Dispatch_HarnessError verifies that a StartRun failure
// calls tracker.Fail and returns an error result.
func TestDispatcher_Dispatch_HarnessError(t *testing.T) {
	srv := newHealthzServer()
	defer srv.Close()

	issue := claimedIssue(10)
	tr := newMockTracker(issue)
	ws := &mockWorkspace{path: "/tmp/ws-10", harnessURL: srv.URL}

	cl := &mockHarnessClient{
		startFunc: func(_ context.Context, _, _ string) (string, error) {
			return "", errors.New("connection refused")
		},
		statusFunc: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("should not be called")
		},
	}

	cfg := fastDispatchConfig()
	d := NewDispatcher(cfg, wsFactory(ws), tr, clFactory(cl))

	ctx := context.Background()
	if err := d.Dispatch(ctx, issue); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}

	result := <-d.Results()

	if result.Success {
		t.Error("expected Success=false")
	}
	if result.Error == nil {
		t.Error("expected non-nil error")
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.failed) != 1 || tr.failed[0] != 10 {
		t.Errorf("tracker.failed = %v, want [10]", tr.failed)
	}
	if len(tr.complete) != 0 {
		t.Errorf("tracker.complete = %v, want []", tr.complete)
	}
}

// TestDispatcher_Dispatch_WorkspaceProvisionError verifies that a workspace
// provision failure calls tracker.Fail.
func TestDispatcher_Dispatch_WorkspaceProvisionError(t *testing.T) {
	issue := claimedIssue(7)
	tr := newMockTracker(issue)
	ws := &mockWorkspace{provideErr: errors.New("disk full")}

	cl := &mockHarnessClient{
		startFunc: func(_ context.Context, _, _ string) (string, error) {
			return "", errors.New("should not be called")
		},
		statusFunc: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("should not be called")
		},
	}

	cfg := fastDispatchConfig()
	d := NewDispatcher(cfg, wsFactory(ws), tr, clFactory(cl))

	ctx := context.Background()
	if err := d.Dispatch(ctx, issue); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}

	result := <-d.Results()

	if result.Success {
		t.Error("expected Success=false")
	}
	if result.Error == nil {
		t.Error("expected non-nil error")
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.failed) != 1 || tr.failed[0] != 7 {
		t.Errorf("tracker.failed = %v, want [7]", tr.failed)
	}
}

// TestDispatcher_Dispatch_Concurrency dispatches 3 issues with MaxConcurrent=2
// and verifies that at most 2 run simultaneously.
func TestDispatcher_Dispatch_Concurrency(t *testing.T) {
	srv := newHealthzServer()
	defer srv.Close()

	const numIssues = 3
	issues := make([]*TrackedIssue, numIssues)
	for i := range issues {
		issues[i] = claimedIssue(100 + i)
	}

	tr := newMockTracker(issues...)

	ws := &mockWorkspace{path: "/tmp/ws", harnessURL: srv.URL}

	var (
		mu         sync.Mutex
		concurrent int
		maxSeen    int
	)

	gate := make(chan struct{})
	var started atomic.Int32

	cl := &mockHarnessClient{
		startFunc: func(_ context.Context, _, _ string) (string, error) {
			return "run-x", nil
		},
		statusFunc: func(_ context.Context, runID string) (string, error) {
			// Increment concurrent counter and track maximum.
			mu.Lock()
			concurrent++
			if concurrent > maxSeen {
				maxSeen = concurrent
			}
			mu.Unlock()

			// Signal that this goroutine is active.
			started.Add(1)

			// Block until gate is released.
			<-gate

			mu.Lock()
			concurrent--
			mu.Unlock()
			return "completed", nil
		},
	}

	cfg := DispatchConfig{
		MaxConcurrent: 2,
		StallTimeout:  5 * time.Second,
		HarnessURL:    srv.URL,
		PollInterval:  5 * time.Millisecond,
	}
	d := NewDispatcher(cfg, wsFactory(ws), tr, clFactory(cl))

	ctx := context.Background()

	// Dispatch all issues. Since MaxConcurrent=2 and the status func blocks,
	// the third Dispatch call should block until a slot is free.
	errCh := make(chan error, numIssues)
	var wg sync.WaitGroup
	for _, issue := range issues {
		wg.Add(1)
		go func(i *TrackedIssue) {
			defer wg.Done()
			errCh <- d.Dispatch(ctx, i)
		}(issue)
	}

	// Wait until at least 2 goroutines have entered status polling.
	deadline := time.Now().Add(2 * time.Second)
	for started.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	// Release all gates.
	close(gate)

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Errorf("Dispatch returned error: %v", err)
		}
	}

	// Drain results.
	for i := 0; i < numIssues; i++ {
		<-d.Results()
	}

	if maxSeen > 2 {
		t.Errorf("maximum concurrent runs = %d, want <= 2", maxSeen)
	}
}

// TestDispatcher_Dispatch_ContextCancel verifies that cancelling the context
// during a run causes the run to be cleaned up and marked failed.
func TestDispatcher_Dispatch_ContextCancel(t *testing.T) {
	srv := newHealthzServer()
	defer srv.Close()

	issue := claimedIssue(99)
	tr := newMockTracker(issue)
	ws := &mockWorkspace{path: "/tmp/ws-99", harnessURL: srv.URL}

	started := make(chan struct{})
	cl := &mockHarnessClient{
		startFunc: func(_ context.Context, _, _ string) (string, error) {
			return "run-99", nil
		},
		statusFunc: func(ctx context.Context, _ string) (string, error) {
			// Signal that polling has started.
			select {
			case started <- struct{}{}:
			default:
			}
			// Block until context is cancelled.
			<-ctx.Done()
			return "", ctx.Err()
		},
	}

	cfg := fastDispatchConfig()
	ctx, cancel := context.WithCancel(context.Background())
	d := NewDispatcher(cfg, wsFactory(ws), tr, clFactory(cl))

	if err := d.Dispatch(ctx, issue); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}

	// Wait until status polling has begun, then cancel.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("status polling never started")
	}
	cancel()

	result := <-d.Results()
	if result.Success {
		t.Error("expected Success=false after context cancel")
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.failed) != 1 || tr.failed[0] != 99 {
		t.Errorf("tracker.failed = %v, want [99]", tr.failed)
	}
}

// TestDispatcher_Stall verifies that when RunStatus keeps returning "running"
// past StallTimeout, the issue is marked failed.
func TestDispatcher_Stall(t *testing.T) {
	srv := newHealthzServer()
	defer srv.Close()

	issue := claimedIssue(55)
	tr := newMockTracker(issue)
	ws := &mockWorkspace{path: "/tmp/ws-55", harnessURL: srv.URL}

	cl := &mockHarnessClient{
		startFunc: func(_ context.Context, _, _ string) (string, error) {
			return "run-55", nil
		},
		statusFunc: func(_ context.Context, _ string) (string, error) {
			// Always return "running" — simulates a stalled run.
			return "running", nil
		},
	}

	cfg := DispatchConfig{
		MaxConcurrent: 1,
		StallTimeout:  50 * time.Millisecond, // very short for tests
		HarnessURL:    srv.URL,
		PollInterval:  5 * time.Millisecond,
	}
	d := NewDispatcher(cfg, wsFactory(ws), tr, clFactory(cl))

	ctx := context.Background()
	if err := d.Dispatch(ctx, issue); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}

	result := <-d.Results()
	if result.Success {
		t.Error("expected Success=false for stalled run")
	}
	if result.Error == nil {
		t.Error("expected non-nil error for stalled run")
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.failed) != 1 || tr.failed[0] != 55 {
		t.Errorf("tracker.failed = %v, want [55]", tr.failed)
	}
}

// TestDispatcher_Shutdown cancels all in-flight dispatches.
func TestDispatcher_Shutdown(t *testing.T) {
	srv := newHealthzServer()
	defer srv.Close()

	const numIssues = 3
	issues := make([]*TrackedIssue, numIssues)
	for i := range issues {
		issues[i] = claimedIssue(200 + i)
	}
	tr := newMockTracker(issues...)
	ws := &mockWorkspace{path: "/tmp/ws", harnessURL: srv.URL}

	block := make(chan struct{})
	cl := &mockHarnessClient{
		startFunc: func(_ context.Context, _, _ string) (string, error) {
			return "run-x", nil
		},
		statusFunc: func(ctx context.Context, _ string) (string, error) {
			select {
			case <-block:
				return "completed", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	}

	cfg := DispatchConfig{
		MaxConcurrent: numIssues,
		StallTimeout:  5 * time.Second,
		HarnessURL:    srv.URL,
		PollInterval:  5 * time.Millisecond,
	}
	d := NewDispatcher(cfg, wsFactory(ws), tr, clFactory(cl))

	ctx := context.Background()
	for _, issue := range issues {
		if err := d.Dispatch(ctx, issue); err != nil {
			t.Fatalf("Dispatch returned error: %v", err)
		}
	}

	// Give goroutines a moment to start.
	time.Sleep(30 * time.Millisecond)

	// Shutdown should cancel all in-flight runs.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	d.Shutdown(shutdownCtx)

	// After shutdown, all running entries should be cleared.
	d.mu.Lock()
	runningCount := len(d.running)
	d.mu.Unlock()
	if runningCount != 0 {
		t.Errorf("running map has %d entries after Shutdown, want 0", runningCount)
	}
}

// TestDispatcher_ShutdownWaitsForRunningCleanup verifies Shutdown does not
// return before deferred dispatch cleanup removes entries from the running map.
func TestDispatcher_ShutdownWaitsForRunningCleanup(t *testing.T) {
	d := NewDispatcher(
		DispatchConfig{MaxConcurrent: 1},
		wsFactory(&mockWorkspace{}),
		newMockTracker(),
		clFactory(&mockHarnessClient{}),
	)

	canceled := make(chan struct{})
	d.mu.Lock()
	d.running[42] = func() { close(canceled) }
	d.mu.Unlock()
	d.sem <- struct{}{}

	go func() {
		<-canceled
		<-d.sem
		time.Sleep(50 * time.Millisecond)
		d.mu.Lock()
		delete(d.running, 42)
		d.mu.Unlock()
	}()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	defer shutdownCancel()

	d.Shutdown(shutdownCtx)

	d.mu.Lock()
	runningCount := len(d.running)
	d.mu.Unlock()
	if runningCount != 0 {
		t.Fatalf("running map has %d entries after Shutdown, want 0", runningCount)
	}
}

// TestDispatcher_Results_Channel verifies that Results() always returns the same channel.
func TestDispatcher_Results_Channel(t *testing.T) {
	d := NewDispatcher(fastDispatchConfig(), wsFactory(&mockWorkspace{}), newMockTracker(), clFactory(&mockHarnessClient{}))
	c1 := d.Results()
	c2 := d.Results()
	if c1 != c2 {
		t.Error("Results() should return the same channel on every call")
	}
}

// TestDispatcher_Dispatch_FailedRunStatus verifies that a "failed" status from
// harnessd causes the issue to be marked failed.
func TestDispatcher_Dispatch_FailedRunStatus(t *testing.T) {
	srv := newHealthzServer()
	defer srv.Close()

	issue := claimedIssue(77)
	tr := newMockTracker(issue)
	ws := &mockWorkspace{path: "/tmp/ws-77", harnessURL: srv.URL}

	cl := &mockHarnessClient{
		startFunc: func(_ context.Context, _, _ string) (string, error) {
			return "run-77", nil
		},
		statusFunc: func(_ context.Context, _ string) (string, error) {
			return "failed", nil
		},
	}

	cfg := fastDispatchConfig()
	d := NewDispatcher(cfg, wsFactory(ws), tr, clFactory(cl))

	if err := d.Dispatch(context.Background(), issue); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}

	result := <-d.Results()
	if result.Success {
		t.Error("expected Success=false for failed run status")
	}
	if result.Error == nil {
		t.Error("expected non-nil error")
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.failed) != 1 || tr.failed[0] != 77 {
		t.Errorf("tracker.failed = %v, want [77]", tr.failed)
	}
	if len(tr.complete) != 0 {
		t.Errorf("tracker.complete = %v, want []", tr.complete)
	}
}

// ---------------------------------------------------------------------------
// New tests for factory-based Dispatcher
// ---------------------------------------------------------------------------

// TestDispatcher_UsesWorkspaceHarnessURL verifies that the harness client is
// created with the URL returned by the provisioned workspace, not a static config URL.
func TestDispatcher_UsesWorkspaceHarnessURL(t *testing.T) {
	const dynamicURL = "http://dynamic:9999"

	// Start a real healthz server that responds to /healthz at the dynamic URL.
	// Since we can't bind to "dynamic:9999" in tests, we intercept via the
	// client factory capturing the URL.
	var capturedURL string
	var capturedURLMu sync.Mutex

	issue := claimedIssue(300)
	tr := newMockTracker(issue)

	// Workspace returns the dynamic URL.
	ws := &mockWorkspace{path: "/tmp/ws-300", harnessURL: dynamicURL}

	// The client factory captures the URL it's called with.
	// The client itself returns a completed run immediately.
	clientFactory := func(url string) HarnessClient {
		capturedURLMu.Lock()
		capturedURL = url
		capturedURLMu.Unlock()
		return &mockHarnessClient{
			startFunc: func(_ context.Context, _, _ string) (string, error) {
				return "run-300", nil
			},
			statusFunc: func(_ context.Context, _ string) (string, error) {
				return "completed", nil
			},
		}
	}

	// Use a custom waitForHarnessReady that we bypass by pointing at a real server.
	// We set up a healthz server and point the workspace URL at it.
	srv := newHealthzServer()
	defer srv.Close()
	ws.mu.Lock()
	ws.harnessURL = srv.URL // override to real server for healthz
	ws.mu.Unlock()

	cfg := fastDispatchConfig()
	d := NewDispatcher(cfg, wsFactory(ws), tr, clientFactory)

	if err := d.Dispatch(context.Background(), issue); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}

	result := <-d.Results()
	if !result.Success {
		t.Errorf("expected success, got: %v", result.Error)
	}

	capturedURLMu.Lock()
	got := capturedURL
	capturedURLMu.Unlock()

	if got != srv.URL {
		t.Errorf("clientFactory called with URL %q, want %q", got, srv.URL)
	}
}

// TestDispatcher_DestroysWorkspaceOnCompletion verifies that ws.Destroy is called
// after a successful run completes.
func TestDispatcher_DestroysWorkspaceOnCompletion(t *testing.T) {
	srv := newHealthzServer()
	defer srv.Close()

	issue := claimedIssue(301)
	tr := newMockTracker(issue)
	ws := &mockWorkspace{path: "/tmp/ws-301", harnessURL: srv.URL}

	cl := &mockHarnessClient{
		startFunc: func(_ context.Context, _, _ string) (string, error) {
			return "run-301", nil
		},
		statusFunc: func(_ context.Context, _ string) (string, error) {
			return "completed", nil
		},
	}

	cfg := fastDispatchConfig()
	d := NewDispatcher(cfg, wsFactory(ws), tr, clFactory(cl))

	if err := d.Dispatch(context.Background(), issue); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}

	result := <-d.Results()
	if !result.Success {
		t.Errorf("expected success, got: %v", result.Error)
	}

	// Give the deferred Destroy a moment to execute (it runs in the goroutine
	// after the result is sent to the channel).
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		ws.mu.Lock()
		destroyed := ws.destroyed
		ws.mu.Unlock()
		if destroyed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	ws.mu.Lock()
	destroyed := ws.destroyed
	ws.mu.Unlock()
	if !destroyed {
		t.Error("expected ws.Destroy to be called after successful completion")
	}
}

// TestDispatcher_DestroysWorkspaceOnFailure verifies that ws.Destroy is called
// even when StartRun fails.
func TestDispatcher_DestroysWorkspaceOnFailure(t *testing.T) {
	srv := newHealthzServer()
	defer srv.Close()

	issue := claimedIssue(302)
	tr := newMockTracker(issue)
	ws := &mockWorkspace{path: "/tmp/ws-302", harnessURL: srv.URL}

	cl := &mockHarnessClient{
		startFunc: func(_ context.Context, _, _ string) (string, error) {
			return "", errors.New("connection refused")
		},
		statusFunc: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("should not be called")
		},
	}

	cfg := fastDispatchConfig()
	d := NewDispatcher(cfg, wsFactory(ws), tr, clFactory(cl))

	if err := d.Dispatch(context.Background(), issue); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}

	result := <-d.Results()
	if result.Success {
		t.Error("expected failure")
	}

	// Give the deferred Destroy a moment to execute.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		ws.mu.Lock()
		destroyed := ws.destroyed
		ws.mu.Unlock()
		if destroyed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	ws.mu.Lock()
	destroyed := ws.destroyed
	ws.mu.Unlock()
	if !destroyed {
		t.Error("expected ws.Destroy to be called even when StartRun fails")
	}
}

// TestNewDispatcherSimple verifies that NewDispatcherSimple returns a non-nil dispatcher.
func TestNewDispatcherSimple(t *testing.T) {
	tr := newMockTracker()
	d := NewDispatcherSimple(fastDispatchConfig(), func() workspace.Workspace {
		return &mockWorkspace{harnessURL: "http://localhost:9999"}
	}, tr)
	if d == nil {
		t.Fatal("expected non-nil dispatcher")
	}
}

// TestHTTPHarnessClient_StartRun verifies that StartRun posts to /v1/runs and returns the run ID.
func TestHTTPHarnessClient_StartRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/v1/runs") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = fmt.Fprint(w, `{"run_id":"test-run-123","status":"queued"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client := NewHTTPHarnessClient(srv.URL)
	runID, err := client.StartRun(context.Background(), "test prompt", "/workspace")
	if err != nil {
		t.Fatalf("StartRun error: %v", err)
	}
	if runID != "test-run-123" {
		t.Fatalf("expected run ID %q, got %q", "test-run-123", runID)
	}
}

// TestHTTPHarnessClient_StartRun_ErrorStatus verifies that a non-202 response returns an error.
func TestHTTPHarnessClient_StartRun_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewHTTPHarnessClient(srv.URL)
	_, err := client.StartRun(context.Background(), "test prompt", "/workspace")
	if err == nil {
		t.Fatal("expected error for non-202 status, got nil")
	}
}

// TestHTTPHarnessClient_RunStatus verifies that RunStatus fetches /v1/runs/{id} and returns the status.
func TestHTTPHarnessClient_RunStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"status":"completed"}`)
	}))
	defer srv.Close()

	client := NewHTTPHarnessClient(srv.URL)
	status, err := client.RunStatus(context.Background(), "test-run-123")
	if err != nil {
		t.Fatalf("RunStatus error: %v", err)
	}
	if status != "completed" {
		t.Fatalf("expected status %q, got %q", "completed", status)
	}
}

// TestHTTPHarnessClient_RunStatus_ErrorStatus verifies that a non-200 response returns an error.
func TestHTTPHarnessClient_RunStatus_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewHTTPHarnessClient(srv.URL)
	_, err := client.RunStatus(context.Background(), "missing-run")
	if err == nil {
		t.Fatal("expected error for non-200 status, got nil")
	}
}

// TestDispatcher_ConcurrentUsesDistinctWorkspaces dispatches 3 issues concurrently
// and verifies that 3 distinct workspace instances are created (not 1 shared).
func TestDispatcher_ConcurrentUsesDistinctWorkspaces(t *testing.T) {
	srv := newHealthzServer()
	defer srv.Close()

	const numIssues = 3
	issues := make([]*TrackedIssue, numIssues)
	for i := range issues {
		issues[i] = claimedIssue(400 + i)
	}
	tr := newMockTracker(issues...)

	// Track all workspace instances created by the factory.
	var (
		instancesMu sync.Mutex
		instances   []*mockWorkspace
	)

	factory := func() workspace.Workspace {
		ws := &mockWorkspace{path: "/tmp/ws", harnessURL: srv.URL}
		instancesMu.Lock()
		instances = append(instances, ws)
		instancesMu.Unlock()
		return ws
	}

	gate := make(chan struct{})
	var started atomic.Int32

	cl := &mockHarnessClient{
		startFunc: func(_ context.Context, _, _ string) (string, error) {
			return "run-x", nil
		},
		statusFunc: func(_ context.Context, _ string) (string, error) {
			started.Add(1)
			<-gate
			return "completed", nil
		},
	}

	cfg := DispatchConfig{
		MaxConcurrent: numIssues,
		StallTimeout:  5 * time.Second,
		HarnessURL:    srv.URL,
		PollInterval:  5 * time.Millisecond,
	}
	d := NewDispatcher(cfg, factory, tr, clFactory(cl))

	ctx := context.Background()
	for _, issue := range issues {
		if err := d.Dispatch(ctx, issue); err != nil {
			t.Fatalf("Dispatch returned error: %v", err)
		}
	}

	// Wait until all goroutines are running.
	deadline := time.Now().Add(2 * time.Second)
	for started.Load() < int32(numIssues) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	close(gate)

	// Drain results.
	for i := 0; i < numIssues; i++ {
		<-d.Results()
	}

	instancesMu.Lock()
	count := len(instances)
	// Verify all instances are distinct pointers.
	seen := make(map[*mockWorkspace]bool)
	for _, ws := range instances {
		seen[ws] = true
	}
	instancesMu.Unlock()

	if count != numIssues {
		t.Errorf("factory called %d times, want %d", count, numIssues)
	}
	if len(seen) != numIssues {
		t.Errorf("got %d distinct workspace instances, want %d", len(seen), numIssues)
	}
}

// TestBuildPrompt_IncludesSynthesisDoctrine verifies that buildPrompt prepends
// the coordinator synthesis doctrine and includes the issue content.
func TestBuildPrompt_IncludesSynthesisDoctrine(t *testing.T) {
	issue := &TrackedIssue{
		Number: 503,
		Title:  "Add coordinator synthesis doctrine",
		Body:   "This is the issue body with implementation details.",
	}

	prompt := buildPrompt(issue)

	// Verify synthesis doctrine is present with key anti-pattern markers.
	wantPhrases := []string{
		"COORDINATOR SYNTHESIS DOCTRINE",
		`NEVER write "based on your findings"`,
		"WRONG vs RIGHT delegation patterns",
		"SYNTHESIS VERIFICATION",
		"internal/harness/runner.go:4304",
		"internal/auth/handler.go:89-120",
		"internal/config/loader.go:156",
		"internal/server/handler.go:203-218",
	}
	for _, phrase := range wantPhrases {
		if !strings.Contains(prompt, phrase) {
			t.Errorf("buildPrompt missing expected phrase: %q", phrase)
		}
	}

	// Verify issue content is present.
	if !strings.Contains(prompt, "503") {
		t.Error("buildPrompt missing issue number 503")
	}
	if !strings.Contains(prompt, "Add coordinator synthesis doctrine") {
		t.Error("buildPrompt missing issue title")
	}
	if !strings.Contains(prompt, "This is the issue body with implementation details.") {
		t.Error("buildPrompt missing issue body")
	}

	// Verify the doctrine comes before the issue content.
	doctrinePos := strings.Index(prompt, "COORDINATOR SYNTHESIS DOCTRINE")
	issuePos := strings.Index(prompt, "Implement GitHub issue #503")
	if doctrinePos < 0 || issuePos < 0 || doctrinePos >= issuePos {
		t.Error("synthesis doctrine must appear before the issue content")
	}
}

// TestBuildPrompt_DoctrineForDifferentIssue verifies the doctrine is included
// for issues of varying size and content.
func TestBuildPrompt_DoctrineForDifferentIssue(t *testing.T) {
	issue := &TrackedIssue{
		Number: 1,
		Title:  "A",
		Body:   "B",
	}

	prompt := buildPrompt(issue)

	if !strings.Contains(prompt, "COORDINATOR SYNTHESIS DOCTRINE") {
		t.Error("synthesis doctrine missing for minimal issue")
	}
	if !strings.HasPrefix(prompt, "COORDINATOR SYNTHESIS DOCTRINE") {
		t.Error("synthesis doctrine must be at the very start of the prompt")
	}
	if !strings.Contains(prompt, "Implement GitHub issue #1: A\n\nB") {
		t.Error("issue content not appended correctly after doctrine")
	}
}
