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
	"math/rand"
	"sync"
	"testing"
	"time"
)

// runOracleSwarm assigns each goroutine a fixed ordered pair of accounts
// (cycling through every ordered pair, shuffled) and has it repeatedly
// Transfer along that direction, so that every account pair sees sustained
// concurrent contention in both directions for the whole run rather than a
// single one-shot attempt. A one-shot burst of random pairs did not reliably
// deadlock the buggy implementation (the bad interleaving is probabilistic
// and can be missed by chance); looping widens the race window enough to
// make the deadlock dependable. It reports whether the swarm completed
// within timeout.
func runOracleSwarm(accounts []*Account, numGoroutines, itersPerGoroutine int, seed int64, timeout time.Duration) bool {
	n := len(accounts)
	pairs := make([][2]int, 0, n*(n-1))
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j {
				pairs = append(pairs, [2]int{i, j})
			}
		}
	}

	rng := rand.New(rand.NewSource(seed))
	rng.Shuffle(len(pairs), func(i, j int) { pairs[i], pairs[j] = pairs[j], pairs[i] })

	start := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		p := pairs[i%len(pairs)]
		from, to := accounts[p[0]], accounts[p[1]]

		wg.Add(1)
		go func(from, to *Account) {
			defer wg.Done()
			<-start
			for k := 0; k < itersPerGoroutine; k++ {
				_ = Transfer(from, to, int64(k%50)+1)
			}
		}(from, to)
	}

	close(start)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func newOracleAccounts(numAccounts int, initial int64) []*Account {
	accounts := make([]*Account, numAccounts)
	for i := range accounts {
		accounts[i] = NewAccount(i, initial)
	}
	return accounts
}

// Transfers running concurrently in both directions (a->b and b->a) must not
// deadlock. The buggy implementation always locks `from` before `to`, so
// opposite-direction concurrent transfers between the same pair of accounts
// can each hold one lock while waiting on the other, forever.
func TestOracleNoDeadlock(t *testing.T) {
	accounts := newOracleAccounts(6, 100000)
	if !runOracleSwarm(accounts, 200, 20, 1, 5*time.Second) {
		t.Fatal("deadlock: transfers did not complete within 5s (buggy lock ordering)")
	}
}

// The total balance across all accounts must be conserved under concurrent
// transfers, and the accounting must be race-free.
func TestOracleConserved(t *testing.T) {
	const numAccounts = 6
	const initial int64 = 100000
	accounts := newOracleAccounts(numAccounts, initial)
	initialTotal := int64(numAccounts) * initial

	if !runOracleSwarm(accounts, 200, 20, 2, 5*time.Second) {
		t.Fatal("deadlock: transfers did not complete within 5s (buggy lock ordering)")
	}

	var total int64
	for _, a := range accounts {
		total += a.Balance()
	}
	if total != initialTotal {
		t.Fatalf("balance not conserved: got total %d, want %d", total, initialTotal)
	}
}

// Transfer must reject an overdraft and leave both balances untouched.
func TestOracleRejectsOverdraft(t *testing.T) {
	a := NewAccount(1, 50)
	b := NewAccount(2, 0)

	err := Transfer(a, b, 100)
	if err == nil {
		t.Fatal("Transfer with insufficient funds returned nil error")
	}
	if got, want := a.Balance(), int64(50); got != want {
		t.Fatalf("a.Balance() = %d, want %d (overdrawn transfer must not change balances)", got, want)
	}
	if got, want := b.Balance(), int64(0); got != want {
		t.Fatalf("b.Balance() = %d, want %d (overdrawn transfer must not change balances)", got, want)
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
