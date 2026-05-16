# Detect missing default-deny NetworkPolicy

Portal ships a built-in NetworkPolicy analyser. `np.default-deny-missing` ([`internal/network/checks.go`](../../internal/network/checks.go)) reports any namespace that has pods but no NetworkPolicy selecting them with an empty ingress rule.

## Enable the layer

```yaml
# values.yaml
audit:
  enabled: true
network:
  enabled: true
```

`--network` implies `--audit` because the analyser builds its pod→NP graph from the audit informer caches (no extra API calls).

## What the check does

The check ([`checkDefaultDenyMissing` in `internal/network/checks.go`](../../internal/network/checks.go)) walks each namespace's pod set. For each pod, it asks: is there a `NetworkPolicy` in this namespace whose `podSelector` matches this pod and whose `ingress: []` (i.e. default deny)? If no, the namespace produces a synthetic `Violation` with `rule = "np.default-deny-missing"`. The finding flows through the regular pipeline: PolicyReport entry, AlertManager alert (if configured), `portal_np_findings{check="np.default-deny-missing"}` increment.

## Auto-resolve

The analyser is fully event-driven. On any `OnAdd`/`OnUpdate`/`OnDelete` for `Pod`, `NetworkPolicy`, or `Namespace`, affected namespaces are re-evaluated. When you add a default-deny NetworkPolicy:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: default-deny
  namespace: production
spec:
  podSelector: {}
  policyTypes: [Ingress]
  ingress: []
```

the finding clears within ~1 s (informer event — no waiting for the 10-min resync). AlertManager alerts auto-resolve via the existing `endsAt` mechanism inherited from podwatcher-poc.

## Test

```bash
# Create a noisy namespace.
kubectl create namespace netpol-demo
kubectl -n netpol-demo run web --image=nginx
# Expect: np.default-deny-missing fires for namespace netpol-demo

# Apply default-deny.
kubectl apply -f default-deny.yaml
# Expect: finding clears within ~1 s; portal_np_findings stops climbing.
```

## Companion checks

The analyser also ships `np.broad-cidr`, `np.unreachable-selector`, and `np.policy-without-targets` ([`internal/network/checks.go`](../../internal/network/checks.go)). All four emit through the same pipeline with `gvk: NetworkPolicy` or `gvk: Namespace`.
