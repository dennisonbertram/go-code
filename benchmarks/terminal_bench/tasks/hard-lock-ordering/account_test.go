package main

import "testing"

// Happy-path smoke test. The full grading suite (including concurrent
// deadlock, race, and overdraft checks) is applied separately.
func TestTransferHappyPath(t *testing.T) {
	a := NewAccount(1, 1000)
	b := NewAccount(2, 500)

	if err := Transfer(a, b, 200); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := a.Balance(), int64(800); got != want {
		t.Fatalf("a.Balance() = %d, want %d", got, want)
	}
	if got, want := b.Balance(), int64(700); got != want {
		t.Fatalf("b.Balance() = %d, want %d", got, want)
	}
}
