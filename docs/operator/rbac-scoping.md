# RBAC scoping

Portal does **not** need cluster-admin. Its `ClusterRole` is composed of two pieces:

1. **Base permissions** — always required when the chart is installed.
2. **Conditional action permissions** — gated by `rbac.actions.<action>` Helm values; opt in only to actions you actually enable.

The chart templates this layout in `deploy/helm/portal/templates/clusterrole.yaml`.

## Base permissions

When `rbac.create: true` (default):

- `get,list,watch` on every audited GVK. The set is the union of:
  - GVKs Portal watches by default (Pods, Deployments, NetworkPolicies, Namespaces — required by the NetworkPolicy analyser).
  - GVKs from the `watchedGvks` Helm value.
  - GVKs referenced by `cluster.<gvk>.*` calls in rules (the audit module exposes `WatchedGVKs()` for tooling that wants to recompute this).
- `get,list,watch,update,patch` on `portalclusterrules.portal.io` and `portalrules.portal.io`. The `update`/`patch` verbs are for `.status` writes by the reconciler.
- `create,get,list,watch,update,patch,delete` on `policyreports.wgpolicyk8s.io` and `clusterpolicyreports.wgpolicyk8s.io`. Required because the PolicyReport sink creates one CR per (namespace, rule) pair and updates results incrementally.
- `create,get,update,patch` on `leases.coordination.k8s.io` (leader election).
- `get,list,watch` on `namespaces` (the admission webhook resolves namespace annotations for bypass detection — see `internal/admission/handler.go`).

## Conditional action permissions

Per the Helm `values.yaml`:

```yaml
rbac:
  actions:
    label:        false   # patch metadata.labels on workload kinds
    annotate:     false   # patch metadata.annotations
    evict:        false   # create pods/eviction
    patchnp:      false   # patch networkpolicies.networking.k8s.io
    revoketoken:  false   # delete secrets (kubernetes.io/service-account-token)
```

Each `true` toggle adds the matching `rule:` block to the `ClusterRole`. The action code itself returns `ErrNotConfigured` if invoked without its client (`internal/actions/<name>/action.go` — see `Configure()`), so accidentally enabling an action without RBAC produces a hard error instead of a silent partial failure.

By default **only the AlertManager action is on**, because it needs no RBAC. The `label` action is documented as the lowest-risk on-cluster action and is the recommended first step beyond alerts.

## Minimum-viable RBAC scenarios

### Admission-only

You only want to deny/warn at admission, no audit, no actions:

```yaml
admission:  { enabled: true }
audit:      { enabled: false }
network:    { enabled: false }
rbac:
  create: true
  actions: {}   # all false (default)
```

Effective permissions: `get,list,watch namespaces` (bypass-annotation lookup), `get,list,watch,patch portalclusterrules/portalrules` (CR loader and `.status` writer), `coordination.k8s.io/v1.Lease` (for leader election if you keep `replicaCount: 2`). No `policyreports`, no workload-kind permissions.

### Full audit + network + actions

```yaml
admission:  { enabled: true }
audit:      { enabled: true }
network:    { enabled: true }
watchedGvks: [v1/Pod, apps/v1/Deployment, networking.k8s.io/v1/NetworkPolicy]
rbac:
  create: true
  actions:
    label:        true
    annotate:     true
    evict:        true
    patchnp:      true
    revoketoken:  false   # high blast radius — opt in only for specific runtime scenarios
```

This is the standard production posture once Portal is proven in the environment.

### Decommissioning a built-in action

Disable the toggle and `helm upgrade`. The next reconciliation drops the matching `rule:` block; even if a rule still requests that action, `internal/actions/engine` records `portal_actions_total{result="error"}` and continues. This is by design — RBAC removal cannot crash Portal.

## What Portal does **not** ask for

- No `*` verbs.
- No write access to `nodes`, `roles`, `rolebindings`, `clusterroles`, `clusterrolebindings`.
- No `delete` on workload kinds — eviction goes through `pods/eviction`, which is a separate subresource RBAC vector.

If your cluster runs Pod Security Admission at `restricted`, Portal's Deployment template ships with `runAsNonRoot: true`, `seccompProfile: { type: RuntimeDefault }`, and `readOnlyRootFilesystem: true` — it slots into `restricted` without escalation.
