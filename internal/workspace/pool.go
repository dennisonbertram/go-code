package workspace

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// entryState tracks the lifecycle of a poolEntry.
type entryState int

const (
	entryStateIdle         entryState = iota // available for leasing
	entryStateInUse                          // currently leased out
	entryStateResetting                      // Destroy in progress, not ready yet
	entryStateProvisioning                   // Provision in progress
)

// poolEntry holds a pre-provisioned workspace and its lifecycle state.
type poolEntry struct {
	ws    Workspace
	id    string
	state entryState
}

// Pool maintains a set of pre-provisioned workspaces ready for immediate use.
// The background goroutine keeps the pool at target size, replacing destroyed
// or returned entries as needed.
//
// Pool is safe for concurrent use by multiple goroutines.
type Pool struct {
	mu         sync.Mutex
	entries    []*poolEntry
	factory    Factory // creates new Workspace instances
	baseOpts   Options // base Options used for provisioning; ID is overridden per entry
	targetSize int
	idCounter  int
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup // tracks the maintainLoop goroutine
	returnWg   sync.WaitGroup // tracks inflight Return goroutines
	ready      chan struct{}  // closed once pool reaches target size for the first time
	readyOnce  sync.Once
}

// NewPool creates a Pool that maintains targetSize pre-provisioned workspaces.
// factory is called to create new Workspace instances.
// baseOpts provides BaseDir and other config; ID is auto-generated per entry.
//
// The pool starts a background goroutine to maintain the target size. Call
// Close to stop the background goroutine and destroy all workspaces.
func NewPool(factory Factory, baseOpts Options, targetSize int) *Pool {
	ctx, cancel := context.WithCancel(context.Background())
	p := &Pool{
		factory:    factory,
		baseOpts:   baseOpts,
		targetSize: targetSize,
		ctx:        ctx,
		cancel:     cancel,
		ready:      make(chan struct{}),
	}
	p.wg.Add(1)
	go p.maintainLoop()
	return p
}

// Factory returns a WorkspaceFactory that creates PoolWorkspace leases backed
// by this pool. Each call to the factory produces a fresh, unprovisioned
// PoolWorkspace. Callers must call Provision (which leases from the pool)
// and Destroy (which returns the lease).
func (p *Pool) Factory() Factory {
	return func() Workspace {
		return NewPoolWorkspace(p)
	}
}

// Get leases an available workspace from the pool, blocking until one is
// available or ctx is done. Returns the leased Workspace and its pool ID.
// The caller must call Return(id) when done to release the workspace back to
// the pool.
func (p *Pool) Get(ctx context.Context) (Workspace, string, error) {
	// Wait for the pool to reach target size at least once.
	select {
	case <-p.ready:
	case <-ctx.Done():
		return nil, "", ctx.Err()
	case <-p.ctx.Done():
		return nil, "", fmt.Errorf("workspace: pool closed")
	}

	for {
		p.mu.Lock()
		for _, e := range p.entries {
			if e.state == entryStateIdle && e.ws != nil {
				e.state = entryStateInUse
				ws := e.ws
				id := e.id
				p.mu.Unlock()
				return ws, id, nil
			}
		}
		p.mu.Unlock()

		// No available entry; wait a short interval and retry.
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-p.ctx.Done():
			return nil, "", fmt.Errorf("workspace: pool closed")
		case <-time.After(100 * time.Millisecond):
			// retry
		}
	}
}

// Return releases a leased workspace back to the pool.
// The workspace is asynchronously destroyed and the slot is then reprovisioned
// by the background goroutine. The entry transitions through entryStateResetting
// so that fillPool does not attempt to reprovision it while Destroy is in flight.
// Calling Return with an unknown or already-returned id is a no-op.
func (p *Pool) Return(id string) {
	p.mu.Lock()
	var target *poolEntry
	for _, e := range p.entries {
		if e.id == id && e.state == entryStateInUse {
			target = e
			break
		}
	}
	if target == nil {
		p.mu.Unlock()
		return
	}
	// Capture ws and transition to resetting state before releasing the lock.
	ws := target.ws
	target.ws = nil
	target.state = entryStateResetting
	p.mu.Unlock()

	p.returnWg.Add(1)
	go func() {
		defer p.returnWg.Done()

		if ws != nil {
			_ = ws.Destroy(context.Background())
		}

		p.mu.Lock()
		target.state = entryStateIdle
		p.mu.Unlock()

		// Trigger reprovisioning now that the slot is idle.
		p.fillPool()
	}()
}

