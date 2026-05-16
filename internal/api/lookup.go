package api

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Lookup is exposed to rule expressions as `cluster.<gvk>.byName(ns, name)` and
// `cluster.<gvk>.list(ns, selector)`. Backed by audit's shared informer caches
// at runtime; pluggable for tests.
type Lookup interface {
	// ByName fetches a single object from the cache. Returns nil if absent.
	ByName(gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error)
	// List returns every object of gvk in namespace matching selector. Empty
	// namespace means cluster-wide.
	List(gvk schema.GroupVersionKind, namespace string, selector map[string]string) ([]*unstructured.Unstructured, error)
	// Watched reports the set of GVKs this Lookup currently has informers for.
	Watched() []schema.GroupVersionKind
}

// DepRecorder is the write side of the reverse-dependency index. The lookup
// implementation records (rule, object) → (referenced) edges; the audit loop
// queries the inverse on resource-change events to enqueue re-evaluation.
type DepRecorder interface {
	Record(rule string, observed, referenced ObjectRef)
	Dependents(referenced ObjectRef) []DepEntry
}

// ObjectRef identifies one cluster object for the dep index.
type ObjectRef struct {
	GVK       schema.GroupVersionKind
	Namespace string
	Name      string
}

// DepEntry is an (rule, observed-object) pair recorded as depending on some
// referenced object.
type DepEntry struct {
	Rule     string
	Observed ObjectRef
}
