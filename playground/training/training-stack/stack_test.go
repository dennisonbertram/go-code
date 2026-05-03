package main

import (
	"testing"
)

func TestIntStack(t *testing.T) {
	s := &Stack[int]{}
	if l := s.Len(); l != 0 {
		t.Errorf("expected Len 0, got %d", l)
	}
	val, ok := s.Pop()
	if ok {
		t.Errorf("Pop should fail on empty stack, got %v", val)
	}
	val, ok = s.Peek()
	if ok {
		t.Errorf("Peek should fail on empty stack, got %v", val)
	}

	s.Push(1)
	s.Push(2)
	s.Push(3)

	if l := s.Len(); l != 3 {
		t.Errorf("expected Len 3, got %d", l)
	}

	if top, ok := s.Peek(); !ok || top != 3 {
		t.Errorf("expected Peek (3, true), got (%v, %v)", top, ok)
	}

	if v, ok := s.Pop(); !ok || v != 3 {
		t.Errorf("expected Pop (3, true), got (%v, %v)", v, ok)
	}
	if v, ok := s.Pop(); !ok || v != 2 {
		t.Errorf("expected Pop (2, true), got (%v, %v)", v, ok)
	}
	if v, ok := s.Pop(); !ok || v != 1 {
		t.Errorf("expected Pop (1, true), got (%v, %v)", v, ok)
	}
	if l := s.Len(); l != 0 {
		t.Errorf("expected Len 0 after Pops, got %d", l)
	}

	// Pop again, should fail
	if _, ok := s.Pop(); ok {
		t.Errorf("Pop should fail on empty stack after all Pops")
	}
}

func TestStringStack(t *testing.T) {
	s := &Stack[string]{}
	if l := s.Len(); l != 0 {
		t.Errorf("expected Len 0, got %d", l)
	}
	val, ok := s.Pop()
	if ok {
		t.Errorf("Pop should fail on empty stack, got %v", val)
	}
	val, ok = s.Peek()
	if ok {
		t.Errorf("Peek should fail on empty stack, got %v", val)
	}

	s.Push("a")
	s.Push("b")
	s.Push("c")

	if l := s.Len(); l != 3 {
		t.Errorf("expected Len 3, got %d", l)
	}

	if top, ok := s.Peek(); !ok || top != "c" {
		t.Errorf("expected Peek (c, true), got (%v, %v)", top, ok)
	}

	if v, ok := s.Pop(); !ok || v != "c" {
		t.Errorf("expected Pop (c, true), got (%v, %v)", v, ok)
	}
	if v, ok := s.Pop(); !ok || v != "b" {
		t.Errorf("expected Pop (b, true), got (%v, %v)", v, ok)
	}
	if v, ok := s.Pop(); !ok || v != "a" {
		t.Errorf("expected Pop (a, true), got (%v, %v)", v, ok)
	}
	if l := s.Len(); l != 0 {
		t.Errorf("expected Len 0 after Pops, got %d", l)
	}
	if _, ok := s.Pop(); ok {
		t.Errorf("Pop should fail on empty stack after all Pops")
	}
}
