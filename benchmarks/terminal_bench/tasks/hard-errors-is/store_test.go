package main

import "testing"

// Happy-path smoke test. The full grading suite (including the errors.Is /
// errors.As regression checks) is applied separately.
func TestStoreHappyPath(t *testing.T) {
	s := NewStore()
	s.Put("a", "1")

	got, err := s.Get("a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1" {
		t.Fatalf("got %q, want %q", got, "1")
	}
}
