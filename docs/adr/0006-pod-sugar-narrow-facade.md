# ADR 0006 — Pod sugar is a deliberately narrow façade

**Status.** Accepted, implemented in v1.

## Context

`podwatcher-poc` rules talk about pods in terms of a curated façade: `container.securityContext.privileged`, `spec.hostPID`, `metadata.labels`. The façade is a small subset of `v1.Pod`, picked to cover the rules podwatcher-poc actually shipped.

Portal supports arbitrary GVKs via `object.*` (the raw `*unstructured.Unstructured`). The question: do we mirror the entire `v1.Pod` shape in Go for type safety, or do we keep the façade narrow?

## Decision

**The pod sugar (`internal/context/pod/`) is intentionally narrow.** It covers exactly the fields podwatcher-poc exposed, plus the multi-container iteration that requires sugar to express terse.

Specifically the sugar surfaces:

```
container.{name, containerType, image.{registry, name, tag, sha256},
           command, args, ports,
           securityContext.{privileged, allowPrivilegeEscalation,
                            readOnlyRootFilesystem, runAsUser, runAsGroup,
                            runAsNonRoot, procMount, seccompProfileType,
                            capabilities.{add, drop}}}
spec.{hostPID, hostNetwork, hostIPC, serviceAccountName,
      automountServiceAccountToken}
securityContext.{runAsUser, runAsGroup, runAsNonRoot, fsGroup,
                 supplementalGroups, seccompProfileType}
metadata.{name, namespace, labels, annotations}
object   # always available — the universal escape hatch
request  # admission only
```

Everything else — `volumes`, `env`, `probes`, `lifecycle`, `nodeSelector`, `tolerations`, `affinity`, `topology constraints`, `priorityClassName`, `runtimeClassName`, `dns*`, `restartPolicy`, `schedulerName`, `imagePullSecrets`, `hostAliases`, the rest of `v1.Pod` — is reachable via `object.spec.<path>` directly.

## Rationale — why narrow

- **Maintenance load.** A typed mirror of `v1.Pod` would couple Portal's release schedule to upstream K8s shape changes. The `v1.Pod` type adds fields regularly (PodSchedulingReadiness, ResourceClaims, etc.); each addition would force a sugar update or operators would notice that "Portal's surface is older than my cluster".
- **The escape hatch is sufficient.** `object.spec.tolerations[0].operator == "Exists"` is barely longer than `tolerations[0].operator == "Exists"`. The compactness loss is small; the maintenance win is large.
- **Compat with podwatcher-poc.** The sugar surface **matches** podwatcher-poc's exposed fields exactly. Migrated rules don't change shape, only syntax.
- **Multi-container iteration is the load-bearing part.** The sugar's real job is letting a rule say `container.securityContext.privileged` once and have the engine evaluate it per std/init/ephemeral container. That's hard to express from raw `object.spec.containers[*]` and is why the sugar exists at all. Beyond that, terseness is a bonus, not a goal.

## Rationale — why not a typed mirror

- **Type safety is an illusion at this layer.** expr-lang reads from `map[string]any` regardless; whether the value came from a typed struct or an `Unstructured` is invisible to rule authors.
- **Field gaps cause silent ambiguity.** If `v1.Pod.Spec.OS` exists in K8s but not in our typed mirror, rules referencing it would either compile-error in Go (boring) or silently return `nil` (worse than `object.spec.os`).
- **Forward compatibility.** New K8s fields would each require a Portal release before they could be referenced. The escape hatch makes this a non-issue today.

## What grows the sugar

The sugar grows **additively when real rules demand it**. The procedure:

1. A real rule needs a field outside the sugar.
2. The rule author writes the rule using `object.spec.<path>`.
3. If the same pattern appears across multiple rules or community contributions, file an issue proposing the sugar extension.
4. PR adds the field to `internal/context/pod/builder.go` with a `_test.go` covering it.
5. Documentation in `docs/concepts/context-and-pod-sugar.md` is updated in the same PR.

We **will not pre-emptively** add fields "because they exist in `v1.Pod`". Every sugar field is a maintenance commitment.

## Consequences

- Rule authors who want exotic Pod fields use `object.spec.*`. Documented openly; not a footgun.
- The pod builder code stays small (a few hundred lines, not several thousand).
- When K8s ships a new pod-spec field, Portal's compatibility is automatic for `object.spec.*` reach. Sugar updates are an opt-in operation.
- Other GVKs (Deployment, StatefulSet, NetworkPolicy, etc.) get the generic context builder by default. There is **no plan** to add Deployment sugar, StatefulSet sugar, NetworkPolicy sugar; rule authors talk to `object.*`. If a real-world pattern justifies it, we'd discuss — but `object.*` is the universal answer and intentionally so.
