package api

import "context"

// RuleLoader feeds parsed Rules into a central index. The folder loader walks
// a directory and watches it via fsnotify; the CR loader watches the two
// PortalClusterRule / PortalRule CRDs. Both produce the same Rule shape.
type RuleLoader interface {
	Name() string
	Start(ctx context.Context, onUpdate func(snapshot []Rule)) error
	Stop(ctx context.Context) error
}
