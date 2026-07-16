package main

import "strings"

type User struct {
	Name string
	Age  int
}

type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return strings.Join(e.Problems, "; ")
}

// Validate checks u and reports problems via a *ValidationError.
//
// Contract:
//   - Validate must return a nil error for a valid user (non-empty name,
//     0 <= age <= 150).
//   - Validate must return a non-nil error for an invalid user, and that
//     error must be assertable to *ValidationError via errors.As, exposing
//     the accumulated Problems.
//
// BUG: this implementation always constructs and returns a *ValidationError,
// even when Problems is empty. Because the return type is the `error`
// interface, a nil *ValidationError wrapped in `error` is NOT equal to a nil
// error — the interface carries a non-nil type descriptor even though the
// pointer value is nil. So `Validate(validUser) != nil` is true, and callers
// that check `if err != nil` see an error for perfectly valid input.
func Validate(u User) error {
	e := &ValidationError{}
	if u.Name == "" {
		e.Problems = append(e.Problems, "name is empty")
	}
	if u.Age < 0 || u.Age > 150 {
		e.Problems = append(e.Problems, "age out of range")
	}
	return e
}
