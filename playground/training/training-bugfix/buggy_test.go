package main

import "testing"

func TestSumSlice_Empty(t *testing.T) {
	result := SumSlice([]int{})
	if result != 0 {
		t.Errorf("SumSlice([]) = %d; want 0", result)
	}
}

func TestSumSlice_Single(t *testing.T) {
	result := SumSlice([]int{42})
	if result != 42 {
		t.Errorf("SumSlice([42]) = %d; want 42", result)
	}
}

func TestSumSlice_Multi(t *testing.T) {
	input := []int{1, 2, 3, 4, 5}
	expected := 15
	result := SumSlice(input)
	if result != expected {
		t.Errorf("SumSlice(%v) = %d; want %d", input, result, expected)
	}
}
