package main

import "container/list"

// entry is the value stored in each list.Element.
type entry struct {
	key   int
	value int
}

// LRU is a fixed-capacity int->int cache with least-recently-used eviction.
//
// Contract:
//   - Both Get (on a hit) and Put must mark the key as most-recently-used.
//   - When Put would exceed capacity, evict exactly the least-recently-used
//     key.
//   - Get returns (value, true) on hit and (0, false) on miss; Put updates
//     the value if the key already exists (no duplicate entry, no spurious
//     eviction).
//
// BUG: Get looks up the map and returns the value WITHOUT moving the
// element to the most-recently-used position. Put correctly moves/inserts
// at the front and evicts from the back, but because Get never refreshes
// recency, a key that was just read can still be evicted as if it were the
// least-recently-used entry.
type LRU struct {
	capacity int
	ll       *list.List
	items    map[int]*list.Element
}

// NewLRU creates an LRU cache that holds at most capacity entries.
func NewLRU(capacity int) *LRU {
	return &LRU{
		capacity: capacity,
		ll:       list.New(),
		items:    make(map[int]*list.Element),
	}
}

// Put inserts or updates key with value, marking it most-recently-used. If
// this would exceed capacity, the least-recently-used entry is evicted.
func (c *LRU) Put(key, value int) {
	if el, ok := c.items[key]; ok {
		el.Value.(*entry).value = value
		c.ll.MoveToFront(el)
		return
	}

	el := c.ll.PushFront(&entry{key: key, value: value})
	c.items[key] = el

	if c.ll.Len() > c.capacity {
		back := c.ll.Back()
		if back != nil {
			c.ll.Remove(back)
			delete(c.items, back.Value.(*entry).key)
		}
	}
}

// Get returns the value for key and whether it was found.
func (c *LRU) Get(key int) (int, bool) {
	el, ok := c.items[key]
	if !ok {
		return 0, false
	}
	return el.Value.(*entry).value, true
}

// Len returns the number of entries currently in the cache.
func (c *LRU) Len() int {
	return c.ll.Len()
}
