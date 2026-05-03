package main

import (
	"math/rand"
	"testing"
)

func isSortedStrict(a []int) bool {
	for i := 0; i < len(a)-1; i++ {
		if a[i] >= a[i+1] {
			return false
		}
	}
	return true
}

func TestSequentialInsertInOrder(t *testing.T) {
	tree := NewBTree(3)
	for i := 1; i <= 1000; i++ {
		tree.Insert(i)
	}
	inorder := tree.InOrder()
	if len(inorder) != 1000 {
		t.Fatalf("Expected 1000 keys, got %d", len(inorder))
	}
	for i, k := range inorder {
		if k != i+1 {
			t.Fatalf("Key at pos %d should be %d, got %d", i, i+1, k)
		}
	}
	if !isSortedStrict(inorder) {
		t.Error("InOrder traversal not strictly sorted")
	}
}

func TestRandomDelete(t *testing.T) {
	tree := NewBTree(3)
	nums := rand.Perm(1000)
	for _, v := range nums {
		tree.Insert(v)
	}
	deleteSet := rand.Perm(500)
	for _, v := range deleteSet {
		tree.Delete(v)
	}
	inorder := tree.InOrder()
	if !isSortedStrict(inorder) {
		t.Fatal("InOrder traversal not strictly sorted after delete")
	}
	for _, val := range deleteSet {
		if tree.Search(val) {
			t.Fatalf("Deleted value %d still found", val)
		}
	}
	for _, val := range inorder {
		if !tree.Search(val) {
			t.Fatalf("Existing value %d not found", val)
		}
	}
}

func TestDeleteNonExistent(t *testing.T) {
	tree := NewBTree(3)
	tree.Insert(42)
	tree.Delete(99)
	inorder := tree.InOrder()
	if len(inorder) != 1 || inorder[0] != 42 {
		t.Fatal("Non-existent key deletion affected BTree")
	}
}

func TestDeleteSoleKey(t *testing.T) {
	tree := NewBTree(3)
	tree.Insert(56)
	tree.Delete(56)
	inorder := tree.InOrder()
	if len(inorder) != 0 {
		t.Fatal("Deleting sole key did not empty the tree")
	}
}
