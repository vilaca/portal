package patchnp

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

func TestPatchNP_TargetsNPViolation(t *testing.T) {
	client := newFakeClient()
	a := New(client)
	v := api.Violation{
		GVK:       schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy"},
		Namespace: "ns1",
		Name:      "deny-egress",
	}
	patch := map[string]any{"spec": map[string]any{"policyTypes": []any{"Egress"}}}
	if err := a.Execute(context.Background(), v, map[string]any{"patch": patch}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	found := false
	for _, act := range client.Actions() {
		if act.GetVerb() != "patch" || act.GetResource().Resource != "networkpolicies" {
			continue
		}
		if act.GetNamespace() != "ns1" {
			t.Fatalf("expected ns1, got %s", act.GetNamespace())
		}
		body := map[string]any{}
		_ = json.Unmarshal(act.(interface{ GetPatch() []byte }).GetPatch(), &body)
		if body["kind"] != "NetworkPolicy" {
			t.Fatalf("expected kind NetworkPolicy, got %v", body["kind"])
		}
		md := body["metadata"].(map[string]any)
		if md["name"] != "deny-egress" {
			t.Fatalf("expected name deny-egress, got %v", md["name"])
		}
		if _, ok := body["spec"]; !ok {
			t.Fatalf("expected spec in patch, got %v", body)
		}
		found = true
	}
	if !found {
		t.Fatalf("no patch on networkpolicies, got %v", client.Actions())
	}
}

func TestPatchNP_TargetsByParams(t *testing.T) {
	client := newFakeClient()
	a := New(client)
	v := api.Violation{
		GVK:       schema.GroupVersionKind{Version: "v1", Kind: "Pod"},
		Namespace: "app-ns",
		Name:      "p1",
	}
	patch := map[string]any{"spec": map[string]any{"policyTypes": []any{"Egress"}}}
	if err := a.Execute(context.Background(), v, map[string]any{
		"patch":      patch,
		"targetName": "deny-egress",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, act := range client.Actions() {
		if act.GetVerb() == "patch" && act.GetResource().Resource == "networkpolicies" {
			if act.GetNamespace() != "app-ns" {
				t.Fatalf("expected fallback namespace app-ns, got %s", act.GetNamespace())
			}
			return
		}
	}
	t.Fatalf("expected NP patch, got %v", client.Actions())
}

func TestPatchNP_MissingPatchErrors(t *testing.T) {
	a := New(newFakeClient())
	err := a.Execute(context.Background(), api.Violation{
		GVK:       schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy"},
		Namespace: "ns", Name: "np",
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPatchNP_MissingTargetErrors(t *testing.T) {
	a := New(newFakeClient())
	err := a.Execute(context.Background(), api.Violation{
		GVK:       schema.GroupVersionKind{Version: "v1", Kind: "Pod"},
		Namespace: "ns", Name: "p",
	}, map[string]any{"patch": map[string]any{"spec": map[string]any{}}})
	if err == nil {
		t.Fatal("expected error when no targetName")
	}
}

func TestRegistered(t *testing.T) {
	if api.ActionFor(actionType) == nil {
		t.Fatal("patch-networkpolicy action not registered")
	}
}
