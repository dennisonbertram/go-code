package main

import (
	"math/rand"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLRUTable(t *testing.T) {
	tcs := []struct {
		name     string
		capacity int
		puts     []struct {
			k string
			v string
		}
		gets []struct {
			k  string
			v  string
			ok bool
		}
	}{
		{
			name:     "no eviction",
			capacity: 2,
			puts: []struct{ k, v string }{
				{"a", "1"}, {"b", "2"},
			},
			gets: []struct {
				k, v string
				ok   bool
			}{
				{"a", "1", true}, {"b", "2", true},
			},
		},
		{
			name:     "eviction order",
			capacity: 2,
			puts: []struct{ k, v string }{
				{"a", "1"}, {"b", "2"}, {"c", "3"},
			},
			gets: []struct {
				k, v string
				ok   bool
			}{
				{"a", "", false}, {"b", "2", true}, {"c", "3", true},
			},
		},
		{
			name:     "update refreshes",
			capacity: 2,
			puts: []struct{ k, v string }{
				{"a", "1"}, {"b", "2"}, {"a", "x"}, {"c", "3"},
			},
			gets: []struct {
				k, v string
				ok   bool
			}{
				{"b", "", false}, {"a", "x", true}, {"c", "3", true},
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			lru := NewLRU(tc.capacity)
			for _, p := range tc.puts {
				lru.Put(p.k, p.v)
			}
			for _, g := range tc.gets {
				v, ok := lru.Get(g.k)
				if ok != g.ok || v != g.v {
					t.Errorf("Get(%q) got = (%q,%v); want = (%q,%v)", g.k, v, ok, g.v, g.ok)
				}
			}
		})
	}
}

func TestLRUConcurrent(t *testing.T) {
	const N = 512
	const rounds = 1024
	lru := NewLRU(N / 4)
	var found atomic.Uint64
	wg := sync.WaitGroup{}
	for i := 0; i < N; i++ {
		k, v := strconv.Itoa(i), strconv.Itoa(i*i)
		lru.Put(k, v)
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(seed))
			for j := 0; j < rounds; j++ {
				idx := rnd.Intn(N)
				k := strconv.Itoa(idx)
				if v, ok := lru.Get(k); ok {
					exp := strconv.Itoa(idx * idx)
					if v == exp {
						found.Add(1)
					} else {
						t.Errorf("Key %s: got %s, want %s", k, v, exp)
					}
				}
				// random insert
				setk := strconv.Itoa(rnd.Intn(N))
				setv := strconv.Itoa(rnd.Intn(10000))
				lru.Put(setk, setv)
			}
		}(time.Now().UnixNano() + int64(i))
	}
	wg.Wait()
	if found.Load() == 0 {
		t.Fatalf("Concurrent: no gets matched")
	}
}
