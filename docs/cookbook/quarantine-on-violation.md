# Quarantine on violation: label + AlertManager

Audit a privileged container, label the pod for downstream segregation, and page operators via AlertManager. One rule, two actions.

## Manifest

```yaml
apiVersion: portal.io/v1alpha1
kind: PortalClusterRule
metadata:
  name: privileged-quarantine
spec:
  name: privileged-quarantine
  enabled: true
  severity: critical
  mode: [audit]
  match:
    gvk:
      - {group: "", version: v1, kind: Pod}
  rule: container.securityContext.privileged == true
  actions:
    - type: label
      key: portal.security/quarantine
      value: "true"
    - type: alertmanager
      template: privileged-detected
      rateLimit: 1/hour
```

## Action behaviour

- `label` ([`internal/actions/label/action.go`](../../internal/actions/label/action.go)) applies `portal.security/quarantine=true` via server-side apply, field manager `portal`. Idempotent — re-running is a no-op. Default rate limit 5 s, overridden per-action above.
- `alertmanager` ([`internal/actions/alertmanager_action/action.go`](../../internal/actions/alertmanager_action/action.go)) routes the rule's `template:` shorthand through the AlertManager sink. Default rate limit 5 m; overridden to 1/h here so repeated audit fan-out from a noisy informer event doesn't flood the on-call channel.

Both actions share the dispatcher's idempotency cache: a second identical attempt on the same pod within the window is suppressed and counted as `portal_actions_total{result="duplicate"}`.

## RBAC

The `label` action requires `rbac.actions.label: true` in `values.yaml` so the chart grants `patch` on workload kinds — see [../reference/helm-values.md](../reference/helm-values.md). AlertManager has no in-cluster RBAC requirement; it makes an outbound HTTP call to `alertmanager.url`.

## Downstream segregation

A separate `NetworkPolicy` selecting `portal.security/quarantine=true` is the typical companion (apply with kustomize or via the `patch-networkpolicy` action). The label is your join key.
