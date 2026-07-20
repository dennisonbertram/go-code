package bchan

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestBoundedChan_ProducerConsumer(t *testing.T) {
	const (
		nProducers          = 10
		nConsumers          = 10
		elementsPerProducer = 1000
		capacity            = 32
	)
	ch := New[int](capacity)
	var wg sync.WaitGroup
	produceTotal := nProducers * elementsPerProducer
	consumeCount := 0
	consumeMu := sync.Mutex{}

	// Producers
	wg.Add(nProducers)
	for p := 0; p < nProducers; p++ {
		go func(pidx int) {
			defer wg.Done()
			for i := 0; i < elementsPerProducer; i++ {
				if err := ch.Send(context.Background(), pidx*elementsPerProducer+i); err != nil {
					t.Errorf("Send error: %v", err)
				}
			}
		}(p)
	}

	// Consumers
	var consumeWg sync.WaitGroup
	consumeWg.Add(nConsumers)
	for c := 0; c < nConsumers; c++ {
		go func() {
			defer consumeWg.Done()
			for {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, err := ch.Recv(ctx)
				cancel()
				if err != nil {
					if err == context.Canceled || err == context.DeadlineExceeded {
						return
					}
					t.Errorf("Recv error: %v", err)
					return
				}
				consumeMu.Lock()
				consumeCount++
				consumeMu.Unlock()
			}
		}()
	}

	wg.Wait()
	// All sends finished
	end := time.Now().Add(1 * time.Second)
	for ch.Len() > 0 && time.Now().Before(end) {
		time.Sleep(10 * time.Millisecond)
	}
	ch.mu.Lock()
	ch.closed = true
	ch.notEmpty.Broadcast()
	ch.mu.Unlock()
	consumeWg.Wait()

	if consumeCount != produceTotal {
		t.Fatalf("mismatch: sent %d received %d", produceTotal, consumeCount)
	}
}

func TestBoundedChan_SendRespectCtxCanceled(t *testing.T) {
	ch := New[int](1)
	ctx, cancel := context.WithCancel(context.Background())
	if err := ch.Send(ctx, 1); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	done := make(chan error, 1)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel2()
	go func() {
		done <- ch.Send(ctx2, 2) // this will block, buffer is full
	}()
	time.Sleep(10 * time.Millisecond)
	cancel2()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected error on canceled context")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("blocked send did not return after cancel")
	}
}
