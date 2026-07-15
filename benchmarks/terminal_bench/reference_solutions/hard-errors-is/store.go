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

// Unwrap makes *NotFoundError participate in the errors.Is chain: it
// unwraps to the ErrNotFound sentinel, so errors.Is(err, ErrNotFound)
// succeeds, while the error itself is still a *NotFoundError so
// errors.As(err, &nfe) finds it and exposes Key.
func (e *NotFoundError) Unwrap() error {
	return ErrNotFound
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
// Reference solution: NotFoundError.Unwrap() returns ErrNotFound, so the
// error returned on a miss satisfies both errors.Is(err, ErrNotFound) and
// errors.As(err, &nfe), with nfe.Key set to the missing key.
func (s *Store) Get(key string) (string, error) {
	v, ok := s.data[key]
	if !ok {
		return "", &NotFoundError{Key: key}
	}
	return v, nil
}
