package main

import (
	"sync"
	"testing"
	"time"
)

func TestWorkerPoolSubmitAndShutdown(t *testing.T) {
	workers := 5
	wp := NewWorkerPool(workers)
	jobs := 50

	results := make([]<-chan interface{}, jobs)
	for i := 0; i < jobs; i++ {
		iCopy := i
		results[i] = wp.Submit(func() interface{} {
			time.Sleep(10 * time.Millisecond) // Simulate some work
			return iCopy * 2
		})
	}

	// Collect results concurrently
	for i, resCh := range results {
		val, ok := <-resCh
		if !ok {
			t.Errorf("Channel closed unexpectedly for job %d", i)
			continue
		}
		if val.(int) != i*2 {
			t.Errorf("Invalid result for job %d: got %v, want %v", i, val, i*2)
		}
	}

	wp.Shutdown()
}

func TestWorkerPoolGracefulShutdownWithPendingJobs(t *testing.T) {
	workers := 3
	wp := NewWorkerPool(workers)
	jobs := 20

	var wg sync.WaitGroup
	wg.Add(jobs)

	for i := 0; i < jobs; i++ {
		iCopy := i
		go func() {
			defer wg.Done()
			res := <-wp.Submit(func() interface{} {
				time.Sleep(time.Duration(5+2*iCopy) * time.Millisecond)
				return iCopy
			})
			if res != iCopy {
				t.Errorf("Wrong result, got %v, want %v", res, iCopy)
			}
		}()
	}
	wg.Wait()
	wp.Shutdown()
// Confirm a second shutdown is safe (no panic or block)
	wp.Shutdown()
}
