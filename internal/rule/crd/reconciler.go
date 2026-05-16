package crd

import "context"

// Reconciler is a scaffold-only stand-in for the Wave 2 controller-runtime
// reconciler that will write .status back to each PortalClusterRule /
// PortalRule. In Wave 1 it satisfies a Reconcile method shape but performs
// no work — the rest of the controller-runtime manager wiring lands with
// the audit module.
//
// TODO(wave2): replace with a controller-runtime reconcile.Reconciler that
// uses a token-bucketed status writer (the API-server hammer guard from the
// design) to update .status.{evalCount,violationCount,lastApplied,parseError,
// activeOn} from engine metrics.
type Reconciler struct{}

// Reconcile is a no-op in Wave 1.
//
// TODO(wave2): implement.
func (r *Reconciler) Reconcile(_ context.Context, _ any) error {
	return nil
}
