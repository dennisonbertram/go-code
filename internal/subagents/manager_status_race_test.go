package subagents

import (
	"context"
	"sync"
	"testing"

	"go-agent-harness/internal/harness"
)

// TestManagerDeleteStatusDataRace reproduces a data race on managed.Status.
// Delete calls refresh() (which writes Status under m.mu) and then reads
// managed.Status without holding the lock. A concurrent refresh() goroutine on
// the same subagent writes the same field, so the race detector flags the read.
func TestManagerDeleteStatusDataRace(t *testing.T) {
	t.Parallel()

	engine := &statefulRunEngine{
		status: harness.RunStatusRunning,
	}

	mgr, err := NewManager(Options{InlineRunner: engine})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	ctx := context.Background()
	item, err := mgr.Create(ctx, Request{Prompt: "race me"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	m := mgr.(*manager)
	managed, err := m.getManaged(item.ID)
	if err != nil {
		t.Fatalf("getManaged: %v", err)
	}

	var wg sync.WaitGroup
	const iters = 1000

	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			m.refresh(managed)
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			// Status is running, so Delete always returns ErrActive and never
			// removes the subagent from the map.
			_ = mgr.Delete(ctx, item.ID)
		}
	}()

	wg.Wait()
}
