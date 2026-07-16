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

// Bug 1: a comma inside a quoted field must not split the field.
func TestOracleQuotedFieldWithComma(t *testing.T) {
	input := `"a,b",c`
	got, err := ParseCSV(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := [][]string{{"a,b", "c"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseCSV(%q) = %#v, want %#v", input, got, want)
	}
}

// Bug 2: a doubled double-quote ("") inside a quoted field must collapse to
// a single literal quote character.
func TestOracleEscapedQuote(t *testing.T) {
	input := `"a""b",c`
	got, err := ParseCSV(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := [][]string{{`a"b`, "c"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseCSV(%q) = %#v, want %#v", input, got, want)
	}
}

// Bug 3: a newline embedded inside a quoted field must not start a new
// record.
func TestOracleEmbeddedNewline(t *testing.T) {
	input := "\"a\nb\",c"
	got, err := ParseCSV(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := [][]string{{"a\nb", "c"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseCSV(%q) = %#v, want %#v", input, got, want)
	}
}

// Bug 4: a trailing newline at the end of the input must not create a
// spurious empty final record.
func TestOracleTrailingNewline(t *testing.T) {
	input := "a,b\n"
	got, err := ParseCSV(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := [][]string{{"a", "b"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseCSV(%q) = %#v, want %#v", input, got, want)
	}
}

// Plain multi-row, unquoted input with no trailing newline must still
// parse correctly.
func TestOracleHappyPathMultiRow(t *testing.T) {
	input := "a,b,c\nd,e,f"
	got, err := ParseCSV(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := [][]string{{"a", "b", "c"}, {"d", "e", "f"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseCSV(%q) = %#v, want %#v", input, got, want)
	}
}

// An empty input string parses to zero rows; an empty line parses to a
// single row with one empty field.
func TestOracleEmptyInputAndEmptyLine(t *testing.T) {
	got, err := ParseCSV("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ParseCSV(\"\") = %#v, want zero rows", got)
	}

	got2, err := ParseCSV("\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want2 := [][]string{{""}}
	if !reflect.DeepEqual(got2, want2) {
		t.Fatalf("ParseCSV(\"\\n\") = %#v, want %#v", got2, want2)
	}
}

// Integration case: quoted field containing both a comma and escaped
// quotes, a separate quoted field containing an embedded newline, a plain
// trailing row, and a trailing newline at the very end — all four
// behaviors must work together, not just in isolation.
func TestOracleIntegration(t *testing.T) {
	input := "id,name,bio\n" +
		"1,\"Doe, Jane\",\"Loves \"\"Go\"\" and\n" +
		"simplicity\"\n" +
		"2,Bob,plain\n"

	got, err := ParseCSV(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := [][]string{
		{"id", "name", "bio"},
		{"1", "Doe, Jane", "Loves \"Go\" and\nsimplicity"},
		{"2", "Bob", "plain"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseCSV(%q) = %#v, want %#v", input, got, want)
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
