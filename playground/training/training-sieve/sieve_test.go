package main

import (
	"testing"
)

func TestPrimesUpTo100(t *testing.T) {
	primes := Primes(100)
	expected := []int{2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37, 41, 43, 47, 53, 59, 61, 67, 71, 73, 79, 83, 89, 97}
	if len(primes) != len(expected) {
		t.Fatalf("expected %d primes, got %d", len(expected), len(primes))
	}
	for i, p := range expected {
		if primes[i] != p {
			t.Errorf("at %d: want %d, got %d", i, p, primes[i])
		}
	}
}

func TestPrimesUpTo1000(t *testing.T) {
	primes := Primes(1000)
	if len(primes) != 168 {
		t.Fatalf("expected 168 primes up to 1000, got %d", len(primes))
	}
	// Spot check a few known prime locations
	spots := []struct{ idx, val int }{
		{0, 2}, {1, 3}, {10, 31}, {99, 541}, {167, 997},
	}
	for _, spot := range spots {
		if primes[spot.idx] != spot.val {
			t.Errorf("at index %d: want %d, got %d", spot.idx, spot.val, primes[spot.idx])
		}
	}
}

func TestPrimesUpTo1000000(t *testing.T) {
	primes := Primes(1_000_000)
	if len(primes) != 78498 {
		t.Errorf("expected 78498 primes up to 1000000, got %d", len(primes))
	}
	if primes[0] != 2 || primes[1] != 3 || primes[len(primes)-1] != 999_983 {
		t.Errorf("sanity check failed: got first two and last primes as %v,%v...%v", primes[0], primes[1], primes[len(primes)-1])
	}
}

func BenchmarkSieve(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = Primes(10_000_000)
	}
}
