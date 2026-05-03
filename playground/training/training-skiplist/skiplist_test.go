package main

import (
	"sync"
	"testing"
)

func TestSkipListSequential(t *testing.T) {
	sl := NewSkipList()
	keys := []int{10, 20, 5, 15, 25, 30}
	values := []string{"a", "b", "c", "d", "e", "f"}
	for i, k := range keys {
		sl.Insert(k, values[i])
	}
	for i, k := range keys {
		v, ok := sl.Search(k)
		if !ok {
			t.Errorf("key %d not found", k)
		}
		if v.(string) != values[i] {
			t.Errorf("value mismatch for key %d: got %v, want %v", k, v, values[i])
		}
	}
	// Delete a key
	if !sl.Delete(20) {
		t.Errorf("Delete returned false for key 20")
	}
	if _, ok := sl.Search(20); ok {
		t.Errorf("key 20 still found after delete")
	}
	wantKeys := []int{5, 10, 15, 25, 30}
	out := sl.Range(0, 100)
	if len(out) != len(wantKeys) {
		t.Fatalf("Range: wrong length got %v want %v", out, wantKeys)
	}
	for i := range wantKeys {
		if wantKeys[i] != out[i] {
			t.Fatalf("Range: wrong key at %d got %d want %d", i, out[i], wantKeys[i])
		}
	}
}

func TestSkipListConcurrentInsert(t *testing.T) {
	sl := NewSkipList()
	numGoroutines := 8
	keysPerG := 1000
	total := numGoroutines * keysPerG
	startKey := 10000
	wg := sync.WaitGroup{}
	wg.Add(numGoroutines)
	for g := 0; g < numGoroutines; g++ {
		go func(gid int) {
			base := startKey + gid*keysPerG
			for i := 0; i < keysPerG; i++ {
				sl.Insert(base+i, gid*keysPerG+i)
			}
			wg.Done()
		}(g)
	}
	wg.Wait()

	// Check all inserted keys are present
	missing := 0
	for k := startKey; k < startKey+total; k++ {
		if v, ok := sl.Search(k); !ok {
			t.Errorf("missing key %d", k)
			missing++
		} else if v != k-startKey {
			t.Errorf("key %d has wrong value %v", k, v)
		}
	}
	if missing > 0 {
		t.Fatalf("there are %d missing keys", missing)
	}
}
