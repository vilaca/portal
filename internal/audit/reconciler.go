// reconciler.go contains the controller-runtime Reconciler that watches
// PortalClusterRule / PortalRule CRs, reloads the engine's RuleIndex on
// every change, and writes .status back to each CR. Status writes go
// through a token-bucket rate limiter (rate.NewLimiter(1, 10)) so noisy
// rules don't hammer the API server.
//
// This sits on top of internal/rule/v1alpha1's own SetupWithManager: that
// reconciler is responsible for the periodic status sweep; the one here
// is the index-reload trigger. We keep them separate because the index
// reload is a global side-effect (one event reloads ALL rules) while the
// status update is per-rule.

package audit

import (
	"context"
	"sync"

	"golang.org/x/time/rate"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vilaca/portal/internal/api"
	crd "github.com/vilaca/portal/internal/rule/v1alpha1"
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

// Reconcile fires for every CR change. We list every CR (both kinds),
// rebuild the engine's rule index, AND patch status.lastApplied on the
// triggering CR. Patching status AFTER idx.Replace is essential — the
// e2e harness (and any consumer) uses status.lastApplied as the signal
// that "this rule is now live in the engine."
func (r *RuleReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	rules, err := r.snapshot(ctx)
	if err != nil {
		return reconcile.Result{}, err
	}
	if r.Index != nil {
		r.Index.Replace(rules)
	}
	// Status patch is rate-limited; if we're over budget, the periodic
	// resync from the v1alpha1 Reconciler will catch up later.
	if r.limiter.Allow() {
		_ = r.patchStatusForRequest(ctx, req)
	}
	return reconcile.Result{}, nil
}

// patchStatusForRequest writes status.lastApplied (and status.parseError if
// applicable) for the CR that triggered this reconcile. Other CRs' status
// is updated by the v1alpha1.Reconciler on its own schedule.
func (r *RuleReconciler) patchStatusForRequest(ctx context.Context, req reconcile.Request) error {
	// Try cluster-scoped first; if NotFound, try namespaced.
	cluster := &crd.PortalClusterRule{}
	if err := r.Client.Get(ctx, req.NamespacedName, cluster); err == nil {
		before := cluster.DeepCopy()
		cluster.Status.LastApplied = metav1.Now()
		cluster.Status.ActiveOn = append(cluster.Status.ActiveOn[:0], cluster.Spec.Mode...)
		if r.Engine != nil {
			cluster.Status.ParseError = r.Engine.ParseError(cluster.Spec.Name)
		}
		return r.Client.Status().Patch(ctx, cluster, client.MergeFrom(before))
	}
	ns := &crd.PortalRule{}
	if err := r.Client.Get(ctx, req.NamespacedName, ns); err != nil {
		return nil
	}
	before := ns.DeepCopy()
	ns.Status.LastApplied = metav1.Now()
	ns.Status.ActiveOn = append(ns.Status.ActiveOn[:0], ns.Spec.Mode...)
	if r.Engine != nil {
		ns.Status.ParseError = r.Engine.ParseError(ns.Spec.Name)
	}
	return r.Client.Status().Patch(ctx, ns, client.MergeFrom(before))
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
// internal/rule/v1alpha1.Reconciler so we don't duplicate the patch logic.
func (r *RuleReconciler) writeAllStatus(_ context.Context) error {
	// Intentionally a no-op shim in v1: per-CR status patching is owned by
	// internal/rule/v1alpha1.Reconciler, which runs in the same manager and is
	// triggered by the same CR change. We expose this method so future
	// audit-specific status (e.g. last-evaluated timestamp) can be added
	// without re-wiring the controller.
	return nil
}
