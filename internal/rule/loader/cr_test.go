package loader

import (
	"context"
	"sort"
	"sync"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vilaca/portal/internal/api"
	crd "github.com/vilaca/portal/internal/rule/v1alpha1"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := crd.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

func makePCR(name, expr string) *crd.PortalClusterRule {
	return &crd.PortalClusterRule{
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID("uid-" + name)},
		Spec: crd.PortalClusterRuleSpec{
			Name:    name,
			Enabled: true,
			Match: crd.Matcher{
				GVK: []crd.RuleGVK{{Group: "", Version: "v1", Kind: "Pod"}},
			},
			Expression: expr,
		},
	}
}

func makePR(ns, name, expr string) *crd.PortalRule {
	return &crd.PortalRule{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: types.UID("uid-" + ns + "-" + name)},
		Spec: crd.PortalRuleSpec{
			Name:    name,
			Enabled: true,
			Match: crd.Matcher{
				GVK: []crd.RuleGVK{{Group: "", Version: "v1", Kind: "Pod"}},
			},
			Expression: expr,
		},
	}
}

func TestCRLoader_SnapshotConvertsAll(t *testing.T) {
	scheme := newScheme(t)
	pcr1 := makePCR("rule-a", "true")
	pcr2 := makePCR("rule-b", "false")
	pr1 := makePR("prod", "rule-c", "1 == 1")

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pcr1, pcr2, pr1).
		Build()

	ld := NewCRFromClient(c)

	var (
		mu      sync.Mutex
		got     []api.Rule
		fired   int
	)
	ld.SetOnUpdate(func(rules []api.Rule) {
		mu.Lock()
		got = rules
		fired++
		mu.Unlock()
	})

	rules, err := ld.Trigger(context.Background())
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if len(rules) != 3 {
		t.Fatalf("expected 3 rules, got %d: %+v", len(rules), rules)
	}
	mu.Lock()
	if fired != 1 {
		t.Errorf("expected 1 onUpdate, got %d", fired)
	}
	if len(got) != 3 {
		t.Errorf("expected 3 rules in callback, got %d", len(got))
	}
	mu.Unlock()

	names := []string{rules[0].Name, rules[1].Name, rules[2].Name}
	sort.Strings(names)
	want := []string{"rule-a", "rule-b", "rule-c"}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("rule[%d]=%q want %q", i, names[i], want[i])
		}
	}

	// Validate origin tags survived.
	origins := map[string]string{}
	for _, r := range rules {
		origins[r.Name] = r.Source.Origin
	}
	if origins["rule-a"] != "PortalClusterRule" {
		t.Errorf("rule-a origin=%q want PortalClusterRule", origins["rule-a"])
	}
	if origins["rule-c"] != "PortalRule" {
		t.Errorf("rule-c origin=%q want PortalRule", origins["rule-c"])
	}
}

func TestCRLoader_TriggerFiresOnMutation(t *testing.T) {
	scheme := newScheme(t)
	pcr1 := makePCR("rule-a", "true")
	pcr2 := makePCR("rule-b", "false")
	pr1 := makePR("prod", "rule-c", "1 == 1")

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pcr1, pcr2, pr1).
		Build()

	ld := NewCRFromClient(c)

	type snap struct {
		rules []api.Rule
	}
	ch := make(chan snap, 4)
	ld.SetOnUpdate(func(rules []api.Rule) {
		cp := make([]api.Rule, len(rules))
		copy(cp, rules)
		ch <- snap{rules: cp}
	})

	if _, err := ld.Trigger(context.Background()); err != nil {
		t.Fatalf("first Trigger: %v", err)
	}
	first := <-ch
	if len(first.rules) != 3 {
		t.Fatalf("first snapshot len=%d want 3", len(first.rules))
	}

	// Mutate one CR (update the expression on rule-b).
	pcr2.Spec.Expression = "1 != 0"
	if err := c.Update(context.Background(), pcr2); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if _, err := ld.Trigger(context.Background()); err != nil {
		t.Fatalf("second Trigger: %v", err)
	}
	second := <-ch
	if len(second.rules) != 3 {
		t.Fatalf("second snapshot len=%d want 3", len(second.rules))
	}
	for _, r := range second.rules {
		if r.Name == "rule-b" && r.Expression != "1 != 0" {
			t.Errorf("rule-b expression=%q want %q", r.Expression, "1 != 0")
		}
	}
}

// _ ensures the client compiles into a real type assertion path; some
// fake-client flavours change between minor versions.
var _ client.Client = fake.NewClientBuilder().Build()
