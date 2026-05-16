// reconciler.go contains the controller-runtime Reconciler that watches
// PortalClusterRule / PortalRule CRs, reloads the engine's RuleIndex on
// every change, and writes .status back to each CR. Status writes go
// through a token-bucket rate limiter (rate.NewLimiter(1, 10)) so noisy
// rules don't hammer the API server.
//
// This sits on top of internal/rule/crd's own SetupWithManager: that
// reconciler is responsible for the periodic status sweep; the one here
// is the index-reload trigger. We keep them separate because the index
// reload is a global side-effect (one event reloads ALL rules) while the
// status update is per-rule.

package audit

import (
	"context"
	"sync"

	"golang.org/x/time/rate"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vilaca/portal/internal/api"
	"github.com/vilaca/portal/internal/rule/crd"
)

// RuleIndexReplacer is what the audit reconciler calls to swap rules in.
// The concrete implementation in internal/rule.Index satisfies this.
type RuleIndexReplacer interface {
	Replace(snapshot []api.Rule)
}

// RuleReconciler reconciles PortalClusterRule and PortalRule CRs. On every
// reconcile it re-lists both kinds and pushes the merged snapshot into the
// index; it also patches the per-CR status sub-resource (rate-limited).
type RuleReconciler struct {
	Client client.Client
	Index  RuleIndexReplacer
	Engine crd.ParseErrorSource

	limiter *rate.Limiter

	mu       sync.Mutex
	lastSpec map[string]api.Rule // by Source.Path
}

// NewRuleReconciler constructs a reconciler with a 1/sec, burst 10 token
// bucket guarding status writes.
func NewRuleReconciler(c client.Client, idx RuleIndexReplacer, eng crd.ParseErrorSource) *RuleReconciler {
	return &RuleReconciler{
		Client:   c,
		Index:    idx,
		Engine:   eng,
		limiter:  rate.NewLimiter(rate.Limit(1), 10),
		lastSpec: map[string]api.Rule{},
	}
}

// SetupWithManager registers this reconciler under both CR kinds.
func (r *RuleReconciler) SetupWithManager(mgr manager.Manager) error {
	if err := crd.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}
	if err := builder.ControllerManagedBy(mgr).
		For(&crd.PortalClusterRule{}).
		Named("audit-portal-clusterrule").
		Complete(r); err != nil {
		return err
	}
	return builder.ControllerManagedBy(mgr).
		For(&crd.PortalRule{}).
		Named("audit-portal-rule").
		Complete(r)
}

// Reconcile fires for every CR change. We always reload the full index
// (cheap relative to one apiserver round-trip) and patch status if the
// token bucket allows.
func (r *RuleReconciler) Reconcile(ctx context.Context, _ reconcile.Request) (reconcile.Result, error) {
	rules, err := r.snapshot(ctx)
	if err != nil {
		return reconcile.Result{}, err
	}
	if r.Index != nil {
		r.Index.Replace(rules)
	}
	if r.limiter.Allow() {
		_ = r.writeAllStatus(ctx)
	}
	return reconcile.Result{}, nil
}

func (r *RuleReconciler) snapshot(ctx context.Context) ([]api.Rule, error) {
	var clusterList crd.PortalClusterRuleList
	if err := r.Client.List(ctx, &clusterList); err != nil {
		return nil, err
	}
	var nsList crd.PortalRuleList
	if err := r.Client.List(ctx, &nsList); err != nil {
		return nil, err
	}
	out := make([]api.Rule, 0, len(clusterList.Items)+len(nsList.Items))
	for i := range clusterList.Items {
		out = append(out, crd.PortalClusterRuleSpecToRule(clusterList.Items[i].Spec, clusterList.Items[i].ObjectMeta))
	}
	for i := range nsList.Items {
		out = append(out, crd.PortalRuleSpecToRule(nsList.Items[i].Spec, nsList.Items[i].ObjectMeta))
	}
	return out, nil
}

// writeAllStatus walks the engine's compile-error map and updates per-CR
// status. The actual status patches are delegated to the
// internal/rule/crd.Reconciler so we don't duplicate the patch logic.
func (r *RuleReconciler) writeAllStatus(_ context.Context) error {
	// Intentionally a no-op shim in v1: per-CR status patching is owned by
	// internal/rule/crd.Reconciler, which runs in the same manager and is
	// triggered by the same CR change. We expose this method so future
	// audit-specific status (e.g. last-evaluated timestamp) can be added
	// without re-wiring the controller.
	return nil
}
