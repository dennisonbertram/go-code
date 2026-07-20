package main

import (
	"sync"
	"testing"
)

func Test_SlabCorrectness(t *testing.T) {
	a := NewAllocator([]int{8, 16, 32, 64, 128, 256})
	tests := []struct {
		size       int
		expectSlab int
	}{
		{1, 8}, {8, 8}, {9, 16}, {22, 32}, {70, 128}, {255, 256},
	}
	for _, tt := range tests {
		b := a.Alloc(tt.size)
		if cap(b) != tt.expectSlab {
			t.Errorf("Alloc(%d): expected cap %d, got %d", tt.size, tt.expectSlab, cap(b))
		}
		if len(b) != tt.size {
			t.Errorf("Alloc(%d): expected len %d, got %d", tt.size, tt.size, len(b))
		}
	}
}

func Test_SlabReuse(t *testing.T) {
	a := NewAllocator([]int{8, 16, 32})
	b1 := a.Alloc(8)
	ptr1 := &b1[0]
	a.Free(b1)
	b2 := a.Alloc(8)
	ptr2 := &b2[0]
	if ptr1 != ptr2 {
		t.Errorf("Reuse: expected same backing array, got different ones")
	}
}

func Test_LargeFallback(t *testing.T) {
	a := NewAllocator([]int{16, 32, 64})
	b := a.Alloc(128)
	if cap(b) != 128 {
		t.Errorf("Large fallback: got cap %d, want 128", cap(b))
	}
	// Should not be freed back to slab
	a.Free(b) // should no-op, no panic
}

func Test_ConcurrentSafe(t *testing.T) {
	a := NewAllocator([]int{8, 16, 32, 64, 128, 256})
	N := 128
	WG := sync.WaitGroup{}
	WG.Add(N * 2)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer WG.Done()
			b := a.Alloc(24)
			if cap(b) < 24 {
				t.Errorf("Concurrent: cap(b) < 24")
			}
			a.Free(b)
		}(i)
		go func(i int) {
			defer WG.Done()
			b := a.Alloc(120)
			if cap(b) < 120 {
				t.Errorf("Concurrent: cap(b) < 120")
			}
			a.Free(b)
		}(i)
	}
	WG.Wait()
}
