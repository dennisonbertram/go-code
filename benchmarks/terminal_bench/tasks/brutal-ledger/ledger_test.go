package main

import "testing"

// Happy-path smoke test. The full grading suite (including TopN ordering,
// TopN bounds, and concurrency checks) is applied separately.
func TestLedgerHappyPath(t *testing.T) {
	l := NewLedger()
	l.Post("alice", 100)
	if got, want := l.Balance("alice"), int64(100); got != want {
		t.Fatalf("Balance(\"alice\") = %d, want %d", got, want)
	}
}
