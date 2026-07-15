from __future__ import annotations

import subprocess
from pathlib import Path

# Hidden behavioral oracle. This test file is injected into the container at
# grading time only -- the agent never sees it. It writes a fresh Go test
# into /app and runs it, so the decisive checks cannot be inspected or
# weakened by the agent.
ORACLE_TEST = r'''
package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A single key hit by many concurrent callers: compute must run exactly
// once for that key, and every caller must observe the same resulting
// value. The buggy cache.go's unsynchronized check-then-act lets many
// callers see "absent" before any of them stores a value, so compute runs
// more than once.
func TestOracleSingleFlight(t *testing.T) {
	c := NewCache()

	const n = 100
	var calls int64
	start := make(chan struct{})
	results := make([]int, n)

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = c.GetOrCompute("shared-key", func() int {
				atomic.AddInt64(&calls, 1)
				time.Sleep(5 * time.Millisecond)
				return 42
			})
		}(i)
	}
	close(start)
	wg.Wait()

	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Fatalf("compute was called %d times for one key, want exactly 1", got)
	}
	for i, v := range results {
		if v != 42 {
			t.Fatalf("results[%d] = %d, want 42", i, v)
		}
	}
}

// Distinct keys computed concurrently must each resolve to their own,
// correct value.
func TestOracleDistinctKeys(t *testing.T) {
	c := NewCache()

	const n = 50
	start := make(chan struct{})
	results := make([]int, n)

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			key := fmt.Sprintf("key-%d", i)
			results[i] = c.GetOrCompute(key, func() int {
				return i * i
			})
		}(i)
	}
	close(start)
	wg.Wait()

	for i, v := range results {
		want := i * i
		if v != want {
			t.Fatalf("results[%d] = %d, want %d", i, v, want)
		}
	}
}

// Sequential calls: once a key is cached, a later call must not invoke
// compute again.
func TestOracleCachedSecondCall(t *testing.T) {
	c := NewCache()

	first := c.GetOrCompute("once", func() int { return 9 })
	if first != 9 {
		t.Fatalf("first GetOrCompute = %d, want 9", first)
	}

	second := c.GetOrCompute("once", func() int {
		panic("compute must not be invoked for an already-cached key")
	})
	if second != 9 {
		t.Fatalf("second GetOrCompute = %d, want 9", second)
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
