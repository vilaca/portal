package engine

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimiter_BudgetEnforcement(t *testing.T) {
	l := NewLimiter()
	// Two events allowed inside the window, third blocked.
	if !l.Allow("k", time.Minute, 2) {
		t.Fatal("first should be allowed")
	}
	if !l.Allow("k", time.Minute, 2) {
		t.Fatal("second should be allowed")
	}
	if l.Allow("k", time.Minute, 2) {
		t.Fatal("third should be blocked")
	}
}

func TestLimiter_WindowExpiry(t *testing.T) {
	now := time.Unix(0, 0)
	l := &SlidingLimiter{now: func() time.Time { return now }, m: map[string]*windowState{}}
	if !l.Allow("k", 100*time.Millisecond, 1) {
		t.Fatal("first should be allowed")
	}
	if l.Allow("k", 100*time.Millisecond, 1) {
		t.Fatal("second within window should be blocked")
	}
	now = now.Add(101 * time.Millisecond)
	if !l.Allow("k", 100*time.Millisecond, 1) {
		t.Fatal("after window reset, should be allowed")
	}
}

func TestLimiter_KeysAreIndependent(t *testing.T) {
	l := NewLimiter()
	if !l.Allow("a", time.Minute, 1) {
		t.Fatal("a first should be allowed")
	}
	if !l.Allow("b", time.Minute, 1) {
		t.Fatal("b first should be allowed")
	}
	if l.Allow("a", time.Minute, 1) {
		t.Fatal("a second should be blocked")
	}
}

func TestLimiter_Concurrent(t *testing.T) {
	l := NewLimiter()
	const goroutines = 16
	const perGoroutine = 50
	budget := 100
	var allowed int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				if l.Allow("shared", time.Hour, budget) {
					atomic.AddInt64(&allowed, 1)
				}
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt64(&allowed); got != int64(budget) {
		t.Fatalf("expected exactly %d allowed under concurrent load, got %d", budget, got)
	}
}

func TestLimiter_ZeroBudgetOrWindowAlwaysAllows(t *testing.T) {
	l := NewLimiter()
	// Pathological inputs: rather than deadlock the dispatcher we pass
	// through.
	for i := 0; i < 100; i++ {
		if !l.Allow("k", 0, 5) {
			t.Fatal("zero window should pass through")
		}
		if !l.Allow("k", time.Second, 0) {
			t.Fatal("zero budget should pass through")
		}
	}
}
