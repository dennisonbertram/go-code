package main

import (
	"strings"
	"sync"
)

type EventBus struct {
	mu      sync.RWMutex
	topics  map[string]map[chan string]struct{}
	subWild map[string]map[chan string]struct{} // wildcards, e.g., "user.*"
}

func NewEventBus() *EventBus {
	return &EventBus{
		topics:  make(map[string]map[chan string]struct{}),
		subWild: make(map[string]map[chan string]struct{}),
	}
}

func (b *EventBus) Subscribe(topic string) <-chan string {
	ch := make(chan string, 16)
	b.mu.Lock()
	defer b.mu.Unlock()
	if strings.HasSuffix(topic, "*") {
		if b.subWild[topic] == nil {
			b.subWild[topic] = make(map[chan string]struct{})
		}
		b.subWild[topic][ch] = struct{}{}
	} else {
		if b.topics[topic] == nil {
			b.topics[topic] = make(map[chan string]struct{})
		}
		b.topics[topic][ch] = struct{}{}
	}
	return ch
}

func (b *EventBus) Publish(topic string, msg string) {
	b.mu.RLock()
	var chans []chan string
	for ch := range b.topics[topic] {
		chans = append(chans, ch)
	}
	for wild, set := range b.subWild {
		prefix := strings.TrimSuffix(wild, "*")
		if strings.HasPrefix(topic, prefix) {
			for ch := range set {
				chans = append(chans, ch)
			}
		}
	}
	b.mu.RUnlock()
	for _, ch := range chans {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (b *EventBus) Unsubscribe(topic string, ch <-chan string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, ok := ch.(chan string)
	if !ok {
		return // must be same channel type
	}
	isWild := strings.HasSuffix(topic, "*")
	if isWild {
		if chans, ok := b.subWild[topic]; ok {
			delete(chans, c)
			if len(chans) == 0 {
				delete(b.subWild, topic)
			}
		}
	} else {
		if chans, ok := b.topics[topic]; ok {
			delete(chans, c)
			if len(chans) == 0 {
				delete(b.topics, topic)
			}
		}
	}
	close(c)
}
