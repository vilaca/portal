// Package generic provides a fallback api.ContextBuilder that accepts every
// GVK and exposes only the universal "object", "metadata", and "request"
// keys. The pod-shaped builder (internal/context/pod) takes precedence when
// it supports a GVK; this builder catches the rest.
package generic

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vilaca/portal/internal/api"
)

func init() {
	api.RegisterContextBuilder("generic", func() api.ContextBuilder { return New() })
}

// New returns a ContextBuilder whose Supports always returns true.
func New() api.ContextBuilder { return &builder{} }

type builder struct{}

func (b *builder) Supports(_ schema.GroupVersionKind) bool { return true }

func (b *builder) Build(obj *unstructured.Unstructured) (api.Context, error) {
	if obj == nil {
		return api.Context{Env: map[string]any{}}, nil
	}
	env := map[string]any{
		"object":   obj.Object,
		"metadata": metadataMap(obj),
		"request":  nil, // admission code rebinds this per-request
	}
	return api.Context{
		GVK:    obj.GroupVersionKind(),
		Object: obj,
		Env:    env,
	}, nil
}

func metadataMap(obj *unstructured.Unstructured) map[string]any {
	return map[string]any{
		"name":        obj.GetName(),
		"namespace":   obj.GetNamespace(),
		"labels":      stringMap(obj.GetLabels()),
		"annotations": stringMap(obj.GetAnnotations()),
	}
}

func stringMap(in map[string]string) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
