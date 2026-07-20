package main

import (
	"sync"
)

// LFU represents a thread-safe Least Frequently Used cache
// with O(1) get and put operations.
type LFU struct {
	capacity int
	mutex    sync.Mutex
	items    map[int]*entry
	freqs    map[int]*freqNode // frequency -> freq node
	head     *freqNode         // least freq
	tail     *freqNode         // most freq
}

type entry struct {
	key    int
	value  int
	freq   int
	prev   *entry
	next   *entry
	parent *freqNode
}

type freqNode struct {
	freq int
	head *entry // dummy head (for LRU at this freq)
	tail *entry // dummy tail
	prev *freqNode
	next *freqNode
}

// NewLFU creates a new LFU cache with the given capacity.
func NewLFU(capacity int) *LFU {
	lfu := &LFU{
		capacity: capacity,
		items:    make(map[int]*entry),
		freqs:    make(map[int]*freqNode),
	}
	return lfu
}

// Get retrieves a value by key, updating its frequency.
func (l *LFU) Get(key int) (int, bool) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	if ent, ok := l.items[key]; ok {
		l.increment(ent)
		return ent.value, true
	}
	return 0, false
}

// Put inserts or updates a value by key, possibly evicting an item.
func (l *LFU) Put(key int, value int) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	if l.capacity == 0 {
		return
	}
	if ent, ok := l.items[key]; ok {
		ent.value = value
		l.increment(ent)
		return
	}
	if len(l.items) >= l.capacity {
		l.evict()
	}
	ent := &entry{key: key, value: value, freq: 1}
	freq := l.freqNode(1)
	l.items[key] = ent
	l.attach(ent, freq)
	if l.head == nil || l.head.freq > 1 {
		l.insertFreqNode(freq, l.head)
		l.head = freq
		if l.tail == nil {
			l.tail = freq
		}
	}
}

// increment increases frequency for an entry.
func (l *LFU) increment(ent *entry) {
	curr := ent.parent
	nextFreq := ent.freq + 1
	l.detach(ent, curr)
	ent.freq = nextFreq
	node := l.freqNode(nextFreq)
	l.attach(ent, node)
	if node.next == nil && curr != nil && curr.freq < node.freq {
		l.insertFreqNode(node, curr.next)
		l.tail = node
	}
	if curr.head == curr.tail { // curr is now empty
		l.removeFreqNode(curr)
		if l.head == curr {
			l.head = node
		}
	}
}

// attach adds entry to the freqNode's LRU list (at head, MRU).
func (l *LFU) attach(ent *entry, freq *freqNode) {
	ent.parent = freq
	ent.prev = nil
	ent.next = freq.head
	if freq.head != nil {
		freq.head.prev = ent
	} else {
		freq.tail = ent
	}
	freq.head = ent
}

// detach removes entry from its freqNode's LRU list.
func (l *LFU) detach(ent *entry, freq *freqNode) {
	if ent.prev != nil {
		ent.prev.next = ent.next
	} else {
		freq.head = ent.next
	}
	if ent.next != nil {
		ent.next.prev = ent.prev
	} else {
		freq.tail = ent.prev
	}
	ent.prev, ent.next = nil, nil
}

// freqNode finds or creates freqNode for freq.
func (l *LFU) freqNode(freq int) *freqNode {
	if n, ok := l.freqs[freq]; ok {
		return n
	}
	n := &freqNode{freq: freq}
	l.freqs[freq] = n
	return n
}

// insertFreqNode inserts n before target.
func (l *LFU) insertFreqNode(n, target *freqNode) {
	if target == nil {
		return
	}
	n.next = target
	n.prev = target.prev
	if target.prev != nil {
		target.prev.next = n
	}
	target.prev = n
}

func (l *LFU) removeFreqNode(n *freqNode) {
	if n.prev != nil {
		n.prev.next = n.next
	}
	if n.next != nil {
		n.next.prev = n.prev
	}
	delete(l.freqs, n.freq)
}

// evict removes the LRU item from the lowest frequency node.
func (l *LFU) evict() {
	if l.head == nil {
		return
	}
	victim := l.head.tail
	if victim == nil {
		return
	}
	l.detach(victim, l.head)
	delete(l.items, victim.key)
	if l.head.head == nil { // freqNode now empty
		next := l.head.next
		l.removeFreqNode(l.head)
		l.head = next
		if l.head == nil {
			l.tail = nil
		}
	}
}
