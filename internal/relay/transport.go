package relay

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// TransportSession represents an active connection from a worker to the relay.
type TransportSession struct {
	// ID uniquely identifies this session.
	ID string `json:"id"`

	// WorkerID identifies the connected worker.
	WorkerID string `json:"worker_id"`

	// TenantID scopes the session.
	TenantID string `json:"tenant_id"`

	// ConnectedAt is when the session was established.
	ConnectedAt time.Time `json:"connected_at"`

	// LastActivity is when the session last sent or received data.
	LastActivity time.Time `json:"last_activity"`

	// ActiveRunIDs are the runs currently dispatched to this worker.
	ActiveRunIDs []string `json:"active_run_ids,omitempty"`
}

// TransportEvent represents an event relayed from a worker to the relay.
type TransportEvent struct {
	// RunID identifies the run this event belongs to.
	RunID string `json:"run_id"`

	// Seq is the monotonically increasing sequence number.
	Seq int `json:"seq"`

	// EventType is the type of event (e.g. "run.started", "run.completed", "tool.started").
	EventType string `json:"event_type"`

	// Payload is the JSON-encoded event payload.
	Payload string `json:"payload"`

	// Timestamp is when the event occurred on the worker.
	Timestamp time.Time `json:"timestamp"`
}

// TransportCommand represents a command sent from the relay to a worker.
type TransportCommand struct {
	// ID uniquely identifies this command.
	ID string `json:"id"`

	// RunID identifies the target run.
	RunID string `json:"run_id"`

	// Command is the action: "cancel", "steer", "approve", "deny".
	Command string `json:"command"`

	// Payload carries command-specific data.
	Payload string `json:"payload,omitempty"`

	// CreatedAt is when the command was created.
	CreatedAt time.Time `json:"created_at"`
}

// DispatchRequest is sent from relay to worker to start a new run.
type DispatchRequest struct {
	// RunContract is the full contract for the run.
	RunContract *RunContract `json:"run_contract"`

	// DispatchID uniquely identifies this dispatch.
	DispatchID string `json:"dispatch_id"`

	// DispatchedAt is when the dispatch was created.
	DispatchedAt time.Time `json:"dispatched_at"`
}

// TransportManager manages worker transport sessions and run dispatch.
type TransportManager struct {
	mu       sync.RWMutex
	sessions map[string]*TransportSession // session ID → session
	byWorker map[string]string            // worker ID → session ID
}

// NewTransportManager creates a new transport manager.
func NewTransportManager() *TransportManager {
	return &TransportManager{
		sessions: make(map[string]*TransportSession),
		byWorker: make(map[string]string),
	}
}

// RegisterSession records a new worker transport session.
// If the worker already has a session, it is replaced (reconnect).
func (tm *TransportManager) RegisterSession(session *TransportSession) error {
	if session.ID == "" {
		return errors.New("transport: session ID is required")
	}
	if session.WorkerID == "" {
		return errors.New("transport: worker ID is required")
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	// If this worker already had a session, remove the old one.
	if oldSessionID, ok := tm.byWorker[session.WorkerID]; ok {
		delete(tm.sessions, oldSessionID)
	}

	tm.sessions[session.ID] = session
	tm.byWorker[session.WorkerID] = session.ID
	return nil
}

// RemoveSession removes a transport session.
func (tm *TransportManager) RemoveSession(sessionID string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	session, ok := tm.sessions[sessionID]
	if ok {
		delete(tm.byWorker, session.WorkerID)
		delete(tm.sessions, sessionID)
	}
}

// GetSession returns the session for a session ID.
func (tm *TransportManager) GetSession(sessionID string) (*TransportSession, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	session, ok := tm.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("transport: session %q not found", sessionID)
	}
	return session, nil
}

// GetWorkerSession returns the session for a worker ID.
func (tm *TransportManager) GetWorkerSession(workerID string) (*TransportSession, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	sessionID, ok := tm.byWorker[workerID]
	if !ok {
		return nil, fmt.Errorf("transport: no session for worker %q", workerID)
	}
	session, ok := tm.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("transport: session %q for worker %q not found", sessionID, workerID)
	}
	return session, nil
}

