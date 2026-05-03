package main

import (
	"container/list"
	"sync"
)

type entry struct {
	key   string
	value string
}

type LRU struct {
	capacity int
	ll       *list.List
	cache    map[string]*list.Element
	mu       sync.Mutex
}

func NewLRU(capacity int) *LRU {
	if capacity <= 0 {
		panic("Capacity must be > 0")
	}
	return &LRU{
		capacity: capacity,
		ll:       list.New(),
		cache:    make(map[string]*list.Element),
	}
}

func (l *LRU) Get(key string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if elem, ok := l.cache[key]; ok {
		l.ll.MoveToFront(elem)
		return elem.Value.(*entry).value, true
	}
	return "", false
}

func (l *LRU) Put(key, value string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if elem, ok := l.cache[key]; ok {
		l.ll.MoveToFront(elem)
		elem.Value.(*entry).value = value
		return
	}
	if l.ll.Len() >= l.capacity {
		oldest := l.ll.Back()
		if oldest != nil {
			l.ll.Remove(oldest)
			ent := oldest.Value.(*entry)
			delete(l.cache, ent.key)
		}
	}
	e := &entry{key, value}
	elem := l.ll.PushFront(e)
	l.cache[key] = elem
}
