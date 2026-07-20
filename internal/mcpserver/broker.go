package mcpserver

import (
	"encoding/json"
	"sync"
)

// Notification is a JSON-RPC 2.0 notification payload used for SSE fan-out.
type Notification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// channelBufSize is the buffer size for each subscriber channel.
// Non-blocking sends drop notifications when the buffer is full.
const channelBufSize = 64

// Broker fans out notifications to registered subscribers.
// Subscriptions can be per-run (keyed by run ID) or global (receive all notifications).
// It is safe for concurrent use.
type Broker struct {
	mu          sync.RWMutex
	subscribers map[string][]chan Notification // run_id → channels
	globalSubs  []chan Notification            // receive ALL notifications
}

// NewBroker creates a new Broker.
func NewBroker() *Broker {
	return &Broker{
		subscribers: make(map[string][]chan Notification),
	}
}

// Subscribe registers a channel to receive notifications for runID.
// Returns the channel and a cancel func that removes the subscription.
func (b *Broker) Subscribe(runID string) (<-chan Notification, func()) {
	ch := make(chan Notification, channelBufSize)

	b.mu.Lock()
	b.subscribers[runID] = append(b.subscribers[runID], ch)
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		chans := b.subscribers[runID]
		updated := make([]chan Notification, 0, len(chans))
		for _, c := range chans {
			if c != ch {
				updated = append(updated, c)
			}
		}
		if len(updated) == 0 {
			delete(b.subscribers, runID)
		} else {
			b.subscribers[runID] = updated
		}
	}
	return ch, cancel
}

// SubscribeAll registers a channel that receives ALL published notifications.
// Returns the channel and a cancel func.
func (b *Broker) SubscribeAll() (<-chan Notification, func()) {
	ch := make(chan Notification, channelBufSize)

	b.mu.Lock()
	b.globalSubs = append(b.globalSubs, ch)
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		updated := make([]chan Notification, 0, len(b.globalSubs))
		for _, c := range b.globalSubs {
			if c != ch {
				updated = append(updated, c)
			}
		}
		b.globalSubs = updated
	}
	return ch, cancel
}

// Publish sends n to all subscribers of runID AND all global subscribers.
// Non-blocking: drops notifications for subscribers with full buffers.
func (b *Broker) Publish(runID string, n Notification) {
	b.mu.RLock()
	perRun := make([]chan Notification, len(b.subscribers[runID]))
	copy(perRun, b.subscribers[runID])
	global := make([]chan Notification, len(b.globalSubs))
	copy(global, b.globalSubs)
	b.mu.RUnlock()

	for _, ch := range perRun {
		select {
		case ch <- n:
		default:
		}
	}
	for _, ch := range global {
		select {
		case ch <- n:
		default:
		}
	}
}

// PublishAll sends n to all global subscribers only (no per-run filtering).
func (b *Broker) PublishAll(n Notification) {
	b.mu.RLock()
	global := make([]chan Notification, len(b.globalSubs))
	copy(global, b.globalSubs)
	b.mu.RUnlock()

	for _, ch := range global {
		select {
		case ch <- n:
		default:
		}
	}
}

// ActiveSubscriptions returns total count of active channels (per-run + global).
func (b *Broker) ActiveSubscriptions() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	total := len(b.globalSubs)
	for _, chans := range b.subscribers {
		total += len(chans)
	}
	return total
}
