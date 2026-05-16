package lookup

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vilaca/portal/internal/api"
)

// virtualLookup wraps an underlying api.Lookup with a per-request overlay
// (typically the inbound object at admission). Reads consult the overlay
// first; on miss they fall through to the base.
type virtualLookup struct {
	base    api.Lookup
	inbound *unstructured.Unstructured
}

// NewVirtual returns a Lookup that materialises inbound into the read path
// before delegating to base. inbound may be nil — then NewVirtual returns the
// base unchanged (no overlay).
func NewVirtual(base api.Lookup, inbound *unstructured.Unstructured) api.Lookup {
	if inbound == nil {
		return base
	}
	return &virtualLookup{base: base, inbound: inbound}
}

// ByName returns the overlay object when (gvk,ns,name) matches; otherwise
// delegates to base.
func (v *virtualLookup) ByName(gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error) {
	if v.matches(gvk, namespace, name) {
		return v.inbound, nil
	}
	return v.base.ByName(gvk, namespace, name)
}

// List delegates to base then prepends the overlay object when it satisfies
// gvk/namespace/selector AND is not already present (matched by name). When
// inbound has the requested gvk but is in a different namespace, fall-through
// preserves base semantics.
func (v *virtualLookup) List(gvk schema.GroupVersionKind, namespace string, selector map[string]string) ([]*unstructured.Unstructured, error) {
	out, err := v.base.List(gvk, namespace, selector)
	if err != nil {
		return nil, err
	}
	if v.inbound == nil {
		return out, nil
	}
	if v.inbound.GroupVersionKind() != gvk {
		return out, nil
	}
	if namespace != "" && v.inbound.GetNamespace() != namespace {
		return out, nil
	}
	if len(selector) > 0 {
		sel := labels.SelectorFromSet(labels.Set(selector))
		if !sel.Matches(labels.Set(v.inbound.GetLabels())) {
			return out, nil
		}
	}
	// Replace existing same-name entry if present (UPDATE semantics).
	name := v.inbound.GetName()
	for i, u := range out {
		if u != nil && u.GetName() == name && u.GetNamespace() == v.inbound.GetNamespace() {
			out[i] = v.inbound
			return out, nil
		}
	}
	return append([]*unstructured.Unstructured{v.inbound}, out...), nil
}

// Watched delegates to base.
func (v *virtualLookup) Watched() []schema.GroupVersionKind { return v.base.Watched() }

func (v *virtualLookup) matches(gvk schema.GroupVersionKind, namespace, name string) bool {
	if v.inbound == nil {
		return false
	}
	if v.inbound.GroupVersionKind() != gvk {
		return false
	}
	if v.inbound.GetName() != name {
		return false
	}
	if namespace != "" && v.inbound.GetNamespace() != namespace {
		return false
	}
	return true
}
