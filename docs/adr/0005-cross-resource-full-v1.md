# ADR 0005 — Full cross-resource lookups in v1

**Status.** Accepted, implemented in v1.

## Context

A privileged-container check is simple — `container.securityContext.privileged == true`. Real security policies often need cross-resource reasoning: "every `Deployment` in production must have at least one `PodDisruptionBudget` selecting it", "no `Service` of type `LoadBalancer` may exist in a namespace marked `internal`", "every `Pod` must be selected by a default-deny `NetworkPolicy`".

These rules are impossible to express against `object.*` alone — they require querying the rest of the cluster.

The early PLAN draft scoped cross-resource lookups to v2 (informer-based, but layered on top of the v1 admission/audit pipeline). The final PLAN promotes it to v1.

## Decision

**Cross-resource is in v1.** `cluster.<gvk>.byName(ns, name)` and `cluster.<gvk>.list(ns, selector)` are first-class expression-language bindings, backed by `internal/audit`'s shared informer caches, with a reverse-dependency index re-evaluating dependent rules when referenced resources change.

The implementation:

- `internal/lookup/cluster.go` — the helper bindings.
- `internal/lookup/depindex.go` — the reverse-dependency index (bounded LRU, default 500 k entries).
- `internal/lookup/virtual.go` — admission-time virtual cluster view (inbound object materialised into the read path so admission rules see the soon-to-be cluster state).

## Rationale

- **It's the single biggest expressiveness win for security rules.** Without it, Portal is "another podwatcher-poc with admission". With it, Portal covers PDB-coverage, allowed-registries-via-namespace-allowlist, dependent-resource checks — the things real policy teams want.
- **The plumbing is already there.** v1 ships informers for audit. Exposing the caches behind a helper API is incremental, not a new subsystem. Skipping it to v2 would mean shipping v1 with the informers idle for the lookup module's purpose.
- **Per-rule engine selection (v3) needs a stable env shape.** If `cluster.*` lands in v2, every rule that uses it has to be re-validated when v3 changes the env. Landing it in v1 freezes the shape early.

## Cost we accept

- **Reverse-dependency index complexity.** When rule R evaluates object O and reads X, we record `(referenced=X) → depends=(R,O)`. On informer events for X, every `(R, O)` dependent is re-enqueued for evaluation. This:
  - Adds a bounded LRU sized to 500 k entries by default. Per `internal/lookup/depindex.go` — capacity is configurable.
  - Bounds the cluster-wide blast radius of a single resource change. A `Secret` update touching 10 k dependents would otherwise pile-drive the work queue.
- **Cycle protection.** A rule that re-derives its own dependency on every eval would loop forever. The mitigation: per `(rule, object)` pair, allow at most N (default 3) re-evals in a sliding W-second (default 10s) window. Excess increments `portal_lookup_cycle_suppressed_total` and is captured in the audit log. Correctness preserved by the 10-minute resync safety net.
- **Admission-time consistency.** The inbound object isn't in the informer cache yet (CREATE), or is stale (UPDATE). `internal/lookup/virtual.go` provides a per-request overlay that materialises the inbound object before reads. For rules that need stricter semantics — e.g. cluster-wide uniqueness — `cluster.consistent.<gvk>.byName(...)` bypasses the cache and does a direct API call (one round-trip added; opt-in).
- **RBAC widening.** The informer needs `get,list,watch` on every GVK referenced by `cluster.*`. The chart computes this from the `watchedGvks` Helm value plus a startup pass over the loaded rule corpus.

## Alternative considered — defer to v2

- **Pro.** v1 ships sooner; the dep-index + cycle protection + virtual view are all non-trivial.
- **Con.** Every cross-resource policy a user wants in v1 has to be implemented externally (a different tool, or a one-off controller). The wedge against Kyverno weakens — Kyverno has cross-resource lookups today.
- **Decision.** The complexity is one-time engineering; the user-visible payoff is permanent. Land it now.

## Consequences

- The interface seam (`api.Lookup`, `api.DepRecorder` in `internal/api/lookup.go`) is part of v1's public surface. Refactoring it requires CR migration. We took care to land it once.
- Tests under `internal/lookup/` cover the dep-index, cycle protection, and the virtual view. The latency budget for cross-resource lookups is "cache read = nanoseconds; consistent path = single API round-trip" — see `docs/concepts/cross-resource.md` (parallel author) for the user-facing semantics.
- The `cluster.consistent.<gvk>.*` opt-in is documented as a power tool: use only when correctness demands it.
