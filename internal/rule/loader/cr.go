package loader

import (
	"context"

	"github.com/vilaca/portal/internal/api"
)

// ClusterClient is an opaque marker interface for the CR loader's K8s
// dependency. In Wave 2 this will become an alias for kubernetes.Interface
// from k8s.io/client-go; for now it's empty so this package has no client-go
// dependency. Anything (including nil) satisfies it.
type ClusterClient interface{}

// cr is a scaffold-only RuleLoader for the CR informer source.
//
// TODO(wave2): real implementation needs the audit module's shared informer
// cache wiring. Until then Start returns nil immediately without producing
// any rules — the folder loader carries the full load.
type cr struct {
	client ClusterClient
}

// NewCR returns a no-op RuleLoader stub for PortalClusterRule / PortalRule.
//
// TODO(wave2): replace with an informer-driven implementation that emits
// rules whenever the two CRDs change.
func NewCR(client ClusterClient) api.RuleLoader {
	return &cr{client: client}
}

// Name returns "cr".
func (c *cr) Name() string { return "cr" }

// Start is a no-op in Wave 1: returns nil immediately without invoking
// onUpdate.
//
// TODO(wave2): wire to the shared informer factory in internal/audit.
func (c *cr) Start(_ context.Context, _ func(snapshot []api.Rule)) error {
	return nil
}

// Stop is a no-op.
func (c *cr) Stop(_ context.Context) error { return nil }
