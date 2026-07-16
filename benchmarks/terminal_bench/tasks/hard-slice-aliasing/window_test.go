package main

import (
	"reflect"
	"testing"
)

// Happy-path smoke test. The full grading suite (including independence and
// edge-case checks) is applied separately.
func TestWindowsHappyPath(t *testing.T) {
	data := []int{1, 2, 3, 4, 5}
	got := Windows(data, 3)
	want := [][]int{{1, 2, 3}, {2, 3, 4}, {3, 4, 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Windows(%v, 3) = %v, want %v", data, got, want)
	}
}
