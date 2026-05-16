# Expression language

Portal's default expression engine is [`expr-lang/expr`](https://github.com/expr-lang/expr) via [`internal/expr/exprlang`](../../internal/expr/exprlang/engine.go). The engine compiles each `rule:` expression once at load time and re-runs the compiled program per evaluation.

The engine interface is pluggable (`api.ExpressionEngine`) — CEL/Rego/starlark are drop-in candidates for v3 without changing the rule schema.

## Bound env

The env keys exposed by the pod ContextBuilder ([`internal/context/pod/builder.go`](../../internal/context/pod/builder.go)) are:

| Key | When | Type |
|-----|------|------|
| `object` | always | nested `map[string]any` from `unstructured.Unstructured` |
| `metadata` | always | shortcut to `object.metadata` |
| `container` | pod-shaped GVKs only | one container per evaluation pass |
| `spec` | pod-shaped GVKs only | the pod-shape `.spec` projection |
| `securityContext` | pod-shaped GVKs only | the pod-level `.spec.securityContext` |
| `request` | admission only | `{operation, dryRun, userInfo, oldObject}` |
| `cluster.<gvk>` | always | informer-cache-backed accessor |
| `consistentCluster.<gvk>` | always | direct-API-call accessor |

Detailed coverage of the pod-sugar fields is in [../concepts/context-and-pod-sugar.md](../concepts/context-and-pod-sugar.md); the cross-resource helpers are in [../concepts/cross-resource.md](../concepts/cross-resource.md).

## Operators Portal rules lean on

| Operator | Example |
|----------|---------|
| Membership | `container.image.registry in ["docker.io", "ghcr.io"]` |
| Regex (operator form) | `object.metadata.name matches "^web-"` |
| Null-safe nav | `container.securityContext?.privileged == true` |
| Null coalesce | `container.image.tag ?? "latest"` |
| String prefixes/suffixes | `startsWith(object.metadata.name, "web-")`, `endsWith(...)` |

`startsWith`, `endsWith`, `contains`, and the `matches` operator are part of expr-lang's stdlib.

## Custom helpers

| Helper | Signature | Notes |
|--------|-----------|-------|
| `regexMatch` | `regexMatch(s, pattern string) bool` | Function-form alternative to the `matches` operator for when the operator form is awkward (e.g. dynamic pattern). Pattern cache is global. |

The helper is registered in [`internal/expr/exprlang/engine.go`](../../internal/expr/exprlang/engine.go).

## Result contract

Every `rule:` expression **must** evaluate to `bool`. A non-bool return surfaces as:

- a compile-time error (caught at load; written to `.status.parseError`), or
- an eval-time error (`expression did not produce bool, got %T`) which the engine wraps as `eval: ...`.

Errors at eval time mark the evaluation as failed and increment the error counters — they do not bubble up as a deny decision.

## Worked snippets

```text
# privileged or escalating
container.securityContext.privileged == true
  || container.securityContext.allowPrivilegeEscalation == true

# every Deployment must have a matching PDB
cluster.poddisruptionbudgets.list(
  object.metadata.namespace,
  object.spec.selector.matchLabels
) | len == 0

# block images outside an allow-list
not (container.image.registry in ["ghcr.io", "registry.k8s.io"])

# require a team label
not ("team" in keys(object.metadata.labels ?? {}))
```
