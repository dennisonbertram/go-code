package main

import "sync"

// entry holds the memoized result for a single key. sync.Once guarantees
// that compute() runs at most once per entry, no matter how many goroutines
// race to resolve it, and that every goroutine observes the value written
// by the winning call (Once.Do establishes a happens-before edge between
// the function it runs and every call to Do returning).
type entry struct {
	once  sync.Once
	value int
}

// Cache is a thread-safe memoizing cache of string -> int.
//
// Reference solution: GetOrCompute never lets two callers "win" the race to
// compute the same key. The map itself (which entries exist) is protected
// by a plain mutex, but the map lock is held only long enough to get or
// create the *entry for a key -- it is never held while compute() runs, so
// unrelated keys are never serialized against each other. Per-key
// single-flight is provided by that entry's sync.Once, which is safe to
// call concurrently and runs compute() exactly once.
type Cache struct {
	mu    sync.Mutex
	items map[string]*entry
}

// NewCache creates an empty Cache.
func NewCache() *Cache {
	return &Cache{items: make(map[string]*entry)}
}

// GetOrCompute returns the cached value for key, computing and storing it
// via compute if it is not already present. compute is called at most once
// per key even under concurrent callers.
func (c *Cache) GetOrCompute(key string, compute func() int) int {
	c.mu.Lock()
	e, ok := c.items[key]
	if !ok {
		e = &entry{}
		c.items[key] = e
	}
	c.mu.Unlock()

	e.once.Do(func() {
		e.value = compute()
	})

	return e.value
}
