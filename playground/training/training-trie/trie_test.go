package main

import (
	"math/rand"
	"strconv"
	"sync"
	"testing"
)

func TestTrieSequential(t *testing.T) {
	trie := NewTrie()
	words := []string{"apple", "banana", "grape", "orange"}
	for _, w := range words {
		trie.Insert(w)
	}
	for _, w := range words {
		if !trie.Search(w) {
			t.Errorf("Should find %s after insert", w)
		}
	}
	for _, w := range words {
		if !trie.Delete(w) {
			t.Errorf("Should delete %s", w)
		}
		if trie.Search(w) {
			t.Errorf("Should not find %s after delete", w)
		}
	}
}

func TestTrieConcurrentStress(t *testing.T) {
	trie := NewTrie()
	var wg sync.WaitGroup
	words := 100
	workers := 20

	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			randGen := rand.New(rand.NewSource(int64(i)))
			for j := 0; j < words; j++ {
				w := "w" + strconv.Itoa(i) + "_" + strconv.Itoa(randGen.Intn(10000))
				trie.Insert(w)
				_ = trie.Search(w)
				trie.Delete(w)
			}
		}()
	}
	wg.Wait()
}

func BenchmarkConcurrentInsert(b *testing.B) {
	trie := NewTrie()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			w := "word" + strconv.Itoa(i)
			trie.Insert(w)
			i++
		}
	})
}
