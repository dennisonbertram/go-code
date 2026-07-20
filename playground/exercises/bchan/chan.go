package bchan

import (
	"context"
	"sync"
)

// BoundedChan is a generic bounded, blocking channel.
type BoundedChan[T any] struct {
	cap      int
	mu       sync.Mutex
	notFull  *sync.Cond // Wait when full
	notEmpty *sync.Cond // Wait when empty
	data     []T
	closed   bool
}

// New creates a new bounded channel with the given capacity.
func New[T any](capacity int) *BoundedChan[T] {
	if capacity <= 0 {
		panic("capacity must be > 0")
	}
	bc := &BoundedChan[T]{
		cap:  capacity,
		data: make([]T, 0, capacity),
	}
	bc.notFull = sync.NewCond(&bc.mu)
	bc.notEmpty = sync.NewCond(&bc.mu)
	return bc
}

// Send sends v to the channel, blocking if full. Returns ctx.Err() if context is canceled.
func (bc *BoundedChan[T]) Send(ctx context.Context, v T) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	for len(bc.data) == bc.cap {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		waitCh := make(chan struct{})
		go func() { bc.notFull.Wait(); close(waitCh) }()
		bc.mu.Unlock()
		select {
		case <-ctx.Done():
			bc.mu.Lock()
			return ctx.Err()
		case <-waitCh:
			bc.mu.Lock()
		}
	}
	if bc.closed {
		return context.Canceled // treat closed as canceled for Send
	}
	bc.data = append(bc.data, v)
	bc.notEmpty.Signal()
	return nil
}

// Recv blocks until it receives a value or the context is done. Returns (zero, ctx.Err()) if ctx is canceled.
func (bc *BoundedChan[T]) Recv(ctx context.Context) (T, error) {
	var zero T
	bc.mu.Lock()
	defer bc.mu.Unlock()
	for len(bc.data) == 0 {
		if bc.closed {
			return zero, context.Canceled
		}
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		waitCh := make(chan struct{})
		go func() { bc.notEmpty.Wait(); close(waitCh) }()
		bc.mu.Unlock()
		select {
		case <-ctx.Done():
			bc.mu.Lock()
			return zero, ctx.Err()
		case <-waitCh:
			bc.mu.Lock()
		}
	}
	v := bc.data[0]
	bc.data = bc.data[1:]
	bc.notFull.Signal()
	return v, nil
}

// Len returns the current number of elements in the channel.
func (bc *BoundedChan[T]) Len() int {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	return len(bc.data)
}

// Cap returns the channel's capacity.
func (bc *BoundedChan[T]) Cap() int {
	return bc.cap
}
