package main

import (
	"reflect"
	"testing"
)

// Happy-path smoke test. The full grading suite (including long words,
// irregular whitespace, and embedded newlines) is applied separately.
func TestWrapHappyPath(t *testing.T) {
	got := Wrap("go is fun", 20)
	want := []string{"go is fun"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Wrap(%q, 20) = %v, want %v", "go is fun", got, want)
	}
}
