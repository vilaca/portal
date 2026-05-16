# Kubernetes Policy Tools — Feature Matrix

A side-by-side comparison of Portal and the leading policy and security tools in the Kubernetes ecosystem.

## Categories at a glance

| Tool | Primary category | Stage |
|------|------------------|-------|
| **Portal** | Admission control + watch-driven audit + NetworkPolicy analysis + response actions | v0.1.0-alpha |
| **OPA / Gatekeeper** | Admission control (validation + mutation) | CNCF Graduated (OPA) |
| **Kyverno** | Admission control + resource lifecycle management | CNCF Incubating |
| **Kubewarden** | Admission control (validation + mutation) | CNCF Sandbox |
| **jsPolicy** | Admission control (validation + mutation) | Community (Loft) |
| **Polaris** | Configuration auditing / best practices | Community (Fairwinds) |
| **Falco** | Runtime security — detection | CNCF Graduated |
| **Tetragon** | Runtime security — detection + enforcement | CNCF Incubating |

> Admission-control tools intercept API requests *before* objects are persisted. Runtime tools observe the workload *after* it starts. Portal sits across admission, watch-driven audit, and a declarative NetworkPolicy analyser — see [the deep matrix](#deep-feature-matrix) for what that means in practice.

---

## Deep feature matrix

| Feature | **Portal** | OPA / Gatekeeper | Kyverno | Kubewarden | jsPolicy | Polaris | Falco | Tetragon |
|---|---|---|---|---|---|---|---|---|
| **Policy language** | `expr-lang/expr` (terse, null-safe, set literals) | Rego | YAML (Kyverno DSL), CEL, JMESPath | Rust, Go, Rego, Swift, JS/TS via WASM | JavaScript / TypeScript | YAML check config | Falco rules DSL (YAML) | Tracing Policy CRDs (YAML) + eBPF |
| **Execution model** | Single Go binary: admission webhook + audit informers + NP analyser + action engine | Sidecar admission webhook | Native admission webhook | WASM runtime in cluster | Embedded Node.js sandbox | Static/CLI + dashboard | eBPF + kernel module / userspace | eBPF (kernel) |
| **Validation** | Yes (`deny`/`warn`/`dryrun`) | Yes | Yes | Yes | Yes | Yes (audit only) | Yes (alert-only) | Yes |
| **Mutation** | No (out of scope for v1) | Yes (assign / modifyset) | Yes (powerful, JSON-patch + strategic merge) | Yes | Yes | No | No | No |
| **Generation of new resources** | No | No | Yes (clone, generate, sync) | No | Limited (via JS) | No | No | No |
| **Cleanup / TTL of resources** | No | No | Yes (CleanupPolicy) | No | No | No | No | No |
| **Image verification (cosign/notary)** | No (v3 candidate) | Via custom Rego | Yes, native (verifyImages) | Yes (sigstore policies) | Via JS | No | No | No |
| **Audit / background scan of existing objects** | Yes — informer/watch-driven, never polls | Yes (constraint audit) | Yes (background scan) | Yes | Yes | Primary mode | N/A | N/A |
| **Cross-resource policy (read other live objects from a rule)** | Yes — `cluster.<resource>.list/byName(...)` over informer caches | Via `data.*` sync | Via context / API calls | Limited | Via JS fetch | No | N/A | N/A |
| **Static NetworkPolicy analysis** | Yes (built-in: default-deny-missing, broad-CIDR, unreachable-selector, policy-without-targets) | No (write your own) | No (write your own) | No | No | No | No | No |
| **Response actions on a violation** | Yes (label / annotate / evict / patch NP / revoke SA token / AlertManager, all idempotent + rate-limited) | No | No | No | No | No | Limited (via Talon) | Yes (kernel SIGKILL/override) |
| **Dry-run / report-only** | Yes (`enforcementAction: dryrun`) | Yes | Yes | Yes (monitor mode) | Yes | Yes (default) | N/A | Yes |
| **Policy distribution** | CRDs (`PortalClusterRule`, `PortalRule`) + folder-loader fallback | ConstraintTemplate CRDs; OCI via external tooling | CRDs; native OCI registry support | OCI registry (artifacts.kubewarden.io) | CRDs / npm modules | Static config bundled | Falcoctl + OCI artifacts | CRDs |
| **GitOps friendliness** | Strong (CRDs `kubectl apply`able; `.status.parseError` on each rule) | Strong (plain YAML CRDs) | Strong (plain YAML CRDs) | Strong (CRDs + OCI refs) | Strong | Strong | Moderate (rule files) | Strong |
| **AlertManager-native output** | Yes (drops onto an existing Prometheus route without an adapter) | No | No | No | No | No | Via Falcosidekick | No |
| **PolicyReport CRD output** | Yes | Via gatekeeper-policy-manager | Yes, first-class | Yes | Limited | Yes | No | No |
| **Pluggable expression engine** | Yes (`ExpressionEngine` iface; CEL/Rego/starlark drop-in v3) | No (Rego is the engine) | Yes (pattern / CEL / deny / JMESPath) | Yes (WASM) | No | No | No | No |
| **CEL support** | Pluggable (v3) | Indirect via K8s ValidatingAdmissionPolicy | Yes (first-class) | No | No | No | No | No |
| **Resource footprint** | ~30 MB Go (admission + audit + NP analyser in one pod) | Moderate (one OPA pod per replica) | Light-to-moderate | Light (WASM, per-policy server) | Light (Node.js) | Negligible (audit-only) | Light per node (DaemonSet) | Very light (eBPF in kernel) |
| **Performance characteristics** | Sub-20 ms p99 admission (compiled expr-lang VM in-process); sub-1 s p99 audit event→action | Rego eval can be slow on complex policies; partial eval helps | Generally faster than Rego for typical checks | Near-native (WASM JIT) | V8 engine; fast for JS workloads | N/A (offline) | Microsecond per event in kernel | Kernel-level, lowest overhead |
| **Learning curve** | Low — boolean expressions over a Pod-shaped env; one schema for all event sources | Steep (Rego is a new paradigm) | Low–moderate (YAML, familiar) | Varies by chosen language | Low (JS/TS) | Very low | Moderate (DSL + sys-calls knowledge) | Moderate–high (eBPF concepts) |
| **Runtime threat detection** | v2 (K8s API audit log) | No | No | No | No | No | Yes (syscalls, K8s audit, container events) | Yes (process, network, file, capability) |
| **Runtime enforcement (block/kill)** | v2 — response-based (the same action engine, fed by audit-log events) | No | No | No | No | No | Limited (via reaction tooling) | Yes (signal/override syscalls) |
| **Multi-cluster / fleet support** | Per-cluster install | Via Gatekeeper Policy Library or Rancher/ACK add-ons | Native (Kyverno multi-tenancy) | Yes (CRD-driven) | Yes | Via CLI batches | Falcosidekick, Falco Talon | Yes |
| **Maturity / production usage** | Early alpha — no production deployments yet | Highest among admission tools | Very high, growing fastest | Growing | Niche | Wide for audit/CI | Highest among runtime tools | Newer but rapidly growing |
| **License** | Apache 2.0 | Apache 2.0 | Apache 2.0 | Apache 2.0 | Apache 2.0 | Apache 2.0 | Apache 2.0 | Apache 2.0 |
| **Maintainer** | Independent | Styra + community | Nirmata + community | SUSE + community | Loft Labs | Fairwinds | Sysdig + community | Isovalent (now Cisco) + community |

---

## When to pick which

- **One tool spanning admission + audit + NetworkPolicy hygiene + automated response**, with terse boolean rules, AlertManager-native output, and the same rule firing in admission and audit → **Portal**. Trade-off: alpha maturity, no mutation, no image verification yet.
- **Pure admission validation across many domains** with the richest expression power → **OPA/Gatekeeper** (if your team can absorb Rego) or **Kyverno** (if not).
- **Heavy mutation, generation, image verification, cleanup** → **Kyverno**. It's the most "batteries-included" admission tool.
- **Polyglot teams who don't want a DSL** → **Kubewarden** (any WASM-compilable language) or **jsPolicy** (JS/TS).
- **CI-time / dashboard auditing without an admission webhook** → **Polaris**.
- **Runtime detection of malicious behavior** → **Falco** (mature, large rule library).
- **Runtime detection *and* enforcement at the kernel** → **Tetragon**.

Admission and runtime are complementary, not competing — a typical hardened cluster runs one tool from each row.

---

## Aside: what "constraint audit" means

The matrix's "Audits live cluster state" row references **constraint audit** (Gatekeeper) and **background scan** (Kyverno). Both are the same idea under different names: a controller that periodically re-evaluates the cluster's existing objects against the installed policies, *without* blocking anything, and writes the results back as status or as `PolicyReport` CRDs.

### Gatekeeper's model

Gatekeeper builds two CRD layers on top of OPA:

- **ConstraintTemplate** — defines a *type* of policy in Rego (e.g. `K8sRequiredLabels`).
- **Constraint** — an instance of that template applied with parameters (e.g. "all Namespaces must have a `cost-center` label").

For every Constraint, Gatekeeper runs two parallel loops:

1. **Admission enforcement.** A ValidatingWebhook calls OPA on every `CREATE` / `UPDATE` and rejects requests that violate the Constraint. This only protects *new or changed* objects going forward.
2. **Constraint audit.** A background controller wakes up on `auditInterval` (default 60s), lists the matching kinds from the API server (or from Gatekeeper's `sync` cache), runs each object through OPA, and writes findings to the Constraint's `.status.violations`. A Prometheus metric (`gatekeeper_violations`) is exported alongside.

```yaml
status:
  violations:
    - enforcementAction: deny
      kind: Namespace
      message: 'you must provide labels: {"cost-center"}'
      name: legacy-team
```

### Why audit exists *in addition to* admission

Three structural reasons admission-only is not enough:

- **Pre-existing objects.** When you install a new Constraint, the cluster is already full of resources the webhook never saw. Audit surfaces which of them are non-compliant.
- **Cluster-wide invariants.** A Constraint may depend on the whole data set ("only one Ingress per host"); admission only sees the inbound request. Audit re-evaluates with the live picture and catches drift introduced by *other* operations.
- **Dry-run / progressive rollout.** A Constraint can be installed with `enforcementAction: dryrun` (or `warn`). Admission then does not block, but audit still populates `.status.violations` — letting you measure impact before flipping to `deny`.

### How other tools express the same idea

| Tool | Name for the audit loop | Output |
|---|---|---|
| **Portal** | Informer/watch audit loop (`--audit`); same rules as admission | `PolicyReport` + AlertManager + Prometheus + actions |
| Gatekeeper | Constraint audit | `Constraint.status.violations`, `gatekeeper_violations` metric |
| Kyverno | Background scan | `PolicyReport` / `ClusterPolicyReport` CRDs |
| Kubewarden | Audit Scanner | `PolicyReport` CRDs |
| Polaris | Default mode (no admission loop) | Dashboard, JSON, exit code |

Functionally they are interchangeable for the "list and re-evaluate" job. They differ in what they emit (CRD vs metric vs alert) and in whether the same tool also enforces at admission time.

---

## Defense in depth

No single policy tool covers the full attack surface of a Kubernetes cluster. A workload passes through several control points between `kubectl apply` and a malicious syscall hitting the kernel, and each layer can only see — and stop — a subset of threats. A defense-in-depth posture stacks tools so a failure or gap at one layer is caught at the next.

### The layers

| Layer | When it runs | What it can stop | What it cannot stop | Representative tools |
|---|---|---|---|---|
| **1. Author-time** | Editor, pre-commit | Obvious misconfig in YAML before it's committed | Anything dynamic; anything authored elsewhere | `kubeconform`, `kube-linter`, `checkov`, IDE plugins |
| **2. CI / pipeline** | Pull request, build | Policy violations in manifests, Helm charts, Kustomize output, IaC | Cluster-state drift; out-of-band `kubectl apply` | **Polaris** (CLI), **Conftest** (Rego), Kyverno CLI, Datree successors, Checkov |
| **3. Supply chain** | Image build, registry push | Unsigned images, known CVEs, malicious base layers, SBOM violations | Zero-days; runtime tampering | Cosign / Sigstore, Trivy, Grype, Notary v2 |
| **4. Admission** | API server request, before persistence | Privileged pods, hostPath mounts, missing labels, unsigned images, forbidden APIs | Anything that happens *after* the pod starts | **Portal**, **OPA/Gatekeeper**, **Kyverno**, **Kubewarden**, **jsPolicy**, K8s `ValidatingAdmissionPolicy` (CEL) |
| **5. In-cluster posture / continuous audit** | Watch/event-driven re-evaluation against live cluster state | Drift from baseline, newly non-compliant objects, RBAC sprawl, abandoned secrets | Active exploitation | **Portal** (`--audit`), Kyverno background scan, Gatekeeper audit, **Polaris** dashboard, kube-bench, kubescape |
| **6. Network policy** | Every packet, plus *static* NetworkPolicy graph analysis | East-west lateral movement, unexpected egress, DNS exfiltration, *and* gaps in the declared NP graph (default-deny missing, unreachable selectors) | Anything within an allowed flow | Cilium, Calico, native `NetworkPolicy`, **Portal** (`--network` for static graph checks) |
| **7. Runtime detection** | Live syscalls and events | Shells in containers, crypto-miners, sensitive file reads, privilege escalation | Configuration mistakes; the breach itself if alerting is slow | **Falco**, **Tetragon** (detection mode), Tracee |
| **8. Runtime enforcement** | Live syscalls, in-kernel | The malicious syscall itself, in microseconds | Anything the policy didn't anticipate | **Tetragon** (`Sigkill` / `Override`), Falco + Talon (slower, userspace) |

### Why one layer is never enough

Each layer has a structural blind spot that only the next layer can cover:

- **Admission cannot see behavior.** A pod that declares `privileged: false` and a benign image can still `curl | sh` at runtime. Only layers 7–8 catch that.
- **Runtime cannot see intent.** Falco sees a shell spawn but not the YAML that mounted `/var/run/docker.sock` and made it possible. Only layer 4 prevents the mount in the first place.
- **CI cannot see the live cluster.** A manifest can pass every CI check and still be applied to the wrong namespace, or be edited in-place with `kubectl edit`. Only layer 5 notices the drift.
- **Image scans go stale.** A CVE disclosed an hour after the image was admitted is invisible to layer 3 forever; layer 5 (rescanning running images) or layer 7 (catching the exploit) has to cover it.
- **Network policy is allow-list, not behavior-aware.** An attacker who pivots inside an allowed flow looks legitimate to layer 6.

### A reference stack

For a hardened cluster, a defensible minimum is:

1. **CI** — Kyverno CLI or Conftest runs the same policies that the cluster enforces, against the manifest before merge.
2. **Supply chain** — Cosign-signed images, Trivy in the pipeline, signature verification enforced at admission.
3. **Admission** — Kyverno *or* OPA/Gatekeeper, with the policy library version-controlled alongside the apps.
4. **Continuous audit** — the same admission controller's background-scan mode, exporting Policy Reports to a dashboard.
5. **Network** — default-deny `NetworkPolicy` per namespace; Cilium if L7 semantics matter.
6. **Runtime** — Falco for breadth of detections and large rule library; Tetragon if you need synchronous enforcement.
7. **Response** — Falcosidekick + Falco Talon, or a SOAR, to turn alerts into containment actions (quarantine label, pod delete, NetworkPolicy patch).

The exact tool choices matter less than the layering. Two tools at the same layer is duplication; one tool covering two layers (e.g. Kyverno doing both admission and audit) is fine; *zero* tools at any layer is a gap that an attacker will eventually find.

### Policy authoring across layers — the open problem

The biggest unsolved issue in this space is that **no single policy language spans all layers**. The same intent ("workloads in `prod` must not run as root") is expressed differently in Rego (admission), a Falco rule (runtime), a Cilium policy (network), and a CI linter config — and they drift apart over time.

Projects attempting to unify this surface (OpenSSF policy WG, the CNCF Policy WG's `PolicyReport` CRD, Styra DAS's multi-system control plane, Kyverno's expansion into image verification and CEL) are partial answers. A tool that genuinely lets one policy compile to multiple enforcement points is still an open opportunity.

---

## Rego — the language behind OPA/Gatekeeper

**Rego** is the declarative policy language used by the Open Policy Agent. It is the canonical answer to "how do I express a policy as code?" in the CNCF ecosystem and is also embedded by Kubewarden, Conftest, Styra DAS, and others.

### Heritage
Rego is inspired by **Datalog**, a subset of Prolog with guaranteed termination. From Datalog it inherits:

- A purely declarative model — you describe *what* is true, never *how* to compute it.
- Set-oriented evaluation — rules iterate over collections implicitly.
- Unification of queries and data — both are values in the same JSON-shaped document tree.

### Core model
Every Rego program operates on a single virtual document tree rooted at `data`, plus a per-query `input` document. Policies are written as rules that produce values:

```rego
package kubernetes.admission

deny[msg] {
    input.request.kind.kind == "Pod"
    input.request.object.spec.containers[_].securityContext.privileged == true
    msg := "privileged containers are not allowed"
}
```

Key semantics:

- **Rules** assign a value to a virtual document path when their body is true. Multiple rules with the same head form a *set* or *partial* definition (here, `deny` is a set of denial messages).
- **Variables** are unified, not assigned. `_` is a wildcard that iterates over collection members.
- **No side effects, no loops, no mutation.** Iteration is implicit through variable references.
- **Total functions over JSON.** Every expression produces a value, an undefined, or an error; there is no `nil`/`null` confusion.

### Why teams love it

- Composability: rules are values, so a policy library can be assembled from imported packages.
- Partial evaluation: OPA can pre-compute parts of a policy against known data, producing fast residual queries — this is what makes Gatekeeper viable at admission-time scale.
- Single language for many domains: the same Rego engine evaluates Kubernetes admission, Terraform plans, microservice authz, CI configs, and SQL row filters.

### Why teams struggle with it

- The paradigm shift from imperative to logic programming is real; new users routinely write rules that look correct but never match.
- Error messages historically lag the language; OPA 0.50+ improved this but Rego is still less forgiving than CEL or a YAML DSL.
- Debugging requires `opa eval --explain` or the Rego Playground — there is no step debugger in the conventional sense.
- The "everything is JSON, every rule is a set" mental model is powerful but uncommon, which limits the talent pool.

### Rego v1 (OPA 1.0)
Released **2024-11**. Tightens defaults: `if` and `contains` keywords are required, imports are stricter, and ambiguous rules are rejected at parse time. Existing v0 policies continue to run but are scheduled to be migrated. New work should target v1.

### Alternatives gaining ground
- **CEL** (Common Expression Language) — adopted natively by Kubernetes (`ValidatingAdmissionPolicy`, CRD validation) and by Kyverno. Less expressive than Rego but built into the API server and far easier to learn.
- **WebAssembly policies** (Kubewarden) — sidestep the DSL question entirely by letting you write in a general-purpose language.
- **YAML DSLs** (Kyverno, Falco rules) — domain-specific and approachable but bounded in what they can express.

Rego remains the gold standard when policy logic is genuinely complex; for simple "field X must equal Y" checks, CEL or a YAML DSL is usually a better fit.
