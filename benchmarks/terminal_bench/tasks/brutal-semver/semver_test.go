package main

import "testing"

// Happy-path smoke test. The full grading suite (including pre-release and
// build-metadata precedence rules) is applied separately.
func TestCompareHappyPath(t *testing.T) {
	if got := Compare("1.0.0", "2.0.0"); got != -1 {
		t.Fatalf("Compare(%q, %q) = %d, want -1", "1.0.0", "2.0.0", got)
	}
	if got := Compare("2.0.0", "1.0.0"); got != 1 {
		t.Fatalf("Compare(%q, %q) = %d, want 1", "2.0.0", "1.0.0", got)
	}
	if got := Compare("1.0.0", "1.0.0"); got != 0 {
		t.Fatalf("Compare(%q, %q) = %d, want 0", "1.0.0", "1.0.0", got)
	}
}
