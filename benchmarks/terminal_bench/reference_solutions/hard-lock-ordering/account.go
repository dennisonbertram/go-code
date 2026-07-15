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
// Reference solution: acquires the two accounts' mutexes in a canonical
// order (lower ID first) rather than always locking `from` first, so
// concurrent transfers running in opposite directions between the same pair
// of accounts can never deadlock.
func Transfer(from, to *Account, amount int64) error {
	if from == to {
		// Transferring to yourself: nothing to move, and there is only one
		// lock to take.
		from.mu.Lock()
		defer from.mu.Unlock()
		if from.balance < amount {
			return errors.New("insufficient funds")
		}
		return nil
	}

	first, second := from, to
	if second.ID < first.ID {
		first, second = second, first
	}

	first.mu.Lock()
	defer first.mu.Unlock()
	second.mu.Lock()
	defer second.mu.Unlock()

	if from.balance < amount {
		return errors.New("insufficient funds")
	}

	from.balance -= amount
	to.balance += amount
	return nil
}
