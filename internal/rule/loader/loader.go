// Package loader provides RuleLoader implementations for the two rule sources
// (folder, CR) and a composite that merges multiple loaders into a single
// onUpdate stream.
package loader

import (
	"context"
	"sync"

	"github.com/vilaca/portal/internal/api"
)

// Multi is a composite api.RuleLoader: it fans Start out to N child loaders
// and merges their latest snapshots into a single onUpdate callback. The
// merged list is recomputed every time any child fires.
type Multi struct {
	children []api.RuleLoader

	mu        sync.Mutex
	snapshots map[string][]api.Rule // last snapshot per child, keyed by loader.Name()
}

// NewMulti returns a composite loader. Order of children determines the
// emission order in the merged snapshot.
func NewMulti(loaders ...api.RuleLoader) api.RuleLoader {
	return &Multi{
		children:  loaders,
		snapshots: make(map[string][]api.Rule),
	}
}

// Name returns "multi". The composite has no inherent source identity.
func (m *Multi) Name() string { return "multi" }

// Start invokes Start on each child loader, intercepting their callbacks to
// remember the latest snapshot per child and re-emit the merged union.
func (m *Multi) Start(ctx context.Context, onUpdate func(snapshot []api.Rule)) error {
	for _, child := range m.children {
		name := child.Name()
		c := child
		err := c.Start(ctx, func(snap []api.Rule) {
			merged := m.recordAndMerge(name, snap)
			onUpdate(merged)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Stop invokes Stop on each child loader. The first error wins; remaining
// children are still asked to stop.
func (m *Multi) Stop(ctx context.Context) error {
	var firstErr error
	for _, child := range m.children {
		if err := child.Stop(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// recordAndMerge stores snap as the latest snapshot for name and returns the
// union of every child's last snapshot.
func (m *Multi) recordAndMerge(name string, snap []api.Rule) []api.Rule {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Defensive copy so subsequent caller mutations don't poison the cache.
	cp := make([]api.Rule, len(snap))
	copy(cp, snap)
	m.snapshots[name] = cp

	total := 0
	for _, child := range m.children {
		total += len(m.snapshots[child.Name()])
	}
	merged := make([]api.Rule, 0, total)
	for _, child := range m.children {
		merged = append(merged, m.snapshots[child.Name()]...)
	}
	return merged
}
