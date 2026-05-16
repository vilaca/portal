package network

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
)

// ----------------------------------------------------------------------------
// stub listers
// ----------------------------------------------------------------------------

type stubLister struct {
	items []*unstructured.Unstructured
}

func (s *stubLister) List(sel labels.Selector) ([]runtime.Object, error) {
	var out []runtime.Object
	for _, it := range s.items {
		if sel.Matches(labels.Set(it.GetLabels())) {
			out = append(out, it)
		}
	}
	return out, nil
}
func (s *stubLister) Get(name string) (runtime.Object, error) {
	for _, it := range s.items {
		if it.GetName() == name {
			return it, nil
		}
	}
	return nil, &notFound{name: name}
}
func (s *stubLister) ByNamespace(ns string) cache.GenericNamespaceLister {
	return &stubNs{parent: s, ns: ns}
}

type stubNs struct {
	parent *stubLister
	ns     string
}

func (s *stubNs) List(sel labels.Selector) ([]runtime.Object, error) {
	var out []runtime.Object
	for _, it := range s.parent.items {
		if it.GetNamespace() != s.ns {
			continue
		}
		if sel.Matches(labels.Set(it.GetLabels())) {
			out = append(out, it)
		}
	}
	return out, nil
}
func (s *stubNs) Get(name string) (runtime.Object, error) {
	for _, it := range s.parent.items {
		if it.GetNamespace() == s.ns && it.GetName() == name {
			return it, nil
		}
	}
	return nil, &notFound{name: name}
}

type notFound struct{ name string }

func (n *notFound) Error() string { return n.name + " not found" }

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

func mkPod(ns, name string, lbls map[string]string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Pod"})
	u.SetNamespace(ns)
	u.SetName(name)
	u.SetLabels(lbls)
	return u
}

func mkNP(ns, name string, spec map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(NPGVK)
	u.SetNamespace(ns)
	u.SetName(name)
	u.Object["spec"] = spec
	return u
}

func mkNamespace(name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(NSGVK)
	u.SetName(name)
	return u
}

// ----------------------------------------------------------------------------
// tests
// ----------------------------------------------------------------------------

func TestBuildModelBasic(t *testing.T) {
	pods := &stubLister{items: []*unstructured.Unstructured{
		mkPod("a", "p1", map[string]string{"app": "x"}),
		mkPod("a", "p2", map[string]string{"app": "y"}),
		mkPod("b", "p3", nil),
	}}
	nps := &stubLister{items: []*unstructured.Unstructured{
		mkNP("a", "deny", map[string]any{"podSelector": map[string]any{}}),
	}}
	nss := &stubLister{items: []*unstructured.Unstructured{
		mkNamespace("a"),
		mkNamespace("b"),
	}}
	m, err := BuildModel(pods, nps, nss, "")
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	if len(m.PodsByNamespace["a"]) != 2 {
		t.Errorf("expected 2 pods in a, got %d", len(m.PodsByNamespace["a"]))
	}
	if len(m.NetworkPoliciesByNamespace["a"]) != 1 {
		t.Errorf("expected 1 NP in a, got %d", len(m.NetworkPoliciesByNamespace["a"]))
	}
	if !m.DefaultDenyApplies("a") {
		t.Errorf("expected default-deny in a")
	}
	if m.DefaultDenyApplies("b") {
		t.Errorf("expected no default-deny in b")
	}
}

func TestModelPodsMatchingAndNPsForPod(t *testing.T) {
	pods := &stubLister{items: []*unstructured.Unstructured{
		mkPod("a", "p1", map[string]string{"app": "x"}),
		mkPod("a", "p2", map[string]string{"app": "y"}),
	}}
	nps := &stubLister{items: []*unstructured.Unstructured{
		mkNP("a", "match-x", map[string]any{
			"podSelector": map[string]any{
				"matchLabels": map[string]any{"app": "x"},
			},
		}),
	}}
	m, _ := BuildModel(pods, nps, nil, "")
	matches := m.PodsMatching("a", labels.SelectorFromSet(labels.Set{"app": "x"}))
	if len(matches) != 1 {
		t.Errorf("expected 1 pod match, got %d", len(matches))
	}
	got := m.NPsForPod("a", labels.Set{"app": "x"})
	if len(got) != 1 {
		t.Errorf("expected 1 NP for pod, got %d", len(got))
	}
}
