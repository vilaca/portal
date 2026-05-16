package lookup

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vilaca/portal/internal/api"
)

func TestCycleGuardAllowDeny(t *testing.T) {
	g := NewCycleGuard(3, 10*time.Second)
	// inject fake clock
	now := time.Unix(0, 0)
	g.now = func() time.Time { return now }

	obj := api.ObjectRef{GVK: schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, Namespace: "ns", Name: "x"}

	for i := 0; i < 3; i++ {
		if !g.Allow("rule", obj) {
			t.Fatalf("expected Allow at %d", i)
		}
	}
	if g.Allow("rule", obj) {
		t.Fatalf("expected 4th to be denied")
	}
}

func TestCycleGuardWindowExpires(t *testing.T) {
	g := NewCycleGuard(2, 100*time.Millisecond)
	now := time.Unix(0, 0)
	g.now = func() time.Time { return now }

	obj := api.ObjectRef{GVK: schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, Name: "x"}
	if !g.Allow("r", obj) || !g.Allow("r", obj) {
		t.Fatalf("expected first two allowed")
	}
	if g.Allow("r", obj) {
		t.Fatalf("expected third denied within window")
	}
	// Advance past window
	now = now.Add(200 * time.Millisecond)
	if !g.Allow("r", obj) {
		t.Fatalf("expected allow after window")
	}
}

func TestCycleGuardPerRuleObjectKeyIsolated(t *testing.T) {
	g := NewCycleGuard(1, time.Second)
	now := time.Unix(0, 0)
	g.now = func() time.Time { return now }
	objA := api.ObjectRef{GVK: schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, Name: "a"}
	objB := api.ObjectRef{GVK: schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, Name: "b"}
	if !g.Allow("R", objA) {
		t.Fatal("A1 should pass")
	}
	if g.Allow("R", objA) {
		t.Fatal("A2 should be denied")
	}
	if !g.Allow("R", objB) {
		t.Fatal("B1 should pass (different object)")
	}
	if !g.Allow("S", objA) {
		t.Fatal("S/A should pass (different rule)")
	}
}

func TestCycleGuardDefaults(t *testing.T) {
	g := NewCycleGuard(0, 0)
	if g.budget != DefaultCycleBudget || g.window != DefaultCycleWindow {
		t.Fatalf("defaults not applied: %+v", g)
	}
}
