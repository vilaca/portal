package audit

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"

	"github.com/vilaca/portal/internal/api"
)

type stubEngine struct{}

func (stubEngine) Evaluate(_ api.Context, _ api.EventMeta) []api.Violation { return nil }

func TestNew_DefaultsApplied(t *testing.T) {
	cfg := &rest.Config{Host: "http://localhost:1"}
	gvks := []schema.GroupVersionKind{{Version: "v1", Kind: "Pod"}}
	src, err := New(cfg, gvks, stubEngine{}, nil, nil, Options{LeaderElection: false})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c := src.(*Controller)
	if c.opts.ResyncPeriod != DefaultResyncPeriod {
		t.Errorf("ResyncPeriod=%v want %v", c.opts.ResyncPeriod, DefaultResyncPeriod)
	}
	if c.opts.WorkerPoolSize != DefaultWorkerPoolSize {
		t.Errorf("WorkerPoolSize=%d want %d", c.opts.WorkerPoolSize, DefaultWorkerPoolSize)
	}
	if c.opts.LeaseLockName != DefaultLeaseLockName {
		t.Errorf("LeaseLockName=%q want %q", c.opts.LeaseLockName, DefaultLeaseLockName)
	}
	if c.opts.Identity == "" {
		t.Errorf("Identity should be set")
	}
	if c.SharedInformerFactory() == nil {
		t.Errorf("SharedInformerFactory is nil")
	}
	if _, err := c.Lister(gvks[0]); err != nil {
		t.Errorf("Lister(%s): %v", gvks[0], err)
	}
	if _, err := c.Lister(schema.GroupVersionKind{Kind: "Other"}); err == nil {
		t.Errorf("expected error for unwatched GVK")
	}
}

func TestNew_RejectsBadInputs(t *testing.T) {
	cfg := &rest.Config{Host: "http://localhost:1"}
	if _, err := New(nil, []schema.GroupVersionKind{{Version: "v1", Kind: "Pod"}}, stubEngine{}, nil, nil, Options{}); err == nil {
		t.Errorf("expected error for nil cfg")
	}
	if _, err := New(cfg, nil, stubEngine{}, nil, nil, Options{}); err == nil {
		t.Errorf("expected error for empty GVKs")
	}
	if _, err := New(cfg, []schema.GroupVersionKind{{Version: "v1", Kind: "Pod"}}, nil, nil, nil, Options{}); err == nil {
		t.Errorf("expected error for nil engine")
	}
}

func TestDefaultResourceForGVK(t *testing.T) {
	cases := []struct {
		gvk schema.GroupVersionKind
		res string
	}{
		{schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, "pods"},
		{schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}, "deployments"},
		{schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy"}, "networkpolicys"},
	}
	for _, c := range cases {
		got := defaultResourceForGVK(c.gvk)
		if got.Resource != c.res {
			t.Errorf("defaultResourceForGVK(%v).Resource=%q want %q", c.gvk, got.Resource, c.res)
		}
	}
}

func TestIsLeader_NonElectedIsTrueAfterStart(t *testing.T) {
	// We can't actually start informers against a fake server here, but
	// we can verify the initial state and the LE-disabled fast-path
	// behaviour on the atomic.
	cfg := &rest.Config{Host: "http://localhost:1"}
	src, err := New(cfg, []schema.GroupVersionKind{{Version: "v1", Kind: "Pod"}}, stubEngine{}, nil, nil, Options{LeaderElection: false})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c := src.(*Controller)
	if c.IsLeader() {
		t.Errorf("IsLeader=true before Start; want false")
	}
	// Simulate the bit Start would flip in LE-disabled mode.
	c.isLeader.Store(true)
	if !c.IsLeader() {
		t.Errorf("IsLeader=false after Store; want true")
	}
}

