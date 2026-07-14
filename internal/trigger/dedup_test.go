package trigger

import (
	"strconv"
	"testing"
	"time"
)

// TestDeliveryDedupCache_RejectsReplayedKey is an ATTACK test (S5): the same
// source+deliveryID presented twice must be rejected the second time — a
// captured valid signed payload replayed with its original delivery ID must
// not re-trigger a run.
func TestDeliveryDedupCache_RejectsReplayedKey(t *testing.T) {
	t.Parallel()
	c := NewDeliveryDedupCache(5*time.Minute, 100)

	if err := c.CheckAndRecord("github", "delivery-1"); err != nil {
		t.Fatalf("first delivery should be accepted, got error: %v", err)
	}
	if err := c.CheckAndRecord("github", "delivery-1"); err == nil {
		t.Fatal("replayed delivery ID must be rejected, got nil error")
	}
}

// TestDeliveryDedupCache_DifferentIDsAccepted verifies that distinct delivery
// IDs are independent — dedup must not falsely reject unrelated deliveries.
func TestDeliveryDedupCache_DifferentIDsAccepted(t *testing.T) {
	t.Parallel()
	c := NewDeliveryDedupCache(5*time.Minute, 100)

	if err := c.CheckAndRecord("github", "delivery-a"); err != nil {
		t.Fatalf("delivery-a should be accepted: %v", err)
	}
	if err := c.CheckAndRecord("github", "delivery-b"); err != nil {
		t.Fatalf("delivery-b should be accepted: %v", err)
	}
}

// TestDeliveryDedupCache_ScopedPerSource verifies that the same delivery ID
// under two DIFFERENT sources is not treated as a collision — dedup keys are
// scoped per source.
func TestDeliveryDedupCache_ScopedPerSource(t *testing.T) {
	t.Parallel()
	c := NewDeliveryDedupCache(5*time.Minute, 100)

	if err := c.CheckAndRecord("github", "shared-id"); err != nil {
		t.Fatalf("github delivery should be accepted: %v", err)
	}
	if err := c.CheckAndRecord("linear", "shared-id"); err != nil {
		t.Fatalf("linear delivery with the same raw ID under a different source should be accepted: %v", err)
	}
}

// TestDeliveryDedupCache_EmptyDeliveryIDAlwaysAllowed verifies that a source
// with no delivery ID at all (nothing to dedup on) is never blocked by dedup.
func TestDeliveryDedupCache_EmptyDeliveryIDAlwaysAllowed(t *testing.T) {
	t.Parallel()
	c := NewDeliveryDedupCache(5*time.Minute, 100)

	for i := 0; i < 3; i++ {
		if err := c.CheckAndRecord("external-trigger", ""); err != nil {
			t.Fatalf("empty delivery ID must never be dedup-rejected, got: %v", err)
		}
	}
}

// TestDeliveryDedupCache_AllowsReplayAfterTTLExpiry verifies the TTL window:
// once an entry has expired, the same delivery ID is treated as fresh again
// (bounded exposure, not permanent state growth).
func TestDeliveryDedupCache_AllowsReplayAfterTTLExpiry(t *testing.T) {
	t.Parallel()
	fakeNow := time.Now()
	c := NewDeliveryDedupCache(1*time.Minute, 100)
	c.nowFunc = func() time.Time { return fakeNow }

	if err := c.CheckAndRecord("github", "delivery-ttl"); err != nil {
		t.Fatalf("first delivery should be accepted: %v", err)
	}
	// Still within the TTL window: must be rejected.
	fakeNow = fakeNow.Add(30 * time.Second)
	if err := c.CheckAndRecord("github", "delivery-ttl"); err == nil {
		t.Fatal("expected rejection within the TTL window")
	}
	// Past the TTL window: must be allowed again.
	fakeNow = fakeNow.Add(1 * time.Minute)
	if err := c.CheckAndRecord("github", "delivery-ttl"); err != nil {
		t.Fatalf("expected acceptance after TTL expiry, got: %v", err)
	}
}

// TestDeliveryDedupCache_BoundedSize verifies the cache never grows past
// maxSize — a flood of distinct delivery IDs must not cause unbounded memory
// growth (evicting the oldest entries to make room).
func TestDeliveryDedupCache_BoundedSize(t *testing.T) {
	t.Parallel()
	const maxSize = 10
	c := NewDeliveryDedupCache(1*time.Hour, maxSize)

	for i := 0; i < maxSize*5; i++ {
		if err := c.CheckAndRecord("github", "delivery-"+strconv.Itoa(i)); err != nil {
			t.Fatalf("delivery %d should be accepted: %v", i, err)
		}
	}

	c.mu.Lock()
	size := len(c.seen)
	c.mu.Unlock()
	if size > maxSize {
		t.Fatalf("expected cache size to stay <= %d, got %d", maxSize, size)
	}
}
