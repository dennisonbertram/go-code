package main

import (
	"net/http"
	"sync"
	"time"
)

type RateLimiter struct {
	rps    float64
	tokens float64
	last   time.Time
	mu     sync.Mutex
	next   http.Handler
}

func NewRateLimiter(rps float64, next http.Handler) *RateLimiter {
	return &RateLimiter{
		rps:    rps,
		tokens: rps, // allow burst of rps tokens
		last:   time.Now(),
		next:   next,
	}
}

func (rl *RateLimiter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rl.mu.Lock()
	now := time.Now()
	elapsed := now.Sub(rl.last).Seconds()
	// replenish tokens
	rl.tokens += elapsed * rl.rps
	if rl.tokens > rl.rps {
		rl.tokens = rl.rps
	}
	if rl.tokens >= 1 {
		rl.tokens--
		rl.last = now
		rl.mu.Unlock()
		rl.next.ServeHTTP(w, r)
		return
	}
	// Not enough tokens
	rl.last = now
	rl.mu.Unlock()
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusTooManyRequests)
	w.Write([]byte("rate limit exceeded"))
}
