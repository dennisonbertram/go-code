package main

import "testing"

// Happy-path smoke test: a single goroutine calling GetOrCompute. This does
// NOT exercise concurrent callers -- the full grading suite (including the
// single-flight and data-race checks) is applied separately.
func TestGetOrComputeHappyPath(t *testing.T) {
	c := NewCache()

	calls := 0
	v := c.GetOrCompute("a", func() int {
		calls++
		return 7
	})
	if v != 7 {
		t.Fatalf(`GetOrCompute("a") = %d, want 7`, v)
	}
	if calls != 1 {
		t.Fatalf("compute called %d times, want 1", calls)
	}

	// A second call for the same key must return the cached value without
	// invoking compute again.
	v2 := c.GetOrCompute("a", func() int {
		t.Fatalf("compute should not be called for an already-cached key")
		return -1
	})
	if v2 != 7 {
		t.Fatalf(`GetOrCompute("a") on second call = %d, want 7`, v2)
	}

	// A different key computes independently.
	v3 := c.GetOrCompute("b", func() int { return 100 })
	if v3 != 100 {
		t.Fatalf(`GetOrCompute("b") = %d, want 100`, v3)
	}
}