// Close shuts down the pool, stopping the background goroutine and destroying
// all workspaces (both available and in-use).
func (p *Pool) Close() {
	p.cancel()
	p.wg.Wait()
	// Wait for any inflight Return goroutines to finish so that we do not race
	// on entry.ws while they are still running fillPool.
	p.returnWg.Wait()

	p.mu.Lock()
	entries := p.entries
	p.entries = nil
	// Collect all live workspace handles and nil them out while holding the
	// lock, so no concurrent writer can race with our subsequent Destroy calls.
	var toDestroy []Workspace
	for _, e := range entries {
		if e.ws != nil {
			toDestroy = append(toDestroy, e.ws)
			e.ws = nil
		}
	}
	p.mu.Unlock()

	for _, ws := range toDestroy {
		_ = ws.Destroy(context.Background())
	}
	for _, repoPath := range distinctWorktreeRepoPaths(toDestroy) {
		_ = pruneWorktreeRepo(context.Background(), repoPath)
	}
}

func distinctWorktreeRepoPaths(workspaces []Workspace) []string {
	seen := map[string]struct{}{}
	var paths []string
	for _, ws := range workspaces {
		wt, ok := ws.(*WorktreeWorkspace)
		if !ok || wt.repoPath == "" {
			continue
		}
		if _, exists := seen[wt.repoPath]; exists {
			continue
		}
		seen[wt.repoPath] = struct{}{}
		paths = append(paths, wt.repoPath)
	}
	return paths
}

// Len returns the number of available (idle) entries currently in the pool.
func (p *Pool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, e := range p.entries {
		if e.state == entryStateIdle && e.ws != nil {
			n++
		}
	}
	return n
}

// Ready returns a channel that is closed once the pool has reached its target
// size for the first time.
func (p *Pool) Ready() <-chan struct{} {
	return p.ready
}

func (p *Pool) maintainLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	// Attempt an immediate fill before waiting for the first tick.
	p.fillPool()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.fillPool()
		}
	}
}

// fillPool provisions workspaces until the pool reaches targetSize.
// Only entries in entryStateIdle with a nil ws are eligible for reprovisioning.
// Entries in entryStateResetting or entryStateProvisioning are skipped.
// fillPool is a no-op if the pool context has been cancelled.
func (p *Pool) fillPool() {
	// Bail out immediately if the pool is closed; avoids racing with Close().
	if p.ctx.Err() != nil {
		return
	}

	p.mu.Lock()
	// Ensure we have enough entry slots.
	for len(p.entries) < p.targetSize {
		p.idCounter++
		p.entries = append(p.entries, &poolEntry{id: fmt.Sprintf("pool-%d", p.idCounter)})
	}

	// Collect entries that need provisioning: idle with no workspace.
	var toProvision []*poolEntry
	for _, e := range p.entries {
		if e.state == entryStateIdle && e.ws == nil {
			e.state = entryStateProvisioning
			toProvision = append(toProvision, e)
		}
	}
	p.mu.Unlock()

	provisioned := 0
	for _, e := range toProvision {
		// Stop provisioning if pool has been closed.
		if p.ctx.Err() != nil {
			// Revert provisioning state back to idle so the entry can be
			// retried or cleaned up.
			p.mu.Lock()
			if e.state == entryStateProvisioning {
				e.state = entryStateIdle
			}
			p.mu.Unlock()
			break
		}

		ws := p.factory()
		opts := p.baseOpts
		opts.ID = e.id
		if err := ws.Provision(p.ctx, opts); err != nil {
			// Provisioning failed; reset state so maintainLoop will retry.
			p.mu.Lock()
			if e.state == entryStateProvisioning {
				e.state = entryStateIdle
			}
			p.mu.Unlock()
			continue
		}

		p.mu.Lock()
		// Only assign if the entry is still in provisioning state.
		if e.state == entryStateProvisioning {
			e.ws = ws
			e.state = entryStateIdle
			provisioned++
		} else {
			// Entry was claimed or reprovisioned elsewhere; discard this one.
			p.mu.Unlock()
			_ = ws.Destroy(context.Background())
			continue
		}
		p.mu.Unlock()
	}

	// Signal readiness once we have at least targetSize live workspaces.
	p.mu.Lock()
	live := 0
	for _, e := range p.entries {
		if e.ws != nil {
			live++
		}
	}
	p.mu.Unlock()

	if live >= p.targetSize {
		p.readyOnce.Do(func() { close(p.ready) })
	}
}
