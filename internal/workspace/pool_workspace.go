package workspace

import (
	"context"
	"fmt"
)

// PoolWorkspace implements Workspace using a pre-provisioned workspace leased
// from a Pool. Provision leases a workspace from the pool (blocking until one
// is available or ctx is done). Destroy returns the workspace to the pool for
// reuse rather than destroying the underlying resource.
//
// Note: PoolWorkspace is not registered in the default factory via init()
// because it requires a configured Pool instance. Callers construct it
// directly via NewPoolWorkspace and call Provision to lease from the pool.
type PoolWorkspace struct {
	pool *Pool
	ws   Workspace
	id   string
}

// NewPoolWorkspace creates a PoolWorkspace backed by the given pool.
// Provision must be called before HarnessURL or WorkspacePath are valid.
func NewPoolWorkspace(pool *Pool) *PoolWorkspace {
	return &PoolWorkspace{pool: pool}
}

// Provision leases a workspace from the pool, blocking until one is available
// or ctx is done. opts.ID is ignored; the pool manages workspace IDs.
func (w *PoolWorkspace) Provision(ctx context.Context, _ Options) error {
	if w.pool == nil {
		return fmt.Errorf("workspace: pool is nil")
	}
	ws, id, err := w.pool.Get(ctx)
	if err != nil {
		return err
	}
	w.ws = ws
	w.id = id
	return nil
}

// HarnessURL returns the harness URL of the leased workspace.
// Returns an empty string if Provision has not been called.
func (w *PoolWorkspace) HarnessURL() string {
	if w.ws == nil {
		return ""
	}
	return w.ws.HarnessURL()
}

// WorkspacePath returns the filesystem path of the leased workspace root.
// Returns an empty string if Provision has not been called.
func (w *PoolWorkspace) WorkspacePath() string {
	if w.ws == nil {
		return ""
	}
	return w.ws.WorkspacePath()
}

// WaitReady delegates to the inner workspace's WaitReady. It returns an error
// if the pool workspace has not been provisioned.
func (w *PoolWorkspace) WaitReady(ctx context.Context) error {
	if w.ws == nil {
		return fmt.Errorf("workspace: pool workspace not provisioned")
	}
	return w.ws.WaitReady(ctx)
}

// Destroy returns the leased workspace to the pool for reuse.
// It does not destroy the underlying workspace resource.
// It is safe to call Destroy on an un-provisioned PoolWorkspace (no-op).
func (w *PoolWorkspace) Destroy(_ context.Context) error {
	if w.ws == nil || w.pool == nil {
		return nil
	}
	w.pool.Return(w.id)
	w.ws = nil
	w.id = ""
	return nil
}
