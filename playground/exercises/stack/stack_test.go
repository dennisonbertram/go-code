package main

import (
	"testing"
)

func TestIntStack(t *testing.T) {
	s := Stack[int]{}

	if l := s.Len(); l != 0 {
		t.Errorf("expected length 0, got %d", l)
	}

	// Pop/peek on empty
	if v, ok := s.Pop(); ok {
		t.Errorf("expected pop on empty to fail, got %v", v)
	}
	if v, ok := s.Peek(); ok {
		t.Errorf("expected peek on empty to fail, got %v", v)
	}

	s.Push(10)
	s.Push(20)
	if l := s.Len(); l != 2 {
		t.Errorf("expected length 2, got %d", l)
	}

	if v, ok := s.Peek(); !ok || v != 20 {
		t.Errorf("expected peek 20, got %v (%v)", v, ok)
	}

	if v, ok := s.Pop(); !ok || v != 20 {
		t.Errorf("expected pop 20, got %v (%v)", v, ok)
	}
	if v, ok := s.Pop(); !ok || v != 10 {
		t.Errorf("expected pop 10, got %v (%v)", v, ok)
	}
	if l := s.Len(); l != 0 {
		t.Errorf("expected length 0 after pops, got %d", l)
	}
}

func TestStringStack(t *testing.T) {
	s := Stack[string]{}

	if l := s.Len(); l != 0 {
		t.Errorf("expected length 0, got %d", l)
	}

	// Pop/peek on empty
	if v, ok := s.Pop(); ok {
		t.Errorf("expected pop on empty to fail, got %v", v)
	}
	if v, ok := s.Peek(); ok {
		t.Errorf("expected peek on empty to fail, got %v", v)
	}

	s.Push("foo")
	s.Push("bar")
	if l := s.Len(); l != 2 {
		t.Errorf("expected length 2, got %d", l)
	}

	if v, ok := s.Peek(); !ok || v != "bar" {
		t.Errorf("expected peek 'bar', got %v (%v)", v, ok)
	}

	if v, ok := s.Pop(); !ok || v != "bar" {
		t.Errorf("expected pop 'bar', got %v (%v)", v, ok)
	}
	if v, ok := s.Pop(); !ok || v != "foo" {
		t.Errorf("expected pop 'foo', got %v (%v)", v, ok)
	}
	if l := s.Len(); l != 0 {
		t.Errorf("expected length 0 after pops, got %d", l)
	}
}
