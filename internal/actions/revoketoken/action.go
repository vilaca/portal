// Package revoketoken implements api.Action "revoke-sa-token": when a Pod
// violation fires, the action looks up the pod's serviceAccountName, lists
// Secrets in the same namespace of type kubernetes.io/service-account-token
// annotated for that SA, and deletes them.
//
// Deleting the secret forces a token rotation: legacy clients holding the
// revoked token will fail authentication on next API call. This is a v1
// blunt instrument; future work may replace with the TokenRequest API.
//
// Idempotent() is false because re-running after a re-creation by the SA
// controller would re-revoke; the dispatcher's idempotency cache still
// suppresses re-runs within the rate-limit window.
package revoketoken

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/vilaca/portal/internal/api"
)

const (
	actionType         = "revoke-sa-token"
	saTokenSecretType  = string(corev1.SecretTypeServiceAccountToken)
	saNameAnnotation   = "kubernetes.io/service-account.name"
)

// ErrNotConfigured is returned when Execute runs before Configure().
var ErrNotConfigured = errors.New("revoke-sa-token action not configured")

func init() {
	api.RegisterAction(actionType, func() api.Action { return &action{} })
}

// Configure swaps the registered factory for one backed by clientset.
func Configure(client kubernetes.Interface) {
	api.RegisterAction(actionType, func() api.Action { return New(client) })
}

// New constructs the action bound to client.
func New(client kubernetes.Interface) api.Action {
	return &action{client: client}
}

type action struct {
	client kubernetes.Interface
}

func (a *action) Type() string                    { return actionType }
func (a *action) Idempotent() bool                { return false }
func (a *action) DefaultRateLimit() time.Duration { return 60 * time.Second }

// Execute revokes SA tokens for the pod referenced by v. Returns an error
// for non-Pod GVKs so the dispatcher records {result="error"}.
func (a *action) Execute(ctx context.Context, v api.Violation, _ map[string]any) error {
	if a.client == nil {
		return ErrNotConfigured
	}
	if v.GVK.Kind != "Pod" {
		return fmt.Errorf("%s: not a Pod (got %s)", actionType, v.GVK.Kind)
	}
	pod, err := a.client.CoreV1().Pods(v.Namespace).Get(ctx, v.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("%s: get pod: %w", actionType, err)
	}
	sa := pod.Spec.ServiceAccountName
	if sa == "" {
		sa = "default"
	}
	secrets, err := a.client.CoreV1().Secrets(v.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("%s: list secrets: %w", actionType, err)
	}
	var lastErr error
	deleted := 0
	for _, s := range secrets.Items {
		if string(s.Type) != saTokenSecretType {
			continue
		}
		if s.Annotations[saNameAnnotation] != sa {
			continue
		}
		if err := a.client.CoreV1().Secrets(v.Namespace).Delete(ctx, s.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			lastErr = err
			continue
		}
		deleted++
	}
	if deleted == 0 && lastErr == nil {
		// Soft success: no tokens existed. This still means "no work to do"
		// rather than a failure — the SA may use TokenRequest. The
		// dispatcher logs {result="ok"} in this case.
		return nil
	}
	return lastErr
}
