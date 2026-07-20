package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"go-agent-harness/internal/harness/tools/descriptions"
)

// Constants
const (
	MaxCallbackDelay    = 1 * time.Hour
	MinCallbackDelay    = 5 * time.Second
	MaxCallbacksPerConv = 10
)

// RunStarter is the interface for starting a new run on a conversation.
// Implemented by the runner; injected via lazy adapter to avoid circular deps.
//
// tenantID and agentID carry the originating run's scope so a callback fired
// from a tenant- or agent-scoped conversation starts its follow-up run on the
// SAME scope. Without them the follow-up run is denied access to the scoped
// conversation at fire time (a direct autonomy breaker). Both may be empty for
// the default/unscoped case.
type RunStarter interface {
	StartRun(prompt, conversationID, tenantID, agentID string) error
}

// SetRequest carries the parameters for scheduling a delayed callback. It is a
// small struct (rather than a long positional argument list) so the run scope
// (tenant + agent) can be threaded through Set -> fire -> StartRun without
// repeated signature churn.
type SetRequest struct {
	ConversationID string
	Delay          time.Duration
	Prompt         string
	// TenantID and AgentID capture the originating run's scope so the fired
	// follow-up run is started on the same tenant + agent. Both may be empty
	// for the default/unscoped case.
	TenantID string
	AgentID  string
}

// CallbackEvents is an optional sink for callback lifecycle notifications.
// The CallbackManager calls Emit when a callback is scheduled, fires, or is
// canceled. The event name matches the harness EventType string values
// ("callback.scheduled", "callback.fired", "callback.canceled"); the manager
// uses plain strings to avoid importing the runner event bus (no import cycle).
//
// Implementations MUST be safe for concurrent use and MUST NOT call back into
// the CallbackManager. A nil sink disables emission entirely.
type CallbackEvents interface {
	Emit(event string, info CallbackInfo)
}

// Event name constants for the callback lifecycle. These mirror the harness
// EventType string values but live here so the tools package stays free of a
// dependency on the runner.
const (
	eventCallbackScheduled = "callback.scheduled"
	eventCallbackFired     = "callback.fired"
	eventCallbackCanceled  = "callback.canceled"
)

// CallbackOption configures a CallbackManager at construction time.
type CallbackOption func(*CallbackManager)

// WithEventSink wires an optional CallbackEvents sink onto the manager so
// callback lifecycle events are observable. Passing a nil sink is a no-op.
func WithEventSink(sink CallbackEvents) CallbackOption {
	return func(m *CallbackManager) {
		m.events = sink
	}
}

// CallbackState represents the lifecycle state of a callback.
type CallbackState string

const (
	CallbackStatePending  CallbackState = "pending"
	CallbackStateFired    CallbackState = "fired"
	CallbackStateCanceled CallbackState = "canceled"
)

// CallbackInfo holds metadata about a scheduled callback.
type CallbackInfo struct {
	ID             string        `json:"id"`
	ConversationID string        `json:"conversation_id"`
	Delay          string        `json:"delay"`
	Prompt         string        `json:"prompt"`
	State          CallbackState `json:"state"`
	FiresAt        time.Time     `json:"fires_at"`
	CreatedAt      time.Time     `json:"created_at"`
	// TenantID and AgentID capture the originating run's scope so the fired
	// follow-up run is started on the same tenant + agent. Both may be empty
	// for the default/unscoped case. Omitted from JSON when empty to preserve
	// the existing tool-result shape for unscoped callbacks.
	TenantID string `json:"tenant_id,omitempty"`
	AgentID  string `json:"agent_id,omitempty"`
}

type pendingCallback struct {
	info  CallbackInfo
	timer *time.Timer
}

// CallbackManager manages delayed callbacks for agent conversations.
type CallbackManager struct {
	mu        sync.Mutex
	callbacks map[string]*pendingCallback // keyed by callback ID
	byConv    map[string][]string         // conversation ID -> callback IDs
	starter   RunStarter
	now       func() time.Time
	stopped   bool
	// events is an optional sink for callback lifecycle notifications. Nil by
	// default (no emission). Set via WithEventSink. Read-only after construction.
	events CallbackEvents
}

