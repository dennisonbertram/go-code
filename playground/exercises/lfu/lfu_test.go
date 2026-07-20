package main

import (
	"sync"
	"testing"
)

func TestLFUCapacityOne(t *testing.T) {
	c := NewLFU(1)
	c.Put(1, 1)
	v, ok := c.Get(1)
	if !ok || v != 1 {
		t.Fatalf("Expected 1 got %v ok=%v", v, ok)
	}
	c.Put(2, 2)
	_, ok = c.Get(1)
	if ok {
		t.Fatalf("Expected 1 to be evicted")
	}
	v, ok = c.Get(2)
	if !ok || v != 2 {
		t.Fatalf("Expected 2 got %v ok=%v", v, ok)
	}
}

func TestLFUCapacityTwoEvictionOrder(t *testing.T) {
	c := NewLFU(2)
	c.Put(1, 1)
	c.Put(2, 2)
	c.Get(1)    // {1:2, 2:1}
	c.Put(3, 3) // evicts 2
	_, ok := c.Get(2)
	if ok {
		t.Fatalf("Expected 2 to be evicted")
	}
	if v, ok := c.Get(1); !ok || v != 1 {
		t.Fatal("Expected 1 present")
	}
	if v, ok := c.Get(3); !ok || v != 3 {
		t.Fatal("Expected 3 present")
	}
}

func TestLFUTieEviction(t *testing.T) {
	c := NewLFU(2)
	c.Put(1, 1)
	c.Put(2, 2)
	c.Put(3, 3) // None freq increased; evict LRU (1)
	_, ok := c.Get(1)
	if ok {
		t.Fatal("Expected key=1 (LRU, freq=1) evicted")
	}
	if v, ok := c.Get(2); !ok || v != 2 {
		t.Fatal("Expected 2 present")
	}
	if v, ok := c.Get(3); !ok || v != 3 {
		t.Fatal("Expected 3 present")
	}
}

func TestLFUConcurrent(t *testing.T) {
	c := NewLFU(16)
	wg := sync.WaitGroup{}
	for i := 0; i < 16; i++ {
		c.Put(i, i*i)
	}
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				v, ok := c.Get(i)
				if !ok || v != i*i {
					t.Errorf("Missing key=%d want=%d got=%d ok=%v", i, i*i, v, ok)
				}
			}
		}(i)
	}
	wg.Wait()
}
