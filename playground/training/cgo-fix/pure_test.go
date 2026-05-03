package main

import (
	"math"
	"testing"
)

func TestHypotenuse(t *testing.T) {
	got := Hypotenuse(3, 4)
	if math.Abs(got-5.0) > 1e-9 {
		t.Fatalf("got %v", got)
	}
}
