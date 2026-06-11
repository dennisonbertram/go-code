package workspace_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"go-agent-harness/internal/workspace"
)

// Compile-time interface compliance check.
var _ workspace.Workspace = (*mockWorkspace)(nil)

// mockWorkspace is a test implementation of the Workspace interface.
type mockWorkspace struct {
	mu              sync.Mutex
	provisionCalled bool
	destroyCalled   bool
	provisionErr    error
	harnessURL      string
	workspacePath   string
	opts            workspace.Options
}

func (m *mockWorkspace) Provision(_ context.Context, opts workspace.Options) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.provisionCalled = true
	m.opts = opts
	return m.provisionErr
}

func (m *mockWorkspace) HarnessURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.harnessURL
}

func (m *mockWorkspace) WorkspacePath() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.workspacePath
}

func (m *mockWorkspace) WaitReady(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return nil
}

func (m *mockWorkspace) Destroy(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.destroyCalled = true
	return nil
}

// newMockFactory returns a Factory that creates a mockWorkspace with the
// given pre-set harnessURL and workspacePath.
func newMockFactory(harnessURL, workspacePath string) (workspace.Factory, *[]*mockWorkspace) {
	var created []*mockWorkspace
	f := func() workspace.Workspace {
		m := &mockWorkspace{
			harnessURL:    harnessURL,
			workspacePath: workspacePath,
		}
		created = append(created, m)
		return m
	}
	return f, &created
}

// --------------------------------------------------------------------------
// TestRegistry_Register
// --------------------------------------------------------------------------

func TestRegistry_Register(t *testing.T) {
	t.Run("succeeds for unique name", func(t *testing.T) {
		r := workspace.NewRegistry()
		f, _ := newMockFactory("http://localhost:8080", "/tmp/ws")
		if err := r.Register("local", f); err != nil {
			t.Fatalf("Register: unexpected error: %v", err)
		}
	})

	t.Run("duplicate name returns ErrAlreadyExists", func(t *testing.T) {
		r := workspace.NewRegistry()
		f, _ := newMockFactory("http://localhost:8080", "/tmp/ws")
		if err := r.Register("local", f); err != nil {
			t.Fatalf("first Register: %v", err)
		}
		err := r.Register("local", f)
		if !errors.Is(err, workspace.ErrAlreadyExists) {
			t.Fatalf("second Register: got %v, want ErrAlreadyExists", err)
		}
	})

	t.Run("empty name returns ErrInvalidName", func(t *testing.T) {
		r := workspace.NewRegistry()
		f, _ := newMockFactory("", "")
		err := r.Register("", f)
		if !errors.Is(err, workspace.ErrInvalidName) {
			t.Fatalf("Register with empty name: got %v, want ErrInvalidName", err)
		}
	})
}

// --------------------------------------------------------------------------
// TestRegistry_New_Found
// --------------------------------------------------------------------------

func TestRegistry_New_Found(t *testing.T) {
	r := workspace.NewRegistry()
	f, created := newMockFactory("http://localhost:9090", "/workspace/issue-42")
	if err := r.Register("local", f); err != nil {
		t.Fatalf("Register: %v", err)
	}

	opts := workspace.Options{ID: "issue-42", RepoURL: "https://github.com/example/repo"}
	ws, err := r.New(context.Background(), "local", opts)
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	if ws == nil {
		t.Fatal("New: returned nil Workspace")
	}

	// Verify Provision was called with the correct options.
	if len(*created) != 1 {
		t.Fatalf("expected 1 workspace created, got %d", len(*created))
	}
	mock := (*created)[0]
	if !mock.provisionCalled {
		t.Error("Provision was not called")
	}
	if mock.opts.ID != "issue-42" {
		t.Errorf("Provision opts.ID = %q, want %q", mock.opts.ID, "issue-42")
	}
	if mock.opts.RepoURL != "https://github.com/example/repo" {
		t.Errorf("Provision opts.RepoURL = %q, want %q", mock.opts.RepoURL, "https://github.com/example/repo")
	}

	// Verify interface methods work.
	if got := ws.HarnessURL(); got != "http://localhost:9090" {
		t.Errorf("HarnessURL = %q, want %q", got, "http://localhost:9090")
	}
	if got := ws.WorkspacePath(); got != "/workspace/issue-42" {
		t.Errorf("WorkspacePath = %q, want %q", got, "/workspace/issue-42")
	}
}

// --------------------------------------------------------------------------
// TestRegistry_New_NotFound
// --------------------------------------------------------------------------

func TestRegistry_New_NotFound(t *testing.T) {
	r := workspace.NewRegistry()
	_, err := r.New(context.Background(), "nonexistent", workspace.Options{ID: "x"})
	if !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("New with unknown name: got %v, want ErrNotFound", err)
	}
}

// --------------------------------------------------------------------------
// TestRegistry_New_EmptyID
// --------------------------------------------------------------------------

func TestRegistry_New_EmptyID(t *testing.T) {
	r := workspace.NewRegistry()
	f, _ := newMockFactory("", "")
	if err := r.Register("local", f); err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, err := r.New(context.Background(), "local", workspace.Options{ID: ""})
	if !errors.Is(err, workspace.ErrInvalidID) {
		t.Fatalf("New with empty ID: got %v, want ErrInvalidID", err)
	}
}

