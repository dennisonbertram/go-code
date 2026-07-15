from __future__ import annotations

import subprocess
from pathlib import Path

# Hidden behavioral oracle. This test file is injected into the container at
# grading time only — the agent never sees it. It writes a fresh Go test into
# /app and runs it, so the decisive checks cannot be inspected or weakened by
# the agent.
#
# semver.go ships with three independent bugs; each TestOracle* function
# below is designed so at least one of its assertions fails against the
# buggy starting code while all of them pass against a correct fix:
#   1. TestOracleChainAscending        — pre-release identifiers are compared
#                                          lexically instead of numerically
#                                          (e.g. "2" vs "11").
#   2. TestOraclePrereleaseLowerThanRelease / TestOracleChainAscending —
#                                          a pre-release version is not
#                                          treated as lower precedence than
#                                          the same version without one.
#   3. TestOracleBuildMetadataIgnored  — build metadata is not ignored for
#                                          precedence.
ORACLE_TEST = r'''
package main

import "testing"

// The canonical ascending precedence chain from semver.org §11.
var chain = []string{
	"1.0.0-alpha",
	"1.0.0-alpha.1",
	"1.0.0-alpha.beta",
	"1.0.0-beta",
	"1.0.0-beta.2",
	"1.0.0-beta.11",
	"1.0.0-rc.1",
	"1.0.0",
}

// Every version in the chain must compare equal to itself, and every
// adjacent pair must compare strictly in ascending order (and strictly
// descending in the reverse direction).
func TestOracleChainAscending(t *testing.T) {
	for _, x := range chain {
		if got := Compare(x, x); got != 0 {
			t.Fatalf("Compare(%q, %q) = %d, want 0", x, x, got)
		}
	}
	for i := 0; i+1 < len(chain); i++ {
		x, y := chain[i], chain[i+1]
		if got := Compare(x, y); got != -1 {
			t.Fatalf("Compare(%q, %q) = %d, want -1", x, y, got)
		}
		if got := Compare(y, x); got != 1 {
			t.Fatalf("Compare(%q, %q) = %d, want 1", y, x, got)
		}
	}
}

// MAJOR and PATCH numeric comparisons, independent of any pre-release
// handling.
func TestOracleCoreNumeric(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "2.0.0", -1},
		{"2.0.0", "1.0.0", 1},
		{"2.1.0", "2.1.1", -1},
		{"2.1.1", "2.1.0", 1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Fatalf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// A pre-release version must have strictly lower precedence than the same
// version without one, in both directions.
func TestOraclePrereleaseLowerThanRelease(t *testing.T) {
	if got := Compare("1.0.0-alpha", "1.0.0"); got != -1 {
		t.Fatalf("Compare(%q, %q) = %d, want -1", "1.0.0-alpha", "1.0.0", got)
	}
	if got := Compare("1.0.0", "1.0.0-alpha"); got != 1 {
		t.Fatalf("Compare(%q, %q) = %d, want 1", "1.0.0", "1.0.0-alpha", got)
	}
}

// Build metadata (everything from the first '+' onward) must never affect
// precedence.
func TestOracleBuildMetadataIgnored(t *testing.T) {
	cases := [][2]string{
		{"1.0.0+build.1", "1.0.0"},
		{"1.0.0+a", "1.0.0+b"},
		{"1.0.0-alpha+x", "1.0.0-alpha+y"},
	}
	for _, c := range cases {
		if got := Compare(c[0], c[1]); got != 0 {
			t.Fatalf("Compare(%q, %q) = %d, want 0 (build metadata must be ignored)", c[0], c[1], got)
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
