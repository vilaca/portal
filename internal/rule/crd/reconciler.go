// Package crd's reconciler updates .status on PortalClusterRule / PortalRule
// CRs to reflect the rule engine's view of each rule:
//
//   - .status.parseError: engine.ParseError(name); empty when the rule
//     compiled cleanly.
//   - .status.lastApplied: metav1.Now() on every reconcile pass.
//   - .status.activeOn: copy of spec.mode (admission/audit/network).
//   - .status.evalCount / .status.violationCount: live counters incremented
//     by the audit controller via IncEval / IncViolation below; reconcile
//     copies the current value through to the CR.
//
// The audit controller increments the counters via the package-level helpers
// IncEval(name) / IncViolation(name) so this package owns the source of truth.
// SetupWithManager registers the reconciler with both kinds. Reconcile is
// rate-limited externally via controller-runtime's workqueue + the engine's
// own status writer in internal/audit; this reconciler is unrate-limited
// because controller-runtime backs the kinds with exponential backoff.

package crd

import (
	"context"
	"sync"
	"sync/atomic"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ParseErrorSource is the surface this reconciler asks for parse errors.
// The engine's *dispatcher type satisfies it.
type ParseErrorSource interface {
	ParseError(rule string) string
}

// --- package-level counters ---------------------------------------------

var counters sync.Map // rule name -> *ruleCounters

type ruleCounters struct {
	eval       atomic.Int64
	violations atomic.Int64
}

func getOrCreate(name string) *ruleCounters {
	if v, ok := counters.Load(name); ok {
		return v.(*ruleCounters)
	}
	c := &ruleCounters{}
	actual, _ := counters.LoadOrStore(name, c)
	return actual.(*ruleCounters)
}

// IncEval increments the eval counter for rule name.
func IncEval(name string) { getOrCreate(name).eval.Add(1) }

// IncViolation increments the violation counter for rule name.
func IncViolation(name string) { getOrCreate(name).violations.Add(1) }

// EvalCount returns the current eval count for rule name.
func EvalCount(name string) int64 {
	if v, ok := counters.Load(name); ok {
		return v.(*ruleCounters).eval.Load()
	}
	return 0
}

// ViolationCount returns the current violation count for rule name.
func ViolationCount(name string) int64 {
	if v, ok := counters.Load(name); ok {
		return v.(*ruleCounters).violations.Load()
	}
	return 0
}

// ResetCounters clears all counters (test helper).
func ResetCounters() {
	counters.Range(func(k, _ any) bool {
		counters.Delete(k)
		return true
	})
}

// --- reconciler ---------------------------------------------------------

// Reconciler updates .status on PortalClusterRule / PortalRule.
type Reconciler struct {
	Client client.Client
	Engine ParseErrorSource

	// Namespaced is true when this reconciler watches PortalRule, false for
	// PortalClusterRule. SetupWithManager creates one Reconciler of each.
	Namespaced bool
}

// SetupWithManager registers one reconciler per CR kind on mgr.
func SetupWithManager(mgr manager.Manager, eng ParseErrorSource) error {
	if err := (&Reconciler{Client: mgr.GetClient(), Engine: eng, Namespaced: false}).register(mgr); err != nil {
		return err
	}
	return (&Reconciler{Client: mgr.GetClient(), Engine: eng, Namespaced: true}).register(mgr)
}

func (r *Reconciler) register(mgr manager.Manager) error {
	b := builder.ControllerManagedBy(mgr)
	if r.Namespaced {
		b = b.For(&PortalRule{}).Named("portalrule-status")
	} else {
		b = b.For(&PortalClusterRule{}).Named("portalclusterrule-status")
	}
	return b.Complete(r)
}

// Reconcile reads the CR, computes a fresh status, and patches the status
// sub-resource. Missing objects are a no-op (controllerutil handles it).
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	if r.Namespaced {
		return r.reconcileNamespaced(ctx, req)
	}
	return r.reconcileCluster(ctx, req)
}

func (r *Reconciler) reconcileCluster(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	obj := &PortalClusterRule{}
	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	return r.writeStatus(ctx, obj, &obj.Spec, &obj.Status)
}

func (r *Reconciler) reconcileNamespaced(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	obj := &PortalRule{}
	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	return r.writeStatus(ctx, obj, &obj.Spec, &obj.Status)
}

// writeStatus is the shared path. It computes the next status, patches if
// changed, and returns. obj is typed (PortalClusterRule|PortalRule), spec
// and status are pointers into obj so the patch sees the mutation.
func (r *Reconciler) writeStatus(ctx context.Context, obj client.Object, spec *RuleSpec, status *RuleStatus) (reconcile.Result, error) {
	before := obj.DeepCopyObject().(client.Object)

	if r.Engine != nil {
		status.ParseError = r.Engine.ParseError(spec.Name)
	}
	status.LastApplied = metav1.Now()
	if spec.Mode != nil {
		status.ActiveOn = append(status.ActiveOn[:0], spec.Mode...)
	} else {
		status.ActiveOn = nil
	}
	status.EvalCount = EvalCount(spec.Name)
	status.ViolationCount = ViolationCount(spec.Name)

	// MergeFrom for status sub-resource; client.Status() routes to the
	// status endpoint when the CRD has subresource:status enabled. The fake
	// client ignores the subresource distinction.
	if err := r.Client.Status().Patch(ctx, obj, client.MergeFrom(before)); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

// compile-time assertions.
var (
	_ reconcile.Reconciler = (*Reconciler)(nil)
	_ client.Object        = (*PortalClusterRule)(nil)
	_ client.Object        = (*PortalRule)(nil)
)
