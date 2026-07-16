from __future__ import annotations

import subprocess
from pathlib import Path

# Hidden behavioral oracle. This test file is injected into the container at
# grading time only — the agent never sees it. It writes a fresh Go test into
# /app and runs it, so the decisive checks cannot be inspected or weakened by
# the agent.
ORACLE_TEST = r'''
package main

import (
	"reflect"
	"testing"
)

// The ready set must always break ties by choosing the lexicographically
// smallest ready node, and the result must be identical across repeated
// calls with the same input.
func TestOracleDeterministicOrder(t *testing.T) {
	nodes := []string{"a", "b", "c", "d", "e"}
	edges := [][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}, {"d", "e"}}
	want := []string{"a", "b", "c", "d", "e"}

	got1, err1 := TopoSort(nodes, edges)
	if err1 != nil {
		t.Fatalf("unexpected error: %v", err1)
	}
	if !reflect.DeepEqual(got1, want) {
		t.Fatalf("TopoSort() = %v, want %v", got1, want)
	}

	got2, err2 := TopoSort(nodes, edges)
	if err2 != nil {
		t.Fatalf("unexpected error on second call: %v", err2)
	}
	if !reflect.DeepEqual(got2, want) {
		t.Fatalf("second call TopoSort() = %v, want %v", got2, want)
	}
	if !reflect.DeepEqual(got1, got2) {
		t.Fatalf("non-deterministic output: first call = %v, second call = %v", got1, got2)
	}
}

// Disconnected nodes (no incident edges) must be included in the output and
// must participate in the same lexicographic tie-break as any other ready
// node.
func TestOracleTieBreakAndDisconnected(t *testing.T) {
	nodes := []string{"x", "m", "a", "z"}
	edges := [][2]string{{"z", "m"}}
	want := []string{"a", "x", "z", "m"}

	got, err := TopoSort(nodes, edges)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TopoSort() = %v, want %v", got, want)
	}
}

// A cyclic graph must be reported as an error with a nil result, not a
// partial or incorrect order with a nil error.
func TestOracleCycle(t *testing.T) {
	nodes := []string{"a", "b", "c"}
	edges := [][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}}

	got, err := TopoSort(nodes, edges)
	if err == nil {
		t.Fatalf("expected a non-nil error for a cyclic graph, got nil (result=%v)", got)
	}
	if len(got) != 0 {
		t.Fatalf("expected a nil/empty result for a cyclic graph, got %v", got)
	}
}
'''


def _write_oracle() -> None:
    Path("/app/zz_oracle_test.go").write_text(ORACLE_TEST)


def test_build_succeeds() -> None:
    r = subprocess.run(
        ["go", "build", "./..."], cwd="/app", capture_output=True, text=True, timeout=60
    )
    assert r.returncode == 0, f"go build failed:\nstdout: {r.stdout}\nstderr: {r.stderr}"


def test_behavioral_oracle() -> None:
    _write_oracle()
    r = subprocess.run(
        ["go", "test", "-race", "-count=1", "-run", "TestOracle", "./..."],
        cwd="/app",
        capture_output=True,
        text=True,
        timeout=150,
    )
    assert r.returncode == 0, (
        f"behavioral oracle failed:\nstdout: {r.stdout}\nstderr: {r.stderr}"
    )
