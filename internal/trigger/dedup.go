package trigger

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// DeliveryDedupCache is a bounded, TTL-based cache used to reject replayed
// webhook/trigger deliveries (S5 hardening). A captured, validly-signed
// webhook payload can otherwise be replayed indefinitely to re-trigger runs;
// dedup on the provider's delivery ID closes that gap.
//
// Entries are keyed by "source:deliveryID" so dedup is scoped per source.
// Safe for concurrent use.
type DeliveryDedupCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	maxSize int
	seen    map[string]time.Time
	// nowFunc is injectable for testing; defaults to time.Now.
	nowFunc func() time.Time
}

// NewDeliveryDedupCache returns a cache that remembers a delivery key for ttl
// and never grows past maxSize entries (oldest evicted first). A zero or
// negative ttl/maxSize falls back to sane defaults.
func NewDeliveryDedupCache(ttl time.Duration, maxSize int) *DeliveryDedupCache {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	if maxSize <= 0 {
		maxSize = 10000
	}
	return &DeliveryDedupCache{
		ttl:     ttl,
		maxSize: maxSize,
		seen:    make(map[string]time.Time),
	}
}

// CheckAndRecord returns an error if "source:deliveryID" has already been
// seen within the TTL window (a replay); otherwise it records the key and
// returns nil. An empty deliveryID is always allowed through — there is
// nothing to dedup on when the source did not supply one.
func (c *DeliveryDedupCache) CheckAndRecord(source, deliveryID string) error {
	if deliveryID == "" {
		return nil
	}
	key := strings.ToLower(strings.TrimSpace(source)) + ":" + deliveryID

	now := time.Now
	if c.nowFunc != nil {
		now = c.nowFunc
	}
	nowT := now()

	c.mu.Lock()
	defer c.mu.Unlock()

	c.evictExpiredLocked(nowT)

	if seenAt, ok := c.seen[key]; ok && nowT.Sub(seenAt) < c.ttl {
		return fmt.Errorf("duplicate delivery for source %q: id %q was already processed", source, deliveryID)
	}

	c.seen[key] = nowT
	c.evictOverflowLocked()
	return nil
}

// evictExpiredLocked removes entries whose TTL has elapsed. Callers must
// hold c.mu.
func (c *DeliveryDedupCache) evictExpiredLocked(now time.Time) {
	for k, t := range c.seen {
		if now.Sub(t) >= c.ttl {
			delete(c.seen, k)
		}
	}
}

// evictOverflowLocked removes the oldest entries until the cache is back
// under maxSize. Callers must hold c.mu.
func (c *DeliveryDedupCache) evictOverflowLocked() {
	if len(c.seen) <= c.maxSize {
		return
	}
	type kv struct {
		key string
		t   time.Time
	}
	entries := make([]kv, 0, len(c.seen))
	for k, t := range c.seen {
		entries = append(entries, kv{k, t})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].t.Before(entries[j].t) })
	overflow := len(c.seen) - c.maxSize
	for i := 0; i < overflow; i++ {
		delete(c.seen, entries[i].key)
	}
}
