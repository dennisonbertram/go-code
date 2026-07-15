package main

import (
	"reflect"
	"testing"
)

// Happy-path smoke test covering only a trivial comma-separated line. The
// full grading suite (quoted fields with embedded commas, escaped quotes,
// embedded newlines, and trailing-newline handling) is applied separately.
func TestParseCSVSmoke(t *testing.T) {
	got, err := ParseCSV("a,b,c")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := [][]string{{"a", "b", "c"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseCSV(%q) = %v, want %v", "a,b,c", got, want)
	}
}
