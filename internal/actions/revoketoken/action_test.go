package revoketoken

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/vilaca/portal/internal/api"
)

func TestRevoke_DeletesMatchingSecrets(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "ns1"},
		Spec:       corev1.PodSpec{ServiceAccountName: "myapp"},
	}
	matching := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myapp-token-xyz",
			Namespace: "ns1",
			Annotations: map[string]string{
				saNameAnnotation: "myapp",
			},
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}
	otherSA := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-token",
			Namespace: "ns1",
			Annotations: map[string]string{
				saNameAnnotation: "different",
			},
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}
	notATokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "tls", Namespace: "ns1"},
		Type:       corev1.SecretTypeTLS,
	}
	client := fake.NewSimpleClientset(pod, matching, otherSA, notATokenSecret)
	a := New(client)
	v := api.Violation{
		GVK:       schema.GroupVersionKind{Version: "v1", Kind: "Pod"},
		Namespace: "ns1",
		Name:      "p1",
	}
	if err := a.Execute(context.Background(), v, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	deletes := 0
	for _, act := range client.Actions() {
		if act.GetVerb() == "delete" && act.GetResource().Resource == "secrets" {
			deletes++
			if act.GetNamespace() != "ns1" {
				t.Fatalf("expected ns1 delete, got %s", act.GetNamespace())
			}
		}
	}
	if deletes != 1 {
		t.Fatalf("expected exactly 1 secret delete, got %d (actions=%v)", deletes, client.Actions())
	}
}

func TestRevoke_NonPodErrors(t *testing.T) {
	a := New(fake.NewSimpleClientset())
	err := a.Execute(context.Background(), api.Violation{
		GVK: schema.GroupVersionKind{Version: "v1", Kind: "Service"},
	}, nil)
	if err == nil {
		t.Fatal("expected error for non-Pod")
	}
}

func TestRevoke_PodMissingErrors(t *testing.T) {
	a := New(fake.NewSimpleClientset())
	err := a.Execute(context.Background(), api.Violation{
		GVK:       schema.GroupVersionKind{Version: "v1", Kind: "Pod"},
		Namespace: "ns",
		Name:      "ghost",
	}, nil)
	if err == nil {
		t.Fatal("expected error for missing pod")
	}
}

func TestRegistered(t *testing.T) {
	if api.ActionFor(actionType) == nil {
		t.Fatal("revoke-sa-token action not registered")
	}
}