// --------------------------------------------------------------------------
// TestRegistry_New_EmptyName
// --------------------------------------------------------------------------

func TestRegistry_New_EmptyName(t *testing.T) {
	r := workspace.NewRegistry()
	_, err := r.New(context.Background(), "", workspace.Options{ID: "issue-99"})
	if !errors.Is(err, workspace.ErrInvalidName) {
		t.Fatalf("New with empty name: got %v, want ErrInvalidName", err)
	}
}

// --------------------------------------------------------------------------
// TestRegistry_New_ProvisionError
// --------------------------------------------------------------------------

func TestRegistry_New_ProvisionError(t *testing.T) {
	r := workspace.NewRegistry()
	provisionErr := errors.New("disk full")
	f := func() workspace.Workspace {
		return &mockWorkspace{provisionErr: provisionErr}
	}
	if err := r.Register("failing", f); err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, err := r.New(context.Background(), "failing", workspace.Options{ID: "issue-1"})
	if !errors.Is(err, provisionErr) {
		t.Fatalf("New with failing Provision: got %v, want %v", err, provisionErr)
	}
}

// --------------------------------------------------------------------------
// TestRegistry_List
// --------------------------------------------------------------------------

func TestRegistry_List(t *testing.T) {
	r := workspace.NewRegistry()
	names := []string{"zebra", "alpha", "mango", "beta"}
	for _, n := range names {
		f, _ := newMockFactory("", "")
		if err := r.Register(n, f); err != nil {
			t.Fatalf("Register %q: %v", n, err)
		}
	}

	got := r.List()
	want := []string{"alpha", "beta", "mango", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("List returned %d names, want %d: %v", len(got), len(want), got)
	}
	for i, g := range got {
		if g != want[i] {
			t.Errorf("List[%d] = %q, want %q", i, g, want[i])
		}
	}
}

// --------------------------------------------------------------------------
// TestRegistry_List_Empty
// --------------------------------------------------------------------------

func TestRegistry_List_Empty(t *testing.T) {
	r := workspace.NewRegistry()
	got := r.List()
	if got == nil {
		t.Fatal("List returned nil, want empty (non-nil) slice")
	}
	if len(got) != 0 {
		t.Fatalf("List returned %v, want empty slice", got)
	}
}

// --------------------------------------------------------------------------
// TestRegistry_Concurrent
// --------------------------------------------------------------------------

func TestRegistry_Concurrent(t *testing.T) {
	r := workspace.NewRegistry()
	const goroutines = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)

	// Half goroutines register unique names; half call New (some will hit ErrNotFound).
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("impl-%d", i)
			f, _ := newMockFactory("http://localhost", "/tmp")
			_ = r.Register(name, f)
			// Attempt to create — may or may not find it depending on scheduling.
			_, _ = r.New(context.Background(), name, workspace.Options{ID: fmt.Sprintf("id-%d", i)})
			_ = r.List()
		}()
	}
	wg.Wait()
}

// --------------------------------------------------------------------------
// TestDefaultRegistry_Functions
// --------------------------------------------------------------------------

func TestDefaultRegistry_Functions(t *testing.T) {
	// Use unique names to avoid conflicts with other tests sharing the default registry.
	const implName = "test-default-impl-unique-12345"

	f, created := newMockFactory("http://harness.local", "/ws/default")
	if err := workspace.Register(implName, f); err != nil {
		t.Fatalf("workspace.Register: %v", err)
	}

	// Duplicate should fail.
	if err := workspace.Register(implName, f); !errors.Is(err, workspace.ErrAlreadyExists) {
		t.Fatalf("duplicate workspace.Register: got %v, want ErrAlreadyExists", err)
	}

	// List should contain our name.
	found := false
	for _, n := range workspace.List() {
		if n == implName {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("workspace.List did not contain %q", implName)
	}

	// New should provision.
	ws, err := workspace.New(context.Background(), implName, workspace.Options{ID: "default-test"})
	if err != nil {
		t.Fatalf("workspace.New: %v", err)
	}
	if ws == nil {
		t.Fatal("workspace.New returned nil")
	}
	if len(*created) != 1 || !(*created)[0].provisionCalled {
		t.Error("Provision was not called via package-level New")
	}
}

// --------------------------------------------------------------------------
// TestWorkspace_InterfaceCompliance
// --------------------------------------------------------------------------

// TestWorkspace_InterfaceCompliance ensures mockWorkspace satisfies the
// Workspace interface. The var _ check at the top of the file is the primary
// guard; this test documents the expectation explicitly.
func TestWorkspace_InterfaceCompliance(t *testing.T) {
	var ws workspace.Workspace = &mockWorkspace{
		harnessURL:    "http://localhost:8080",
		workspacePath: "/tmp/ws",
	}

	ctx := context.Background()
	opts := workspace.Options{ID: "compliance-test"}

	if err := ws.Provision(ctx, opts); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if got := ws.HarnessURL(); got != "http://localhost:8080" {
		t.Errorf("HarnessURL = %q, want %q", got, "http://localhost:8080")
	}
	if got := ws.WorkspacePath(); got != "/tmp/ws" {
		t.Errorf("WorkspacePath = %q, want %q", got, "/tmp/ws")
	}
	if err := ws.Destroy(ctx); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
}
