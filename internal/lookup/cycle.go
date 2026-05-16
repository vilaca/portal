package lookup

import (
	"sync"
	"time"

	"github.com/vilaca/portal/internal/api"
)

// CycleGuard implements per-(rule, object) sliding-window re-evaluation
// budget protection. Default 3 calls per 10s window.
//
// CycleGuard is pure logic: it does NOT increment Prometheus counters or log.
// Callers MUST bump portal_lookup_cycle_suppressed_total and emit an audit
// log line naming the rule + object when Allow returns false.
type CycleGuard struct {
	budget int
	window time.Duration
	states sync.Map // key -> *windowState
	now    func() time.Time
}

type windowState struct {
	mu          sync.Mutex
	timestamps  []time.Time
}

// DefaultCycleBudget is the default per-window budget.
const DefaultCycleBudget = 3

// DefaultCycleWindow is the default sliding-window length.
const DefaultCycleWindow = 10 * time.Second

// NewCycleGuard returns a guard with the given budget and window. Non-positive
// values fall back to the defaults.
func NewCycleGuard(budget int, window time.Duration) *CycleGuard {
	if budget <= 0 {
		budget = DefaultCycleBudget
	}
	if window <= 0 {
		window = DefaultCycleWindow
	}
	return &CycleGuard{
		budget: budget,
		window: window,
		now:    time.Now,
	}
}

// Allow records an attempt and returns true if it fits within budget. The
// caller is responsible for the side effects (metric/log) when false is
// returned.
func (g *CycleGuard) Allow(rule string, obj api.ObjectRef) bool {
	key := cycleKey(rule, obj)
	v, _ := g.states.LoadOrStore(key, &windowState{})
	st := v.(*windowState)

	st.mu.Lock()
	defer st.mu.Unlock()

	now := g.now()
	cutoff := now.Add(-g.window)
	// Drop expired timestamps from the head.
	keep := st.timestamps[:0]
	for _, t := range st.timestamps {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	st.timestamps = keep
	if len(st.timestamps) >= g.budget {
		return false
	}
	st.timestamps = append(st.timestamps, now)
	return true
}

func cycleKey(rule string, obj api.ObjectRef) string {
	return rule + "|" + obj.GVK.String() + "|" + obj.Namespace + "|" + obj.Name
}
