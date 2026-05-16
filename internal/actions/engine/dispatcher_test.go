package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vilaca/portal/internal/api"
	prom "github.com/vilaca/portal/internal/sink/prometheus"
)

// stubAction is a recording api.Action used across the dispatcher tests.
type stubAction struct {
	mu       sync.Mutex
	typ      string
	rate     time.Duration
	calls    int
	delay    time.Duration
	err      error
	lastPars map[string]any
}

func (s *stubAction) Type() string                    { return s.typ }
func (s *stubAction) Idempotent() bool                { return true }
func (s *stubAction) DefaultRateLimit() time.Duration { return s.rate }
func (s *stubAction) Execute(_ context.Context, _ api.Violation, p map[string]any) error {
	s.mu.Lock()
	s.calls++
	s.lastPars = p
	d := s.delay
	err := s.err
	s.mu.Unlock()
	if d > 0 {
		time.Sleep(d)
	}
	return err
}
func (s *stubAction) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func sampleViolation() api.Violation {
	return api.Violation{
		Rule:      "r1",
		GVK:       schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
		Namespace: "default",
		Name:      "p1",
		Mode:      api.ModeAudit,
		At:        time.Now(),
	}
}

// counterValue reads portal_actions_total{action,result}.
func counterValue(t *testing.T, action, result string) float64 {
	t.Helper()
	c, err := prom.ActionsTotal.GetMetricWithLabelValues(action, result)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues: %v", err)
	}
	return testutil.ToFloat64(c)
}

// resetCounters clears the portal_actions_total counter vector so each test
// starts from a known baseline. promauto registered the metric on the
// default registry — Reset is safe to call between tests in the same
// process.
func resetCounters(t *testing.T) {
	t.Helper()
	prom.ActionsTotal.Reset()
}

func TestDispatcher_ModeFilter(t *testing.T) {
	resetCounters(t)
	a := &stubAction{typ: "admission-only", rate: time.Second}
	d := New(map[string]api.Action{"admission-only": a}, NewLimiter(), NewLRU(0), Options{WorkerPoolSize: 2, QueueSize: 8})

	v := sampleViolation() // audit mode
	v.Actions = []api.ActionSpec{{Type: "admission-only", On: []api.Mode{api.ModeAdmission}}}
	d.Dispatch(context.Background(), v)

	if err := d.Drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got := a.Calls(); got != 0 {
		t.Fatalf("expected mode filter to skip, got %d calls", got)
	}
}

func TestDispatcher_Idempotency(t *testing.T) {
	resetCounters(t)
	a := &stubAction{typ: "label", rate: 10 * time.Second}
	d := New(map[string]api.Action{"label": a}, NewLimiter(), NewLRU(0), Options{WorkerPoolSize: 2, QueueSize: 8})

	v := sampleViolation()
	v.Actions = []api.ActionSpec{{Type: "label"}}
	d.Dispatch(context.Background(), v)
	d.Dispatch(context.Background(), v)
	d.Dispatch(context.Background(), v)
	if err := d.Drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got := a.Calls(); got != 1 {
		t.Fatalf("expected exactly 1 execute under idempotency, got %d", got)
	}
	if v := counterValue(t, "label", resultDuplicate); v < 2 {
		t.Fatalf("expected >= 2 duplicate counter, got %v", v)
	}
}

func TestDispatcher_RateLimit(t *testing.T) {
	resetCounters(t)
	// DefaultRateLimit() is 0 so idempotency Seen always returns false; the
	// only gate is the per-spec rate limit.
	a := &stubAction{typ: "evict", rate: 0}
	d := New(map[string]api.Action{"evict": a}, NewLimiter(), NewLRU(0), Options{WorkerPoolSize: 1, QueueSize: 8})

	v := sampleViolation()
	v.Actions = []api.ActionSpec{{Type: "evict", RateLimit: "2/sec"}}
	d.Dispatch(context.Background(), v)
	d.Dispatch(context.Background(), v)
	d.Dispatch(context.Background(), v)
	if err := d.Drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got := a.Calls(); got != 2 {
		t.Fatalf("expected 2 executes with budget=2, got %d", got)
	}
	if v := counterValue(t, "evict", resultRateLimited); v != 1 {
		t.Fatalf("expected 1 ratelimited counter, got %v", v)
	}
}

