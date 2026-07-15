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

// moneyWire is the on-the-wire JSON shape for Money.
type moneyWire struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// MarshalJSON renders Money as a human-readable decimal dollar amount, e.g.
// {"amount":29.99,"currency":"USD"}.
//
// BUG: encoding the amount as a float64 dollar value loses precision for some
// cent values. Multiplying back out in UnmarshalJSON does not always recover
// the original integer number of cents, so
// json.Unmarshal(json.Marshal(m)) does not always reproduce m.
func (m Money) MarshalJSON() ([]byte, error) {
	amount := float64(m.Cents) / 100.0
	return json.Marshal(moneyWire{Amount: amount, Currency: m.Currency})
}

// UnmarshalJSON parses the decimal dollar amount back into integer cents.
func (m *Money) UnmarshalJSON(data []byte) error {
	var w moneyWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	m.Cents = int64(w.Amount * 100)
	m.Currency = w.Currency
	return nil
}

// String renders Money for logging/debugging purposes.
func (m Money) String() string {
	return fmt.Sprintf("%d %s cents", m.Cents, m.Currency)
}