// NewCallbackManager creates a new CallbackManager. Optional CallbackOption
// values (e.g. WithEventSink) configure observability; the zero-option call
// NewCallbackManager(starter) remains valid and emits no events.
func NewCallbackManager(starter RunStarter, opts ...CallbackOption) *CallbackManager {
	m := &CallbackManager{
		callbacks: make(map[string]*pendingCallback),
		byConv:    make(map[string][]string),
		starter:   starter,
		now:       time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	return m
}

// emitEvent forwards a callback lifecycle event to the configured sink, if any.
// It is always called OUTSIDE the manager lock so the sink cannot deadlock or
// re-enter the manager while it holds m.mu.
func (m *CallbackManager) emitEvent(event string, info CallbackInfo) {
	if m.events == nil {
		return
	}
	m.events.Emit(event, info)
}

// removeFromByConv removes id from the per-conversation byConv slice, freeing
// the slot for future callbacks. It prunes the map entry when the slice
// becomes empty. Callers MUST hold m.mu.
func (m *CallbackManager) removeFromByConv(convID, id string) {
	ids := m.byConv[convID]
	for i, v := range ids {
		if v == id {
			// Swap-remove: replace the found element with the last, then shorten.
			ids[i] = ids[len(ids)-1]
			ids[len(ids)-1] = "" // zero for GC
			ids = ids[:len(ids)-1]
			break
		}
	}
	if len(ids) == 0 {
		delete(m.byConv, convID)
	} else {
		m.byConv[convID] = ids
	}
}

// Set schedules a new delayed callback. The SetRequest carries the
// conversation, delay, prompt, and the originating run's scope (tenant +
// agent); the scope is stored on the callback and threaded through to StartRun
// when the callback fires so the follow-up run runs on the same tenant + agent.
func (m *CallbackManager) Set(req SetRequest) (CallbackInfo, error) {
	conversationID := req.ConversationID
	delay := req.Delay
	prompt := req.Prompt

	if delay < MinCallbackDelay {
		return CallbackInfo{}, fmt.Errorf("delay %v is less than minimum %v", delay, MinCallbackDelay)
	}
	if delay > MaxCallbackDelay {
		return CallbackInfo{}, fmt.Errorf("delay %v exceeds maximum %v", delay, MaxCallbackDelay)
	}
	if prompt == "" {
		return CallbackInfo{}, fmt.Errorf("prompt must not be empty")
	}

	m.mu.Lock()

	if m.stopped {
		m.mu.Unlock()
		return CallbackInfo{}, fmt.Errorf("callback manager is shut down")
	}

	// Check per-conversation limit
	if len(m.byConv[conversationID]) >= MaxCallbacksPerConv {
		m.mu.Unlock()
		return CallbackInfo{}, fmt.Errorf("conversation %s has reached the maximum of %d callbacks", conversationID, MaxCallbacksPerConv)
	}

	id := uuid.New().String()
	now := m.now()
	info := CallbackInfo{
		ID:             id,
		ConversationID: conversationID,
		Delay:          delay.String(),
		Prompt:         prompt,
		State:          CallbackStatePending,
		FiresAt:        now.Add(delay),
		CreatedAt:      now,
		TenantID:       req.TenantID,
		AgentID:        req.AgentID,
	}

	timer := time.AfterFunc(delay, func() {
		m.fire(id)
	})

	m.callbacks[id] = &pendingCallback{info: info, timer: timer}
	m.byConv[conversationID] = append(m.byConv[conversationID], id)
	m.mu.Unlock()

	// Emit outside the lock so the sink cannot re-enter the manager.
	m.emitEvent(eventCallbackScheduled, info)

	return info, nil
}

// Cancel cancels a pending callback.
func (m *CallbackManager) Cancel(id string) (CallbackInfo, error) {
	m.mu.Lock()

	cb, ok := m.callbacks[id]
	if !ok {
		m.mu.Unlock()
		return CallbackInfo{}, fmt.Errorf("callback %s not found", id)
	}

	switch cb.info.State {
	case CallbackStateFired:
		m.mu.Unlock()
		return CallbackInfo{}, fmt.Errorf("callback %s already fired", id)
	case CallbackStateCanceled:
		m.mu.Unlock()
		return CallbackInfo{}, fmt.Errorf("callback %s already canceled", id)
	}

	cb.timer.Stop()
	cb.info.State = CallbackStateCanceled
	info := cb.info
	// Remove from byConv so the slot is freed for future callbacks on this
	// conversation. The callbacks map entry is kept so state can still be
	// queried by white-box tests; only the per-conversation slot matters.
	m.removeFromByConv(info.ConversationID, id)
	m.mu.Unlock()

	// Emit outside the lock so the sink cannot re-enter the manager.
	m.emitEvent(eventCallbackCanceled, info)

	return info, nil
}

// List returns all callbacks for a conversation.
func (m *CallbackManager) List(conversationID string) []CallbackInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	ids := m.byConv[conversationID]
	result := make([]CallbackInfo, 0, len(ids))
	for _, id := range ids {
		if cb, ok := m.callbacks[id]; ok {
			result = append(result, cb.info)
		}
	}
	return result
}

// ListAll returns all callbacks across every conversation. Like List, it only
// returns callbacks still tracked in the per-conversation index — pending
// callbacks. Fired and canceled callbacks are removed from that index when
// they transition, so they are excluded here too.
func (m *CallbackManager) ListAll() []CallbackInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]CallbackInfo, 0, len(m.callbacks))
	for _, ids := range m.byConv {
		for _, id := range ids {
			if cb, ok := m.callbacks[id]; ok {
				result = append(result, cb.info)
			}
		}
	}
	return result
}

