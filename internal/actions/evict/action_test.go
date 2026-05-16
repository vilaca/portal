package evict

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/vilaca/portal/internal/api"
)

func TestEvict_PostsEviction(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "ns1"}}
	client := fake.NewSimpleClientset(pod)
	a := New(client)

	v := api.Violation{
		GVK:       schema.GroupVersionKind{Version: "v1", Kind: "Pod"},
		Namespace: "ns1",
		Name:      "p1",
	}
	if err := a.Execute(context.Background(), v, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	found := false
	for _, act := range client.Actions() {
		if act.GetVerb() == "create" && act.GetSubresource() == "eviction" {
			found = true
			ca, ok := act.(clienttesting.CreateAction)
			if !ok {
				t.Fatalf("expected CreateAction, got %T", act)
			}
			if ca.GetNamespace() != "ns1" {
				t.Fatalf("expected ns1, got %s", ca.GetNamespace())
			}
		}
	}
	if !found {
		t.Fatalf("expected eviction subresource create, got actions=%v", client.Actions())
	}
}

func TestEvict_NonPodErrors(t *testing.T) {
	a := New(fake.NewSimpleClientset())
	v := api.Violation{GVK: schema.GroupVersionKind{Version: "v1", Kind: "Service"}, Namespace: "ns", Name: "s"}
	if err := a.Execute(context.Background(), v, nil); err == nil {
		t.Fatal("expected error for non-Pod")
	}
}

func TestRegistered(t *testing.T) {
	if api.ActionFor(actionType) == nil {
		t.Fatal("evict action not registered")
	}
}
