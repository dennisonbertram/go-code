package main

import (
	"sort"
	"sync"
)

// Ledger is an in-memory account ledger, safe for concurrent use.
type Ledger struct {
	mu       sync.RWMutex
	balances map[string]int64
}

// NewLedger creates an empty Ledger.
func NewLedger() *Ledger {
	return &Ledger{balances: make(map[string]int64)}
}

// Post records a signed amount to an account. Multiple posts to the same
// account accumulate.
func (l *Ledger) Post(account string, amount int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.balances[account] += amount
}

// Balance returns the net balance for one account (0 if unknown).
func (l *Ledger) Balance(account string) int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.balances[account]
}

// Total returns the sum of every account's balance.
func (l *Ledger) Total() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var total int64
	for _, v := range l.balances {
		total += v
	}
	return total
}

// TopN returns up to n accounts with the highest balance, ties broken by
// account name ascending. Never panics.
func (l *Ledger) TopN(n int) []string {
	if n <= 0 {
		return []string{}
	}

	l.mu.RLock()
	names := make([]string, 0, len(l.balances))
	balances := make(map[string]int64, len(l.balances))
	for k, v := range l.balances {
		names = append(names, k)
		balances[k] = v
	}
	l.mu.RUnlock()

	sort.Slice(names, func(i, j int) bool {
		bi, bj := balances[names[i]], balances[names[j]]
		if bi != bj {
			return bi > bj
		}
		return names[i] < names[j]
	})

	if n > len(names) {
		n = len(names)
	}
	return names[:n]
}
