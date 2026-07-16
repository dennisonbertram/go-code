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
	"context"
	"runtime"
	"testing"
	"time"
)

// Process must return ctx.Err() promptly when the context is already cancelled,
// instead of draining every (slow) job.
func TestOracleCancelReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	jobs := make([]int, 200)
	for i := range jobs {
		jobs[i] = i
	}
	slow := func(x int) int { time.Sleep(50 * time.Millisecond); return x * 2 }

	type result struct {
		err error
	}
	done := make(chan result, 1)
	go func() {
		_, err := Process(ctx, jobs, 4, slow)
		done <- result{err: err}
	}()

	select {
	case r := <-done:
		if r.err == nil {
			t.Fatalf("Process returned nil error after cancellation; expected ctx.Err()")
		}
		if r.err != context.Canceled {
			t.Fatalf("Process returned %v; expected context.Canceled", r.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("Process did not return within 3s after the context was cancelled " +
			"(it appears to have ignored cancellation and drained every job)")
	}
}

// After a cancelled Process returns, no worker goroutines should still be running.
func TestOracleNoGoroutineLeak(t *testing.T) {
	base := runtime.NumGoroutine()
	for iter := 0; iter < 5; iter++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		jobs := make([]int, 200)
		slow := func(x int) int { time.Sleep(20 * time.Millisecond); return x }
		_, _ = Process(ctx, jobs, 8, slow)
	}
	// Give any (incorrectly) leaked goroutines a chance to surface.
	time.Sleep(200 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > base+2 {
		t.Fatalf("goroutine leak: started with %d, ended with %d after cancelled runs", base, after)
	}
}

// With no cancellation, every job must be processed correctly, in order.
func TestOracleHappyPath(t *testing.T) {
	jobs := []int{2, 3, 4, 5, 6, 7, 8, 9}
	got, err := Process(context.Background(), jobs, 3, func(x int) int { return x * x })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(jobs) {
		t.Fatalf("len(results) = %d, want %d", len(got), len(jobs))
	}
	for i, j := range jobs {
		if got[i] != j*j {
			t.Fatalf("results[%d] = %d, want %d", i, got[i], j*j)
		}
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