func TestMapperBackedResolver(t *testing.T) {
	// Naive defaultResourceForGVK gives "networkpolicys"; a real mapper
	// hands back "networkpolicies". This test pins that the mapper-backed
	// resolver wins when both are wired.
	m := meta.NewDefaultRESTMapper([]schema.GroupVersion{
		{Group: "networking.k8s.io", Version: "v1"},
	})
	m.Add(schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy"}, meta.RESTScopeNamespace)

	res := mapperBackedResolver(m)(schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy"})
	if res.Resource != "networkpolicies" {
		t.Fatalf("mapper-backed resolver returned %q; want %q", res.Resource, "networkpolicies")
	}

	// Unknown Kind: should fall through to defaultResourceForGVK rather than error.
	unknown := mapperBackedResolver(m)(schema.GroupVersionKind{Group: "x", Version: "v1", Kind: "Mystery"})
	if unknown.Resource != "mysterys" { // naive fallback's wrong-but-deterministic plural
		t.Fatalf("expected fallback to defaultResourceForGVK; got %q", unknown.Resource)
	}
}

func TestRESTMapperAccessor(t *testing.T) {
	cfg := &rest.Config{Host: "http://localhost:1"}
	m := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Version: "v1"}})
	src, err := New(cfg, []schema.GroupVersionKind{{Version: "v1", Kind: "Pod"}}, stubEngine{}, nil, nil, Options{RESTMapper: m})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if src.(*Controller).RESTMapper() != m {
		t.Fatal("RESTMapper() accessor did not return the configured mapper")
	}
}

type countingSink struct{ count atomic.Int64 }

func (s *countingSink) Name() string                                 { return "counting" }
func (s *countingSink) Emit(_ context.Context, _ api.Violation) error { s.count.Add(1); return nil }
func (s *countingSink) Close() error                                  { return nil }

type countingDispatcher struct {
	count atomic.Int64
	last  api.Violation
}

func (d *countingDispatcher) Dispatch(_ context.Context, v api.Violation) {
	d.last = v
	d.count.Add(1)
}
func (d *countingDispatcher) Drain(_ context.Context) error { return nil }

// TestEmitGCViolation_BypassesSinks is the regression for the bug where
// synthetic delete-GC violations were flowing through fanOut, polluting
// every sink (stdout, prometheus, alertmanager, and worst — the policyreport
// sink, which would add a Result that the GC action is meant to remove).
// Post-fix: GC goes to the dispatcher only.
func TestEmitGCViolation_BypassesSinks(t *testing.T) {
	sink := &countingSink{}
	disp := &countingDispatcher{}
	c := &Controller{
		sinks:      []api.OutputSink{sink},
		dispatcher: disp,
	}
	c.emitGCViolation(context.Background(),
		workItem{GVK: schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, Namespace: "ns", Name: "p1", EventType: "delete"},
		api.EventMeta{Source: "audit", EventID: "evt-1", At: time.Now()},
	)
	if got := sink.count.Load(); got != 0 {
		t.Errorf("sink emitted %d times; want 0 — GC should not reach sinks", got)
	}
	if got := disp.count.Load(); got != 1 {
		t.Errorf("dispatcher dispatched %d times; want 1", got)
	}
	if disp.last.Rule != "__audit_object_deleted__" {
		t.Errorf("dispatched violation Rule=%q; want __audit_object_deleted__", disp.last.Rule)
	}
	if len(disp.last.Actions) != 1 || disp.last.Actions[0].Type != policyReportGCActionType {
		t.Errorf("dispatched violation actions=%v; want one policyreport-gc", disp.last.Actions)
	}
}

func TestEmitGCViolation_NilDispatcherIsNoop(t *testing.T) {
	sink := &countingSink{}
	c := &Controller{sinks: []api.OutputSink{sink}, dispatcher: nil}
	c.emitGCViolation(context.Background(),
		workItem{GVK: schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, Name: "p"},
		api.EventMeta{},
	)
	if got := sink.count.Load(); got != 0 {
		t.Errorf("sink emitted with nil dispatcher; want 0")
	}
}

// Sanity check: the workItem zero value is usable.
func TestWorkItem(t *testing.T) {
	w := workItem{GVK: schema.GroupVersionKind{Kind: "X"}, Name: "n", EventType: "add"}
	if w.Name != "n" {
		t.Fail()
	}
	_ = time.Now
}
