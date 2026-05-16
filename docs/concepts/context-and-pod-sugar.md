# Context and pod sugar

The expr-lang env Portal exposes to a rule has three layers ([`internal/api/context.go`](https://github.com/vilaca/portal/blob/main/internal/api/context.go), [`internal/context/pod/builder.go`](https://github.com/vilaca/portal/blob/main/internal/context/pod/builder.go)):

1. **`object` — universal.** The raw resource as `*unstructured.Unstructured` rendered as nested maps. Every field in the K8s schema is reachable: `object.spec.replicas`, `object.spec.volumes[0].hostPath`, `object.spec.tolerations`, custom CRD fields, anything. This is the escape hatch — no field is ever inaccessible.
2. **Pod sugar — Pods and pod-shaped workloads only.** A deliberately narrow façade matching `podwatcher-poc`'s surface byte-for-byte.
3. **`request` — admission only.** `operation`, `dryRun`, `userInfo.{username,uid,groups,extra}`, `oldObject`.

For non-pod GVKs only `object`, `metadata`, `request` are bound; the sugar keys are absent.

## What the pod sugar covers

The pod ContextBuilder produces these top-level env keys for Pods and any GVK with a `PodTemplateSpec` (Deployment, StatefulSet, DaemonSet, ReplicaSet, Job, CronJob):

```text
container.name
container.containerType        # "standard" | "init" | "ephemeral"
container.image.registry       # parsed from .image
container.image.name
container.image.tag
container.image.sha256
container.command
container.args
container.ports
container.securityContext.{privileged,allowPrivilegeEscalation,readOnlyRootFilesystem,
                           runAsUser,runAsGroup,runAsNonRoot,procMount,seccompProfileType,
                           capabilities.{add,drop}}

spec.hostPID
spec.hostNetwork
spec.hostIPC
spec.serviceAccountName
spec.automountServiceAccountToken

securityContext.{runAsUser,runAsGroup,runAsNonRoot,fsGroup,supplementalGroups,seccompProfileType}

metadata.{name,namespace,labels,annotations}
```

Anything outside this list — volumes, env, probes, lifecycle, nodeSelector, tolerations, affinity, topology spread constraints, priorityClassName, runtimeClassName, dns settings, restartPolicy, schedulerName, imagePullSecrets, hostAliases — is reachable via `object.spec.<path>`.

The sugar is **not** a typed mirror of `v1.Pod`. It grows additively when real rules demand it.

## Multi-container iteration

The pod ContextBuilder's primary load-bearing job is fan-out: every rule is evaluated **once per container** with `container` rebound each pass. Standard, init, and ephemeral containers are all included; the iteration index is reflected in `container.containerType`.

A rule like `container.securityContext.privileged == true` fires per container — no `foreach` boilerplate. From raw `object.spec.containers[]` you would have to write the loop yourself.

## Non-pod resources

For any GVK without a `PodTemplateSpec` the env is:

```text
object       # universal
metadata     # convenience alias for object.metadata
request      # admission only
```

A rule against a ConfigMap or NetworkPolicy reaches its fields via `object.data.<key>` / `object.spec.ingress[0].from`. The container/spec/securityContext keys are bound to `nil` and any access through them must use the `?.` operator.
