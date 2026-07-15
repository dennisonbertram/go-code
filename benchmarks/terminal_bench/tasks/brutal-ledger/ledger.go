package main

import "sort"

// Ledger is an in-memory account ledger.
//
// Contract:
//   - Post accumulates: multiple posts to the same account add up to a net
//     balance.
//   - Balance returns the net balance for one account (0 if unknown).
//   - Total returns the sum of every account's balance.
//   - TopN returns up to n account names ordered by balance DESCENDING, ties
//     broken by account name ASCENDING. If n >= number of accounts, it
//     returns all of them. If n <= 0, it returns an empty slice. TopN must
//     never panic.
//   - The Ledger must be safe for concurrent use: concurrent Post/Balance/
//     Total calls from multiple goroutines must be free of data races.
type Ledger struct {
	balances map[string]int64
}

// NewLedger creates an empty Ledger.
func NewLedger() *Ledger {
	return &Ledger{balances: make(map[string]int64)}
}

// Post records a signed amount to an account.
//
// BUG: the underlying map is read and written with no synchronization at
// all, so concurrent Post/Balance/Total calls race under `go test -race`
// (and can even trigger a fatal "concurrent map writes" crash).
func (l *Ledger) Post(account string, amount int64) {
	l.balances[account] += amount
}

// Balance returns the net balance for one account (0 if unknown).
func (l *Ledger) Balance(account string) int64 {
	return l.balances[account]
}

// Total returns the sum of every account's balance.
func (l *Ledger) Total() int64 {
	var total int64
	for _, v := range l.balances {
		total += v
	}
	return total
}

// TopN returns up to n accounts with the highest balance, ties broken by
// account name ascending.
func (l *Ledger) TopN(n int) []string {
	names := make([]string, 0, len(l.balances))
	for k := range l.balances {
		names = append(names, k)
	}

	// BUG: sorts ascending (lowest balance first) instead of descending, and
	// the comparator only looks at balance, so accounts with equal balances
	// come out in whatever order the map happened to yield them instead of
	// being tie-broken by name.
	sort.Slice(names, func(i, j int) bool {
		return l.balances[names[i]] < l.balances[names[j]]
	})

	// BUG: no bounds checking on n — n greater than len(names) slices out of
	// range and panics, and n <= 0 is not special-cased to return an empty
	// slice (a negative n also panics here).
	return names[:n]
}
