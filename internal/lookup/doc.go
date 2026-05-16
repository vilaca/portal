// Package lookup implements Portal's cross-resource lookup helpers exposed to
// rule expressions as `cluster.<gvk>.byName(ns, name)` and
// `cluster.<gvk>.list(ns, selector)`. Backed by audit's shared informer caches
// so reads do not incur extra API calls.
//
// Architecture (PLAN §"Phase 4 — Cross-resource lookups"):
//
//   - cluster.go         — api.Lookup over informer caches; ToExprEnv() builds
//                          the "cluster" and "consistentCluster" expr-lang
//                          environment values.
//   - depindex.go        — bounded LRU implementation of api.DepRecorder.
//                          Records (referenced → []DepEntry{rule, observed})
//                          edges so the reverse-dep loop can enqueue
//                          re-evaluation when a referenced object changes.
//   - cycle.go           — per (rule, object) sliding-window budget. Allow()
//                          returns false when the budget is exhausted; the
//                          caller is responsible for bumping the metric
//                          portal_lookup_cycle_suppressed_total and logging.
//   - virtual.go         — admission-time overlay: wraps an underlying
//                          api.Lookup so the inbound object materialises into
//                          the read path before reads hit the cache.
//
// AST helpers:
//
//   - ExtractClusterRefs(expression) walks an expr-lang AST and returns every
//     `cluster.<gvk>.byName/list(...)` reference as a schema.GroupVersionKind.
//     Wire-up uses this to compute the set of GVKs that need informers.
//
// Strong-consistency lookups:
//
//   - The expr env exposes a separate top-level "consistentCluster" key that
//     bypasses the informer cache and issues a live API call per lookup. This
//     is opt-in, slower (one round-trip per call), and intended for narrow
//     uses such as uniqueness checks at admission time.
package lookup
