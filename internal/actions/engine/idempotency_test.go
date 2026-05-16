package engine

import (
	"container/list"
	"fmt"
	"testing"
	"time"
)

func TestLRU_FirstSeenIsFalse(t *testing.T) {
	c := NewLRU(0)
	if c.Seen("k1", time.Minute) {
		t.Fatal("first Seen should return false")
	}
	if !c.Seen("k1", time.Minute) {
		t.Fatal("second Seen within TTL should return true")
	}
}

func TestLRU_TTLExpiry(t *testing.T) {
	now := time.Unix(0, 0)
	c := &LRU{capacity: 100, now: func() time.Time { return now }, ll: list.New(), m: map[string]*list.Element{}}
	if c.Seen("k", 100*time.Millisecond) {
		t.Fatal("first should be false")
	}
	if !c.Seen("k", 100*time.Millisecond) {
		t.Fatal("second within TTL should be true")
	}
	now = now.Add(200 * time.Millisecond)
	if c.Seen("k", 100*time.Millisecond) {
		t.Fatal("after TTL expiry should be false again")
	}
}

func TestLRU_EvictionAtCapacity(t *testing.T) {
	c := NewLRU(3)
	keys := []string{"a", "b", "c", "d", "e"}
	for _, k := range keys {
		c.Seen(k, time.Hour)
	}
	if got := c.Len(); got != 3 {
		t.Fatalf("expected len=3 after inserting %d keys, got %d", len(keys), got)
	}
	// The oldest two ("a" and "b") should have been evicted; revisiting them
	// returns false because they were dropped, then re-inserted.
	if c.Seen("a", time.Hour) {
		t.Fatal("a should have been evicted")
	}
	if c.Seen("b", time.Hour) {
		t.Fatal("b should have been evicted")
	}
}

func TestLRU_NonPositiveTTLNeverRecords(t *testing.T) {
	c := NewLRU(8)
	for i := 0; i < 5; i++ {
		if c.Seen("k", 0) {
			t.Fatal("ttl<=0 should always return false")
		}
	}
	if c.Len() != 0 {
		t.Fatalf("expected empty cache, got len=%d", c.Len())
	}
}

func TestLRU_ManyKeysStable(t *testing.T) {
	c := NewLRU(50)
	for i := 0; i < 1000; i++ {
		c.Seen(fmt.Sprintf("k%d", i), time.Hour)
	}
	if got := c.Len(); got != 50 {
		t.Fatalf("expected capped at 50, got %d", got)
	}
}
