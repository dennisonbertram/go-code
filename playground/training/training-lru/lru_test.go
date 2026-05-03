package main

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestLRUEvictionAndGetPut(t *testing.T) {
	cases := []struct {
		capacity int
		opers    []struct {
			op   string
			key   string
			value string
			want  string
			ok    bool
		}
	}{
		{
			capacity: 2,
			opers: []struct {
				op   string
				key   string
				value string
				want  string
				ok    bool
			}{
				{"put", "a", "1", "", false},
				{"put", "b", "2", "", false},
				{"get", "a", "", "1", true},
				{"put", "c", "3", "", false}, // evict b
				{"get", "b", "", "", false},
				{"get", "c", "", "3", true},
				{"get", "a", "", "1", true},
				{"put", "d", "4", "", false}, // evict c
				{"get", "c", "", "", false},
			},
		},
	}
	for _, tt := range cases {
		cache := NewLRU(tt.capacity)
		for _, op := range tt.opers {
			switch op.op {
			case "put":
				cache.Put(op.key, op.value)
			case "get":
				got, ok := cache.Get(op.key)
				if ok != op.ok || got != op.want {
					t.Errorf("Get(%q) = (%q, %v), want (%q, %v)", op.key, got, ok, op.want, op.ok)
				}
			}
		}
	}
}

func TestLRUConcurrentNoDataRace(t *testing.T) {
	cache := NewLRU(20)
	var stop int32
	var g sync.WaitGroup

	// Spawn writers
	for i := 0; i < 8; i++ {
		g.Add(1)
		go func(id int) {
			defer g.Done()
			k := string(rune('a' + id))
			for atomic.LoadInt32(&stop) == 0 {
				cache.Put(k, k)
			}
		}(i)
	}
	// Spawn readers
	for i := 0; i < 8; i++ {
		g.Add(1)
		go func(id int) {
			defer g.Done()
			k := string(rune('a' + id))
			for atomic.LoadInt32(&stop) == 0 {
				cache.Get(k)
			}
		}(i)
	}
	// Let them run a bit
	wait := make(chan struct{})
	go func() {
		g.Wait()
		close(wait)
	}()
	// Let the goroutines work for a little
	select {
	case <-wait:
		// shouldn't happen, but exit if all g's done
	case <-func() <-chan struct{} {
		c := make(chan struct{})
		go func() { defer close(c); atomic.StoreInt32(&stop, 1) }()
		return c
	}():
		// stop signaled
	}
	atomic.StoreInt32(&stop, 1)
	g.Wait()
}
