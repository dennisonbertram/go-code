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
	"testing"
)

// Get must refresh recency on a hit: a key that was just read must not be
// evicted as if it were the least-recently-used entry.
func TestOracleGetUpdatesRecency(t *testing.T) {
	c := NewLRU(2)
	c.Put(1, 10)
	c.Put(2, 20)

	// Touch key 1 so key 2 becomes the least-recently-used entry.
	if v, ok := c.Get(1); !ok || v != 10 {
		t.Fatalf("Get(1) = (%d, %v), want (10, true)", v, ok)
	}

	// Inserting a third key must now evict key 2, not key 1.
	c.Put(3, 30)

	if v, ok := c.Get(1); !ok || v != 10 {
		t.Fatalf("after eviction: Get(1) = (%d, %v), want (10, true) "+
			"(key 1 was just read and must not have been evicted)", v, ok)
	}
	if v, ok := c.Get(3); !ok || v != 30 {
		t.Fatalf("after eviction: Get(3) = (%d, %v), want (30, true)", v, ok)
	}
	if v, ok := c.Get(2); ok {
		t.Fatalf("after eviction: Get(2) = (%d, true), want a miss "+
			"(key 2 was least-recently-used and should have been evicted)", v)
	}
}

// With no Gets in between, Put must evict strictly in insertion order.
func TestOracleEvictsLRU(t *testing.T) {
	c := NewLRU(2)
	c.Put(1, 100)
	c.Put(2, 200)
	c.Put(3, 300)

	if _, ok := c.Get(1); ok {
		t.Fatalf("Get(1) = (_, true), want a miss (key 1 is least-recently-used)")
	}
	if v, ok := c.Get(2); !ok || v != 200 {
		t.Fatalf("Get(2) = (%d, %v), want (200, true)", v, ok)
	}
	if v, ok := c.Get(3); !ok || v != 300 {
		t.Fatalf("Get(3) = (%d, %v), want (300, true)", v, ok)
	}
}

// Put on an existing key must update its value in place, not create a
// duplicate entry or trigger a spurious eviction.
func TestOracleUpdateExisting(t *testing.T) {
	c := NewLRU(2)
	c.Put(1, 10)
	c.Put(1, 99)

	if got := c.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1", got)
	}
	if v, ok := c.Get(1); !ok || v != 99 {
		t.Fatalf("Get(1) = (%d, %v), want (99, true)", v, ok)
	}
}

// Get on a key that was never inserted must report a miss.
func TestOracleMiss(t *testing.T) {
	c := NewLRU(1)
	if _, ok := c.Get(42); ok {
		t.Fatalf("Get(42) = (_, true), want a miss on an empty cache")
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
