package main

import (
	"testing"
	"time"
)

func TestSlowOp(t *testing.T) {
	start := time.Now()
	result := SlowOp()
	elapsed := time.Since(start)
	if elapsed < 40*time.Millisecond {
		t.Fatalf("too fast: %v", elapsed)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("too slow: %v", elapsed)
	}
	if result != "done" {
		t.Fatalf("got %q", result)
	}
}
