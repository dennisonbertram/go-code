package main

import (
	"encoding/json"
	"fmt"
)

// Money represents a monetary amount as integer cents plus an ISO-4217-style
// currency code. Cents is the single source of truth for the amount; there is
// no separate "dollars" field.
type Money struct {
	Cents    int64
	Currency string
}

// moneyWire is the on-the-wire JSON shape for Money. The amount is encoded as
// an integer number of cents, never a float, so the exact value always
// round-trips.
type moneyWire struct {
	Cents    int64  `json:"cents"`
	Currency string `json:"currency"`
}

// MarshalJSON renders Money as {"cents":2999,"currency":"USD"}. Cents is
// encoded as an integer — never routed through a float64 — so precision is
// never lost.
func (m Money) MarshalJSON() ([]byte, error) {
	return json.Marshal(moneyWire{Cents: m.Cents, Currency: m.Currency})
}

// UnmarshalJSON parses the integer cents value back into Money, symmetric
// with MarshalJSON.
func (m *Money) UnmarshalJSON(data []byte) error {
	var w moneyWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	m.Cents = w.Cents
	m.Currency = w.Currency
	return nil
}

// String renders Money for logging/debugging purposes.
func (m Money) String() string {
	return fmt.Sprintf("%d %s cents", m.Cents, m.Currency)
}
