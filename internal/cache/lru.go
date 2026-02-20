package cache

import (
	"container/list"
	"sync"
	"time"
)

type lruEntry struct {
	adapterID  string
	size       int64
	accessedAt time.Time
}

type LRU struct {
	mu       sync.Mutex
	items    map[string]*list.Element
	order    *list.List
	curSize  int64
	maxSize  int64
}

func NewLRU(maxSize int64) *LRU {
	return &LRU{
		items:   make(map[string]*list.Element),
		order:   list.New(),
		maxSize: maxSize,
	}
}

func (l *LRU) Touch(adapterID string, size int64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if el, ok := l.items[adapterID]; ok {
		entry := el.Value.(*lruEntry)
		entry.accessedAt = time.Now()
		l.order.MoveToFront(el)
		return
	}

	entry := &lruEntry{
		adapterID:  adapterID,
		size:       size,
		accessedAt: time.Now(),
	}
	el := l.order.PushFront(entry)
	l.items[adapterID] = el
	l.curSize += size
}

// Evict removes and returns the least recently used adapter IDs until
// the total size is at or below maxSize, or until enough room is made
// for the given requiredBytes.
func (l *LRU) Evict(requiredBytes int64) []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	var evicted []string
	for l.curSize+requiredBytes > l.maxSize && l.order.Len() > 0 {
		tail := l.order.Back()
		entry := tail.Value.(*lruEntry)
		l.order.Remove(tail)
		delete(l.items, entry.adapterID)
		l.curSize -= entry.size
		evicted = append(evicted, entry.adapterID)
	}
	return evicted
}

func (l *LRU) Remove(adapterID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	el, ok := l.items[adapterID]
	if !ok {
		return false
	}
	entry := el.Value.(*lruEntry)
	l.order.Remove(el)
	delete(l.items, adapterID)
	l.curSize -= entry.size
	return true
}

func (l *LRU) Contains(adapterID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.items[adapterID]
	return ok
}

func (l *LRU) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.order.Len()
}

func (l *LRU) Size() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.curSize
}

func (l *LRU) MaxSize() int64 {
	return l.maxSize
}

func (l *LRU) Items() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	ids := make([]string, 0, l.order.Len())
	for el := l.order.Front(); el != nil; el = el.Next() {
		ids = append(ids, el.Value.(*lruEntry).adapterID)
	}
	return ids
}
