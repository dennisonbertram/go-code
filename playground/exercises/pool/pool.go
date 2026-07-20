package main

import (
	"sync"
)

type WorkerPool struct {
	jobs chan jobWrapper
	wg   sync.WaitGroup
	quit chan struct{}
}

type jobWrapper struct {
	job   func() interface{}
	retCh chan interface{}
}

func NewWorkerPool(workers int) *WorkerPool {
	wp := &WorkerPool{
		jobs: make(chan jobWrapper),
		quit: make(chan struct{}),
	}
	for i := 0; i < workers; i++ {
		wp.wg.Add(1)
		go func() {
			defer wp.wg.Done()
			for {
				select {
				case job, ok := <-wp.jobs:
					if !ok {
						return
					}
					result := job.job()
					job.retCh <- result
					close(job.retCh)
				case <-wp.quit:
					return
				}
			}
		}()
	}
	return wp
}

func (wp *WorkerPool) Submit(job func() interface{}) <-chan interface{} {
	retCh := make(chan interface{}, 1)
	wp.jobs <- jobWrapper{job: job, retCh: retCh}
	return retCh
}

func (wp *WorkerPool) Shutdown() {
	close(wp.jobs)
	wp.wg.Wait()
	close(wp.quit)
}
