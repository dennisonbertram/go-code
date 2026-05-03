package lfqueue

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestQueueStress(t *testing.T) {
	const producers = 8
	const consumers = 8
	const total = 100000
	q := NewQueue[int]()

	var produced [total]int32
	var consumed [total]int32

	var wg sync.WaitGroup
	wg.Add(producers + consumers)

	// Producer goroutines
	for p := 0; p < producers; p++ {
		go func(pid int) {
			for i := pid; i < total; i += producers {
				q.Enqueue(i)
				atomic.AddInt32(&produced[i], 1)
			}
			wg.Done()
		}(p)
	}

	// Consumer goroutines
	var consumedCount int32
	for c := 0; c < consumers; c++ {
		go func(cid int) {
			for {
				if atomic.LoadInt32(&consumedCount) >= int32(total) {
					break
				}
				v, ok := q.Dequeue()
				if ok {
					atomic.AddInt32(&consumedCount, 1)
					atomic.AddInt32(&consumed[v], 1)
				}
			}
			wg.Done()
		}(c)
	}

	wg.Wait()

	for i := 0; i < total; i++ {
		if produced[i] != 1 {
			t.Errorf("item %d produced %d times", i, produced[i])
		}
		if consumed[i] != 1 {
			t.Errorf("item %d consumed %d times", i, consumed[i])
		}
	}
}