# Rule schema

The canonical Go shape is `api.Rule` in [`internal/api/rule.go`](../../internal/api/rule.go). The CRD spec ([`internal/rule/v1alpha1/types.go`](../../internal/rule/v1alpha1/types.go)) is a verbatim mirror except for the engine-internal `Source` field.

```yaml
name: privileged-container
enabled: true
severity: critical
mode: [admission, audit]
enforcementAction: deny
match:
  gvk:
    - {group: "", version: v1, kind: Pod}
  namespaces:
    include: [production]
    exclude: []
rule: container.securityContext.privileged == true
alert: insecure-workload
actions:
  - {type: alertmanager, template: insecure-workload}
  - {type: label, key: portal.security/quarantine, value: "true", on: [audit]}
  - {type: evict, on: [audit], rateLimit: 5/min}
```

## Top-level fields

| Field | Type | Required | Default | Notes |
|-------|------|----------|---------|-------|
| `name` | string | yes | — | Logical identifier. Surfaces as `rule` label on `portal_audit_violations` etc. |
| `enabled` | bool | yes | — | Kill switch. `false` parks the rule. |
| `severity` | enum | no | `""` | One of `info`, `low`, `medium`, `high`, `critical`. CRD validation: `+kubebuilder:validation:Enum=info;low;medium;high;critical`. |
| `mode` | `[]Mode` | no | `[]` | Which loops evaluate. Allowed: `admission`, `audit`, `network`, `runtime` (v2). |
| `enforcementAction` | enum | no | `""` | Admission-only verdict: `deny`, `warn`, `dryrun`. Ignored elsewhere. |
| `match` | `Matcher` | yes | — | See below. |
| `rule` | string | yes | — | An `expr-lang` boolean expression. See [expression-language.md](expression-language.md). |
| `alert` | string | no | `""` | Shorthand: expands to one `actions: [{type: alertmanager, template: <alert>}]` entry. |
| `actions` | `[]ActionSpec` | no | `[]` | See below. Merged with the `alert` shorthand. |

## `match`

```yaml
match:
  gvk:
    - {group: "", version: v1, kind: Pod}
    - {group: apps, version: v1, kind: Deployment}
  namespaces:
    include: [production, staging]
    exclude: [staging-canary]
```

- `match.gvk[]` — required, non-empty. Each entry is `{group, version, kind}`. Use `group: ""` for core types.
- `match.namespaces.include` — if non-empty, the rule applies only to listed namespaces.
- `match.namespaces.exclude` — namespaces removed from the include set (or from all if include is empty).

## `ActionSpec`

```yaml
- type: label
  on: [audit]
  rateLimit: 5/min
  key: portal.security/quarantine
  value: "true"
```

| Field | Type | Notes |
|-------|------|-------|
| `type` | string | Required. One of the registered action types — see [actions.md](actions.md). |
| `on` | `[]Mode` | Restricts the action to specific modes. Empty = all modes the rule fires in. |
| `rateLimit` | string | `<N>/<unit>` (`s`/`sec`, `m`/`min`, `h`/`hr`). Overrides `Action.DefaultRateLimit()`. |
| `params` | map | Free-form per-action parameters. Inlined at the action-spec level in YAML. |

## CRD .status

When loaded from a `PortalClusterRule` / `PortalRule` CR, the reconciler writes a `.status` block per rule ([`internal/rule/v1alpha1/types.go`](../../internal/rule/v1alpha1/types.go) `RuleStatus`):

| Field | Meaning |
|-------|---------|
| `evalCount` | Total evaluations since the last reconcile cycle started. |
| `violationCount` | Subset of `evalCount` that returned `true`. |
| `lastApplied` | Timestamp the rule index last picked up this version. |
| `parseError` | Non-empty if the `rule:` expression failed to compile. |
| `activeOn` | The set of modes the rule is currently active in. |

Update frequency is bounded by a token bucket so noisy rules do not hammer the API server.
