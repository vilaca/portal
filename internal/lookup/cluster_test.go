package lookup

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// ---------------------------------------------------------------------------
// Stub AuditCache backed by an in-memory map. No real informer involved.
// ---------------------------------------------------------------------------

type fakeAuditCache struct {
	listers map[schema.GroupVersionKind]*fakeGenericLister
}

func (f *fakeAuditCache) Lister(gvk schema.GroupVersionKind) (cache.GenericLister, error) {
	l, ok := f.listers[gvk]
	if !ok {
		return nil, &notFoundErr{msg: "not watched: " + gvk.String()}
	}
	return l, nil
}

func (f *fakeAuditCache) SharedInformerFactory() dynamicinformer.DynamicSharedInformerFactory {
	return nil
}

func (f *fakeAuditCache) WatchedGVKs() []schema.GroupVersionKind {
	out := make([]schema.GroupVersionKind, 0, len(f.listers))
	for k := range f.listers {
		out = append(out, k)
	}
	return out
}

type notFoundErr struct{ msg string }

func (e *notFoundErr) Error() string { return e.msg + " not found" }

type fakeGenericLister struct {
	items map[string]map[string]*unstructured.Unstructured // ns -> name -> obj
}

func newFakeLister() *fakeGenericLister {
	return &fakeGenericLister{items: map[string]map[string]*unstructured.Unstructured{}}
}

func (l *fakeGenericLister) add(u *unstructured.Unstructured) {
	ns := u.GetNamespace()
	if l.items[ns] == nil {
		l.items[ns] = map[string]*unstructured.Unstructured{}
	}
	l.items[ns][u.GetName()] = u
}

func (l *fakeGenericLister) List(selector labels.Selector) ([]runtime.Object, error) {
	var out []runtime.Object
	for _, nsMap := range l.items {
		for _, obj := range nsMap {
			if selector.Matches(labels.Set(obj.GetLabels())) {
				out = append(out, obj)
			}
		}
	}
	return out, nil
}

func (l *fakeGenericLister) Get(name string) (runtime.Object, error) {
	for _, nsMap := range l.items {
		if obj, ok := nsMap[name]; ok {
			return obj, nil
		}
	}
	return nil, &notFoundErr{msg: name}
}

func (l *fakeGenericLister) ByNamespace(ns string) cache.GenericNamespaceLister {
	return &fakeNsLister{parent: l, ns: ns}
}

type fakeNsLister struct {
	parent *fakeGenericLister
	ns     string
}

func (l *fakeNsLister) List(selector labels.Selector) ([]runtime.Object, error) {
	var out []runtime.Object
	for _, obj := range l.parent.items[l.ns] {
		if selector.Matches(labels.Set(obj.GetLabels())) {
			out = append(out, obj)
		}
	}
	return out, nil
}

func (l *fakeNsLister) Get(name string) (runtime.Object, error) {
	if obj, ok := l.parent.items[l.ns][name]; ok {
		return obj, nil
	}
	return nil, &notFoundErr{msg: name}
}

func newPod(ns, name string, lbls map[string]string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Pod"})
	u.SetNamespace(ns)
	u.SetName(name)
	u.SetLabels(lbls)
	return u
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestClusterLookupByName(t *testing.T) {
	gvk := schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	fl := newFakeLister()
	fl.add(newPod("default", "p1", map[string]string{"app": "x"}))
	ac := &fakeAuditCache{listers: map[schema.GroupVersionKind]*fakeGenericLister{gvk: fl}}
	l := New(ac)

	u, err := l.ByName(gvk, "default", "p1")
	if err != nil {
		t.Fatalf("ByName: %v", err)
	}
	if u == nil || u.GetName() != "p1" {
		t.Fatalf("expected p1 back, got %v", u)
	}

	got, err := l.ByName(gvk, "default", "missing")
	if err != nil {
		t.Fatalf("ByName missing: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for missing, got %v", got)
	}
}

func TestClusterLookupList(t *testing.T) {
	gvk := schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	fl := newFakeLister()
	fl.add(newPod("default", "p1", map[string]string{"app": "x"}))
	fl.add(newPod("default", "p2", map[string]string{"app": "y"}))
	fl.add(newPod("kube-system", "p3", map[string]string{"app": "x"}))
	ac := &fakeAuditCache{listers: map[schema.GroupVersionKind]*fakeGenericLister{gvk: fl}}
	l := New(ac)

	out, err := l.List(gvk, "default", map[string]string{"app": "x"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out) != 1 || out[0].GetName() != "p1" {
		t.Fatalf("expected p1 only, got %d", len(out))
	}

	all, err := l.List(gvk, "", nil)
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3, got %d", len(all))
	}
}

func TestToExprEnv(t *testing.T) {
	gvk := schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	fl := newFakeLister()
	fl.add(newPod("default", "p1", map[string]string{"app": "x"}))
	ac := &fakeAuditCache{listers: map[schema.GroupVersionKind]*fakeGenericLister{gvk: fl}}
	l := New(ac)
	env := ToExprEnv(l)
	po, ok := env["pods.v1."].(map[string]any)
	if !ok {
		t.Fatalf("expected pods.v1. key, got %v", env)
	}
	byName := po["byName"].(func(args ...any) any)
	res := byName("default", "p1")
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("byName not map: %T", res)
	}
	if md := m["metadata"].(map[string]any); md["name"] != "p1" {
		t.Fatalf("expected p1, got %v", md["name"])
	}

	list := po["list"].(func(args ...any) any)
	listed := list("default", map[string]any{"app": "x"})
	items := listed.([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 list item, got %d", len(items))
	}
}

func TestExtractClusterRefs(t *testing.T) {
	cases := []struct {
		expr string
		want []schema.GroupVersionKind
	}{
		{
			expr: `cluster.pods.v1.byName("default", "x") != nil`,
			want: []schema.GroupVersionKind{{Version: "v1", Kind: "Pod"}},
		},
		{
			expr: `cluster.networkpolicies.v1.networking.k8s.io.list("ns", {}) != nil`,
			want: []schema.GroupVersionKind{{Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy"}},
		},
		{
			expr: `cluster.deployments.v1.apps.byName(ns, name) != nil && cluster.pods.v1.list("", {}) != nil`,
			want: []schema.GroupVersionKind{
				{Version: "v1", Kind: "Pod"},
				{Group: "apps", Version: "v1", Kind: "Deployment"},
			},
		},
		{
			expr: `consistentCluster.pods.v1.byName("ns","x") != nil`,
			want: []schema.GroupVersionKind{{Version: "v1", Kind: "Pod"}},
		},
	}
	for _, tc := range cases {
		got, err := ExtractClusterRefs(tc.expr)
		if err != nil {
			t.Fatalf("%q: %v", tc.expr, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: want %v, got %v", tc.expr, tc.want, got)
		}
	}
}

func TestExtractClusterRefsIgnoresUnrelated(t *testing.T) {
	got, err := ExtractClusterRefs(`object.metadata.name == "x"`)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 refs, got %v", got)
	}
}