// Shutdown stops all pending callbacks and prevents new ones.
func (m *CallbackManager) Shutdown() {
	m.mu.Lock()

	m.stopped = true
	var canceled []CallbackInfo
	for _, cb := range m.callbacks {
		if cb.info.State == CallbackStatePending {
			cb.timer.Stop()
			cb.info.State = CallbackStateCanceled
			canceled = append(canceled, cb.info)
			// Remove from byConv so slots are freed (prevents leaked entries
			// if the manager is reused or inspected after shutdown).
			m.removeFromByConv(cb.info.ConversationID, cb.info.ID)
		}
	}
	m.mu.Unlock()

	// Emit outside the lock so the sink cannot re-enter the manager.
	for _, info := range canceled {
		m.emitEvent(eventCallbackCanceled, info)
	}
}

// fire is called by the timer when a callback is ready.
func (m *CallbackManager) fire(id string) {
	m.mu.Lock()
	cb, ok := m.callbacks[id]
	if !ok || cb.info.State != CallbackStatePending {
		m.mu.Unlock()
		return
	}
	cb.info.State = CallbackStateFired
	info := cb.info
	convID := info.ConversationID
	prompt := info.Prompt
	tenantID := info.TenantID
	agentID := info.AgentID
	// Remove from byConv so the slot is freed for future callbacks on this
	// conversation. The callbacks map entry is kept so state can still be
	// queried; only the per-conversation slot counter matters for the limit.
	m.removeFromByConv(convID, id)
	m.mu.Unlock()

	// Emit outside the lock so the sink cannot re-enter the manager.
	m.emitEvent(eventCallbackFired, info)

	// Call StartRun outside the lock to avoid deadlocks. Carry the originating
	// run's tenant + agent so the follow-up run runs on the same scope.
	if err := m.starter.StartRun(prompt, convID, tenantID, agentID); err != nil {
		// Log error but callback is still marked as fired
		log.Printf("callback %s: StartRun error: %v", id, err)
	}
}

// --- Tool Constructors ---

func setDelayedCallbackTool(mgr *CallbackManager) Tool {
	def := Definition{
		Name:        "set_delayed_callback",
		Description: descriptions.Load("set_delayed_callback"),
		Action:      ActionExecute,
		Mutating:    true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"delay": map[string]any{
					"type":        "string",
					"description": "How long to wait before firing the callback. Go duration format: '30s', '5m', '1h30m'. Minimum 5s, maximum 1h.",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "The prompt to use when starting the new run. Should describe what to check or do.",
				},
			},
			"required": []string{"delay", "prompt"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			Delay  string `json:"delay"`
			Prompt string `json:"prompt"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse set_delayed_callback args: %w", err)
		}

		delay, err := time.ParseDuration(args.Delay)
		if err != nil {
			return "", fmt.Errorf("invalid delay format %q: %w", args.Delay, err)
		}

		md, ok := RunMetadataFromContext(ctx)
		if !ok {
			return "", fmt.Errorf("set_delayed_callback: no run metadata in context")
		}

		info, err := mgr.Set(SetRequest{
			ConversationID: md.ConversationID,
			Delay:          delay,
			Prompt:         args.Prompt,
			TenantID:       md.TenantID,
			AgentID:        md.AgentID,
		})
		if err != nil {
			return "", fmt.Errorf("set_delayed_callback failed: %w", err)
		}

		return MarshalToolResult(info)
	}

	return Tool{Definition: def, Handler: handler}
}

func cancelDelayedCallbackTool(mgr *CallbackManager) Tool {
	def := Definition{
		Name:        "cancel_delayed_callback",
		Description: descriptions.Load("cancel_delayed_callback"),
		Action:      ActionExecute,
		Mutating:    true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"callback_id": map[string]any{
					"type":        "string",
					"description": "The ID of the callback to cancel.",
				},
			},
			"required": []string{"callback_id"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			CallbackID string `json:"callback_id"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse cancel_delayed_callback args: %w", err)
		}

		info, err := mgr.Cancel(args.CallbackID)
		if err != nil {
			return "", fmt.Errorf("cancel_delayed_callback failed: %w", err)
		}

		return MarshalToolResult(info)
	}

	return Tool{Definition: def, Handler: handler}
}

func listDelayedCallbacksTool(mgr *CallbackManager) Tool {
	def := Definition{
		Name:         "list_delayed_callbacks",
		Description:  descriptions.Load("list_delayed_callbacks"),
		Action:       ActionList,
		ParallelSafe: true,
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		md, ok := RunMetadataFromContext(ctx)
		if !ok {
			return "", fmt.Errorf("list_delayed_callbacks: no run metadata in context")
		}

		callbacks := mgr.List(md.ConversationID)
		return MarshalToolResult(callbacks)
	}

	return Tool{Definition: def, Handler: handler}
}
