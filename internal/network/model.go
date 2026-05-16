package network

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
)

// GVKs the analyser cares about. Exposed for wire-up code so it can register
// matching informers.
var (
	PodGVK = schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	NPGVK  = schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy"}
	NSGVK  = schema.GroupVersionKind{Version: "v1", Kind: "Namespace"}
)

// Model is the pod→NP graph used by the built-in checks. It is read-only after
// BuildModel returns; callers rebuild it on every relevant informer event.
type Model struct {
	// PodsByNamespace[ns] is the live pod set in that namespace.
	PodsByNamespace map[string][]*unstructured.Unstructured
	// NetworkPoliciesByNamespace[ns] is the live NP set in that namespace.
	NetworkPoliciesByNamespace map[string][]*unstructured.Unstructured
	// Selectors[NP-key] is the parsed labels.Selector for the NP's podSelector.
	// NP-key is "<namespace>/<name>".
	Selectors map[string]labels.Selector
	// Namespaces is the live namespace list (may be empty when no Namespace
	// lister is provided; np.default-deny-missing falls back to whatever
	// namespaces appear in PodsByNamespace).
	Namespaces []string
}

// BuildModel walks the supplied listers and returns a snapshot Model. When
// namespace is "" the model includes the entire cluster; otherwise the build
// is scoped to that namespace (other namespaces remain empty).
//
// nsLister may be nil; in that case Namespaces is derived from PodsByNamespace.
func BuildModel(podLister, npLister, nsLister cache.GenericLister, namespace string) (*Model, error) {
	m := &Model{
		PodsByNamespace:            map[string][]*unstructured.Unstructured{},
		NetworkPoliciesByNamespace: map[string][]*unstructured.Unstructured{},
		Selectors:                  map[string]labels.Selector{},
	}

	addPod := func(o runtime.Object) {
		u, ok := o.(*unstructured.Unstructured)
		if !ok {
			return
		}
		ns := u.GetNamespace()
		m.PodsByNamespace[ns] = append(m.PodsByNamespace[ns], u)
	}
	addNP := func(o runtime.Object) {
		u, ok := o.(*unstructured.Unstructured)
		if !ok {
			return
		}
		ns := u.GetNamespace()
		m.NetworkPoliciesByNamespace[ns] = append(m.NetworkPoliciesByNamespace[ns], u)
		sel := parsePodSelector(u)
		m.Selectors[ns+"/"+u.GetName()] = sel
	}

	if podLister != nil {
		if namespace == "" {
			items, err := podLister.List(labels.Everything())
			if err != nil {
				return nil, err
			}
			for _, o := range items {
				addPod(o)
			}
		} else {
			items, err := podLister.ByNamespace(namespace).List(labels.Everything())
			if err != nil {
				return nil, err
			}
			for _, o := range items {
				addPod(o)
			}
		}
	}
	if npLister != nil {
		if namespace == "" {
			items, err := npLister.List(labels.Everything())
			if err != nil {
				return nil, err
			}
			for _, o := range items {
				addNP(o)
			}
		} else {
			items, err := npLister.ByNamespace(namespace).List(labels.Everything())
			if err != nil {
				return nil, err
			}
			for _, o := range items {
				addNP(o)
			}
		}
	}
	if nsLister != nil {
		items, err := nsLister.List(labels.Everything())
		if err == nil {
			for _, o := range items {
				if u, ok := o.(*unstructured.Unstructured); ok {
					if namespace == "" || u.GetName() == namespace {
						m.Namespaces = append(m.Namespaces, u.GetName())
					}
				}
			}
		}
	}
	if len(m.Namespaces) == 0 {
		// Fallback: derive from observed pod namespaces.
		seen := map[string]struct{}{}
		for ns := range m.PodsByNamespace {
			seen[ns] = struct{}{}
		}
		for ns := range m.NetworkPoliciesByNamespace {
			seen[ns] = struct{}{}
		}
		for ns := range seen {
			if namespace == "" || ns == namespace {
				m.Namespaces = append(m.Namespaces, ns)
			}
		}
	}
	return m, nil
}

// PodsMatching returns the subset of pods in ns whose labels match sel.
func (m *Model) PodsMatching(ns string, sel labels.Selector) []*unstructured.Unstructured {
	if sel == nil {
		sel = labels.Everything()
	}
	pods := m.PodsByNamespace[ns]
	out := make([]*unstructured.Unstructured, 0, len(pods))
	for _, p := range pods {
		if sel.Matches(labels.Set(p.GetLabels())) {
			out = append(out, p)
		}
	}
	return out
}

// NPsForPod returns every NetworkPolicy in ns whose podSelector matches the
// given pod labels.
func (m *Model) NPsForPod(ns string, podLabels labels.Set) []*unstructured.Unstructured {
	out := []*unstructured.Unstructured{}
	for _, np := range m.NetworkPoliciesByNamespace[ns] {
		sel, ok := m.Selectors[ns+"/"+np.GetName()]
		if !ok {
			continue
		}
		if sel.Matches(podLabels) {
			out = append(out, np)
		}
	}
	return out
}

// DefaultDenyApplies returns true when ns has at least one NetworkPolicy that
// applies the "default deny" pattern (empty podSelector AND empty ingress).
// We treat the absence of an ingress key OR an empty rules list both as
// "empty ingress".
func (m *Model) DefaultDenyApplies(ns string) bool {
	for _, np := range m.NetworkPoliciesByNamespace[ns] {
		spec, _, _ := unstructuredMap(np.Object, "spec")
		if spec == nil {
			continue
		}
		ps, _ := spec["podSelector"].(map[string]any)
		// "empty podSelector" means the selector matches everything.
		if !isEmptySelector(ps) {
			continue
		}
		ing, hasIng := spec["ingress"]
		if hasIng {
			if list, ok := ing.([]any); ok && len(list) > 0 {
				continue
			}
		}
		return true
	}
	return false
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

func parsePodSelector(np *unstructured.Unstructured) labels.Selector {
	spec, _, _ := unstructuredMap(np.Object, "spec")
	if spec == nil {
		return labels.Everything()
	}
	ps, _ := spec["podSelector"].(map[string]any)
	if isEmptySelector(ps) {
		return labels.Everything()
	}
	if ml, ok := ps["matchLabels"].(map[string]any); ok {
		set := labels.Set{}
		for k, v := range ml {
			if s, ok := v.(string); ok {
				set[k] = s
			}
		}
		return labels.SelectorFromSet(set)
	}
	return labels.Everything()
}

func isEmptySelector(s map[string]any) bool {
	if len(s) == 0 {
		return true
	}
	ml, _ := s["matchLabels"].(map[string]any)
	me, _ := s["matchExpressions"].([]any)
	return len(ml) == 0 && len(me) == 0
}

func unstructuredMap(in map[string]any, key string) (map[string]any, bool, error) {
	if in == nil {
		return nil, false, nil
	}
	v, ok := in[key]
	if !ok {
		return nil, false, nil
	}
	m, ok := v.(map[string]any)
	return m, ok, nil
}
