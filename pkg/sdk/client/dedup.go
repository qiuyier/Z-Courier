package client

import (
	"container/list"
	"sync"
)

type messageDeduper struct {
	mu       sync.Mutex
	capacity int
	entries  map[string]*list.Element
	order    *list.List
}

func newMessageDeduper(capacity int) *messageDeduper {
	return &messageDeduper{
		capacity: capacity,
		entries:  make(map[string]*list.Element, capacity),
		order:    list.New(),
	}
}

func (deduper *messageDeduper) contains(messageID string) bool {
	if messageID == "" {
		return false
	}
	deduper.mu.Lock()
	defer deduper.mu.Unlock()
	element := deduper.entries[messageID]
	if element == nil {
		return false
	}
	deduper.order.MoveToBack(element)
	return true
}

func (deduper *messageDeduper) mark(messageID string) {
	if messageID == "" {
		return
	}
	deduper.mu.Lock()
	defer deduper.mu.Unlock()
	if element := deduper.entries[messageID]; element != nil {
		deduper.order.MoveToBack(element)
		return
	}
	element := deduper.order.PushBack(messageID)
	deduper.entries[messageID] = element
	if deduper.order.Len() <= deduper.capacity {
		return
	}
	oldest := deduper.order.Front()
	delete(deduper.entries, oldest.Value.(string))
	deduper.order.Remove(oldest)
}
