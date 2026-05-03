package main

import (
	"sync"
)

type WorkerPool struct {
	jobs      chan jobRequest
	wg        sync.WaitGroup
	shutdown  chan struct{}
	once      sync.Once
}

type jobRequest struct {
	job    func() interface{}
	result chan interface{}
}

func NewWorkerPool(workers int) *WorkerPool {
	wp := &WorkerPool{
		jobs:     make(chan jobRequest),
		shutdown: make(chan struct{}),
	}
	for i := 0; i < workers; i++ {
		wp.wg.Add(1)
		go func() {
			defer wp.wg.Done()
			for req := range wp.jobs {
				select {
				case <-wp.shutdown:
					return
				default:
					res := req.job()
					req.result <- res
					close(req.result)
				}
			}
		}()
	}
	return wp
}

func (wp *WorkerPool) Submit(job func() interface{}) <-chan interface{} {
	resCh := make(chan interface{}, 1)
	jr := jobRequest{job: job, result: resCh}
	select {
	case wp.jobs <- jr:
	case <-wp.shutdown:
		close(resCh)
	}
	return resCh
}

func (wp *WorkerPool) Shutdown() {
	wp.once.Do(func() {
		close(wp.shutdown)
		close(wp.jobs)
		wp.wg.Wait()
	})
}
