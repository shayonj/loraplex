package cache

import (
	"sync"
	"testing"
)

func TestLRUTouchAndContains(t *testing.T) {
	l := NewLRU(1000)
	l.Touch("a", 100)
	l.Touch("b", 200)

	if !l.Contains("a") {
		t.Fatal("expected a to be present")
	}
	if !l.Contains("b") {
		t.Fatal("expected b to be present")
	}
	if l.Contains("c") {
		t.Fatal("expected c to be absent")
	}
	if l.Len() != 2 {
		t.Fatalf("expected len 2, got %d", l.Len())
	}
	if l.Size() != 300 {
		t.Fatalf("expected size 300, got %d", l.Size())
	}
}

func TestLRUTouchUpdatesAccess(t *testing.T) {
	l := NewLRU(1000)
	l.Touch("a", 100)
	l.Touch("b", 200)
	l.Touch("a", 100)

	if l.Len() != 2 {
		t.Fatalf("expected len 2, got %d", l.Len())
	}
	if l.Size() != 300 {
		t.Fatalf("expected size 300 after re-touch, got %d", l.Size())
	}
}

func TestLRUEvictOldest(t *testing.T) {
	l := NewLRU(300)
	l.Touch("a", 100)
	l.Touch("b", 100)
	l.Touch("c", 100)

	evicted := l.Evict(100)
	if len(evicted) != 1 {
		t.Fatalf("expected 1 eviction, got %d", len(evicted))
	}
	if evicted[0] != "a" {
		t.Fatalf("expected a evicted, got %s", evicted[0])
	}
	if l.Contains("a") {
		t.Fatal("a should be evicted")
	}
	if !l.Contains("b") || !l.Contains("c") {
		t.Fatal("b and c should remain")
	}
}

func TestLRUEvictMultiple(t *testing.T) {
	l := NewLRU(300)
	l.Touch("a", 100)
	l.Touch("b", 100)
	l.Touch("c", 100)

	evicted := l.Evict(200)
	if len(evicted) != 2 {
		t.Fatalf("expected 2 evictions, got %d", len(evicted))
	}
}

func TestLRUEvictRespectsAccessOrder(t *testing.T) {
	l := NewLRU(300)
	l.Touch("a", 100)
	l.Touch("b", 100)
	l.Touch("c", 100)
	l.Touch("a", 100)

	evicted := l.Evict(100)
	if evicted[0] != "b" {
		t.Fatalf("expected b evicted first (oldest after a was re-touched), got %s", evicted[0])
	}
}

func TestLRURemove(t *testing.T) {
	l := NewLRU(1000)
	l.Touch("a", 100)
	l.Touch("b", 200)

	ok := l.Remove("a")
	if !ok {
		t.Fatal("expected remove to return true")
	}
	if l.Contains("a") {
		t.Fatal("a should be removed")
	}
	if l.Size() != 200 {
		t.Fatalf("expected size 200, got %d", l.Size())
	}

	ok = l.Remove("nonexistent")
	if ok {
		t.Fatal("expected remove of nonexistent to return false")
	}
}

func TestLRUItems(t *testing.T) {
	l := NewLRU(1000)
	l.Touch("c", 10)
	l.Touch("b", 10)
	l.Touch("a", 10)

	items := l.Items()
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[0] != "a" {
		t.Fatalf("expected most recent first, got %s", items[0])
	}
}

func TestLRUConcurrentAccess(t *testing.T) {
	l := NewLRU(100000)
	var wg sync.WaitGroup

	for i := range 100 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := string(rune('a' + n%26))
			l.Touch(key, 10)
			l.Contains(key)
		}(i)
	}
	wg.Wait()

	if l.Len() == 0 {
		t.Fatal("expected items after concurrent access")
	}
}

func TestLRUEvictEmpty(t *testing.T) {
	l := NewLRU(100)
	evicted := l.Evict(200)
	if len(evicted) != 0 {
		t.Fatalf("expected no evictions on empty LRU, got %d", len(evicted))
	}
}
