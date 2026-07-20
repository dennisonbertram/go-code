package symphd

import (
	"context"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/workspace"
)

// captureWorkspace wraps another workspace and records the Options passed to Provision.
type captureWorkspace struct {
	inner   workspace.Workspace
	capture *workspace.Options
	mu      *sync.Mutex
}

func (c *captureWorkspace) Provision(ctx context.Context, opts workspace.Options) error {
	c.mu.Lock()
	*c.capture = opts
	c.mu.Unlock()
	return c.inner.Provision(ctx, opts)
}

func (c *captureWorkspace) HarnessURL() string                { return c.inner.HarnessURL() }
func (c *captureWorkspace) WorkspacePath() string             { return c.inner.WorkspacePath() }
func (c *captureWorkspace) Destroy(ctx context.Context) error { return c.inner.Destroy(ctx) }

// TestDispatcherPropagatesConfigTOML verifies that when DispatchConfig has
// SubagentConfigTOML set, Dispatch passes it to workspace.Options.ConfigTOML.
func TestDispatcherPropagatesConfigTOML(t *testing.T) {
	const expectedTOML = `model = "gpt-4.1"
auto_compact_enabled = true
`

	var capturedOpts workspace.Options
	var captureMu sync.Mutex

	// Fake healthz server so waitForHarnessReady passes.
	srv := newHealthzServer()
	defer srv.Close()

	inner := &mockWorkspace{
		harnessURL: srv.URL,
		path:       t.TempDir(),
	}

	captureWS := &captureWorkspace{
		inner:   inner,
		capture: &capturedOpts,
		mu:      &captureMu,
	}

	tracker := newMockTracker(claimedIssue(42))

	clientFactory := clFactory(&mockHarnessClient{
		startFunc:  func(_ context.Context, _, _ string) (string, error) { return "run-001", nil },
		statusFunc: func(_ context.Context, _ string) (string, error) { return "completed", nil },
	})

	cfg := DispatchConfig{
		MaxConcurrent:      1,
		StallTimeout:       5 * time.Second,
		PollInterval:       10 * time.Millisecond,
		SubagentConfigTOML: expectedTOML,
	}

	d := NewDispatcher(cfg, wsFactory(captureWS), tracker, clientFactory)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	issue := claimedIssue(42)
	if err := d.Dispatch(ctx, issue); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	select {
	case result := <-d.Results():
		if result.Error != nil {
			t.Errorf("dispatch result error: %v", result.Error)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for dispatch result")
	}

	captureMu.Lock()
	gotTOML := capturedOpts.ConfigTOML
	captureMu.Unlock()

	if gotTOML != expectedTOML {
		t.Errorf("workspace.Options.ConfigTOML:\ngot:  %q\nwant: %q", gotTOML, expectedTOML)
	}
}

// TestDispatcherPropagatesSubagentEnv verifies that SubagentEnv entries are
// merged into workspace.Options.Env when dispatching.
func TestDispatcherPropagatesSubagentEnv(t *testing.T) {
	var capturedOpts workspace.Options
	var captureMu sync.Mutex

	srv := newHealthzServer()
	defer srv.Close()

	inner := &mockWorkspace{
		harnessURL: srv.URL,
		path:       t.TempDir(),
	}
	captureWS := &captureWorkspace{
		inner:   inner,
		capture: &capturedOpts,
		mu:      &captureMu,
	}

	tracker := newMockTracker(claimedIssue(99))

	clientFactory := clFactory(&mockHarnessClient{
		startFunc:  func(_ context.Context, _, _ string) (string, error) { return "run-x", nil },
		statusFunc: func(_ context.Context, _ string) (string, error) { return "completed", nil },
	})

	cfg := DispatchConfig{
		MaxConcurrent: 1,
		StallTimeout:  5 * time.Second,
		PollInterval:  10 * time.Millisecond,
		SubagentEnv: map[string]string{
			"OPENAI_API_KEY": "sk-test-propagated",
		},
	}

	d := NewDispatcher(cfg, wsFactory(captureWS), tracker, clientFactory)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	issue := claimedIssue(99)
	if err := d.Dispatch(ctx, issue); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	select {
	case result := <-d.Results():
		if result.Error != nil {
			t.Errorf("dispatch result error: %v", result.Error)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for dispatch result")
	}

	captureMu.Lock()
	gotEnv := capturedOpts.Env
	captureMu.Unlock()

	if gotEnv["OPENAI_API_KEY"] != "sk-test-propagated" {
		t.Errorf("OPENAI_API_KEY in workspace.Options.Env = %q, want %q",
			gotEnv["OPENAI_API_KEY"], "sk-test-propagated")
	}
}

// TestDispatchConfigSubagentFields verifies DispatchConfig has the SubagentConfigTOML
// and SubagentEnv fields (compile-time + runtime check).
func TestDispatchConfigSubagentFields(t *testing.T) {
	cfg := DispatchConfig{
		SubagentConfigTOML: "model = \"test\"",
		SubagentEnv:        map[string]string{"KEY": "VALUE"},
	}
	if cfg.SubagentConfigTOML != "model = \"test\"" {
		t.Errorf("SubagentConfigTOML: got %q", cfg.SubagentConfigTOML)
	}
	if cfg.SubagentEnv["KEY"] != "VALUE" {
		t.Errorf("SubagentEnv[KEY]: got %q", cfg.SubagentEnv["KEY"])
	}
}

// TestDispatcherEmptySubagentEnvIsNilSafe verifies that dispatching without
// SubagentEnv set does not panic or set nil into workspace.Options.Env.
func TestDispatcherEmptySubagentEnvIsNilSafe(t *testing.T) {
	var capturedOpts workspace.Options
	var captureMu sync.Mutex

	srv := newHealthzServer()
	defer srv.Close()

	inner := &mockWorkspace{
		harnessURL: srv.URL,
		path:       t.TempDir(),
	}
	captureWS := &captureWorkspace{
		inner:   inner,
		capture: &capturedOpts,
		mu:      &captureMu,
	}

	tracker := newMockTracker(claimedIssue(55))

	clientFactory := clFactory(&mockHarnessClient{
		startFunc:  func(_ context.Context, _, _ string) (string, error) { return "run-y", nil },
		statusFunc: func(_ context.Context, _ string) (string, error) { return "completed", nil },
	})

	// No SubagentEnv or SubagentConfigTOML set.
	cfg := DispatchConfig{
		MaxConcurrent: 1,
		StallTimeout:  5 * time.Second,
		PollInterval:  10 * time.Millisecond,
	}

	d := NewDispatcher(cfg, wsFactory(captureWS), tracker, clientFactory)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	issue := claimedIssue(55)
	if err := d.Dispatch(ctx, issue); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	select {
	case <-d.Results():
	case <-ctx.Done():
		t.Fatal("timeout waiting for dispatch result")
	}

	// Should not panic, and ConfigTOML should be empty.
	captureMu.Lock()
	gotTOML := capturedOpts.ConfigTOML
	captureMu.Unlock()

	if gotTOML != "" {
		t.Errorf("ConfigTOML: got %q, want empty", gotTOML)
	}
}