// ListSessions returns all active transport sessions.
func (tm *TransportManager) ListSessions() []*TransportSession {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	result := make([]*TransportSession, 0, len(tm.sessions))
	for _, s := range tm.sessions {
		result = append(result, s)
	}
	return result
}

// AddRunToSession tracks that a run has been dispatched to this worker.
func (tm *TransportManager) AddRunToSession(sessionID, runID string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	session, ok := tm.sessions[sessionID]
	if !ok {
		return fmt.Errorf("transport: session %q not found", sessionID)
	}

	// Avoid duplicates.
	for _, id := range session.ActiveRunIDs {
		if id == runID {
			return nil
		}
	}
	session.ActiveRunIDs = append(session.ActiveRunIDs, runID)
	return nil
}

// RemoveRunFromSession removes a run from the session's active list.
func (tm *TransportManager) RemoveRunFromSession(sessionID, runID string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	session, ok := tm.sessions[sessionID]
	if !ok {
		return
	}

	filtered := make([]string, 0, len(session.ActiveRunIDs))
	for _, id := range session.ActiveRunIDs {
		if id != runID {
			filtered = append(filtered, id)
		}
	}
	session.ActiveRunIDs = filtered
}

// ActiveRunCount returns the number of active runs for a worker.
func (tm *TransportManager) ActiveRunCount(workerID string) int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	sessionID, ok := tm.byWorker[workerID]
	if !ok {
		return 0
	}
	session, ok := tm.sessions[sessionID]
	if !ok {
		return 0
	}
	return len(session.ActiveRunIDs)
}

// EventBus is a simple pub/sub for transport events from workers.
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[string][]chan TransportEvent // runID → subscribers
}

// NewEventBus creates a new event bus.
func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[string][]chan TransportEvent),
	}
}

// Subscribe returns a channel that receives events for a specific run.
func (eb *EventBus) Subscribe(runID string) chan TransportEvent {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	ch := make(chan TransportEvent, 100)
	eb.subscribers[runID] = append(eb.subscribers[runID], ch)
	return ch
}

// Unsubscribe removes a subscription.
func (eb *EventBus) Unsubscribe(runID string, ch chan TransportEvent) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	subs := eb.subscribers[runID]
	filtered := make([]chan TransportEvent, 0, len(subs))
	for _, s := range subs {
		if s != ch {
			filtered = append(filtered, s)
		} else {
			close(s)
		}
	}
	if len(filtered) == 0 {
		delete(eb.subscribers, runID)
	} else {
		eb.subscribers[runID] = filtered
	}
}

// Publish sends an event to all subscribers for a run.
func (eb *EventBus) Publish(ctx context.Context, event TransportEvent) {
	eb.mu.RLock()
	subs := eb.subscribers[event.RunID]
	eb.mu.RUnlock()

	for _, ch := range subs {
		select {
		case ch <- event:
		case <-ctx.Done():
			return
		default:
			// Subscriber is slow; drop the event rather than blocking.
		}
	}
}

// CommandQueue manages the queue of commands for a worker.
type CommandQueue struct {
	mu       sync.Mutex
	commands map[string][]*TransportCommand // workerID → commands
}

// NewCommandQueue creates a new command queue.
func NewCommandQueue() *CommandQueue {
	return &CommandQueue{
		commands: make(map[string][]*TransportCommand),
	}
}

// Enqueue adds a command for a worker.
func (cq *CommandQueue) Enqueue(workerID string, cmd *TransportCommand) {
	cq.mu.Lock()
	defer cq.mu.Unlock()
	cq.commands[workerID] = append(cq.commands[workerID], cmd)
}

// Dequeue returns and removes all pending commands for a worker.
func (cq *CommandQueue) Dequeue(workerID string) []*TransportCommand {
	cq.mu.Lock()
	defer cq.mu.Unlock()

	cmds := cq.commands[workerID]
	delete(cq.commands, workerID)
	if cmds == nil {
		return []*TransportCommand{}
	}
	return cmds
}

// PendingCount returns the number of pending commands for a worker.
func (cq *CommandQueue) PendingCount(workerID string) int {
	cq.mu.Lock()
	defer cq.mu.Unlock()
	return len(cq.commands[workerID])
}
