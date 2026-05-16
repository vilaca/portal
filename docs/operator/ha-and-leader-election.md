# High availability and leader election

Portal v1 is a fail-closed admission controller. If admission goes silent the API server starts rejecting requests in audited namespaces (the explicit design choice — see `../adr/0003-fail-closed-default.md`). HA is therefore not optional.

## Minimum topology

- **`replicaCount: 2`** in `values.yaml`. The Helm chart enforces this default.
- **`PodDisruptionBudget` with `minAvailable: 1`**, shipped in `deploy/helm/portal/templates/poddisruptionbudget.yaml`. Together with `replicaCount: 2`, this guarantees a node drain can never take Portal to zero ready replicas.
- **Anti-affinity** spreads replicas across nodes when available — preferred when there are <2 schedulable nodes, required otherwise.

For three or more replicas, set `replicaCount: 3` and consider raising `minAvailable: 2` if the cluster has the spare capacity.

## What every replica does

Both replicas serve the admission webhook concurrently — `kube-apiserver` load-balances across endpoints, so admission throughput scales horizontally.

When `--audit` is enabled, **informers run on every replica** for cache warmth and fast failover. The shared informer factory keeps the watch cache hot, so when leadership transfers there's no cold-start cost.

What only the **leader** does:
- Dispatches `Action`s (label, evict, etc.).
- Emits to remote sinks that must not be double-written (PolicyReport CRD, AlertManager).
- Writes `.status` updates back to `PortalClusterRule` / `PortalRule`.

This split is enforced in `internal/audit/controller.go`: workers `continue` past `isLeader.Load() == false` before doing anything externally visible, while the informer event handlers still enqueue items so leadership transfer is instant.

## Leader-election parameters

Lease-based, via `client-go/tools/leaderelection`. From `internal/audit/controller.go`:

```go
DefaultLeaseDuration = 15 * time.Second
DefaultRenewDeadline = 10 * time.Second
DefaultRetryPeriod   = 2  * time.Second
DefaultLeaseLockName = "portal-leader"
```

Lease resource: a `coordination.k8s.io/v1.Lease` named `portal-leader` in the Portal install namespace. Inspect it:

```bash
kubectl get lease portal-leader -n portal-system -o yaml
```

The `holderIdentity` is the pod hostname (or `portal-<pid>` when hostname is empty).

## Rolling-restart behaviour

`helm upgrade portal ...` triggers a rolling restart. The sequence (assuming two replicas):

1. New replica `B'` is created. Webhook stays live on the existing leader `A`.
2. `A` is terminated. Pod shutdown calls `Stop()` on the audit controller, which cancels the leader-election goroutine; `leaderelection.RunOrDie` releases the lease (`ReleaseOnCancel: true`).
3. Replica `B` or `B'` picks up the lease within ~15 s (`LeaseDuration`).
4. New leader's informer caches are already warm — it starts dispatching actions immediately.

**No duplicate alerts.** The action dispatcher's `IdempotencyStore` (in-memory LRU keyed by `sha256(rule, gvk, namespace, name, actionType)`) deduplicates re-emissions inside the action's idempotency window — see `internal/api/action.go`. The TTL covers the leader-transfer gap.

## Failure scenarios

- **Both replicas down** — webhook is absent, fail-closed posture means non-excluded namespaces get rejected. System namespaces (`kube-system`, `kube-public`, `kube-node-lease`) and the Portal install namespace are unaffected because they're in the `namespaceSelector` exclusion list. See `recovery-from-self-lockout.md`.
- **Partition that prevents lease acquisition** — neither replica believes it leads; admission still works (no leader required for admission decisions), audit actions stall. Recovery: heal the partition, leader is elected within `LeaseDuration`.
- **One replica wedged but live (e.g. OOM looping)** — readiness probe (`/readyz`) returns 503 when the admission error ring buffer is more than 50% errors over the last 100 requests (`internal/admission/handler.go`), and the Service drops the endpoint. The healthy replica absorbs traffic.
