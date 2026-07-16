package main

import "sync"

// Cache is intended to be a thread-safe memoizing cache of string -> int.
//
// Contract:
//   - GetOrCompute returns the cached value for key if it is already present.
//   - Otherwise it calls compute() exactly once to produce the value, stores
//     it, and returns it.
//   - Under concurrent callers using the SAME key, compute() must be called
//     AT MOST ONCE for that key, and every caller must observe the same
//     resulting value.
//   - The cache must be free of data races.
//
// BUG: the read in GetOrCompute is unsynchronized (no lock is held while
// checking c.items), while the store below is guarded by c.mu. That looks
// "thread-safe" at a glance -- the write is protected by a mutex -- but it
// is not: the unlocked read races with concurrent locked writes, and, worse,
// there is a check-then-act gap between the unlocked "is key present?"
// check and the locked store. Multiple concurrent callers for the same
// absent key can all observe "not present" before any of them has stored a
// value, so each of them calls compute() and stores its own result. compute
// can therefore run many times for a single key, and callers can observe
// different values depending on scheduling -- exactly the single-flight
// violation this cache must not have.
type Cache struct {
	mu    sync.Mutex
	items map[string]int
}

// NewCache creates an empty Cache.
func NewCache() *Cache {
	return &Cache{items: make(map[string]int)}
}

// GetOrCompute returns the cached value for key, computing and storing it
// via compute if it is not already present.
func (c *Cache) GetOrCompute(key string, compute func() int) int {
	if v, ok := c.items[key]; ok { // BUG: unsynchronized read of c.items
		return v
	}

	// BUG: compute() runs with no lock held at all, so concurrent callers
	// that all missed the check above race each other here too.
	v := compute()

	c.mu.Lock()
	c.items[key] = v
	c.mu.Unlock()

	return v
}
