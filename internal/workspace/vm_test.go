package workspace

import (
	"context"
	"errors"
	"testing"
)

// Compile-time interface checks.
var _ Workspace = (*VMWorkspace)(nil)
var _ VMProvider = (*mockVMProvider)(nil)

type mockVMProvider struct {
	createFunc func(ctx context.Context, opts VMCreateOpts) (*VM, error)
	deleteFunc func(ctx context.Context, id string) error
	deletedIDs []string
}

func (m *mockVMProvider) Create(ctx context.Context, opts VMCreateOpts) (*VM, error) {
	if m.createFunc != nil {
		return m.createFunc(ctx, opts)
	}
	return &VM{ID: "vm-123", PublicIP: "1.2.3.4", Status: "active"}, nil
}

func (m *mockVMProvider) Delete(ctx context.Context, id string) error {
	m.deletedIDs = append(m.deletedIDs, id)
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, id)
	}
	return nil
}

func TestVMWorkspace_ImplementsWorkspace(t *testing.T) {
	var _ Workspace = (*VMWorkspace)(nil)
}

func TestVMWorkspace_Provision_EmptyID(t *testing.T) {
	w := NewVM(&mockVMProvider{})
	err := w.Provision(context.Background(), Options{})
	if !errors.Is(err, ErrInvalidID) {
		t.Errorf("expected ErrInvalidID, got %v", err)
	}
}

func TestVMWorkspace_Provision_NilProvider(t *testing.T) {
	w := NewVM(nil)
	err := w.Provision(context.Background(), Options{ID: "test"})
	if err == nil {
		t.Error("expected error for nil provider")
	}
}

