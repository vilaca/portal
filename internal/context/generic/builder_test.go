package generic

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vilaca/portal/internal/api"
)

func TestSupportsAlwaysTrue(t *testing.T) {
	b := New()
	if !b.Supports(schema.GroupVersionKind{Group: "x", Version: "y", Kind: "Z"}) {
		t.Fatal("generic builder must accept every GVK")
	}
}

func TestBuildEnvShape(t *testing.T) {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy"})
	obj.SetName("default-deny")
	obj.SetNamespace("prod")
	obj.SetLabels(map[string]string{"app": "demo"})

	ctx, err := New().Build(obj)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Env["object"] == nil {
		t.Error("object key must be populated")
	}
	md, ok := ctx.Env["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata not a map: %T", ctx.Env["metadata"])
	}
	if md["name"] != "default-deny" || md["namespace"] != "prod" {
		t.Errorf("metadata wrong: %#v", md)
	}
}

func TestRegistered(t *testing.T) {
	if _, ok := api.ContextBuilders()["generic"]; !ok {
		t.Fatal("generic builder not registered")
	}
}
