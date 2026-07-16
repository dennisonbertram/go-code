from __future__ import annotations

import subprocess
from pathlib import Path

# Hidden behavioral oracle. This test file is injected into the container at
# grading time only — the agent never sees it. It writes a fresh Go test into
# /app and runs it, so the decisive checks cannot be inspected or weakened by
# the agent.
#
# wrap.go ships with four independent bugs; each TestOracle* function below
# is designed so at least one of its assertions fails against the buggy
# starting code while all of them pass against a correct fix:
#   1. TestOracleOffByOneWidth      — off-by-one in the line-fit comparison.
#   2. TestOracleLongWordOwnLine    — an over-width word is silently dropped.
#   3. TestOracleWhitespaceCollapse — runs of spaces/tabs are not collapsed.
#   4. TestOracleHardNewline        — '\n' is folded into ordinary whitespace
#                                      instead of forcing a new line.
ORACLE_TEST = r'''
package main

import (
	"reflect"
	"testing"
)

// A line exactly at the width boundary must still be merged: "ab" + " " +
// "cd" is exactly 5 characters wide, so it must stay on one line.
func TestOracleOffByOneWidth(t *testing.T) {
	got := Wrap("ab cd efg hi", 5)
	want := []string{"ab cd", "efg", "hi"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Wrap(%q, 5) = %v, want %v", "ab cd efg hi", got, want)
	}
	for _, line := range got {
		if len(line) > 5 {
			t.Fatalf("line %q exceeds width 5", line)
		}
	}
}

// A word longer than width must occupy its own line, unbroken, and must not
// be dropped or cause the words around it to be silently merged.
func TestOracleLongWordOwnLine(t *testing.T) {
	got := Wrap("hi supercalifragilisticexpialidocious bye", 10)
	want := []string{"hi", "supercalifragilisticexpialidocious", "bye"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Wrap(%q, 10) = %v, want %v", "hi supercalifragilisticexpialidocious bye", got, want)
	}
}

// Runs of spaces/tabs, and leading/trailing whitespace, must collapse to
// single breaks and must never leak empty "words" into the output.
func TestOracleWhitespaceCollapse(t *testing.T) {
	got := Wrap("  a   bb    ccc  ", 80)
	want := []string{"a bb ccc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Wrap(%q, 80) = %v, want %v", "  a   bb    ccc  ", got, want)
	}

	got2 := Wrap("x\t\ty", 80)
	want2 := []string{"x y"}
	if !reflect.DeepEqual(got2, want2) {
		t.Fatalf("Wrap(%q, 80) = %v, want %v", "x\t\ty", got2, want2)
	}
}

// '\n' must force a hard line break, even when width is wide enough that
// the surrounding words would otherwise fit on one line together.
func TestOracleHardNewline(t *testing.T) {
	got := Wrap("line one\nline two", 80)
	want := []string{"line one", "line two"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Wrap(%q, 80) = %v, want %v", "line one\nline two", got, want)
	}
}

// Edge cases from the contract: Wrap("", width) is always empty, and
// width <= 0 disables wrapping per hard line (it must not collapse multiple
// hard lines from '\n' into a single output line).
func TestOracleEdgeCases(t *testing.T) {
	if got := Wrap("", 10); len(got) != 0 {
		t.Fatalf(`Wrap("", 10) = %v, want empty slice`, got)
	}
	if got := Wrap("", 0); len(got) != 0 {
		t.Fatalf(`Wrap("", 0) = %v, want empty slice`, got)
	}

	got := Wrap("alpha beta\ngamma delta", 0)
	want := []string{"alpha beta", "gamma delta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Wrap(%q, 0) = %v, want %v", "alpha beta\ngamma delta", got, want)
	}

	got2 := Wrap("a\n\nb", 10)
	want2 := []string{"a", "", "b"}
	if !reflect.DeepEqual(got2, want2) {
		t.Fatalf("Wrap(%q, 10) = %v, want %v", "a\n\nb", got2, want2)
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
