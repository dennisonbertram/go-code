package main

import "testing"

// Happy-path smoke test. The full grading suite (including recency-on-Get
// and eviction-order checks) is applied separately.
func TestLRUHappyPath(t *testing.T) {
	c := NewLRU(2)
	c.Put(1, 100)
	c.Put(2, 200)

	if v, ok := c.Get(1); !ok || v != 100 {
		t.Fatalf("Get(1) = (%d, %v), want (100, true)", v, ok)
	}
	if c.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", c.Len())
	}
	if _, ok := c.Get(99); ok {
		t.Fatalf("Get(99) = (_, true), want a miss")
	}
}
