package main

// Stack is a generic stack implementation using Go 1.18+ generics
// T is the type of element in the stack
// Methods:
//
//	Push(v T)
//	Pop() (T, bool)
//	Peek() (T, bool)
//	Len() int
type Stack[T any] struct {
	items []T
}

// Push adds a value onto the stack
func (s *Stack[T]) Push(v T) {
	s.items = append(s.items, v)
}

// Pop removes and returns the top value (if any)
func (s *Stack[T]) Pop() (T, bool) {
	if len(s.items) == 0 {
		var zero T
		return zero, false
	}
	lastIdx := len(s.items) - 1
	val := s.items[lastIdx]
	s.items = s.items[:lastIdx]
	return val, true
}

// Peek returns the top value without removing it
func (s *Stack[T]) Peek() (T, bool) {
	if len(s.items) == 0 {
		var zero T
		return zero, false
	}
	return s.items[len(s.items)-1], true
}

// Len returns the current stack size
func (s *Stack[T]) Len() int {
	return len(s.items)
}
