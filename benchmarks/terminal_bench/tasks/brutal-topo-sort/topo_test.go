package main

import (
	"reflect"
	"testing"
)

// Happy-path smoke test: a trivial two-node chain with no ties, no
// disconnected nodes, and no cycle. The full grading suite (deterministic
// tie-break, disconnected-node inclusion, and cycle detection) is applied
// separately.
func TestTopoSortHappyPath(t *testing.T) {
	nodes := []string{"a", "b"}
	edges := [][2]string{{"a", "b"}}

	got, err := TopoSort(nodes, edges)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TopoSort() = %v, want %v", got, want)
	}
}
