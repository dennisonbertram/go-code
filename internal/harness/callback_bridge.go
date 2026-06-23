package harness

import (
	"sync"
	"time"

	htools "go-agent-harness/internal/harness/tools"
)

// CallbackEventBridge forwards delayed-callback lifecycle events
// (callback.scheduled / callback.fired / callback.canceled) from a
// tools.CallbackManager onto the originating run's event stream so they are
// observable on the live SSE stream.
//
// Observability semantics:
//
//   - callback.scheduled is emitted synchronously while the agent's
//     set_delayed_callback tool call is executing, i.e. DURING the originating
//     run. The run is live, so the bridge resolves it by conversation ID and
//     the event is observable on that run's SSE stream in real time.
//   - callback.fired and callback.canceled may occur after the originating run
//     has ended (the timer fires, or the manager is shut down). If a live run
//     still exists for the conversation the event is emitted there; otherwise
//     there is no open stream to deliver it to and the bridge is a no-op for
//     SSE. The event is still delivered to the sink at the unit level (T3).
//
// The runner is bound lazily because the CallbackManager is constructed before
// the Runner exists (the same chicken-and-egg the callbackRunStarter solves).
// A nil runner makes Emit a no-op.
type CallbackEventBridge struct {
	mu     sync.RWMutex
	runner *Runner
}

// NewCallbackEventBridge returns an unbound bridge. Call BindRunner once the
// Runner has been constructed.
func NewCallbackEventBridge() *CallbackEventBridge {
	return &CallbackEventBridge{}
}

// NewCallbackManager builds a tools.CallbackManager whose lifecycle events are,
// by default, bridged onto the originating run's SSE stream via this Runner.
// Use this when the Runner already exists. When the manager must be constructed
// before the Runner (as in harnessd's startup), construct a
// CallbackEventBridge, pass it via tools.WithEventSink, and call BindRunner
// once the Runner is built.
func (r *Runner) NewCallbackManager(starter htools.RunStarter, opts ...htools.CallbackOption) *htools.CallbackManager {
	bridge := NewCallbackEventBridge()
	bridge.BindRunner(r)
	all := make([]htools.CallbackOption, 0, len(opts)+1)
	all = append(all, htools.WithEventSink(bridge))
	all = append(all, opts...)
	return htools.NewCallbackManager(starter, all...)
}

// BindRunner attaches the Runner the bridge forwards events to. Safe to call
// once, after the Runner is built.
func (b *CallbackEventBridge) BindRunner(r *Runner) {
	b.mu.Lock()
	b.runner = r
	b.mu.Unlock()
}

// Emit implements tools.CallbackEvents. It resolves a live run for the
// callback's conversation and emits the event there. If no runner is bound or
// no live run exists for the conversation, Emit is a no-op (the event has no
// open SSE stream to be delivered on).
func (b *CallbackEventBridge) Emit(event string, info htools.CallbackInfo) {
	b.mu.RLock()
	r := b.runner
	b.mu.RUnlock()
	if r == nil {
		return
	}
	r.emitCallbackEvent(event, info)
}

// emitCallbackEvent resolves the live run that owns the callback's conversation
// and emits the lifecycle event on it. No-op when no live run is found.
func (r *Runner) emitCallbackEvent(event string, info htools.CallbackInfo) {
	runID, ok := r.liveRunForConversation(info.ConversationID)
	if !ok {
		return
	}
	r.emit(runID, EventType(event), callbackEventPayload(info))
}

// liveRunForConversation returns the ID of the most-recently-created
// non-terminated run whose conversation matches convID, if one exists.
// When multiple live runs share a conversation (rare), picking the newest
// ensures the event is delivered to the run that scheduled the callback —
// the set_delayed_callback tool call always originates from the most-recent
// run on the conversation.  When there is 0 or 1 match the result is
// identical to the previous unordered iteration.
func (r *Runner) liveRunForConversation(convID string) (string, bool) {
	if convID == "" {
		return "", false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var (
		bestID string
		bestAt time.Time
	)
	for id, state := range r.runs {
		if state == nil || state.terminated {
			continue
		}
		if state.run.ConversationID != convID {
			continue
		}
		if bestID == "" || state.run.CreatedAt.After(bestAt) {
			bestID = id
			bestAt = state.run.CreatedAt
		}
	}
	return bestID, bestID != ""
}

// callbackEventPayload builds the SSE payload for a callback lifecycle event.
func callbackEventPayload(info htools.CallbackInfo) map[string]any {
	return map[string]any{
		"callback_id":     info.ID,
		"conversation_id": info.ConversationID,
		"state":           string(info.State),
		"delay":           info.Delay,
		"prompt":          info.Prompt,
		"fires_at":        info.FiresAt,
		"created_at":      info.CreatedAt,
	}
}
