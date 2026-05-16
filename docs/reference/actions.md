# Built-in actions

Every action implements [`api.Action`](../../internal/api/action.go):

```go
type Action interface {
    Type() string
    Execute(ctx context.Context, v Violation, params map[string]any) error
    Idempotent() bool
    DefaultRateLimit() time.Duration
}
```

Default rate-limit windows and idempotency policy live with each implementation; rate-limit syntax for per-rule overrides is documented in [../concepts/actions-and-rate-limiting.md](../concepts/actions-and-rate-limiting.md).

## `alertmanager`

Implementation: [`internal/actions/alertmanager_action/action.go`](../../internal/actions/alertmanager_action/action.go).

Wraps the AlertManager `OutputSink` so the rule's `alert:` shorthand and explicit `actions: [{type: alertmanager, ...}]` flow through the same dispatcher (idempotency + rate limit).

| Property | Value |
|----------|-------|
| `Idempotent()` | `true` |
| `DefaultRateLimit()` | `5m` |
| RBAC | none in-cluster — outbound HTTP to the configured AlertManager URL |

```yaml
actions:
  - type: alertmanager
    template: insecure-workload
```

## `label`

Implementation: [`internal/actions/label/action.go`](../../internal/actions/label/action.go). Server-side apply with field manager `portal`.

| Property | Value |
|----------|-------|
| `Idempotent()` | `true` |
| `DefaultRateLimit()` | `5s` |
| RBAC | `patch` on the targeted workload kind (gated by `rbac.actions.label`) |
| Params | `key` (required), `value` (default `"true"`) |

```yaml
actions:
  - type: label
    key: portal.security/quarantine
    value: "true"
```

## `annotate`

Implementation: [`internal/actions/annotate/action.go`](../../internal/actions/annotate/action.go). Same SSA mechanic as `label`, targeting `metadata.annotations`.

| Property | Value |
|----------|-------|
| `Idempotent()` | `true` |
| `DefaultRateLimit()` | `5s` |
| RBAC | `patch` on the targeted workload kind (gated by `rbac.actions.annotate`) |
| Params | `key` (required), `value` (default `"true"`) |

```yaml
actions:
  - type: annotate
    key: portal.io/last-violation
    value: "2026-05-16T00:00:00Z"
```

## `evict`

Implementation: [`internal/actions/evict/action.go`](../../internal/actions/evict/action.go). Issues `policy/v1.Eviction` on the violating pod.

| Property | Value |
|----------|-------|
| `Idempotent()` | `false` |
| `DefaultRateLimit()` | `30s` |
| RBAC | `create` on `pods/eviction` (gated by `rbac.actions.evict`) |
| Params | none |

```yaml
actions:
  - type: evict
    on: [audit]
    rateLimit: 5/min
```

## `patch-networkpolicy`

Implementation: [`internal/actions/patchnp/action.go`](../../internal/actions/patchnp/action.go). Server-side apply on `networkpolicies.networking.k8s.io/v1`.

| Property | Value |
|----------|-------|
| `Idempotent()` | `true` |
| `DefaultRateLimit()` | `30s` |
| RBAC | `patch` on `networkpolicies.networking.k8s.io` (gated by `rbac.actions.patchnp`) |
| Params | `patch` (required, map) — the SSA payload to merge. `targetName`, `targetNamespace` — override the target when the violation's own GVK is not a NetworkPolicy. |

```yaml
actions:
  - type: patch-networkpolicy
    targetNamespace: production
    targetName: default-deny
    patch:
      spec:
        policyTypes: [Ingress, Egress]
```

## `revoke-sa-token`

Implementation: [`internal/actions/revoketoken/action.go`](../../internal/actions/revoketoken/action.go). Deletes the ServiceAccount token Secret, forcing rotation.

| Property | Value |
|----------|-------|
| `Idempotent()` | `false` |
| `DefaultRateLimit()` | `60s` |
| RBAC | `delete` on `secrets` (gated by `rbac.actions.revoketoken`) |
| Params | (resolves the SA from the violating object's `spec.serviceAccountName`) |

```yaml
actions:
  - type: revoke-sa-token
    on: [runtime]      # v2; see PLAN.md
```
