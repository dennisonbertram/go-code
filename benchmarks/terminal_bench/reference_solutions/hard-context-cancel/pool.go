package main

import (
	"context"
	"sync"
)

// Reference solution: honors context cancellation and does not leak goroutines.
func Process(ctx context.Context, jobs []int, workers int, fn func(int) int) ([]int, error) {
	results := make([]int, len(jobs))
	idxCh := make(chan int)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case idx, ok := <-idxCh:
					if !ok {
						return
					}
					results[idx] = fn(jobs[idx])
				}
			}
		}()
	}

produce:
	for i := range jobs {
		select {
		case <-ctx.Done():
			break produce
		case idxCh <- i:
		}
	}
	close(idxCh)
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}
