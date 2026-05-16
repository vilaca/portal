# `PortalClusterRule`

> Normally regenerated from the CRD OpenAPI schema (`crd-ref-docs` or similar). The Go types live in [`internal/rule/v1alpha1/types.go`](https://github.com/vilaca/portal/blob/main/internal/rule/v1alpha1/types.go); the YAML CRD is shipped in `deploy/helm/portal/crds/`.

Cluster-scoped Portal rule. Short name: `pcr`. Status subresource enabled.

```yaml
apiVersion: portal.io/v1alpha1
kind: PortalClusterRule
metadata:
  name: privileged-container
spec:
  # ... see ../rule-schema.md
```

## `.spec`

The spec fields are identical to the rule schema. See [`../rule-schema.md`](../rule-schema.md) — every field documented there applies verbatim.

CRD-only metadata:

| OpenAPI marker | Effect |
|----------------|--------|
| `+kubebuilder:resource:scope=Cluster,shortName=pcr` | Cluster-scoped; `kubectl get pcr` shorthand. |
| `+kubebuilder:subresource:status` | Status is a separate subresource (RBAC `patch` on `.status` is split from `patch` on the object). |
| `+kubebuilder:validation:Enum=info;low;medium;high;critical` on `severity` | Enum enforced at admission. |
| `+kubebuilder:validation:Enum=deny;warn;dryrun` on `enforcementAction` | Enum enforced at admission. |
| `+kubebuilder:pruning:PreserveUnknownFields` + `Schemaless` on `actions[].params` | Free-form action params survive round-trip. |

## `.status`

Written by the status reconciler in [`internal/rule/v1alpha1/`](https://github.com/vilaca/portal/blob/main/internal/rule/v1alpha1/). All fields are optional.

| Field | Type | Meaning |
|-------|------|---------|
| `evalCount` | int64 | Total evaluations across all modes since the last reconcile cycle. |
| `violationCount` | int64 | Subset of `evalCount` that returned `true`. |
| `lastApplied` | metav1.Time | When the in-memory rule index last picked up this version of the CR. |
| `parseError` | string | Non-empty if `spec.rule` failed to compile. Cleared when a fixed version is applied. |
| `activeOn` | []string | Modes the rule is currently active in (`admission`, `audit`, `network`, `runtime`). |

Status updates are token-bucket throttled so noisy rules cannot saturate the API server.
