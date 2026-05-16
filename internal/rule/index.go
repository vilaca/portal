// Package rule provides the in-memory rule index used by the engine.
//
// The index is a thread-safe concrete api.RuleIndex implementation. Loaders
// (folder, CR) push complete snapshots via Replace; readers (engine,
// dependency extractor) read via ForGVK / All.
package rule

import (
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vilaca/portal/internal/api"
)

// Index is a thread-safe api.RuleIndex implementation backed by a flat slice
// and a GVK-keyed map. Replace atomically swaps both representations under a
// write lock. ForGVK and All return copies of the internal slices to be safe
// against concurrent mutation by callers.
type Index struct {
	mu      sync.RWMutex
	all     []api.Rule
	byGVK   map[schema.GroupVersionKind][]api.Rule
}

// NewIndex returns an empty Index.
func NewIndex() *Index {
	return &Index{
		byGVK: make(map[schema.GroupVersionKind][]api.Rule),
	}
}

// Replace atomically swaps the index contents with the supplied snapshot.
// Disabled rules are dropped (api.RuleIndex contract: only enabled rules are
// visible). The snapshot is shallow-copied so the caller can keep using its
// slice without affecting the index.
func (i *Index) Replace(snapshot []api.Rule) {
	// Filter out disabled rules and build the GVK map outside the lock so the
	// critical section is just two pointer swaps.
	enabled := make([]api.Rule, 0, len(snapshot))
	byGVK := make(map[schema.GroupVersionKind][]api.Rule)
	for _, r := range snapshot {
		if !r.Enabled {
			continue
		}
		enabled = append(enabled, r)
		for _, gvk := range r.Match.GVK {
			byGVK[gvk] = append(byGVK[gvk], r)
		}
	}

	i.mu.Lock()
	i.all = enabled
	i.byGVK = byGVK
	i.mu.Unlock()
}

// ForGVK returns a copy of the rules registered for gvk.
func (i *Index) ForGVK(gvk schema.GroupVersionKind) []api.Rule {
	i.mu.RLock()
	defer i.mu.RUnlock()
	src := i.byGVK[gvk]
	if len(src) == 0 {
		return nil
	}
	out := make([]api.Rule, len(src))
	copy(out, src)
	return out
}

// All returns a copy of every enabled rule.
func (i *Index) All() []api.Rule {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if len(i.all) == 0 {
		return nil
	}
	out := make([]api.Rule, len(i.all))
	copy(out, i.all)
	return out
}

// compile-time assertion that *Index implements api.RuleIndex.
var _ api.RuleIndex = (*Index)(nil)
