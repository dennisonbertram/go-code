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

// totalPages must be the ceiling of len(items)/pageSize, not plain integer
// division. 5 items at pageSize 2 must report 3 pages, not 2.
func TestOracleTotalPagesCeil(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}
	_, totalPages := Paginate(items, 2, 1)
	if totalPages != 3 {
		t.Fatalf("totalPages = %d, want 3", totalPages)
	}
}

// The last (partial) page must return only the remaining items and must not
// panic from slicing past the end of the backing array.
func TestOraclePartialLastPageNoPanic(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}
	pageItems, totalPages := Paginate(items, 2, 3)
	want := []string{"e"}
	if !reflect.DeepEqual(pageItems, want) {
		t.Fatalf("pageItems = %v, want %v", pageItems, want)
	}
	if totalPages != 3 {
		t.Fatalf("totalPages = %d, want 3", totalPages)
	}
}

// Requesting a page below 1 or above totalPages must return an empty slice
// and the correct totalPages, without panicking.
func TestOracleOutOfRange(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}

	highItems, highTotal := Paginate(items, 2, 99)
	if len(highItems) != 0 {
		t.Fatalf("page=99: pageItems = %v, want empty", highItems)
	}
	if highTotal != 3 {
		t.Fatalf("page=99: totalPages = %d, want 3", highTotal)
	}

	lowItems, lowTotal := Paginate(items, 2, 0)
	if len(lowItems) != 0 {
		t.Fatalf("page=0: pageItems = %v, want empty", lowItems)
	}
	if lowTotal != 3 {
		t.Fatalf("page=0: totalPages = %d, want 3", lowTotal)
	}
}

// When items divide evenly into pages, totalPages must not be off by one
// and the last page must contain exactly the final pageSize items.
func TestOracleExactMultiple(t *testing.T) {
	items := []string{"a", "b", "c", "d"}
	_, totalPages := Paginate(items, 2, 1)
	if totalPages != 2 {
		t.Fatalf("totalPages = %d, want 2", totalPages)
	}
	pageItems, _ := Paginate(items, 2, 2)
	want := []string{"c", "d"}
	if !reflect.DeepEqual(pageItems, want) {
		t.Fatalf("page 2 = %v, want %v", pageItems, want)
	}
}

// Empty input and a non-positive pageSize must both be handled without
// panicking (no divide-by-zero) and must report totalPages == 0.
func TestOracleEmptyAndBadSize(t *testing.T) {
	nilItems, nilTotal := Paginate(nil, 2, 1)
	if len(nilItems) != 0 {
		t.Fatalf("nil items: pageItems = %v, want empty", nilItems)
	}
	if nilTotal != 0 {
		t.Fatalf("nil items: totalPages = %d, want 0", nilTotal)
	}

	items := []string{"a", "b", "c", "d", "e"}
	zeroItems, zeroTotal := Paginate(items, 0, 1)
	if len(zeroItems) != 0 {
		t.Fatalf("pageSize=0: pageItems = %v, want empty", zeroItems)
	}
	if zeroTotal != 0 {
		t.Fatalf("pageSize=0: totalPages = %d, want 0", zeroTotal)
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
