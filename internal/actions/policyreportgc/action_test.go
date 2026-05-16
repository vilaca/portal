package policyreportgc

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynfake "k8s.io/client-go/dynamic/fake"

	"github.com/vilaca/portal/internal/api"
)

// scheme returns a scheme that knows about PolicyReport / ClusterPolicyReport
// as unstructured Lists so the dynamic fake client can index them.
func scheme() *runtime.Scheme {
	s := runtime.NewScheme()
	s.AddKnownTypeWithName(schema.GroupVersionKind{Group: "wgpolicyk8s.io", Version: "v1alpha2", Kind: "PolicyReportList"}, &unstructured.UnstructuredList{})
	s.AddKnownTypeWithName(schema.GroupVersionKind{Group: "wgpolicyk8s.io", Version: "v1alpha2", Kind: "ClusterPolicyReportList"}, &unstructured.UnstructuredList{})
	return s
}

// makeReport builds a fixture PolicyReport with the supplied results array.
func makeReport(ns string, results []any) *unstructured.Unstructured {
	o := &unstructured.Unstructured{}
	o.SetAPIVersion("wgpolicyk8s.io/v1alpha2")
	o.SetKind("PolicyReport")
	o.SetName(reportName)
	if ns != "" {
		o.SetNamespace(ns)
	} else {
		o.SetKind("ClusterPolicyReport")
	}
	_ = unstructured.SetNestedSlice(o.Object, results, "results")
	_ = unstructured.SetNestedField(o.Object, int64(len(results)), "summary", "fail")
	return o
}

func TestPolicyReportGC_RemovesMatchingResult(t *testing.T) {
	// Two results in ns "prod": one for Pod "p1" (about to be deleted), one
	// for Pod "p2" (stays).
	pr := makeReport("prod", []any{
		map[string]any{
			"policy": "no-privileged",
			"resources": []any{
				map[string]any{"kind": "Pod", "name": "p1", "namespace": "prod"},
			},
		},
		map[string]any{
			"policy": "no-privileged",
			"resources": []any{
				map[string]any{"kind": "Pod", "name": "p2", "namespace": "prod"},
			},
		},
	})
	client := dynfake.NewSimpleDynamicClient(scheme(), pr)

	a := New(client)
	v := api.Violation{
		GVK:       schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
		Namespace: "prod",
		Name:      "p1",
	}
	if err := a.Execute(context.Background(), v, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	updated, err := client.Resource(prGVR).Namespace("prod").Get(context.Background(), reportName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	results, _, _ := unstructured.NestedSlice(updated.Object, "results")
	if len(results) != 1 {
		t.Fatalf("results = %d; want 1 after GC", len(results))
	}
	survivor := results[0].(map[string]any)["resources"].([]any)[0].(map[string]any)
	if survivor["name"] != "p2" {
		t.Errorf("wrong survivor: %v", survivor)
	}
	fail, _, _ := unstructured.NestedInt64(updated.Object, "summary", "fail")
	if fail != 1 {
		t.Errorf("summary.fail = %d; want 1", fail)
	}
}

func TestPolicyReportGC_NoMatchIsNoop(t *testing.T) {
	pr := makeReport("prod", []any{
		map[string]any{
			"policy": "no-privileged",
			"resources": []any{
				map[string]any{"kind": "Pod", "name": "p2", "namespace": "prod"},
			},
		},
	})
	client := dynfake.NewSimpleDynamicClient(scheme(), pr)

	a := New(client)
	v := api.Violation{
		GVK:       schema.GroupVersionKind{Version: "v1", Kind: "Pod"},
		Namespace: "prod",
		Name:      "p1-does-not-exist",
	}
	if err := a.Execute(context.Background(), v, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// No update should have happened — the recorded actions on the fake
	// client should contain a Get but no Update.
	hasUpdate := false
	for _, act := range client.Actions() {
		if act.GetVerb() == "update" {
			hasUpdate = true
		}
	}
	if hasUpdate {
		t.Error("expected no Update when no result matched; GC wrote anyway")
	}
}

func TestPolicyReportGC_MissingReportIsNoop(t *testing.T) {
	client := dynfake.NewSimpleDynamicClient(scheme()) // no objects
	a := New(client)
	v := api.Violation{GVK: schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, Namespace: "prod", Name: "p"}
	if err := a.Execute(context.Background(), v, nil); err != nil {
		t.Fatalf("Execute: %v (expected no-op)", err)
	}
}

func TestPolicyReportGC_ClusterScoped(t *testing.T) {
	cpr := makeReport("", []any{
		map[string]any{
			"policy": "no-cluster-admin-binding",
			"resources": []any{
				map[string]any{"kind": "ClusterRoleBinding", "name": "evil", "namespace": ""},
			},
		},
		map[string]any{
			"policy": "no-cluster-admin-binding",
			"resources": []any{
				map[string]any{"kind": "ClusterRoleBinding", "name": "stays", "namespace": ""},
			},
		},
	})
	client := dynfake.NewSimpleDynamicClient(scheme(), cpr)

	a := New(client)
	v := api.Violation{
		GVK:  schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRoleBinding"},
		Name: "evil",
	}
	if err := a.Execute(context.Background(), v, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	updated, err := client.Resource(cprGVR).Get(context.Background(), reportName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get cpr: %v", err)
	}
	results, _, _ := unstructured.NestedSlice(updated.Object, "results")
	if len(results) != 1 {
		t.Fatalf("ClusterPolicyReport results = %d; want 1", len(results))
	}
}

func TestPolicyReportGC_NotConfigured(t *testing.T) {
	a := &action{}
	if err := a.Execute(context.Background(), api.Violation{}, nil); err != ErrNotConfigured {
		t.Fatalf("Execute = %v; want ErrNotConfigured", err)
	}
}

func TestPolicyReportGC_Registered(t *testing.T) {
	if api.ActionFor(actionType) == nil {
		t.Fatal("policyreport-gc action not registered")
	}
}

// Ensure interface compliance.
var _ dynamic.Interface = (*dynfake.FakeDynamicClient)(nil)
