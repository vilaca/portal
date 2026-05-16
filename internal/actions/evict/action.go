// Package evict implements api.Action "evict": post a policy/v1 Eviction to
// the pod's eviction subresource via the typed kubernetes client.
//
// Eviction respects PodDisruptionBudgets — that's the whole reason it's
// preferred over Delete — so this action is well suited as a "shed
// non-conforming workloads" response. It only applies to Pod-kind
// violations; the Execute method returns an error for any other GVK so the
// dispatcher records portal_actions_total{result="error"}.
package evict

import (
	"context"
	"errors"
	"fmt"
	"time"

	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/vilaca/portal/internal/api"
)

const actionType = "evict"

// ErrNotConfigured is returned when Execute runs before Configure().
var ErrNotConfigured = errors.New("evict action not configured")

func init() {
	api.RegisterAction(actionType, func() api.Action { return &action{} })
}

// Configure swaps the registered factory for one backed by clientset.
func Configure(client kubernetes.Interface) {
	api.RegisterAction(actionType, func() api.Action { return New(client) })
}

// New constructs an evict action bound to client.
func New(client kubernetes.Interface) api.Action {
	return &action{client: client}
}

type action struct {
	client kubernetes.Interface
}

func (a *action) Type() string                    { return actionType }
func (a *action) Idempotent() bool                { return false }
func (a *action) DefaultRateLimit() time.Duration { return 30 * time.Second }

// Execute posts /api/v1/namespaces/<ns>/pods/<name>/eviction. The typed
// client's EvictV1 helper builds the subresource URL for us.
func (a *action) Execute(ctx context.Context, v api.Violation, _ map[string]any) error {
	if a.client == nil {
		return ErrNotConfigured
	}
	if v.GVK.Kind != "Pod" {
		return fmt.Errorf("%s: not a Pod (got %s)", actionType, v.GVK.Kind)
	}
	ev := &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      v.Name,
			Namespace: v.Namespace,
		},
	}
	return a.client.CoreV1().Pods(v.Namespace).EvictV1(ctx, ev)
}
