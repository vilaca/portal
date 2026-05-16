package label

import (
	"context"
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
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

// TestLabel_MapperOverridesPluralize verifies that when a RESTMapper is
// wired (NewWithMapper), it wins over the local pluralize() — the canonical
// regression: a Kind whose mapper-resolved Resource differs from the naive
// inflection routes to the mapper's answer.
func TestLabel_MapperOverridesPluralize(t *testing.T) {
	// Stub mapper: "Database" → "databasen" (deliberately not-naive so we
	// can prove the mapper beat pluralize, which would yield "databases").
	m := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Group: "x.example.com", Version: "v1"}})
	gvk := schema.GroupVersionKind{Group: "x.example.com", Version: "v1", Kind: "Database"}
	m.AddSpecific(gvk,
		schema.GroupVersionResource{Group: "x.example.com", Version: "v1", Resource: "databasen"},
		schema.GroupVersionResource{Group: "x.example.com", Version: "v1", Resource: "database"},
		meta.RESTScopeNamespace,
	)

	client := newFakeClient()
	a := NewWithMapper(client, m)
	v := api.Violation{GVK: gvk, Namespace: "ns", Name: "db1"}
	if err := a.Execute(context.Background(), v, map[string]any{"key": "k", "value": "v"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := ""
	for _, act := range client.Actions() {
		if act.GetVerb() == "patch" {
			got = act.GetResource().Resource
			break
		}
	}
	if got != "databasen" {
		t.Fatalf("mapper-wired action routed to %q; want %q (pluralize would have produced %q)", got, "databasen", "databases")
	}
}

// TestLabel_FallsBackWithoutMapper proves the no-mapper path still uses the
// local pluralize. NetworkPolicy is in the small irregular-plural table.
func TestLabel_FallsBackWithoutMapper(t *testing.T) {
	client := newFakeClient()
	a := New(client)
	v := api.Violation{
		GVK:       schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy"},
		Namespace: "ns", Name: "np",
	}
	if err := a.Execute(context.Background(), v, map[string]any{"key": "k"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := ""
	for _, act := range client.Actions() {
		if act.GetVerb() == "patch" {
			got = act.GetResource().Resource
		}
	}
	if got != "networkpolicies" {
		t.Fatalf("fallback pluralize gave %q; want %q", got, "networkpolicies")
	}
}

// silence unused warning for metav1 in case future tests need it.
var _ = metav1.PatchOptions{}

