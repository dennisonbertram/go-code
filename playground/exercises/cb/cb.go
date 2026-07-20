package main

import (
	"errors"
	"sync"
	"time"
)

// State represents the internal state of the circuit breaker
//go:generate stringer -type=State

type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

var (
	ErrCircuitOpen = errors.New("circuit breaker is open")
)

// CircuitBreaker implements a concurrency-safe circuit breaker pattern
// with Closed, Open, and HalfOpen states.
type CircuitBreaker struct {
	FailureThreshold int
	SuccessThreshold int
	Timeout          time.Duration

	mu              sync.Mutex
	state           State
	failures        int
	successes       int
	timer           *time.Timer
	lastStateChange time.Time
}

func NewCircuitBreaker(failureThreshold, successThreshold int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		FailureThreshold: failureThreshold,
		SuccessThreshold: successThreshold,
		Timeout:          timeout,
		state:            Closed,
		lastStateChange:  time.Now(),
	}
}

// State returns the current state for testing purposes
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

func (cb *CircuitBreaker) setState(state State) {
	cb.state = state
	cb.lastStateChange = time.Now()
	if cb.timer != nil {
		cb.timer.Stop()
		cb.timer = nil
	}
	if state == Open {
		cb.timer = time.AfterFunc(cb.Timeout, func() {
			cb.mu.Lock()
			defer cb.mu.Unlock()
			if cb.state == Open {
				cb.setState(HalfOpen)
				cb.failures = 0
				cb.successes = 0
			}
		})
	}
	if state == Closed || state == HalfOpen {
		cb.failures = 0
		cb.successes = 0
	}
}

// Execute runs fn respecting circuit breaker state and transitions.
// Returns ErrCircuitOpen if breaker is open.
func (cb *CircuitBreaker) Execute(fn func() error) error {
	cb.mu.Lock()
	state := cb.state
	cb.mu.Unlock()

	if state == Open {
		return ErrCircuitOpen
	}

	err := fn()

	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case Closed:
		if err != nil {
			cb.failures++
			if cb.failures >= cb.FailureThreshold {
				cb.setState(Open)
			}
		} else {
			cb.failures = 0
		}
	case HalfOpen:
		if err == nil {
			cb.successes++
			if cb.successes >= cb.SuccessThreshold {
				cb.setState(Closed)
			}
		} else {
			cb.setState(Open)
		}
	}
	return err
}
