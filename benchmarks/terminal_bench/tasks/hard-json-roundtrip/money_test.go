package main

import (
	"encoding/json"
	"testing"
)

// Happy-path smoke test. The full grading suite (including the round-trip
// precision oracle) is applied separately.
func TestMoneyMarshalUnmarshalSmoke(t *testing.T) {
	m := Money{Cents: 500, Currency: "USD"}

	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	var back Money
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	if back.Currency != "USD" {
		t.Fatalf("Currency = %q, want %q", back.Currency, "USD")
	}
	if back.Cents != 500 {
		t.Fatalf("Cents = %d, want %d", back.Cents, 500)
	}
}
