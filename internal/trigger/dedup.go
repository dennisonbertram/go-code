package trigger

import (
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
//
// STUB: CheckAndRecord currently always allows the request through. This is
// intentional scaffolding so the test suite in dedup_test.go compiles and
// fails on real assertions (a meaningful red) rather than a compile error.
// The real bounded/TTL logic is implemented in the paired fix commit.
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
	return nil
}
