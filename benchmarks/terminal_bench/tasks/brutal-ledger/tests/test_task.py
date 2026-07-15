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
	"sync"
	"testing"
)

// Post must accumulate, Balance must return the net per account (0 for an
// unknown account), and Total must be the sum across all accounts.
func TestOracleBalancesAndTotal(t *testing.T) {
	l := NewLedger()
	l.Post("alice", 100)
	l.Post("alice", 50)
	l.Post("bob", 200)
	l.Post("bob", -75)
	l.Post("carol", -30)
	l.Post("dave", 0)

	cases := []struct {
		account string
		want    int64
	}{
		{"alice", 150},
		{"bob", 125},
		{"carol", -30},
		{"dave", 0},
		{"unknown", 0},
	}
	for _, c := range cases {
		if got := l.Balance(c.account); got != c.want {
			t.Fatalf("Balance(%q) = %d, want %d", c.account, got, c.want)
		}
	}

	wantTotal := int64(150 + 125 - 30 + 0)
	if got := l.Total(); got != wantTotal {
		t.Fatalf("Total() = %d, want %d", got, wantTotal)
	}
}

// TopN must be ordered by balance descending, with ties broken by account
// name ascending — not by whatever order the underlying map yields.
func TestOracleTopNOrder(t *testing.T) {
	l := NewLedger()
	// "zeta" and "alpha" tie at 300; inserted in an order that would betray
	// an implementation relying on map iteration order or insertion order.
	l.Post("zeta", 300)
	l.Post("alpha", 300)
	l.Post("mid", 200)
	l.Post("low", -50)

	got := l.TopN(4)
	want := []string{"alpha", "zeta", "mid", "low"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TopN(4) = %v, want %v", got, want)
	}

	got2 := l.TopN(2)
	want2 := []string{"alpha", "zeta"}
	if !reflect.DeepEqual(got2, want2) {
		t.Fatalf("TopN(2) = %v, want %v", got2, want2)
	}
}

// TopN must never panic, must return every account (in correct order) when n
// is at least the number of accounts, and must return an empty slice for
// n <= 0.
func TestOracleTopNBounds(t *testing.T) {
	l := NewLedger()
	l.Post("a", 10)
	l.Post("b", 20)
	l.Post("c", 30)

	runSafely := func(label string, fn func()) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("%s panicked: %v", label, r)
			}
		}()
		fn()
	}

	runSafely("TopN(10)", func() {
		got := l.TopN(10)
		want := []string{"c", "b", "a"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("TopN(10) = %v, want %v", got, want)
		}
	})

	runSafely("TopN(0)", func() {
		got := l.TopN(0)
		if len(got) != 0 {
			t.Fatalf("TopN(0) = %v, want an empty slice", got)
		}
	})

	runSafely("TopN(-1)", func() {
		got := l.TopN(-1)
		if len(got) != 0 {
			t.Fatalf("TopN(-1) = %v, want an empty slice", got)
		}
	})
}

// Concurrent Post/Balance/Total from many goroutines must be data-race free
// and must produce exactly the expected totals.
func TestOracleConcurrentPost(t *testing.T) {
	l := NewLedger()
	const goroutines = 100

	var wg sync.WaitGroup
	wg.Add(goroutines * 2)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			l.Post("shared", 1)
			l.Post("other", 2)
		}()
		go func() {
			defer wg.Done()
			_ = l.Balance("shared")
			_ = l.Total()
		}()
	}
	wg.Wait()

	if got, want := l.Balance("shared"), int64(goroutines); got != want {
		t.Fatalf("Balance(\"shared\") = %d, want %d", got, want)
	}
	if got, want := l.Balance("other"), int64(goroutines*2); got != want {
		t.Fatalf("Balance(\"other\") = %d, want %d", got, want)
	}
	if got, want := l.Total(), int64(goroutines+goroutines*2); got != want {
		t.Fatalf("Total() = %d, want %d", got, want)
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
