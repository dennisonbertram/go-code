package main

import "testing"

func TestDouble(t *testing.T) {
	cases := []struct{ in, want int }{{0, 0}, {1, 2}, {5, 10}, {-3, -6}}
	for _, c := range cases {
		if got := Double(c.in); got != c.want {
			t.Errorf("Double(%d)=%d want %d", c.in, got, c.want)
		}
	}
}
