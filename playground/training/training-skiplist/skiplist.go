package main

import (
	"math/rand"
	"sync"
	"time"
)

const (
	maxLevel    = 32
	probability = 0.5
)

type element struct {
	key   int
	value any
	next  []*element // Forward pointers for each level
}

type SkipList struct {
	head   *element
	level  int
	locks  []sync.Mutex     // Per-level mutexes
	randMu sync.Mutex        // protects rand.Seed/global rand
	rand   *rand.Rand
}

func NewSkipList() *SkipList {
	head := &element{
		next: make([]*element, maxLevel),
	}
	return &SkipList{
		head:  head,
		level: 1,
		locks: make([]sync.Mutex, maxLevel),
		rand:  rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// randomLevel returns a random level for node height
func (sl *SkipList) randomLevel() int {
	lvl := 1
	sl.randMu.Lock()
	defer sl.randMu.Unlock()
	for lvl < maxLevel && sl.rand.Float64() < probability {
		lvl++
	}
	return lvl
}

// Insert inserts a key-value into the skip list
func (sl *SkipList) Insert(key int, value any) {
	update := make([]*element, maxLevel)
	current := sl.head
	for i := sl.level - 1; i >= 0; i-- {
		sl.locks[i].Lock()
		for current.next[i] != nil && current.next[i].key < key {
			current = current.next[i]
		}
		update[i] = current
	}
	lvl := sl.randomLevel()
	if lvl > sl.level {
		for i := sl.level; i < lvl; i++ {
			sl.locks[i].Lock()
			update[i] = sl.head
		}
		sl.level = lvl
	}
	existing := update[0].next[0]
	if existing != nil && existing.key == key {
		// Replace value for existing key
		existing.value = value
		for i := 0; i < lvl && i < sl.level; i++ {
			sl.locks[i].Unlock()
		}
		return
	}
	e := &element{
		key:   key,
		value: value,
		next:  make([]*element, lvl),
	}
	for i := 0; i < lvl; i++ {
		e.next[i] = update[i].next[i]
		update[i].next[i] = e
		sl.locks[i].Unlock()
	}
}

// Search looks for a key and returns its value and existence
func (sl *SkipList) Search(key int) (any, bool) {
	current := sl.head
	for i := sl.level - 1; i >= 0; i-- {
		sl.locks[i].Lock()
		for current.next[i] != nil && current.next[i].key < key {
			current = current.next[i]
		}
		sl.locks[i].Unlock()
	}
	candidate := current.next[0]
	if candidate != nil && candidate.key == key {
		return candidate.value, true
	}
	return nil, false
}

// Delete removes a key from the skiplist
func (sl *SkipList) Delete(key int) bool {
	update := make([]*element, maxLevel)
	current := sl.head
	found := false
	for i := sl.level - 1; i >= 0; i-- {
		sl.locks[i].Lock()
		for current.next[i] != nil && current.next[i].key < key {
			current = current.next[i]
		}
		update[i] = current
	}
	target := current.next[0]
	if target == nil || target.key != key {
		for i := sl.level - 1; i >= 0; i-- {
			sl.locks[i].Unlock()
		}
		return false
	}
	found = true
	for i := 0; i < sl.level; i++ {
		if update[i].next[i] == target {
			update[i].next[i] = target.next[i]
		}
		sl.locks[i].Unlock()
	}
	// Reduce level if necessary
	for sl.level > 1 && sl.head.next[sl.level-1] == nil {
		sl.level--
	}
	return found
}

// Range returns sorted keys >= lo and < hi
func (sl *SkipList) Range(lo, hi int) []int {
	res := []int{}
	current := sl.head
	for i := sl.level - 1; i >= 0; i-- {
		sl.locks[i].Lock()
		for current.next[i] != nil && current.next[i].key < lo {
			current = current.next[i]
		}
		sl.locks[i].Unlock()
	}
	current = current.next[0]
	for current != nil && current.key < hi {
		if current.key >= lo {
			res = append(res, current.key)
		}
		current = current.next[0]
	}
	return res
}
