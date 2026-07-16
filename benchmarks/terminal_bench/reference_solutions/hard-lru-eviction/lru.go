package main

import "container/list"

// entry is the value stored in each list.Element.
type entry struct {
	key   int
	value int
}

// LRU is a fixed-capacity int->int cache with least-recently-used eviction.
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

// Get returns the value for key and whether it was found. Reference fix: a
// hit also marks the key as most-recently-used, same as Put does, so a key
// that was just read is not the next eviction candidate.
func (c *LRU) Get(key int) (int, bool) {
	el, ok := c.items[key]
	if !ok {
		return 0, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*entry).value, true
}

// Len returns the number of entries currently in the cache.
func (c *LRU) Len() int {
	return c.ll.Len()
}
