package crd

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type stubEngine struct {
	errs map[string]string
}

func (s *stubEngine) ParseError(rule string) string { return s.errs[rule] }

func TestReconciler_WritesStatus_ClusterRule(t *testing.T) {
	ResetCounters()
	IncEval("ruleX")
	IncEval("ruleX")
	IncViolation("ruleX")

	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	obj := &PortalClusterRule{
		ObjectMeta: metav1.ObjectMeta{Name: "ruleX"},
		Spec: PortalClusterRuleSpec{
			Name: "ruleX",
			Mode: []string{"audit", "admission"},
			Match: Matcher{GVK: []RuleGVK{{Version: "v1", Kind: "Pod"}}},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(obj).
		WithStatusSubresource(&PortalClusterRule{}).
		Build()

	r := &Reconciler{Client: c, Engine: &stubEngine{errs: map[string]string{"ruleX": "boom"}}, Namespaced: false}
	_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "ruleX"}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	got := &PortalClusterRule{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "ruleX"}, got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.ParseError != "boom" {
		t.Errorf("ParseError=%q want %q", got.Status.ParseError, "boom")
	}
	if got.Status.EvalCount != 2 {
		t.Errorf("EvalCount=%d want 2", got.Status.EvalCount)
	}
	if got.Status.ViolationCount != 1 {
		t.Errorf("ViolationCount=%d want 1", got.Status.ViolationCount)
	}
	if got.Status.LastApplied.IsZero() {
		t.Errorf("LastApplied is zero")
	}
	if len(got.Status.ActiveOn) != 2 || got.Status.ActiveOn[0] != "audit" {
		t.Errorf("ActiveOn=%v want [audit admission]", got.Status.ActiveOn)
	}
}

func TestReconciler_WritesStatus_Namespaced(t *testing.T) {
	ResetCounters()

	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	obj := &PortalRule{
		ObjectMeta: metav1.ObjectMeta{Name: "rn", Namespace: "ns1"},
		Spec: PortalRuleSpec{
			Name:  "rn",
			Match: Matcher{GVK: []RuleGVK{{Version: "v1", Kind: "Pod"}}},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(obj).
		WithStatusSubresource(&PortalRule{}).
		Build()

	r := &Reconciler{Client: c, Engine: &stubEngine{}, Namespaced: true}
	_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "rn"}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	got := &PortalRule{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "ns1", Name: "rn"}, got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.ParseError != "" {
		t.Errorf("ParseError=%q want empty", got.Status.ParseError)
	}
	if got.Status.LastApplied.IsZero() {
		t.Errorf("LastApplied is zero")
	}
}

func TestReconciler_MissingObjectIsNoop(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &Reconciler{Client: c, Engine: &stubEngine{}, Namespaced: false}
	_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "nope"}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

func TestPackageCounters(t *testing.T) {
	ResetCounters()
	if EvalCount("x") != 0 || ViolationCount("x") != 0 {
		t.Errorf("expected zero counters")
	}
	IncEval("x")
	IncEval("x")
	IncViolation("x")
	if EvalCount("x") != 2 {
		t.Errorf("EvalCount=%d want 2", EvalCount("x"))
	}
	if ViolationCount("x") != 1 {
		t.Errorf("ViolationCount=%d want 1", ViolationCount("x"))
	}
}
