package api

import (
	"context"
	"time"
)

// Action is one response capability — label, annotate, evict, patch-NP,
// revoke-SA-token, alertmanager. Adding a new action type is one struct + one
// Register call.
type Action interface {
	Type() string
	Execute(ctx context.Context, v Violation, params map[string]any) error
	Idempotent() bool
	DefaultRateLimit() time.Duration
}

// ActionDispatcher routes Violations to enabled Actions. It is non-blocking,
// applies rate-limiting per (rule, target) tuple, and consults an
// IdempotencyStore so re-emissions within the action's idempotency window are
// dropped rather than re-executed.
type ActionDispatcher interface {
	Dispatch(ctx context.Context, v Violation)
	// Drain blocks until in-flight dispatches finish or ctx is cancelled.
	Drain(ctx context.Context) error
}

// RateLimiter limits dispatch frequency per opaque key. Returning false means
// "drop"; the dispatcher counts these into portal_actions_total{result="ratelimited"}.
type RateLimiter interface {
	Allow(key string, window time.Duration) bool
}

// IdempotencyStore remembers recently-dispatched (rule,gvk,ns,name,actionType)
// tuples. Default impl is an in-memory LRU; v2 may swap in a persistent store.
type IdempotencyStore interface {
	// Seen returns true if the key has been observed within ttl, otherwise
	// records it and returns false.
	Seen(key string, ttl time.Duration) bool
}
