// Package loader's CR implementation watches PortalClusterRule and PortalRule
// CRs via a controller-runtime manager/cache and pushes a merged Rule
// snapshot to onUpdate whenever any of them changes.
//
// The loader does not own its manager. Callers (cmd/portal wire-up) own a
// single shared controller-runtime manager (it also runs the audit
// reconcilers + status reconciler) and pass it in via NewCR. Start adds a
// controller to the manager that translates List() of both CR kinds into a
// merged []api.Rule.
package loader

import (
	"context"
	"sort"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vilaca/portal/internal/api"
	crd "github.com/vilaca/portal/internal/rule/v1alpha1"
)

// cr is the controller-runtime-backed RuleLoader.
type cr struct {
	mgr    manager.Manager
	client client.Client

	mu       sync.Mutex
	onUpdate func([]api.Rule)
}

// NewCRFromClient builds a cr loader from a raw client.Client. The
// returned loader's Start is a no-op for controller registration (no
// manager) — it does one immediate snapshot + onUpdate and exposes a
// public Trigger method so tests can simulate change events without a
// real informer cache.
//
// Production wire-up uses NewCR(mgr); this entrypoint exists for fake
// client tests.
func NewCRFromClient(c client.Client) *CR {
	return &CR{cr: cr{client: c}}
}

// CR is the exported wrapper produced by NewCRFromClient so tests can
// call Trigger().
type CR struct{ cr }

// Trigger forces a snapshot + onUpdate emission. Returns the snapshot
// for test assertions.
func (c *CR) Trigger(ctx context.Context) ([]api.Rule, error) {
	rules, err := c.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	cb := c.onUpdate
	c.mu.Unlock()
	if cb != nil {
		cb(rules)
	}
	return rules, nil
}

// SetOnUpdate replaces the callback (test helper).
func (c *CR) SetOnUpdate(fn func([]api.Rule)) {
	c.mu.Lock()
	c.onUpdate = fn
	c.mu.Unlock()
}

// NewCR returns a RuleLoader that watches PortalClusterRule + PortalRule
// CRs through mgr. The caller is responsible for starting mgr; Start
// registers controllers but does not block. The supplied manager's scheme
// MUST already include crd.AddToScheme; NewCR will install it if missing.
func NewCR(mgr manager.Manager) (api.RuleLoader, error) {
	if mgr == nil {
		return nil, errNilManager
	}
	if err := crd.AddToScheme(mgr.GetScheme()); err != nil {
		return nil, err
	}
	return &cr{mgr: mgr, client: mgr.GetClient()}, nil
}

// errNilManager is a sentinel for tests.
var errNilManager = &loaderError{msg: "loader.NewCR: nil manager"}

type loaderError struct{ msg string }

func (e *loaderError) Error() string { return e.msg }

// Name returns "cr".
func (c *cr) Name() string { return "cr" }

// Start wires the controllers and stores onUpdate. The first emission
// happens after the cache syncs and the first Reconcile fires; for tests
// that need an immediate snapshot, see snapshot() below.
//
// Start does NOT block. The caller's manager is what runs the controllers.
func (c *cr) Start(ctx context.Context, onUpdate func(snapshot []api.Rule)) error {
	c.mu.Lock()
	c.onUpdate = onUpdate
	c.mu.Unlock()

	// One controller per kind, both feeding the same emit() path.
	if err := builder.ControllerManagedBy(c.mgr).
		For(&crd.PortalClusterRule{}).
		Named("portal-clusterrule-loader").
		Complete(reconcile.Func(c.reconcileCR)); err != nil {
		return err
	}
	if err := builder.ControllerManagedBy(c.mgr).
		For(&crd.PortalRule{}).
		Named("portal-rule-loader").
		Complete(reconcile.Func(c.reconcileCR)); err != nil {
		return err
	}
	return nil
}

// Stop is a no-op; the underlying manager controls controller lifecycles.
func (c *cr) Stop(_ context.Context) error { return nil }

// reconcileCR fires for every CR change. We re-list both kinds and emit the
// merged snapshot. This is simpler than incremental diff and correct for
// modest CR counts; the engine.Replace path already does the same on its
// side.
func (c *cr) reconcileCR(ctx context.Context, _ reconcile.Request) (reconcile.Result, error) {
	rules, err := c.snapshot(ctx)
	if err != nil {
		return reconcile.Result{}, err
	}
	c.mu.Lock()
	cb := c.onUpdate
	c.mu.Unlock()
	if cb != nil {
		cb(rules)
	}
	return reconcile.Result{}, nil
}

// snapshot lists both kinds via the cached client and returns the merged
// canonical rule slice.
func (c *cr) snapshot(ctx context.Context) ([]api.Rule, error) {
	var clusterList crd.PortalClusterRuleList
	if err := c.client.List(ctx, &clusterList); err != nil {
		return nil, err
	}
	var nsList crd.PortalRuleList
	if err := c.client.List(ctx, &nsList); err != nil {
		return nil, err
	}

	out := make([]api.Rule, 0, len(clusterList.Items)+len(nsList.Items))
	for i := range clusterList.Items {
		out = append(out, crd.PortalClusterRuleSpecToRule(clusterList.Items[i].Spec, clusterList.Items[i].ObjectMeta))
	}
	for i := range nsList.Items {
		out = append(out, crd.PortalRuleSpecToRule(nsList.Items[i].Spec, nsList.Items[i].ObjectMeta))
	}
	// Deterministic order: cluster first by UID, then namespaced by ns/name.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Source.Origin != out[j].Source.Origin {
			return out[i].Source.Origin < out[j].Source.Origin
		}
		return out[i].Source.Path < out[j].Source.Path
	})
	return out, nil
}
