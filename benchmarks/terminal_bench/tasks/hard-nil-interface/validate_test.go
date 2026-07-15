package main

import "testing"

// Happy-path smoke test. The full grading suite (including the nil-interface
// regression checks) is applied separately.
func TestValidateHappyPath(t *testing.T) {
	got := Validate(User{Name: "Grace", Age: 30})
	if got != nil {
		t.Fatalf("unexpected error: %v", got)
	}
}
