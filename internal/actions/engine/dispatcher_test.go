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

// scoperAction implements api.TargetScoper and lets a test pin the resolved
// target namespace/name independently of v.Namespace/v.Name. Mirrors what
// patch-networkpolicy does in production.
type scoperAction struct {
	stubAction
	targetNS, targetName string
}

func (s *scoperAction) TargetScope(_ api.Violation, _ map[string]any) (namespace, name string) {
	return s.targetNS, s.targetName
}

// TestDispatcher_Scope_PortalRule_RejectsCrossNamespace covers the
// dispatcher-level allow-list: a Violation produced by a PortalRule in
// namespace tenant-a must not drive an action against an object outside
// tenant-a, even if the matched object's namespace tries to escape.
func TestDispatcher_Scope_PortalRule_RejectsCrossNamespace(t *testing.T) {
	resetCounters(t)
	a := &stubAction{typ: "evict", rate: time.Second}
	d := New(map[string]api.Action{"evict": a}, NewLimiter(), NewLRU(0), Options{WorkerPoolSize: 2, QueueSize: 8})

	v := sampleViolation()
	v.Namespace = "kube-system" // attacker tried to point at kube-system
	v.RuleSource = api.RuleSource{Origin: "PortalRule", Namespace: "tenant-a"}
	v.Actions = []api.ActionSpec{{Type: "evict"}}
	d.Dispatch(context.Background(), v)

	if err := d.Drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got := a.Calls(); got != 0 {
		t.Fatalf("expected scope check to refuse action, got %d calls", got)
	}
	if got := counterValue(t, "evict", resultOutOfScope); got != 1 {
		t.Fatalf("expected 1 out_of_scope, got %v", got)
	}
}

// TestDispatcher_Scope_PortalRule_AllowsSameNamespace confirms that the
// scope check is only a gate, not a deny-all — when the target namespace
// matches the rule's CR namespace, the action runs.
func TestDispatcher_Scope_PortalRule_AllowsSameNamespace(t *testing.T) {
	resetCounters(t)
	a := &stubAction{typ: "label", rate: time.Second}
	d := New(map[string]api.Action{"label": a}, NewLimiter(), NewLRU(0), Options{WorkerPoolSize: 2, QueueSize: 8})

	v := sampleViolation()
	v.Namespace = "tenant-a"
	v.RuleSource = api.RuleSource{Origin: "PortalRule", Namespace: "tenant-a"}
	v.Actions = []api.ActionSpec{{Type: "label"}}
	d.Dispatch(context.Background(), v)

	if err := d.Drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got := a.Calls(); got != 1 {
		t.Fatalf("expected action to run for in-scope target, got %d calls", got)
	}
}

// TestDispatcher_Scope_PortalClusterRule_AlwaysAllowed verifies the
// operator-trusted origins bypass the scope check entirely.
func TestDispatcher_Scope_PortalClusterRule_AlwaysAllowed(t *testing.T) {
	resetCounters(t)
	a := &stubAction{typ: "evict", rate: time.Second}
	d := New(map[string]api.Action{"evict": a}, NewLimiter(), NewLRU(0), Options{WorkerPoolSize: 2, QueueSize: 8})

	v := sampleViolation()
	v.Namespace = "kube-system" // legitimate target for a cluster rule
	v.RuleSource = api.RuleSource{Origin: "PortalClusterRule"}
	v.Actions = []api.ActionSpec{{Type: "evict"}}
	d.Dispatch(context.Background(), v)

	if err := d.Drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got := a.Calls(); got != 1 {
		t.Fatalf("expected cluster-rule action to execute, got %d calls", got)
	}
}

// TestDispatcher_Scope_PortalRule_UsesTargetScoper ensures the dispatcher
// consults api.TargetScoper when an action's effective target diverges
// from the matched object — the precise primitive that lets patchnp's
// params.targetNamespace be checked against the rule's CR namespace.
func TestDispatcher_Scope_PortalRule_UsesTargetScoper(t *testing.T) {
	resetCounters(t)
	a := &scoperAction{
		stubAction: stubAction{typ: "patch-networkpolicy", rate: time.Second},
		targetNS:   "kube-system",
		targetName: "default-deny",
	}
	d := New(map[string]api.Action{"patch-networkpolicy": a}, NewLimiter(), NewLRU(0), Options{WorkerPoolSize: 2, QueueSize: 8})

	v := sampleViolation()
	v.Namespace = "tenant-a" // matched object inside scope...
	v.RuleSource = api.RuleSource{Origin: "PortalRule", Namespace: "tenant-a"}
	v.Actions = []api.ActionSpec{{Type: "patch-networkpolicy"}} // ...but the scoper says kube-system, which is out
	d.Dispatch(context.Background(), v)

	if err := d.Drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got := a.Calls(); got != 0 {
		t.Fatalf("expected TargetScoper-derived target to fail scope check, got %d calls", got)
	}
	if got := counterValue(t, "patch-networkpolicy", resultOutOfScope); got != 1 {
		t.Fatalf("expected 1 out_of_scope, got %v", got)
	}
}

// TestDispatcher_Scope_PortalRule_RejectsEmptyOriginNamespace catches a
// malformed RuleSource (PortalRule origin without a namespace) — the
// conversion path makes this unreachable, but the dispatcher belt-and-
// suspenders refuses the action.
func TestDispatcher_Scope_PortalRule_RejectsEmptyOriginNamespace(t *testing.T) {
	resetCounters(t)
	a := &stubAction{typ: "evict", rate: time.Second}
	d := New(map[string]api.Action{"evict": a}, NewLimiter(), NewLRU(0), Options{WorkerPoolSize: 2, QueueSize: 8})

	v := sampleViolation()
	v.Namespace = "anything"
	v.RuleSource = api.RuleSource{Origin: "PortalRule", Namespace: ""} // malformed
	v.Actions = []api.ActionSpec{{Type: "evict"}}
	d.Dispatch(context.Background(), v)

	if err := d.Drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got := a.Calls(); got != 0 {
		t.Fatalf("malformed PortalRule origin must not execute, got %d calls", got)
	}
}

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
