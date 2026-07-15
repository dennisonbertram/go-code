package main

import (
	"context"
	"sync"
)

// Process runs fn over every job using `workers` goroutines and returns the
// results in job order.
//
// Contract:
//   - If ctx is cancelled while work is in flight, Process must stop promptly
//     and return a nil result slice together with ctx.Err().
//   - Process must not leave any worker goroutines running after it returns.
//
// BUG: this implementation ignores ctx entirely. It always drains every job
// and returns a nil error, so a cancelled context is silently ignored.
func Process(ctx context.Context, jobs []int, workers int, fn func(int) int) ([]int, error) {
	results := make([]int, len(jobs))
	idxCh := make(chan int)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range idxCh {
				results[idx] = fn(jobs[idx])
			}
		}()
	}

	for i := range jobs {
		idxCh <- i
	}
	close(idxCh)
	wg.Wait()

	return results, nil
}
