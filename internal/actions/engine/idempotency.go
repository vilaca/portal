package engine

import (
	"container/list"
	"sync"
	"time"
)

// LRU is the default api.IdempotencyStore: a bounded LRU with per-entry TTL.
// Capacity defaults to 100_000 (the figure cited in PLAN.md Phase 5);
// evictions happen when the map exceeds capacity, in least-recently-used
// order via the embedded doubly-linked list.
//
// TTL is recorded per-call at Seen-time rather than at insert-time so the
// dispatcher can pass different windows for different actions without
// requiring the cache to know the action type.
type LRU struct {
	mu       sync.Mutex
	capacity int
	now      func() time.Time
	ll       *list.List
	m        map[string]*list.Element
}

type lruEntry struct {
	key       string
	expiresAt time.Time
}

// NewLRU constructs an LRU with the given capacity. Capacity <= 0 falls back
// to the default of 100_000.
func NewLRU(capacity int) *LRU {
	if capacity <= 0 {
		capacity = 100_000
	}
	return &LRU{
		capacity: capacity,
		now:      time.Now,
		ll:       list.New(),
		m:        make(map[string]*list.Element),
	}
}

// Seen implements api.IdempotencyStore. Returns true if the key is present
// and not yet expired; otherwise records it with expiry now+ttl and returns
// false. A non-positive ttl is treated as "never remember" — the call always
// returns false and nothing is recorded.
func (c *LRU) Seen(key string, ttl time.Duration) bool {
	if ttl <= 0 {
		return false
	}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[key]; ok {
		ent := e.Value.(*lruEntry)
		if now.Before(ent.expiresAt) {
			// Refresh recency without extending the TTL — TTL semantics are
			// "since first sighting" so the entry can age out even under
			// continuous traffic.
			c.ll.MoveToFront(e)
			return true
		}
		// Expired: drop it and fall through to "record fresh".
		c.ll.Remove(e)
		delete(c.m, key)
	}
	ent := &lruEntry{key: key, expiresAt: now.Add(ttl)}
	e := c.ll.PushFront(ent)
	c.m[key] = e
	// Evict oldest while over capacity.
	for c.ll.Len() > c.capacity {
		back := c.ll.Back()
		if back == nil {
			break
		}
		c.ll.Remove(back)
		delete(c.m, back.Value.(*lruEntry).key)
	}
	return false
}

// Len returns the current cache size — exposed for tests.
func (c *LRU) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
