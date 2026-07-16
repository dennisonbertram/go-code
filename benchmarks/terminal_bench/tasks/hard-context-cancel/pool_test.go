package main

import (
	"context"
	"testing"
)

// Happy-path smoke test. The full grading suite (including cancellation and
// goroutine-leak checks) is applied separately.
func TestProcessHappyPath(t *testing.T) {
	jobs := []int{1, 2, 3, 4, 5}
	got, err := Process(context.Background(), jobs, 3, func(x int) int { return x * x })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []int{1, 4, 9, 16, 25}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("results[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}
