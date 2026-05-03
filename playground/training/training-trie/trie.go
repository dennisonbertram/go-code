package main

import (
	"sync"
)

type TrieNode struct {
	children map[rune]*TrieNode
	terminal bool
}

type Trie struct {
	root *TrieNode
	mu   sync.RWMutex
}

func NewTrie() *Trie {
	return &Trie{
		root: &TrieNode{children: make(map[rune]*TrieNode)},
	}
}

func (t *Trie) Insert(word string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	node := t.root
	for _, ch := range word {
		if node.children[ch] == nil {
			node.children[ch] = &TrieNode{children: make(map[rune]*TrieNode)}
		}
		node = node.children[ch]
	}
	node.terminal = true
}

func (t *Trie) Search(word string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	node := t.root
	for _, ch := range word {
		if node.children[ch] == nil {
			return false
		}
		node = node.children[ch]
	}
	return node.terminal
}

func (t *Trie) Delete(word string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	deleted, _ := t.deleteHelper(t.root, []rune(word), 0)
	return deleted
}

func (t *Trie) deleteHelper(node *TrieNode, word []rune, depth int) (deleted bool, prune bool) {
	if depth == len(word) {
		if !node.terminal {
			return false, false
		}
		node.terminal = false
		return true, len(node.children) == 0
	}
	ch := word[depth]
	child, ok := node.children[ch]
	if !ok {
		return false, false
	}
	deleted, shouldDelete := t.deleteHelper(child, word, depth+1)
	if shouldDelete {
		delete(node.children, ch)
	}
	return deleted, !node.terminal && len(node.children) == 0
}
