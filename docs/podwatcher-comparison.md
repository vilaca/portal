# PodWatcher-POC vs the Kubernetes Policy Tool Landscape

This document positions [PodWatcher-POC](https://github.com/vilaca/podwatcher-poc) against the tools covered in [feature-matrix.md](./feature-matrix.md): OPA/Gatekeeper, Kyverno, Kubewarden, jsPolicy, Polaris, Falco, and Tetragon.

## What PodWatcher-POC is

A **continuous-audit / posture scanner** for Kubernetes (and, optionally, a standalone Docker host).

- Polls the Kubernetes API on a schedule, lists every pod and container, evaluates each against a set of YAML rules whose body is a **SpEL** (Spring Expression Language) boolean expression, and fires alerts to **Prometheus AlertManager** when a rule matches.
- Surfaces pod-shape data only: `securityContext`, `capabilities`, host namespaces, image coordinates, labels, annotations, command/args, ports.
- Stateless single Java binary. In-cluster RBAC: `get,list pods`. Out-of-cluster: kubeconfig.
- Alert-only. No admission webhook, no mutation, no kill, no enforcement of any kind.
- Dual scan mode: `SCAN_MODE=kubernetes` (default) or `SCAN_MODE=docker` against `/var/run/docker.sock`, sharing the same rule set.

## Where it sits in the defense-in-depth stack

Layer 5 (in-cluster posture / continuous audit) — the same row as Polaris, Gatekeeper audit mode, Kyverno background scan, kubescape. It is **not** an admission tool (layer 4), **not** a runtime tool (layers 7–8), **not** a supply-chain tool (layer 3).

## Feature comparison

| Feature | PodWatcher-POC | OPA/Gatekeeper | Kyverno | Polaris | Falco / Tetragon |
|---|---|---|---|---|---|
| **Rule format** | YAML schema + pluggable expression engine (currently SpEL) | Rego (language *is* the format) | YAML DSL with pluggable predicate (`pattern`, `cel`, `deny`, …) | Fixed YAML check toggles | YAML schema + DSL expression / Tracing CRD |
| **Where evaluated** | External scanner pod, polling | Admission webhook + audit | Admission webhook + audit | CLI / dashboard | Kernel (eBPF) |
| **Blocks bad workloads at apply time** | No | Yes | Yes | No | No |
| **Audits live cluster state** | Yes (polling) | Yes (constraint audit) | Yes (background scan) | Yes (primary mode) | N/A |
| **Detects runtime behavior** | No | No | No | No | Yes |
| **Resource coverage** | Pods + containers only | Any K8s object | Any K8s object | Workloads | Live processes |
| **Cross-resource policies** (e.g. "Pod must reference a NetworkPolicy") | No | Yes | Yes | Limited | N/A |
| **Mutation / generation / cleanup** | No | Mutation only | All three | No | No |
| **Image signature verification** | No | Custom Rego | Yes, native | No | No |
| **Output channel** | AlertManager + Prom metrics + JSON logs | Admission denial; OPA decision log | Admission denial; PolicyReport CRD | Dashboard / JSON | gRPC / sidekick / kernel signal |
| **Policy Report CRD (CNCF)** | No | Via tooling | Yes, native | Yes | No |
| **Non-K8s targets** | Yes — local Docker daemon | No | No | No | Container runtimes (host scope) |
| **Watch vs poll** | Poll (list every cycle) | Watch (admission stream) | Watch | One-shot | Stream (kernel events) |
| **Latency to detection** | Up to one poll interval | Real-time | Real-time | Manual / CI | Microseconds |
| **Footprint** | Single Java pod, RBAC `get,list pods` | OPA pod(s) | Controller + webhook | CLI / one pod | DaemonSet per node |
| **Learning curve** | Low for Java/Spring teams | Steep | Low | Very low | Moderate |
| **Maturity** | POC | Graduated | Incubating | Production | Graduated / Incubating |

## What PodWatcher does that the matrix tools don't

- **Direct AlertManager integration.** Falco needs Falcosidekick, Kyverno emits PolicyReports that need a downstream exporter, Polaris is dashboard-first. PodWatcher speaks AlertManager's API natively — if your org already runs Prometheus, the alert path is one less moving part.
- **Docker-daemon mode.** None of the seven matrix tools scans a standalone Docker host. Polaris is K8s-only; Falco can but is heavy; admission tools are by definition K8s-only. PodWatcher's same rule set runs against `/var/run/docker.sock`.
- **Rule format decoupled from expression engine.** The YAML schema (`name`, `enabled`, `severity`, `filter.namespace.*`, `alert`, `rule`) is the contract; SpEL is the evaluator currently plugged into the `rule:` field. Swapping in CEL, JEXL, Groovy, JS-via-GraalVM, or an embedded OPA/Rego would not require touching rule metadata, filters, alert templates, or tooling. This is the same architectural choice Falco and Kyverno converged on, and it is *cleaner* than OPA/Gatekeeper, where Rego is inseparable from the engine.
- **SpEL as today's default** is approachable for Java/Spring teams and well-documented outside the K8s ecosystem. It does lose some properties of Rego (partial evaluation, decision-log replay) — but those are evaluator-level losses, not architectural ones, and a future PodWatcher could offer Rego as an alternative engine without changing existing rule files.
- **No webhook to operate.** Admission tools require certs, a `ValidatingWebhookConfiguration`, failure-policy decisions, and a healthy webhook pod or your cluster stops accepting deployments. PodWatcher can crash and the cluster is unaffected — just no alerts.

## What it doesn't do (vs. matrix tools)

- **Cannot prevent admission.** The bad pod runs at least until the next scan.
- **Pod-only data model.** No way to write a policy like "every Deployment in `prod` must be backed by a PodDisruptionBudget" or "no ClusterRoleBinding may grant `*` on `secrets`."
- **No mutation, no generation, no cleanup, no image verification, no Policy Report CRD, no decision log.**
- **No runtime visibility.** The pod scanned at T+0 may behave maliciously at T+30s; PodWatcher will never see it. That is Falco/Tetragon territory.
- **Polling overhead.** `list pods` against the API server every interval — fine for hundreds of pods, costly at tens of thousands. Watch-based controllers (Gatekeeper, Kyverno) scale better.
- **POC stage.** No HA, no leader election documented, no resync semantics during API-server flaps spelled out.

## Architecture note: rule format vs expression engine

PodWatcher is best understood as two layers:

1. **A rule-evaluation framework** — YAML rule files with metadata, namespace filters, severity, and an `alert` template reference, evaluated against a normalized **Context** object (`container.*`, `spec.*`, `securityContext.*`, `metadata.*`) that is identical for K8s and Docker scan modes.
2. **An expression engine** plugged into the `rule:` field — currently SpEL.

The framework does not know or care that SpEL is the language. The Context object is exposed as plain Java getters, and any expression engine that can bind to a Java object graph (CEL-Java, JEXL, MVEL, Groovy, Nashorn/GraalJS, embedded OPA via `opa-java`) could replace SpEL behind the same rule schema.

Practical implications:

- **Rule files are portable across engine versions.** Adopting CEL later does not invalidate rules already written.
- **A `language:` field per rule is a small additive change.** Teams could mix engines per rule rather than per cluster.
- **Tooling can target the schema.** A GitOps validator, IDE plugin, or rule-diff tool works against the YAML shape and is independent of evaluator choice.

This is the *opposite* of OPA/Gatekeeper, where Rego is the format and the engine simultaneously, and aligns with the architectural direction Kyverno (multi-engine `validate`) and Falco (rule schema + DSL expression slot) have taken.

## Honest positioning

PodWatcher is a focused **layer-5 niche-fill**: a low-overhead, Prometheus-native pod auditor with a Java-friendly rule language and the unusual ability to also scan plain Docker hosts. It is not a competitor to Kyverno or OPA/Gatekeeper — those are admission controllers. It is closest in spirit to **Polaris**, but with alerting instead of dashboards, SpEL instead of fixed checks, and Docker support as a bonus.

For a defense-in-depth cluster, PodWatcher could plausibly *replace* Polaris and provide the layer-5 slot, while admission (Kyverno/OPA) and runtime (Falco/Tetragon) still need separate tools.

## Rule ergonomics: PodWatcher vs Kyverno

A common question is whether Kyverno is easier to write rules in than PodWatcher. The honest answer: **easier per-rule, harder per-capability**. PodWatcher wins on terseness inside its narrow slice; Kyverno wins on everything outside it.

### Side-by-side: "block privileged containers in `production`"

**PodWatcher** — 8 lines, single boolean expression:

```yaml
name: privileged container
enabled: true
severity: critical
filter:
  namespace:
    include: [production]
rule: >
  container.securityContext.privileged == true
  || container.securityContext.allowPrivilegeEscalation == true
alert: insecure-workload
```

**Kyverno** with `pattern`:

```yaml
apiVersion: kyverno.io/v1
kind: ClusterPolicy
metadata:
  name: disallow-privileged
spec:
  validationFailureAction: Audit
  rules:
    - name: privileged-containers
      match:
        any:
          - resources:
              kinds: [Pod]
              namespaces: [production]
      validate:
        message: "Privileged containers not allowed"
        pattern:
          spec:
            =(initContainers):
              - =(securityContext):
                  =(privileged): "false"
                  =(allowPrivilegeEscalation): "false"
            containers:
              - =(securityContext):
                  =(privileged): "false"
                  =(allowPrivilegeEscalation): "false"
```

**Kyverno with CEL** (≥ 1.11) — closer to PodWatcher's terseness:

```yaml
      validate:
        cel:
          expressions:
            - expression: >
                !object.spec.containers.exists(c,
                  has(c.securityContext) && (
                    (has(c.securityContext.privileged) && c.securityContext.privileged) ||
                    (has(c.securityContext.allowPrivilegeEscalation) && c.securityContext.allowPrivilegeEscalation)
                  ))
              message: "Privileged containers not allowed"
```

### Where PodWatcher rules are easier

- **Less boilerplate.** No `apiVersion` / `kind` / `spec` / `rules[]` / `match` / `validate` wrappers. Just `name + rule + alert`.
- **Automatic iteration over container types.** `container.*` is evaluated once per standard, init, *and* ephemeral container. Kyverno requires repeating the `pattern` block or using `foreach` for each list.
- **Null-safety is part of the language.** SpEL's `?.` and `?:` operators handle nullable `securityContext` fields inline. Kyverno's `pattern` grammar has `=()` for optional, `()` for required, `<` for anchors — a small grammar to internalize.
- **No CRDs to apply.** Drop a YAML file in `RULES_FOLDER`; PodWatcher picks it up. Kyverno requires `kubectl apply`, a controller reconciliation, and a webhook in the path.
- **Familiar language for Java teams.** SpEL is `obj.field == value`, no new mental model.

### Where Kyverno is easier (or only Kyverno works)

- **`pattern` mode is declarative.** For "this object must match this shape," Kyverno's pattern syntax is often more readable than a boolean expression. "All containers must drop ALL capabilities" is a literal YAML shape.
- **Cross-resource policies.** "Every Deployment in `prod` must reference an existing PodDisruptionBudget" — trivial in Kyverno, impossible in PodWatcher.
- **Mutation, generation, cleanup, image verification.** PodWatcher has none of these. Kyverno's `mutate`, `generate`, `cleanup`, `verifyImages` rules reuse the same surrounding schema.
- **Real prevention.** Kyverno's `validationFailureAction: Enforce` blocks the bad workload at admission. PodWatcher only alerts after the fact.
- **Ecosystem support.** PolicyReport CRDs, policy exceptions, Helm bundles, Kyverno CLI for CI, large public policy library (kyverno.io/policies) you can crib from instead of writing every rule yourself.
- **Native K8s integration.** Match by labels, namespace selectors, GVK, kinds — far richer than PodWatcher's namespace include/exclude.

### Scenario-by-scenario

| Scenario | Easier |
|---|---|
| "Container must not be privileged" | PodWatcher (one boolean, eight lines) |
| "Pods must drop ALL capabilities" | Toss-up (Kyverno pattern is short; SpEL is short) |
| "Image registry must be in this allowlist" | PodWatcher (set literal `{'gcr.io',...}.contains(...)`) |
| "Image must be cosign-signed by this key" | Kyverno (PodWatcher can't do it at all) |
| "Block `kubectl exec` against pods labeled `prod`" | Kyverno (admission-only; PodWatcher can't) |
| "Deployment must reference an existing ConfigMap" | Kyverno (cross-resource; PodWatcher can't) |
| "Auto-add `priorityClassName` to pods missing one" | Kyverno (mutation; PodWatcher can't) |
| "Score the cluster on best-practice posture" | Polaris (different tool entirely) |

### The one-liner

For the narrow slice PodWatcher covers, its rule format is genuinely more concise than Kyverno's. Outside that slice — non-pod resources, cross-resource checks, mutation, image verification, actual prevention — Kyverno isn't harder, it's the only option of the two. And Kyverno's public policy library often flips the practical "easier to use" calculation regardless of per-rule verbosity.

## When to choose PodWatcher

- You already run Prometheus + AlertManager and want pod posture alerts in the same pipeline as everything else.
- Your team writes Java and SpEL is a known quantity; Rego is not.
- You need to scan a standalone Docker host (edge devices, CI agents, single-node deployments) with the same rule set you use for K8s.
- You explicitly want **detection without enforcement** — perhaps in a multi-tenant cluster where you cannot block other teams' workloads but must surface violations.
- You want a tool that fails *open* — if it dies, nothing else breaks.

## When to choose something else

- You need to **prevent** non-compliant workloads → Kyverno or OPA/Gatekeeper.
- You need to detect **behavior**, not configuration → Falco or Tetragon.
- You need policies over **non-pod resources** (RBAC, NetworkPolicy, CRDs, Ingress) → Kyverno or OPA/Gatekeeper.
- You need **PolicyReport CRDs** for ecosystem integration → Kyverno or Polaris.
- You need **image signature verification** at admission → Kyverno (native) or OPA + a sigstore library.
