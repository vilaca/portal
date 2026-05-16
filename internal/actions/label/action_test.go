package label

import (
	"context"
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/vilaca/portal/internal/api"
)

// newFakeClient returns a dynamic fake client that bypasses the
// strategic-merge path the fake's default reactor uses (which doesn't
// handle Unstructured objects); the reactor below short-circuits Apply-type
// patches with a stubbed empty object so our tests can assert on the
// recorded Action's GetPatch() output without depending on the strategic-
// merge engine.
func newFakeClient(objs ...runtime.Object) *dynfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	c := dynfake.NewSimpleDynamicClient(scheme, objs...)
	c.PrependReactor("patch", "*", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.Unstructured{}, nil
	})
	return c
}

var _ dynamic.Interface = (*dynfake.FakeDynamicClient)(nil)

func TestLabel_AppliesPatch(t *testing.T) {
	client := newFakeClient()
	a := New(client)
	v := api.Violation{
		GVK:       schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
		Namespace: "ns1",
		Name:      "p1",
	}
	if err := a.Execute(context.Background(), v, map[string]any{"key": "portal.io/quarantine", "value": "true"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Confirm a "patch" action was recorded against pods/p1.
	found := false
	for _, act := range client.Actions() {
		if act.GetVerb() != "patch" {
			continue
		}
		if act.GetResource().Resource != "pods" {
			continue
		}
		found = true
		// Decode the patch body and check shape.
		patchAct := act.(interface{ GetPatch() []byte })
		var body map[string]any
		if err := json.Unmarshal(patchAct.GetPatch(), &body); err != nil {
			t.Fatalf("patch JSON parse: %v", err)
		}
		meta := body["metadata"].(map[string]any)
		labels := meta["labels"].(map[string]any)
		if labels["portal.io/quarantine"] != "true" {
			t.Fatalf("expected label set, got %v", labels)
		}
	}
	if !found {
		t.Fatalf("expected patch on pods, got actions=%v", client.Actions())
	}
}

func TestLabel_MissingKeyErrors(t *testing.T) {
	client := newFakeClient()
	a := New(client)
	err := a.Execute(context.Background(), api.Violation{GVK: schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, Namespace: "ns", Name: "p"}, map[string]any{})
	if err == nil {
		t.Fatal("expected error on missing params.key")
	}
}

func TestLabel_DefaultsValueToTrue(t *testing.T) {
	client := newFakeClient()
	a := New(client)
	v := api.Violation{
		GVK:       schema.GroupVersionKind{Version: "v1", Kind: "Pod"},
		Namespace: "ns1",
		Name:      "p1",
	}
	if err := a.Execute(context.Background(), v, map[string]any{"key": "k"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, act := range client.Actions() {
		if act.GetVerb() == "patch" {
			body := map[string]any{}
			_ = json.Unmarshal(act.(interface{ GetPatch() []byte }).GetPatch(), &body)
			labels := body["metadata"].(map[string]any)["labels"].(map[string]any)
			if labels["k"] != "true" {
				t.Fatalf("expected default value 'true', got %v", labels["k"])
			}
		}
	}
}

func TestPluralize(t *testing.T) {
	cases := map[string]string{
		"Pod":           "pods",
		"NetworkPolicy": "networkpolicies",
		"Ingress":       "ingresses",
		"Deployment":    "deployments",
		"Endpoints":     "endpoints",
		"Service":       "services",
	}
	for in, want := range cases {
		if got := pluralize(in); got != want {
			t.Fatalf("pluralize(%q) = %q; want %q", in, got, want)
		}
	}
}

// Ensure init() registered the placeholder factory.
func TestRegistered(t *testing.T) {
	if api.ActionFor(actionType) == nil {
		t.Fatal("label action not registered")
	}
}

// silence unused warning for metav1 in case future tests need it.
var _ = metav1.PatchOptions{}

