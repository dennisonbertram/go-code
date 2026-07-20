package mcpserver

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// HarnessPoller is the interface the RunPoller uses to check run status.
// Implemented by a thin adapter wrapping RunnerInterface.
type HarnessPoller interface {
	GetRunStatus(runID string) (RunStatus, error)
}

// terminalStatuses is the set of statuses that indicate a run is done.
var terminalStatuses = map[string]bool{
	"completed": true,
	"failed":    true,
}

// RunPoller polls watched runs via HarnessPoller and publishes state-change
// notifications to a Broker.
// It is safe for concurrent use.
type RunPoller struct {
	client   HarnessPoller
	broker   *Broker
	interval time.Duration
	mu       sync.Mutex
	watched  map[string]string // run_id → last known status
}

// NewRunPoller creates a new RunPoller.
func NewRunPoller(client HarnessPoller, broker *Broker, interval time.Duration) *RunPoller {
	return &RunPoller{
		client:   client,
		broker:   broker,
		interval: interval,
		watched:  make(map[string]string),
	}
}

// Watch adds runID to the set of polled runs. Idempotent.
func (p *RunPoller) Watch(runID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.watched[runID]; !exists {
		p.watched[runID] = "" // empty string = unknown prior status
	}
}

// Unwatch removes runID. Called internally after terminal state.
func (p *RunPoller) Unwatch(runID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.watched, runID)
}

// WatchCount returns current number of watched runs. Used in tests.
func (p *RunPoller) WatchCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.watched)
}

// Run starts the poll loop. Blocks until ctx is cancelled.
// On each tick, polls all watched runs and publishes state-change notifications.
func (p *RunPoller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.poll()
		}
	}
}

// poll checks all watched runs once and publishes any status changes.
func (p *RunPoller) poll() {
	p.mu.Lock()
	// Snapshot watched set to avoid holding the lock during RPC calls.
	ids := make([]string, 0, len(p.watched))
	lastStatuses := make(map[string]string, len(p.watched))
	for id, last := range p.watched {
		ids = append(ids, id)
		lastStatuses[id] = last
	}
	p.mu.Unlock()

	for _, runID := range ids {
		status, err := p.client.GetRunStatus(runID)
		if err != nil {
			// Run not found or fetch error — skip silently.
			continue
		}

		last := lastStatuses[runID]
		current := status.Status

		if current == last {
			continue
		}

		// Update the last known status.
		p.mu.Lock()
		if _, still := p.watched[runID]; still {
			p.watched[runID] = current
		}
		p.mu.Unlock()

		if terminalStatuses[current] {
			// Publish run/completed notification.
			params := map[string]any{
				"run_id":   runID,
				"status":   current,
				"cost_usd": 0,
			}
			if status.Error != "" {
				params["error"] = status.Error
			} else {
				params["error"] = ""
			}
			raw, _ := json.Marshal(params)
			p.broker.Publish(runID, Notification{
				Method: "run/completed",
				Params: raw,
			})
			p.Unwatch(runID)
		} else {
			// Publish run/event for non-terminal status change.
			params := map[string]any{
				"run_id":     runID,
				"event_type": "status_changed",
				"status":     current,
			}
			raw, _ := json.Marshal(params)
			p.broker.Publish(runID, Notification{
				Method: "run/event",
				Params: raw,
			})
		}
	}
}
