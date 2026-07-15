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

// A valid user must produce a nil error. This is the classic typed-nil-
// interface trap: the buggy implementation always returns a non-nil
// *ValidationError wrapped in the error interface, so this fails against it
// even though Problems is empty.
func TestOracleValidReturnsNil(t *testing.T) {
	err := Validate(User{Name: "Ada", Age: 37})
	if err != nil {
		t.Fatalf("valid user returned error: %v", err)
	}
}

// An invalid user must produce a non-nil error that is assertable to
// *ValidationError via errors.As, exposing the accumulated Problems.
func TestOracleInvalidReturnsError(t *testing.T) {
	err := Validate(User{Name: "", Age: 200})
	if err == nil {
		t.Fatalf("invalid user returned nil error")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error is not assertable to *ValidationError: %v", err)
	}
	if len(ve.Problems) < 1 {
		t.Fatalf("expected at least one problem, got %d", len(ve.Problems))
	}
}

// Age boundaries (0 and 150) are valid and must return a nil error.
func TestOracleBoundary(t *testing.T) {
	if err := Validate(User{Name: "X", Age: 0}); err != nil {
		t.Fatalf("age 0 should be valid, got error: %v", err)
	}
	if err := Validate(User{Name: "X", Age: 150}); err != nil {
		t.Fatalf("age 150 should be valid, got error: %v", err)
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
