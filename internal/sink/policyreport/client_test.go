package policyreport

import (
	"context"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/vilaca/portal/internal/api"
)

// newFake builds a dynamic fake client with the wgpolicyk8s.io v1alpha2
// list kinds registered, plus a reactor that emulates server-side apply.
//
// client-go's dynamic fake routes ApplyPatchType through the same code path
// as strategic merge patch, which doesn't work on Unstructured. The reactor
// below intercepts apply patches, decodes the payload, and creates-or-
// updates the tracked object — which is what a real apiserver does for
// server-side apply. Production code never reaches this reactor.
func newFake() *fake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		prGVR:  "PolicyReportList",
		cprGVR: "ClusterPolicyReportList",
	}
	cli := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind)
	tracker := cli.Tracker()
	cli.PrependReactor("patch", "*", func(action clienttesting.Action) (bool, runtime.Object, error) {
		p, ok := action.(clienttesting.PatchAction)
		if !ok || p.GetPatchType() != types.ApplyPatchType {
			return false, nil, nil
		}
		gvr := p.GetResource()
		ns := p.GetNamespace()
		name := p.GetName()

		var u unstructured.Unstructured
		if err := u.UnmarshalJSON(p.GetPatch()); err != nil {
			return true, nil, err
		}
		// Ensure name/namespace from the action overrides whatever was
		// in the payload (mirrors apiserver behaviour).
		u.SetName(name)
		if ns != "" {
			u.SetNamespace(ns)
		}

		_, err := tracker.Get(gvr, ns, name)
		if apierrors.IsNotFound(err) {
			if cerr := tracker.Create(gvr, &u, ns); cerr != nil {
				return true, nil, cerr
			}
			return true, &u, nil
		} else if err != nil {
			return true, nil, err
		}
		if uerr := tracker.Update(gvr, &u, ns); uerr != nil {
			return true, nil, uerr
		}
		return true, &u, nil
	})
	return cli
}

func sampleViolation(ns, name string) api.Violation {
	return api.Violation{
		Rule:      "privileged-container",
		Severity:  api.SeverityHigh,
		GVK:       schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
		Namespace: ns,
		Name:      name,
		Mode:      api.ModeAudit,
		Message:   "container is privileged",
		At:        time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func TestEmitCreatesPolicyReportPerNamespace(t *testing.T) {
	cli := newFake()
	s := New(cli)

	if err := s.Emit(context.Background(), sampleViolation("ns-a", "pod-1")); err != nil {
		t.Fatalf("emit ns-a: %v", err)
	}
	if err := s.Emit(context.Background(), sampleViolation("ns-b", "pod-2")); err != nil {
		t.Fatalf("emit ns-b: %v", err)
	}

	for _, ns := range []string{"ns-a", "ns-b"} {
		obj, err := cli.Resource(prGVR).Namespace(ns).Get(context.Background(), reportName, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get PolicyReport %s/%s: %v", ns, reportName, err)
		}
		assertResultCount(t, obj, 1, "PolicyReport "+ns)
	}
}

func TestEmitCreatesClusterPolicyReport(t *testing.T) {
	cli := newFake()
	s := New(cli)
	v := sampleViolation("", "cluster-resource")
	v.GVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Namespace"}
	if err := s.Emit(context.Background(), v); err != nil {
		t.Fatalf("emit cluster: %v", err)
	}
	obj, err := cli.Resource(cprGVR).Get(context.Background(), reportName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get ClusterPolicyReport: %v", err)
	}
	assertResultCount(t, obj, 1, "ClusterPolicyReport")
}

func TestEmitDedupsSameKey(t *testing.T) {
	cli := newFake()
	s := New(cli)
	v := sampleViolation("ns-a", "pod-1")
	if err := s.Emit(context.Background(), v); err != nil {
		t.Fatalf("emit 1: %v", err)
	}
	v.Message = "still privileged (re-emit)"
	if err := s.Emit(context.Background(), v); err != nil {
		t.Fatalf("emit 2: %v", err)
	}
	obj, err := cli.Resource(prGVR).Namespace("ns-a").Get(context.Background(), reportName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	assertResultCount(t, obj, 1, "after re-emit")
	results, _, _ := unstructured.NestedSlice(obj.Object, "results")
	if len(results) == 0 {
		t.Fatalf("no results found")
	}
	m, _ := results[0].(map[string]any)
	if m["message"] != "still privileged (re-emit)" {
		t.Errorf("Result not replaced; message = %v", m["message"])
	}
}

func TestEmitTwoDistinctViolationsAccumulate(t *testing.T) {
	cli := newFake()
	s := New(cli)
	if err := s.Emit(context.Background(), sampleViolation("ns-a", "pod-1")); err != nil {
		t.Fatalf("emit 1: %v", err)
	}
	if err := s.Emit(context.Background(), sampleViolation("ns-a", "pod-2")); err != nil {
		t.Fatalf("emit 2: %v", err)
	}
	obj, err := cli.Resource(prGVR).Namespace("ns-a").Get(context.Background(), reportName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	assertResultCount(t, obj, 2, "ns-a after two distinct emits")
}

func TestPlaceholderUntilConfigured(t *testing.T) {
	sinks := api.Sinks()
	ctor, ok := sinks["policyreport"]
	if !ok {
		t.Fatalf("policyreport sink not registered at init")
	}
	if err := ctor().Emit(context.Background(), sampleViolation("ns-a", "pod-1")); err == nil {
		t.Fatalf("placeholder should return ErrNotConfigured")
	}
}

func TestName(t *testing.T) {
	cli := newFake()
	if got := New(cli).Name(); got != "policyreport" {
		t.Errorf("Name() = %q", got)
	}
}

func assertResultCount(t *testing.T, obj *unstructured.Unstructured, want int, label string) {
	t.Helper()
	results, _, err := unstructured.NestedSlice(obj.Object, "results")
	if err != nil {
		t.Fatalf("%s: NestedSlice: %v", label, err)
	}
	if len(results) != want {
		t.Errorf("%s: results count = %d, want %d", label, len(results), want)
	}
}
