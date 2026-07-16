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

// Reference solution: only construct and return a *ValidationError when
// there is at least one problem. Otherwise return a plain nil error, so
// callers checking `err != nil` do not fall into the typed-nil-interface
// trap.
func Validate(u User) error {
	e := &ValidationError{}
	if u.Name == "" {
		e.Problems = append(e.Problems, "name is empty")
	}
	if u.Age < 0 || u.Age > 150 {
		e.Problems = append(e.Problems, "age out of range")
	}
	if len(e.Problems) == 0 {
		return nil
	}
	return e
}
