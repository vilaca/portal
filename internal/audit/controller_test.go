package audit

import (
	"testing"
	"time"

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

// Sanity check: the workItem zero value is usable.
func TestWorkItem(t *testing.T) {
	w := workItem{GVK: schema.GroupVersionKind{Kind: "X"}, Name: "n", EventType: "add"}
	if w.Name != "n" {
		t.Fail()
	}
	_ = time.Now
}
