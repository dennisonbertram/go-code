package main

import (
	"errors"
	"sync"
)

// Account is a simple bank account protected by its own mutex.
type Account struct {
	ID      int
	mu      sync.Mutex
	balance int64
}

// NewAccount creates an account with the given id and starting balance.
func NewAccount(id int, balance int64) *Account {
	return &Account{ID: id, balance: balance}
}

// Balance safely reads the current balance.
func (a *Account) Balance() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.balance
}

// Transfer moves amount from `from` to `to`.
//
// Contract:
//   - Transfer must be deadlock-free under concurrent transfers that run in
//     both directions (e.g. Transfer(a, b, ...) and Transfer(b, a, ...) at the
//     same time).
//   - Transfer must move `amount` from `from` to `to` atomically and be free
//     of data races.
//   - If `from` has insufficient funds (balance < amount), Transfer must
//     return a non-nil error and leave both balances unchanged.
//
// BUG: this implementation always locks `from` before `to`. Two goroutines
// running Transfer(a, b, ...) and Transfer(b, a, ...) concurrently acquire
// the locks in opposite orders, so they can each hold one lock while waiting
// for the other, deadlocking forever.
func Transfer(from, to *Account, amount int64) error {
	from.mu.Lock()
	defer from.mu.Unlock()

	to.mu.Lock()
	defer to.mu.Unlock()

	if from.balance < amount {
		return errors.New("insufficient funds")
	}

	from.balance -= amount
	to.balance += amount
	return nil
}
