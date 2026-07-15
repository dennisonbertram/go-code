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
	"errors"
	"testing"
)

// A missing key's error must satisfy errors.Is(err, ErrNotFound). The buggy
// implementation returns a bare *NotFoundError with no Unwrap(), so
// errors.Is(err, ErrNotFound) is false against it even though the error text
// says "not found".
func TestOracleIsNotFound(t *testing.T) {
	s := NewStore()
	_, err := s.Get("missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatal("errors.Is(err, ErrNotFound) should be true")
	}
}

// The same error must still be assertable to *NotFoundError via errors.As,
// exposing the specific key that was missing.
func TestOracleAsNotFound(t *testing.T) {
	s := NewStore()
	_, err := s.Get("ghost")
	var nfe *NotFoundError
	if !errors.As(err, &nfe) {
		t.Fatal("errors.As should find *NotFoundError")
	}
	if nfe.Key != "ghost" {
		t.Fatalf("Key=%q want ghost", nfe.Key)
	}
}

// A hit must return the stored value and a nil error.
func TestOracleHit(t *testing.T) {
	s := NewStore()
	s.Put("a", "1")
	v, err := s.Get("a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "1" {
		t.Fatalf("v=%q want %q", v, "1")
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
