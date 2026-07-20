package lfqueue

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestQueueStress(t *testing.T) {
	nProducers := 8
	nConsumers := 8
	totalItems := 100000

	q := NewQueue[int]()
	produced := int64(0)
	consumed := int64(0)

	itemMap := make([]int32, totalItems)
	var itemMapMu sync.Mutex
	var wg sync.WaitGroup
	idChan := make(chan int, totalItems)
	for i := 0; i < totalItems; i++ {
		idChan <- i
	}
	close(idChan)

	wg.Add(nProducers)
	for i := 0; i < nProducers; i++ {
		go func() {
			defer wg.Done()
			for v := range idChan {
				q.Enqueue(v)
				atomic.AddInt64(&produced, 1)
			}
		}()
	}

	// Consumers
	done := make(chan struct{})
	wg.Add(nConsumers)
	for i := 0; i < nConsumers; i++ {
		go func() {
			defer wg.Done()
			for {
				v, ok := q.Dequeue()
				if !ok {
					runtime.Gosched()
					select {
					case <-done:
						return
					default:
					}
					continue
				}
				if v < 0 || v >= totalItems {
					t.Fatalf("got out-of-range value %v", v)
				}
				if atomic.AddInt32(&itemMap[v], 1) != 1 {
					t.Fatalf("duplicate dequeue of %v", v)
				}
				atomic.AddInt64(&consumed, 1)
			}
		}()
	}

	wg.Wait()
	close(done)

	// Drain any items left in the queue
	for {
		v, ok := q.Dequeue()
		if !ok {
			break
		}
		if v < 0 || v >= totalItems {
			t.Fatalf("got out-of-range value %v (drain)", v)
		}
		if atomic.AddInt32(&itemMap[v], 1) != 1 {
			t.Fatalf("duplicate dequeue of %v (drain)", v)
		}
		atomic.AddInt64(&consumed, 1)
	}

	if produced != int64(totalItems) {
		t.Errorf("Not all items produced: got %v want %v", produced, totalItems)
	}
	if consumed != int64(totalItems) {
		t.Errorf("Got lost or duplicated items: consumed %v, want %v", consumed, totalItems)
	}
	for i, count := range itemMap {
		if count != 1 {
			t.Errorf("Value %v delivered %v times", i, count)
		}
	}
}
