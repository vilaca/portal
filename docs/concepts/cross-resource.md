# Cross-resource lookups

Portal exposes the cluster's informer caches to expr-lang via two namespaces:

| Expression | Backed by | Cost |
|------------|-----------|------|
| `cluster.<gvk>.byName(ns, name)` | informer cache | µs per lookup |
| `cluster.<gvk>.list(ns, selector)` | informer cache | filtered cache walk |
| `consistentCluster.<gvk>.byName(ns, name)` | direct API call | one round-trip; opt-in |

Implementations live in [`internal/lookup/`](../../internal/lookup/) and are registered into the expr-lang env at engine construction time.

`<gvk>` is the lowercase-plural resource name — `cluster.pods.byName(...)`, `cluster.deployments.list(...)`, `cluster.poddisruptionbudgets.list(...)`.

## Reverse-dependency index

When rule `R` evaluates object `O` and the lookup helper returns resource `X`, Portal records `(referenced=X) → depends=(R, O)`. On any informer event for `X`, every dependent `(R, O)` pair is re-enqueued through the same worker pool that handles audit events. Stored in a bounded LRU (default 500 k entries) — eviction is corrected by the next watch event or the 10-min resync.

## Cycle protection

Per `(rule, object)` pair, Portal allows **at most 3** re-evaluations in any sliding **10-second** window. Excess re-evals are dropped and counted via `portal_lookup_cycle_suppressed_total`. The cycle metric is emitted alongside an audit-log line naming the rule and object so the loop is debuggable. Long-term correctness is preserved by the periodic resync.

## Admission virtual cluster view

At admission time the inbound object is not yet in the informer cache (CREATE) or is stale (UPDATE / DELETE). The lookup helper wraps the cache with a per-request overlay that materialises the inbound `request.object` before reads. So a rule like:

```text
cluster.deployments.byName(object.metadata.namespace, object.metadata.name) != nil
```

sees the about-to-be-created Deployment during the admission decision for that same Deployment.

For stronger guarantees (e.g. uniqueness checks) use `consistentCluster.<gvk>.byName(...)` — this bypasses the cache and does a direct API call. Slower; opt-in.

## RBAC

Informers need `get, list, watch` on every GVK any rule's `cluster.<gvk>.*` call references. The Helm chart computes the set from the audited rule corpus and lets operators extend it via [`watchedGvks`](../reference/helm-values.md) in `values.yaml`. See [../operator/rbac-scoping.md](../operator/rbac-scoping.md).
