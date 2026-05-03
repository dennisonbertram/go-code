package ptrie

import (
	"fmt"
	"testing"
)

func TestPersistentTrieTwoVersions(t *testing.T) {
	root1 := &Node{children: make(map[rune]*Node)}
	words1 := []string{"apple", "ape", "bat", "bath"}
	for _, w := range words1 {
		root1 = Insert(root1, w)
	}
	// Insert into v1 to create v2
	newWord := "bar"
	root2 := Insert(root1, newWord)

	for _, w := range words1 {
		if !Search(root1, w) {
			t.Fatalf("root1 lost word %q", w)
		}
		if !Search(root2, w) {
			t.Fatalf("root2 missing word %q", w)
		}
	}
	if Search(root1, newWord) {
		t.Fatalf("root1 should not have new word %q", newWord)
	}
	if !Search(root2, newWord) {
		t.Fatalf("root2 missing inserted word %q", newWord)
	}
}

func TestPersistentTrieManyVersions(t *testing.T) {
	versions := 50
	wordsPerVersion := 1000 / versions
	var roots []*Node
	var allWords [][]string
	var current *Node = &Node{children: make(map[rune]*Node)}
	var inserted []string
	for v := 0; v < versions; v++ {
		words := make([]string, 0, wordsPerVersion)
		for i := 0; i < wordsPerVersion; i++ {
			w := fmt.Sprintf("word-%02d-%02d", v, i)
			current = Insert(current, w)
			words = append(words, w)
			inserted = append(inserted, w)
		}
		copyWords := make([]string, len(inserted))
		copy(copyWords, inserted)
		roots = append(roots, current)
		allWords = append(allWords, copyWords)
	}
	// Verify that each version contains exactly the words it should, and no future words
	for v := 0; v < versions; v++ {
		root := roots[v]
		for idx, w := range allWords[v] {
			if !Search(root, w) {
				t.Errorf("root for version %d missing word %q (idx %d)", v, w, idx)
			}
		}
		if v < versions-1 {
			for _, w := range allWords[versions-1][len(allWords[v]):] {
				if Search(root, w) {
					t.Errorf("root for version %d incorrectly contains future word %q", v, w)
				}
			}
		}
	}
}
