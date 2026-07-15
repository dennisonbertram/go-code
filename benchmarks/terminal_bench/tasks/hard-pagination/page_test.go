package main

import (
	"reflect"
	"testing"
)

// Happy-path smoke test. The full grading suite (including boundary and
// out-of-range checks) is applied separately.
func TestPaginateHappyPath(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e", "f"}
	got, totalPages := Paginate(items, 2, 1)
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Paginate page 1 = %v, want %v", got, want)
	}
	if totalPages != 3 {
		t.Fatalf("totalPages = %d, want 3", totalPages)
	}
}
