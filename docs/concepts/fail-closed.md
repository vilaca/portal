# Fail-closed semantics

Portal's `ValidatingWebhookConfiguration` ships with `failurePolicy: Fail` by default. If the webhook is unreachable, the API server **rejects** the request.

## Why fail closed

A misconfigured Portal that fails *open* is silently absent: workloads get admitted regardless of the policies you thought were in force. Fail-closed surfaces faults loudly — broken Portal → broken `kubectl apply`. The cluster operator finds out within minutes instead of months.

The trade-off: the webhook becomes part of the API server's critical path. That is why the chart **requires** ≥ 2 replicas + a PodDisruptionBudget (`minAvailable: 1`) when `failClosed: true`. See [../operator/ha-and-leader-election.md](../operator/ha-and-leader-election.md).

## Toggling

```yaml
# values.yaml
global:
  failClosed: false   # renders failurePolicy: Ignore
```

Set this if your team prefers podwatcher-poc's "if it dies, cluster is fine" property. You give up the loud-failure signal.

## System-namespace exclusion

The webhook's `namespaceSelector` **always** excludes:

- `kube-system`
- `kube-public`
- `kube-node-lease`
- Portal's own install namespace (`--install-namespace`, default `portal-system`)

This exclusion is unconditional, not driven by a Helm value, and applies regardless of `failClosed`. Without it, an in-cluster outage that took down Portal would also stop kubelet / scheduler / control-plane reconcilers from updating objects in those namespaces.

## Per-namespace bypass

For escape hatches outside the system namespaces, label/annotate the **namespace** (not the inbound object) with:

```text
portal.io/bypass=true
```

Requests inside that namespace short-circuit to `allowed=true` and bump `portal_admission_bypass_total{namespace}`. Setting the annotation requires `patch` on namespaces, which is normally a privileged-operator RBAC — by design.

## Recovery from self-lockout

If a misconfigured rule ever bricks admission for the wider cluster:

```bash
# Break-glass: delete the webhook config so the API server stops calling Portal.
kubectl delete validatingwebhookconfiguration portal.io
```

After unblocking, fix the offending rule, then `helm upgrade` re-creates the config. The chart README and [../operator/recovery-from-self-lockout.md](../operator/recovery-from-self-lockout.md) document the procedure.
