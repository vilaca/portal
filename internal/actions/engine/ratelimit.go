// Package engine implements the default api.ActionDispatcher together with
// its in-memory rate limiter and idempotency LRU cache.
//
// The rate limiter here is a per-key sliding window. Each key carries a
// (windowStart, count) pair: while now < windowStart+window the count
// increments; once the window has elapsed, the bucket resets. Allow returns
// false when the count would exceed the supplied budget.
//
// We deviate from the api.RateLimiter signature (which is Allow(key, window))
// because the budget — i.e. the "N" in rule specs like "5/min" — must travel
// with each call. Keeping the budget out of the key avoids per-rule state
// duplication when several rules share a target. The dispatcher therefore
// depends on the package-local Limiter interface defined below.
package engine

import (
	"sync"
	"time"
)

// Limiter is the rate-limit contract the dispatcher uses. Allow returns true
// if a request for key, evaluated within the rolling window of size window,
// is still under budget; otherwise it returns false and the dispatcher
// records portal_actions_total{result="ratelimited"}.
type Limiter interface {
	Allow(key string, window time.Duration, budget int) bool
}

type windowState struct {
	count       int
	windowStart time.Time
}

// SlidingLimiter is the default in-memory Limiter. It is safe for concurrent
// callers. The implementation deliberately resets each window in one go —
// "fixed window with reset" rather than a true rolling window — because the
// dispatcher's budgets are small ("5/min", "2/sec") and the simpler approach
// is enough for v1. A finer-grained algorithm can replace this without
// changing the Limiter contract.
type SlidingLimiter struct {
	mu  sync.Mutex
	now func() time.Time // injectable for tests
	m   map[string]*windowState
}

// NewLimiter constructs a SlidingLimiter with time.Now as the clock.
func NewLimiter() *SlidingLimiter {
	return &SlidingLimiter{
		now: time.Now,
		m:   make(map[string]*windowState),
	}
}

// Allow implements Limiter.
func (l *SlidingLimiter) Allow(key string, window time.Duration, budget int) bool {
	if budget <= 0 || window <= 0 {
		// A zero/negative budget or window means "do not limit"; pass through.
		return true
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.m[key]
	if !ok || now.Sub(st.windowStart) >= window {
		l.m[key] = &windowState{count: 1, windowStart: now}
		return true
	}
	if st.count >= budget {
		return false
	}
	st.count++
	return true
}