func TestDispatcher_Drain_WaitsForInflight(t *testing.T) {
	resetCounters(t)
	a := &stubAction{typ: "slow", rate: 0, delay: 60 * time.Millisecond}
	d := New(map[string]api.Action{"slow": a}, NewLimiter(), NewLRU(0), Options{WorkerPoolSize: 1, QueueSize: 8})

	v := sampleViolation()
	v.Actions = []api.ActionSpec{{Type: "slow"}}
	d.Dispatch(context.Background(), v)
	if err := d.Drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got := a.Calls(); got != 1 {
		t.Fatalf("Drain returned before in-flight finished: calls=%d", got)
	}
}

func TestDispatcher_CountersOKErrorUnknown(t *testing.T) {
	resetCounters(t)
	ok := &stubAction{typ: "ok", rate: 0}
	bad := &stubAction{typ: "bad", rate: 0, err: errors.New("boom")}
	d := New(map[string]api.Action{"ok": ok, "bad": bad}, NewLimiter(), NewLRU(0), Options{WorkerPoolSize: 2, QueueSize: 8})

	v := sampleViolation()
	v.Actions = []api.ActionSpec{{Type: "ok"}, {Type: "bad"}, {Type: "ghost"}}
	d.Dispatch(context.Background(), v)
	if err := d.Drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if v := counterValue(t, "ok", resultOK); v != 1 {
		t.Fatalf("ok counter want 1 got %v", v)
	}
	if v := counterValue(t, "bad", resultError); v != 1 {
		t.Fatalf("error counter want 1 got %v", v)
	}
	if v := counterValue(t, "ghost", resultUnknown); v != 1 {
		t.Fatalf("unknown counter want 1 got %v", v)
	}
}

func TestDispatcher_DropsWhenQueueFull(t *testing.T) {
	resetCounters(t)
	// Block the single worker by gating the action; with QueueSize=1, after
	// one in-flight and one queued, further Dispatches drop.
	gate := make(chan struct{})
	defer close(gate)
	var executed int32
	action := actionFunc(func(_ context.Context, _ api.Violation, _ map[string]any) error {
		atomic.AddInt32(&executed, 1)
		<-gate
		return nil
	})
	d := New(map[string]api.Action{"x": action}, NewLimiter(), NewLRU(0), Options{WorkerPoolSize: 1, QueueSize: 1})

	v := sampleViolation()
	v.Actions = []api.ActionSpec{{Type: "x"}}
	d.Dispatch(context.Background(), v)
	time.Sleep(10 * time.Millisecond) // let worker grab the first item
	d.Dispatch(context.Background(), v)
	d.Dispatch(context.Background(), v)
	if v := counterValue(t, "*", resultDropped); v < 1 {
		t.Fatalf("expected at least 1 drop, got %v", v)
	}
}

// actionFunc is a tiny adapter so tests can inline Execute behaviour.
type actionFunc func(ctx context.Context, v api.Violation, p map[string]any) error

func (f actionFunc) Type() string                                                          { return "x" }
func (f actionFunc) Idempotent() bool                                                      { return false }
func (f actionFunc) DefaultRateLimit() time.Duration                                       { return 0 }
func (f actionFunc) Execute(ctx context.Context, v api.Violation, p map[string]any) error { return f(ctx, v, p) }

func TestParseRateLimit(t *testing.T) {
	cases := []struct {
		in     string
		ok     bool
		budget int
		window time.Duration
	}{
		{"", false, 0, 0},
		{"5/min", true, 5, time.Minute},
		{"2/sec", true, 2, time.Second},
		{"10/h", true, 10, time.Hour},
		{"garbage", false, 0, 0},
		{"0/min", false, 0, 0},
		{"5/day", false, 0, 0},
	}
	for _, tc := range cases {
		w, b, ok := parseRateLimit(tc.in)
		if ok != tc.ok || w != tc.window || b != tc.budget {
			t.Fatalf("parseRateLimit(%q) = (%v,%d,%v); want (%v,%d,%v)", tc.in, w, b, ok, tc.window, tc.budget, tc.ok)
		}
	}
}
