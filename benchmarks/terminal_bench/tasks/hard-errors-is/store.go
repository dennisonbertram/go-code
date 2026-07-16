package main

import (
	"errors"
	"fmt"
)

// ErrNotFound is the sentinel callers are expected to check for with
// errors.Is when a lookup misses.
var ErrNotFound = errors.New("not found")

// NotFoundError carries the specific key that was missing.
type NotFoundError struct {
	Key string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("key %q: not found", e.Key)
}

// Store is a simple in-memory key/value store.
type Store struct {
	data map[string]string
}

// NewStore creates an empty Store.
func NewStore() *Store {
	return &Store{data: make(map[string]string)}
}

// Put sets key to value.
func (s *Store) Put(key, value string) {
	s.data[key] = value
}

// Get returns the value stored at key.
//
// Contract:
//   - On a hit, Get returns the stored value and a nil error.
//   - On a miss, the returned error must satisfy
//     errors.Is(err, ErrNotFound) == true, AND must still be assertable to
//     *NotFoundError via errors.As, exposing the missing Key.
//
// BUG: NotFoundError does not implement Unwrap(), and Get returns a bare
// &NotFoundError{...} without ever wrapping ErrNotFound. errors.Is walks the
// Unwrap() chain looking for a match, and there is none here, so
// errors.Is(err, ErrNotFound) is FALSE for a missing-key error — any caller
// that checks errors.Is(err, ErrNotFound) breaks even though the error text
// says "not found".
func (s *Store) Get(key string) (string, error) {
	v, ok := s.data[key]
	if !ok {
		return "", &NotFoundError{Key: key}
	}
	return v, nil
}