func TestVMWorkspace_Provision_Success(t *testing.T) {
	w := NewVM(&mockVMProvider{})
	err := w.Provision(context.Background(), Options{ID: "test-123"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.HarnessURL() != "http://1.2.3.4:8080" {
		t.Errorf("unexpected HarnessURL: %s", w.HarnessURL())
	}
	if w.WorkspacePath() != "/workspace" {
		t.Errorf("unexpected WorkspacePath: %s", w.WorkspacePath())
	}
}

func TestVMWorkspace_Provision_ProviderError(t *testing.T) {
	provErr := errors.New("quota exceeded")
	w := NewVM(&mockVMProvider{
		createFunc: func(_ context.Context, _ VMCreateOpts) (*VM, error) {
			return nil, provErr
		},
	})
	err := w.Provision(context.Background(), Options{ID: "test"})
	if err == nil {
		t.Error("expected error")
	}
	if !errors.Is(err, provErr) {
		t.Errorf("expected wrapped provErr, got %v", err)
	}
}

func TestVMWorkspace_ProvisionKeepsVMIDOnPostCreateError(t *testing.T) {
	postCreateErr := errors.New("post-create failed")
	mp := &mockVMProvider{}
	w := NewVM(mp)
	w.postCreateHook = func() error {
		return postCreateErr
	}

	err := w.Provision(context.Background(), Options{ID: "test"})
	if !errors.Is(err, postCreateErr) {
		t.Fatalf("expected postCreateErr, got %v", err)
	}
	if w.vmID != "vm-123" {
		t.Fatalf("vmID should be retained for caller cleanup, got %q", w.vmID)
	}
	if err := w.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy after post-create error: %v", err)
	}
	if len(mp.deletedIDs) != 1 || mp.deletedIDs[0] != "vm-123" {
		t.Fatalf("Destroy should delete retained VM ID, got %v", mp.deletedIDs)
	}
}

func TestVMWorkspace_Destroy_NotProvisioned(t *testing.T) {
	w := NewVM(&mockVMProvider{})
	if err := w.Destroy(context.Background()); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestVMWorkspace_Destroy_CallsProvider(t *testing.T) {
	mp := &mockVMProvider{}
	w := NewVM(mp)
	if err := w.Provision(context.Background(), Options{ID: "test"}); err != nil {
		t.Fatalf("provision failed: %v", err)
	}
	if err := w.Destroy(context.Background()); err != nil {
		t.Fatalf("destroy failed: %v", err)
	}
	if len(mp.deletedIDs) != 1 || mp.deletedIDs[0] != "vm-123" {
		t.Errorf("expected provider.Delete called with vm-123, got %v", mp.deletedIDs)
	}
}

func TestVMWorkspace_Destroy_ClearsVMID(t *testing.T) {
	mp := &mockVMProvider{}
	w := NewVM(mp)
	_ = w.Provision(context.Background(), Options{ID: "test"})
	_ = w.Destroy(context.Background())
	// Second destroy should be a no-op (vmID was cleared).
	if err := w.Destroy(context.Background()); err != nil {
		t.Errorf("second destroy should be no-op, got %v", err)
	}
	if len(mp.deletedIDs) != 1 {
		t.Errorf("expected Delete called once, got %d times", len(mp.deletedIDs))
	}
}

func TestVMWorkspace_Destroy_ProviderError(t *testing.T) {
	destroyErr := errors.New("delete failed")
	mp := &mockVMProvider{
		deleteFunc: func(_ context.Context, _ string) error {
			return destroyErr
		},
	}
	w := NewVM(mp)
	_ = w.Provision(context.Background(), Options{ID: "test"})
	err := w.Destroy(context.Background())
	if !errors.Is(err, destroyErr) {
		t.Errorf("expected wrapped destroyErr, got %v", err)
	}
}

func TestVMWorkspace_HarnessURL_BeforeProvision(t *testing.T) {
	w := NewVM(&mockVMProvider{})
	if w.HarnessURL() != "" {
		t.Errorf("expected empty HarnessURL before provision, got %q", w.HarnessURL())
	}
}

func TestVMWorkspace_WorkspacePath_BeforeProvision(t *testing.T) {
	w := NewVM(&mockVMProvider{})
	if w.WorkspacePath() != "" {
		t.Errorf("expected empty WorkspacePath before provision, got %q", w.WorkspacePath())
	}
}

func TestVMWorkspace_RegisteredInFactory(t *testing.T) {
	names := List()
	for _, n := range names {
		if n == "vm" {
			return
		}
	}
	t.Error("'vm' not registered in default factory")
}

func TestHarnessBootstrapScript_NotEmpty(t *testing.T) {
	s := harnessBootstrapScript()
	if len(s) == 0 {
		t.Error("bootstrap script should not be empty")
	}
	if len(s) < 2 || s[:2] != "#!" {
		t.Error("bootstrap script should start with shebang")
	}
}

func TestHarnessBootstrapScript_ContainsKeyElements(t *testing.T) {
	s := harnessBootstrapScript()
	checks := []string{
		"apt-get",
		"harnessd",
		"/workspace",
		"systemctl",
		":8080",
	}
	for _, c := range checks {
		found := false
		for i := 0; i+len(c) <= len(s); i++ {
			if s[i:i+len(c)] == c {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("bootstrap script missing expected element %q", c)
		}
	}
}

func TestVMWorkspace_Provision_NameSanitized(t *testing.T) {
	var capturedOpts VMCreateOpts
	mp := &mockVMProvider{
		createFunc: func(_ context.Context, opts VMCreateOpts) (*VM, error) {
			capturedOpts = opts
			return &VM{ID: "vm-456", PublicIP: "5.6.7.8", Status: "active"}, nil
		},
	}
	w := NewVM(mp)
	_ = w.Provision(context.Background(), Options{ID: "issue #42 / special"})
	// sanitizeBranch replaces non-alphanumeric-dot-hyphen chars with '-'
	if capturedOpts.Name == "" {
		t.Error("expected non-empty Name in VMCreateOpts")
	}
	// Name should start with "workspace-"
	const prefix = "workspace-"
	if len(capturedOpts.Name) < len(prefix) || capturedOpts.Name[:len(prefix)] != prefix {
		t.Errorf("expected name to start with %q, got %q", prefix, capturedOpts.Name)
	}
}
