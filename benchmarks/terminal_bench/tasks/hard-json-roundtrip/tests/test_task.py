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
	"encoding/json"
	"reflect"
	"testing"
)

// Every Money value must survive a Marshal -> Unmarshal round trip exactly.
// The buggy floating-point implementation fails this for values like 29
// cents, where the dollar amount does not multiply back out to an exact
// integer number of cents.
func TestOracleRoundTrip(t *testing.T) {
	cases := []Money{
		{Cents: 0, Currency: "USD"},
		{Cents: 1, Currency: "USD"},
		{Cents: 29, Currency: "USD"},
		{Cents: 99, Currency: "USD"},
		{Cents: 100, Currency: "EUR"},
		{Cents: -4237, Currency: "USD"},
		{Cents: 123456789, Currency: "JPY"},
	}

	for _, m := range cases {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("Marshal(%+v) returned error: %v", m, err)
		}

		var back Money
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatalf("Unmarshal(%s) returned error: %v", b, err)
		}

		if !reflect.DeepEqual(m, back) {
			t.Fatalf("round-trip changed %+v -> %+v (json=%s)", m, back, b)
		}
	}
}

// Currency must always survive the round trip unchanged.
func TestOracleCurrencyPreserved(t *testing.T) {
	m := Money{Cents: 500, Currency: "GBP"}

	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal(%+v) returned error: %v", m, err)
	}

	var back Money
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("Unmarshal(%s) returned error: %v", b, err)
	}

	if back.Currency != "GBP" {
		t.Fatalf("Currency = %q, want %q", back.Currency, "GBP")
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
