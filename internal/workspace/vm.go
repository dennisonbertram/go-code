package workspace

import (
	"context"
	"fmt"
	"os"
)

// VMWorkspace implements Workspace using a cloud VM per workspace.
type VMWorkspace struct {
	harnessURL     string
	workspacePath  string
	vmID           string
	provider       VMProvider
	postCreateHook func() error
}

// NewVM creates a VMWorkspace with the given VMProvider.
func NewVM(provider VMProvider) *VMWorkspace {
	return &VMWorkspace{provider: provider}
}

// Provision creates a cloud VM and stores its address.
// It returns ErrInvalidID if opts.ID is empty, or an error if the
// VMProvider fails to create the VM.
func (w *VMWorkspace) Provision(ctx context.Context, opts Options) error {
	if opts.ID == "" {
		return ErrInvalidID
	}
	if w.provider == nil {
		return fmt.Errorf("workspace: VMProvider is nil")
	}

	userdata := harnessBootstrapScript()

	vm, err := w.provider.Create(ctx, VMCreateOpts{
		Name:     "workspace-" + sanitizeBranch(opts.ID),
		UserData: userdata,
	})
	if err != nil {
		return fmt.Errorf("workspace: vm create: %w", err)
	}
	if vm == nil {
		return fmt.Errorf("workspace: vm create returned nil VM")
	}

	w.vmID = vm.ID
	if w.postCreateHook != nil {
		if err := w.postCreateHook(); err != nil {
			return fmt.Errorf("workspace: vm post-create: %w", err)
		}
	}
	w.harnessURL = "http://" + vm.PublicIP + ":8080"
	w.workspacePath = "/workspace"
	return nil
}

// HarnessURL returns the HTTP endpoint of the harnessd instance running
// inside this workspace. Returns an empty string before Provision succeeds.
func (w *VMWorkspace) HarnessURL() string { return w.harnessURL }

// WorkspacePath returns the filesystem path of the workspace root on the VM.
// Returns an empty string before Provision succeeds.
func (w *VMWorkspace) WorkspacePath() string { return w.workspacePath }

// Destroy deletes the cloud VM. It is a no-op if Provision was not called.
func (w *VMWorkspace) Destroy(ctx context.Context) error {
	if w.vmID == "" {
		return nil
	}
	if w.provider == nil {
		return nil
	}
	if err := w.provider.Delete(ctx, w.vmID); err != nil {
		return fmt.Errorf("workspace: vm delete: %w", err)
	}
	w.vmID = ""
	return nil
}

func init() {
	// Register the "vm" implementation in the package-level default registry.
	// Default: create a HetznerProvider using HETZNER_API_KEY env var.
	_ = Register("vm", func() Workspace {
		apiKey := os.Getenv("HETZNER_API_KEY")
		provider := NewHetznerProvider(apiKey)
		return NewVM(provider)
	})
}
