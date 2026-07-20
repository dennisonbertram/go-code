package main

import "testing"

func TestSumSlice_Empty(t *testing.T) {
	if got := SumSlice([]int{}); got != 0 {
		t.Errorf("empty slice: want 0, got %d", got)
	}
}

func TestSumSlice_Single(t *testing.T) {
	if got := SumSlice([]int{5}); got != 5 {
		t.Errorf("single element: want 5, got %d", got)
	}
}

func TestSumSlice_Multi(t *testing.T) {
	cases := []struct {
		input []int
		want  int
	}{
		{[]int{1, 2, 3}, 6},
		{[]int{-1, 1}, 0},
		{[]int{100, 200, 300}, 600},
	}
	for _, c := range cases {
		if got := SumSlice(c.input); got != c.want {
			t.Errorf("SumSlice(%v): want %d, got %d", c.input, c.want, got)
		}
	}
}
