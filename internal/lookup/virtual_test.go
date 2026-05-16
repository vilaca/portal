package lookup

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestVirtualOverlayByName(t *testing.T) {
	gvk := schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	fl := newFakeLister()
	fl.add(newPod("default", "existing", map[string]string{"app": "old"}))
	ac := &fakeAuditCache{listers: map[schema.GroupVersionKind]*fakeGenericLister{gvk: fl}}
	base := New(ac)

	inbound := newPod("default", "inbound", map[string]string{"app": "new"})
	v := NewVirtual(base, inbound)

	// Inbound shadows.
	u, err := v.ByName(gvk, "default", "inbound")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if u == nil || u.GetLabels()["app"] != "new" {
		t.Fatalf("expected overlay, got %v", u)
	}

	// Fall-through.
	u, err = v.ByName(gvk, "default", "existing")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if u == nil || u.GetName() != "existing" {
		t.Fatalf("expected base passthrough, got %v", u)
	}
}

func TestVirtualOverlayList(t *testing.T) {
	gvk := schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	fl := newFakeLister()
	fl.add(newPod("default", "existing", map[string]string{"app": "x"}))
	ac := &fakeAuditCache{listers: map[schema.GroupVersionKind]*fakeGenericLister{gvk: fl}}
	base := New(ac)

	// CREATE: inbound not in base, included in list.
	inbound := newPod("default", "inbound", map[string]string{"app": "x"})
	v := NewVirtual(base, inbound)
	list, err := v.List(gvk, "default", map[string]string{"app": "x"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}

	// Selector miss for inbound.
	list, err = v.List(gvk, "default", map[string]string{"app": "y"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 (no match), got %d", len(list))
	}

	// Different namespace: fall-through only.
	list, err = v.List(gvk, "kube-system", nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0, got %d", len(list))
	}
}

func TestVirtualUpdateReplaces(t *testing.T) {
	gvk := schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	fl := newFakeLister()
	fl.add(newPod("default", "p", map[string]string{"app": "old"}))
	ac := &fakeAuditCache{listers: map[schema.GroupVersionKind]*fakeGenericLister{gvk: fl}}
	base := New(ac)

	inbound := newPod("default", "p", map[string]string{"app": "new"})
	v := NewVirtual(base, inbound)
	list, err := v.List(gvk, "default", nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1, got %d", len(list))
	}
	if list[0].GetLabels()["app"] != "new" {
		t.Fatalf("expected updated label, got %s", list[0].GetLabels()["app"])
	}
}

func TestVirtualNilInboundPassthrough(t *testing.T) {
	gvk := schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	fl := newFakeLister()
	fl.add(newPod("default", "p", nil))
	ac := &fakeAuditCache{listers: map[schema.GroupVersionKind]*fakeGenericLister{gvk: fl}}
	base := New(ac)
	v := NewVirtual(base, nil)
	if v == nil {
		t.Fatal("nil")
	}
	u, err := v.ByName(gvk, "default", "p")
	if err != nil || u == nil {
		t.Fatalf("expected passthrough hit, err=%v u=%v", err, u)
	}
}
