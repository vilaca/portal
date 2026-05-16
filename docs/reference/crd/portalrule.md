# `PortalRule`

> Normally regenerated from the CRD OpenAPI schema. The Go types live in [`internal/rule/v1alpha1/types.go`](https://github.com/vilaca/portal/blob/main/internal/rule/v1alpha1/types.go); the YAML CRD is shipped in `deploy/helm/portal/crds/`.

Namespaced Portal rule. Short name: `pr`. Status subresource enabled. The spec is identical to [`PortalClusterRule`](portalclusterrule.md); only the scope differs (`+kubebuilder:resource:scope=Namespaced,shortName=pr`).

```yaml
apiVersion: portal.io/v1alpha1
kind: PortalRule
metadata:
  name: enforce-team-label
  namespace: production
spec:
  # ... see ../rule-schema.md
```

## `.spec`

Spec fields are documented once at [`../rule-schema.md`](../rule-schema.md). The Go alias `PortalRuleSpec = RuleSpec` in [`internal/rule/v1alpha1/types.go`](https://github.com/vilaca/portal/blob/main/internal/rule/v1alpha1/types.go) ensures byte-for-byte parity with `PortalClusterRule`.

A `PortalRule` only fires against objects in its own namespace. `match.namespaces.include` and `.exclude` further narrow that set; including a foreign namespace in a `PortalRule` has no effect.

## `.status`

The shared `RuleStatus` shape applies — see [`portalclusterrule.md`](portalclusterrule.md#status).
