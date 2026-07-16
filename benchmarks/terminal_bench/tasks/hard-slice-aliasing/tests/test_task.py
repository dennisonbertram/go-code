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

// Windows must return the correct values for each window, in order.
func TestOracleValues(t *testing.T) {
	data := []int{1, 2, 3, 4, 5}
	got := Windows(data, 3)
	want := [][]int{{1, 2, 3}, {2, 3, 4}, {3, 4, 5}}
	if len(got) != len(want) {
		t.Fatalf("Windows(%v, 3) returned %d windows %v, want %d windows %v",
			data, len(got), got, len(want), want)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("windows[%d] = %v, want %v (full result: %v)", i, got[i], want[i], got)
		}
	}
}

// Returned windows must be independent of the input slice and of each other:
// neither an in-place write nor an append through one window may leak into
// data or into a different window's backing array.
func TestOracleIndependence(t *testing.T) {
	data := []int{1, 2, 3, 4, 5}
	windows := Windows(data, 3)
	if len(windows) < 2 {
		t.Fatalf("expected at least 2 windows for data=%v size=3, got %d: %v", data, len(windows), windows)
	}

	w0 := windows[0]
	w0[0] = 999          // in-place write through the returned window
	w1 := append(w0, 7)  // append through the returned window
	_ = w1

	wantData := []int{1, 2, 3, 4, 5}
	if !reflect.DeepEqual(data, wantData) {
		t.Fatalf("mutating windows[0] corrupted the input slice: data = %v, want %v "+
			"(windows are aliasing data's backing array instead of being independent copies)",
			data, wantData)
	}

	wantW1 := []int{2, 3, 4}
	if !reflect.DeepEqual(windows[1], wantW1) {
		t.Fatalf("mutating/appending windows[0] corrupted windows[1]: got %v, want %v "+
			"(windows are aliasing each other's backing array instead of being independent copies)",
			windows[1], wantW1)
	}
}

// size <= 0 or size > len(data) must yield an empty result; size == len(data)
// must yield exactly one window equal to the whole input.
func TestOracleEdge(t *testing.T) {
	data := []int{1, 2, 3, 4, 5}

	if got := Windows(data, 0); len(got) != 0 {
		t.Fatalf("Windows(data, 0) returned %d windows %v, want 0", len(got), got)
	}
	if got := Windows(data, 10); len(got) != 0 {
		t.Fatalf("Windows(data, 10) returned %d windows %v, want 0 (size > len(data))", len(got), got)
	}

	got := Windows(data, len(data))
	if len(got) != 1 {
		t.Fatalf("Windows(data, len(data)) returned %d windows %v, want exactly 1", len(got), got)
	}
	want := []int{1, 2, 3, 4, 5}
	if !reflect.DeepEqual(got[0], want) {
		t.Fatalf("Windows(data, len(data))[0] = %v, want %v", got[0], want)
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
