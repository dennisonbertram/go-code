package lfqueue

import (
	"sync/atomic"
	"unsafe"
)

// Michael-Scott lock-free queue node
// https://www.cs.rochester.edu/u/scott/papers/1996_PODC_queues.pdf
type node[T any] struct {
	value T
	next  unsafe.Pointer // *node[T]
}

type Queue[T any] struct {
	head unsafe.Pointer // *node[T]
	tail unsafe.Pointer // *node[T]
}

// NewQueue creates a pointer to a new, empty Michael-Scott queue
func NewQueue[T any]() *Queue[T] {
	dummy := unsafe.Pointer(&node[T]{})
	return &Queue[T]{head: dummy, tail: dummy}
}

// Enqueue adds value v to the queue
func (q *Queue[T]) Enqueue(v T) {
	n := &node[T]{value: v}
	np := unsafe.Pointer(n)

	for {
		tail := atomic.LoadPointer(&q.tail)
		tailNode := (*node[T])(tail)
		next := atomic.LoadPointer(&tailNode.next)
		if next != nil {
			// Tail is behind, swing tail forward
			atomic.CompareAndSwapPointer(&q.tail, tail, next)
			continue
		}
		if atomic.CompareAndSwapPointer(&tailNode.next, nil, np) {
			// Enqueue done, swing tail forward
			atomic.CompareAndSwapPointer(&q.tail, tail, np)
			return
		}
	}
}

// Dequeue removes and returns value from front. Second result is false if empty.
func (q *Queue[T]) Dequeue() (v T, ok bool) {
	for {
		head := atomic.LoadPointer(&q.head)
		headNode := (*node[T])(head)
		next := atomic.LoadPointer(&headNode.next)
		if next == nil {
			var zero T
			return zero, false // empty
		}
		// Get value from next node
		nextNode := (*node[T])(next)
		if atomic.CompareAndSwapPointer(&q.head, head, next) {
			return nextNode.value, true
		}
	}
}