package lookup

import (
	"container/list"
	"sync"

	"github.com/vilaca/portal/internal/api"
)

// DefaultDepIndexCapacity is the LRU bound when NewDepIndex receives <=0.
const DefaultDepIndexCapacity = 500_000

// depIndex implements api.DepRecorder with a bounded LRU.
//
// Keyed by api.ObjectRef (referenced object); each value is the slice of
// (rule, observed) edges that read it. On eviction (least-recently-recorded)
// the next informer event or 10-min resync covers correctness — recorded
// edges are advisory.
type depIndex struct {
	mu       sync.Mutex
	maxItems int
	entries  map[api.ObjectRef]*list.Element // -> *depBucket
	order    *list.List                      // front = newest
}

type depBucket struct {
	ref     api.ObjectRef
	entries []api.DepEntry
}

// NewDepIndex constructs an api.DepRecorder bounded to maxEntries keys.
// maxEntries <= 0 selects DefaultDepIndexCapacity.
func NewDepIndex(maxEntries int) api.DepRecorder {
	if maxEntries <= 0 {
		maxEntries = DefaultDepIndexCapacity
	}
	return &depIndex{
		maxItems: maxEntries,
		entries:  make(map[api.ObjectRef]*list.Element),
		order:    list.New(),
	}
}

// Record appends a (rule, observed) edge under the referenced key and marks
// the key MRU. If the index is at capacity, the LRU key is evicted.
func (d *depIndex) Record(rule string, observed, referenced api.ObjectRef) {
	d.mu.Lock()
	defer d.mu.Unlock()
	entry := api.DepEntry{Rule: rule, Observed: observed}
	if el, ok := d.entries[referenced]; ok {
		b := el.Value.(*depBucket)
		// Deduplicate: if (rule, observed) already present skip append.
		for _, e := range b.entries {
			if e.Rule == rule && e.Observed == observed {
				d.order.MoveToFront(el)
				return
			}
		}
		b.entries = append(b.entries, entry)
		d.order.MoveToFront(el)
		return
	}
	b := &depBucket{ref: referenced, entries: []api.DepEntry{entry}}
	el := d.order.PushFront(b)
	d.entries[referenced] = el
	if d.order.Len() > d.maxItems {
		oldest := d.order.Back()
		if oldest != nil {
			ob := oldest.Value.(*depBucket)
			d.order.Remove(oldest)
			delete(d.entries, ob.ref)
		}
	}
}

// Dependents returns the slice of (rule, observed) edges that depend on
// referenced. The returned slice is a snapshot — safe to retain.
func (d *depIndex) Dependents(referenced api.ObjectRef) []api.DepEntry {
	d.mu.Lock()
	defer d.mu.Unlock()
	el, ok := d.entries[referenced]
	if !ok {
		return nil
	}
	d.order.MoveToFront(el)
	b := el.Value.(*depBucket)
	out := make([]api.DepEntry, len(b.entries))
	copy(out, b.entries)
	return out
}
