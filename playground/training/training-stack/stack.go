package main

// Stack is a generic stack implementation
// T can be any type

// Stack is a generic stack of type T
// Methods: Push, Pop, Peek, Len

type Stack[T any] struct {
	elements []T
}

// Push adds an element to the top of the stack
func (s *Stack[T]) Push(v T) {
	s.elements = append(s.elements, v)
}

// Pop removes and returns the top element of the stack.
// If the stack is empty, returns zero value and false.
func (s *Stack[T]) Pop() (T, bool) {
	var zero T
	l := len(s.elements)
	if l == 0 {
		return zero, false
	}
	v := s.elements[l-1]
	s.elements = s.elements[:l-1]
	return v, true
}

// Peek returns the top element without removing it.
// If the stack is empty, returns zero value and false.
func (s *Stack[T]) Peek() (T, bool) {
	var zero T
	l := len(s.elements)
	if l == 0 {
		return zero, false
	}
	return s.elements[l-1], true
}

// Len returns the number of elements in the stack.
func (s *Stack[T]) Len() int {
	return len(s.elements)
}