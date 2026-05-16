package annotate

import (
	"context"
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/vilaca/portal/internal/api"
)

func newFakeClient() *dynfake.FakeDynamicClient {
	c := dynfake.NewSimpleDynamicClient(runtime.NewScheme())
	c.PrependReactor("patch", "*", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.Unstructured{}, nil
	})
	return c
}

func TestAnnotate_AppliesPatch(t *testing.T) {
	client := newFakeClient()
	a := New(client)
	v := api.Violation{
		GVK:       schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
		Namespace: "ns1",
		Name:      "d1",
	}
	if err := a.Execute(context.Background(), v, map[string]any{"key": "portal.io/reason", "value": "violated"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	found := false
	for _, act := range client.Actions() {
		if act.GetVerb() != "patch" {
			continue
		}
		if act.GetResource().Resource != "deployments" {
			continue
		}
		found = true
		body := map[string]any{}
		_ = json.Unmarshal(act.(interface{ GetPatch() []byte }).GetPatch(), &body)
		ann := body["metadata"].(map[string]any)["annotations"].(map[string]any)
		if ann["portal.io/reason"] != "violated" {
			t.Fatalf("expected annotation, got %v", ann)
		}
	}
	if !found {
		t.Fatalf("expected patch on deployments, got %v", client.Actions())
	}
}

func TestAnnotate_MissingKeyErrors(t *testing.T) {
	a := New(newFakeClient())
	err := a.Execute(context.Background(), api.Violation{GVK: schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, Namespace: "ns", Name: "p"}, map[string]any{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRegistered(t *testing.T) {
	if api.ActionFor(actionType) == nil {
		t.Fatal("annotate action not registered")
	}
}
