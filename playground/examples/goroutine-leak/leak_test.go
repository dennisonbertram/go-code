package main

import (
	"runtime"
	"testing"
	"time"
)

func TestNoLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 10; i++ {
		startWorker()
	}
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after-before > 2 {
		t.Fatalf("leaked %d goroutines", after-before)
	}
}
