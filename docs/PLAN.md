# Portal — an evolution of podwatcher-poc

## Context

`podwatcher-poc` is a Java/SpEL layer-5 pod-only continuous-audit scanner: it polls the K8s API, runs SpEL rules over a normalised `Context`, and fires AlertManager alerts. It cannot prevent anything, it sees pods only, polling is slow at scale, and ecosystem integration (PolicyReport CRDs, admission) is absent.

Portal is the productised successor:

- **Language pivot to Go + `expr-lang/expr`.** The SpEL rule-language ergonomics (terse, null-safe, set literals) are preserved syntactically by `expr-lang/expr`; the K8s plumbing moves to `client-go`/`controller-runtime`, the natural ecosystem for admission webhooks and informers. Cross-checked against the six podwatcher-poc example rules: four translate byte-for-byte, two need `.contains()` → `in` — trivial migration.
- **Layer pivot from 5 to 4+5+6 in v1, 7 in v2.** Admission webhook, informer-driven audit (no polling), static NetworkPolicy analyser, and a response-action engine generalised from podwatcher-poc's existing AlertManager call.

## Scope decisions (confirmed with user)

| Decision | Choice |
|---|---|
| Language / runtime | **Go** (1.22+), single static binary, distroless or scratch image |
| Expression engine | **`github.com/expr-lang/expr`** — adopted directly. Existing podwatcher-poc rule files migrated with a one-time syntax fixup script (4/6 unchanged, 2/6 swap `.contains()` for `in`) |
| K8s client | `client-go` + `controller-runtime` informers; `kubernetes-sigs/controller-runtime/pkg/webhook` for admission |
| Platform | Kubernetes only — drop Docker daemon mode |
| v1 layers | 4 (admission) + 5 (continuous audit, opt-in) + 6 (declarative NetworkPolicy analysis, event-driven) |
| Cross-resource policies | **Full v1.** `cluster.<gvk>.byName(ns,name)` and `cluster.<gvk>.list(ns,selector)` exposed to expr-lang, backed by informer caches. Reverse-dependency index re-evaluates dependents when referenced resources change. Cycle protection via bounded re-eval budget. Admission consistency via virtual cluster view (inbound object materialised into the read path) |
| Rule distribution | **CRDs primary + folder fallback.** `PortalClusterRule` (cluster-scoped) and `PortalRule` (namespaced) as the canonical surface — `kubectl apply`, K8s RBAC, GitOps-native, `.status` reporting per rule. `--rules-folder` flag retained for dev, podwatcher-poc migration, and bootstrap. `portal migrate-rules` writes CRs by default, folder format on `--format=folder` |
| Webhook failure mode | **Fail-closed default (`failurePolicy: Fail`).** Portal becomes part of the API server's critical path; ≥2 replicas + PDB mandatory. `kube-system`, `kube-public`, `kube-node-lease`, and Portal's own namespace excluded via `namespaceSelector`. `timeoutSeconds: 5`. Break-glass bypass via `portal.io/bypass=true` annotation, audited. Helm `global.failClosed: false` available for teams who prefer podwatcher-poc's "if it dies, cluster is fine" property |
| Admission capabilities | Validation only, per-rule `enforcementAction: deny \| warn \| dryrun` |
| Resource scope | Any K8s GVK. `object.*` (raw `unstructured.Unstructured`) is always available as the universal accessor. Pod-shaped sugar (`container.*`, `spec.host*`, `securityContext.*`, `metadata.*`) is a **deliberately narrow façade matching podwatcher-poc's surface** — not a typed mirror of `v1.Pod`. Anything outside the sugar reaches `object.*` |
| Output channels | Admission response, PolicyReport CRD (`wgpolicyk8s.io/v1alpha2`), AlertManager, Prometheus |
| Audit loop | Opt-in via flag; **informer/watch-based, never polling**. p99 < 1 s event→action |
| Latency targets | Admission decision p99 < 20 ms (Go in-process eval); audit event→action dispatch p99 < 1 s |
| Network policy | **Declarative NetworkPolicy analysis — event-driven, not periodic.** Reasons about the declared NP graph (CRs + pod labels) and re-evaluates on informer events for Pods, NetworkPolicies, and Namespaces. Sub-second propagation when cluster state changes. Findings clear when fixes are applied. No live packet/flow observation (Hubble/Cilium territory) |
| v1 response actions | Per-rule action list fires from admission decisions and audit findings — `alertmanager`, `label`, `annotate`, `evict`, `patch-networkpolicy`, `revoke-sa-token` |
| v2 layer 7 | K8s API audit log as a new event source, feeds same v1 action engine. No eBPF in Portal |
| v3+ | Tetragon/Falco event consumer, mutation rules, alternate engines (CEL/starlark) alongside expr-lang |

## Comparison matrix: Portal vs podwatcher-poc vs the field

Positioning Portal against podwatcher-poc and the seven tools in `/Users/vilaca/work/portal/docs/feature-matrix.md` (OPA/Gatekeeper, Kyverno, Kubewarden, jsPolicy, Polaris, Falco, Tetragon).

### Category & stack

| | **Portal (v1)** | **podwatcher-poc** | OPA/Gatekeeper | Kyverno | Kubewarden | jsPolicy | Polaris | Falco | Tetragon |
|---|---|---|---|---|---|---|---|---|---|
| Primary layer | **4 + 5 + 6** | 5 | 4 + 5 | 4 + 5 | 4 | 4 | 2 + 5 | 7 | 7 + 8 |
| Language / runtime | **Go** static binary | Java / JVM | Go | Go | Rust (WASM host) | Node.js | Go (CLI + dashboard) | C / eBPF | Go + eBPF |
| Rule language | **expr-lang/expr** | SpEL | Rego | YAML DSL + CEL | Rust/Go/Rego/Swift/JS via WASM | JS/TS | YAML toggles | Falco rules DSL | TracingPolicy CRD + eBPF |
| Stage / maturity | Greenfield (POC successor) | POC | CNCF Graduated | CNCF Incubating | CNCF Sandbox | Community (Loft) | Community (Fairwinds) | CNCF Graduated | CNCF Incubating |

### Capabilities

| | **Portal (v1)** | **podwatcher-poc** | OPA/Gatekeeper | Kyverno | Kubewarden | jsPolicy | Polaris | Falco | Tetragon |
|---|---|---|---|---|---|---|---|---|---|
| Admission validation | **Yes** (`deny`/`warn`/`dryrun`) | No | Yes | Yes | Yes | Yes | No | No | No |
| Admission mutation | No (v3 maybe) | No | Yes | Yes (strong) | Yes | Yes | No | No | No |
| Resource generation / cleanup | No | No | No / No | Yes / Yes | No / No | Limited | No | No | No |
| Continuous audit (live state) | **Yes — informer/watch, never poll** | Yes — poll | Yes — constraint audit | Yes — background scan | Yes — Audit Scanner | Yes | Primary mode | N/A | N/A |
| Resource scope | **Any GVK** (`object.*`); pod sugar for compat | Pods + containers only | Any K8s object | Any K8s object | Any K8s object | Any K8s object | Workloads | Live processes | Live processes |
| Cross-resource policies | **Yes (v1)** — `cluster.<gvk>.*` helpers + reverse-dep index | No | Yes | Yes | Limited | Yes | Limited | N/A | N/A |
| Image signature verification | No (v3 candidate) | No | Custom Rego | Yes (native) | Yes (sigstore) | Via JS | No | No | No |
| Static NetworkPolicy analysis | **Yes (built-in)** | No | No (write your own) | No (write your own) | No | No | No | No | No |
| Runtime detection | **v2** — K8s API audit log source | No | No | No | No | No | No | Yes (syscalls, kaudit) | Yes (process, net, file) |
| Runtime enforcement | **v2 — response-based** (label, evict, patch NP, revoke SA token) | No | No | No | No | No | No | Limited via Talon | Yes (kernel: `SIGKILL`, override) |
| Cross-resource action (e.g. NP patch on violation) | **Yes (v1)** | No (alert only) | No | No | No | No | No | No | No |

### Outputs & integration

| | **Portal (v1)** | **podwatcher-poc** | OPA/Gatekeeper | Kyverno | Kubewarden | jsPolicy | Polaris | Falco | Tetragon |
|---|---|---|---|---|---|---|---|---|---|
| AlertManager (native) | **Yes** (drop-in compat with podwatcher's JSON) | Yes | No | No | No | No | No | Via sidekick | No |
| PolicyReport CRD | **Yes** | No | Via gatekeeper-policy-manager | Yes (first-class) | Yes | Limited | Yes | No | No |
| Admission denial in `kubectl` | **Yes** | N/A | Yes | Yes | Yes | Yes | N/A | N/A | N/A |
| Prometheus metrics | **Yes** | Yes | Yes | Yes | Yes | Some | No | Via exporters | Yes |
| Structured JSON logs | **Yes (`slog`)** | Yes (Log4j2) | Yes | Yes | Yes | Yes | Yes | Yes | Yes |
| GitOps friendliness | YAML rules in folder | YAML rules in folder | CRDs | CRDs | CRDs + OCI | CRDs / npm | Static config | Rule files | CRDs |

### Operational profile

| | **Portal (v1)** | **podwatcher-poc** | OPA/Gatekeeper | Kyverno | Kubewarden | jsPolicy | Polaris | Falco | Tetragon |
|---|---|---|---|---|---|---|---|---|---|
| Watch vs poll for audit | **Watch (informers)** | Poll (list every cycle) | Watch | Watch | Watch | Watch | One-shot | Stream (kernel) | Stream (kernel) |
| Latency to detect (admission) | Real-time (< 20 ms p99 budget) | N/A | Real-time | Real-time | Real-time | Real-time | N/A | N/A | N/A |
| Latency to detect (audit) | Real-time event-driven (< 1 s p99 budget) | Up to one poll interval | Real-time (event) | Real-time (event) | Real-time | Real-time | Manual / CI | µs (kernel) | µs (kernel) |
| Memory footprint (webhook) | ~30 MB (Go) | N/A | Moderate (OPA pod per replica) | Light–moderate | Light (WASM) | Light (Node) | N/A | Light per node | Very light (eBPF) |
| Failure mode | **Fail-closed default** (`failurePolicy: Fail`); `global.failClosed: false` for fail-open. System namespaces always excluded | Cluster unaffected | Configurable | Configurable | Configurable | Configurable | N/A | Cluster unaffected | Cluster unaffected |
| HA story | Lease-based leader election; informers on all replicas | None documented | Standard | Standard | Standard | Standard | N/A | DaemonSet | DaemonSet |
| Multi-cluster | Per-cluster install | Per-cluster install | Add-ons (Rancher/ACK) | Native multi-tenancy | Yes (CRDs) | Yes | Via CLI batches | Falcosidekick, Talon | Yes |

### Rule ergonomics (the wedge)

| | **Portal** | **podwatcher-poc** | OPA/Gatekeeper | Kyverno (pattern) | Kyverno (CEL) | Polaris | Falco |
|---|---|---|---|---|---|---|---|
| "Block privileged container" lines | ~8 | ~8 | ~12 (Rego) | ~18 (pattern) | ~10 (CEL) | toggle | rule block |
| Boilerplate per rule | metadata + boolean | metadata + boolean | template + constraint | apiVersion + spec wrappers | apiVersion + spec wrappers + CEL | none (fixed) | YAML + DSL |
| Implicit container iteration | **Yes** (rule eval'd per std/init/ephem) | Yes | Manual | `foreach` / repeat blocks | `exists(c, ...)` | N/A | N/A |
| Null-safe navigation | `?.` + `??` | `?.` + `?:` | `not / default` | `()` / `=()` anchors | `has(...) &&` | N/A | N/A |
| Inline set literal | `x in ['a','b']` | `{'a','b'}.contains(x)` | set construct | N/A | `['a','b'].exists(...)` | N/A | N/A |
| Engine swap without rule rewrite | **Yes** (`ExpressionEngine` iface; CEL/Rego pluggable v3) | No (SpEL embedded) | No (Rego is the engine) | Yes (pattern / CEL / deny / JMESPath) | — | No | No |

### What Portal does that nobody in this matrix does

- **Generalised response-action engine off admission and audit decisions.** AlertManager call → label → annotate → evict → patch NetworkPolicy → revoke SA token, all idempotent and rate-limited, all from the same rule schema. Closest analogue is Kyverno+Falco+Falco Talon stitched together; Portal does it in one binary.
- **AlertManager-native admission tool.** Every other admission tool emits via PolicyReport, decision logs, or kubectl warnings only. Portal preserves podwatcher-poc's AlertManager JSON byte-for-byte so existing Prometheus routing keeps working when teams upgrade.
- **Built-in static NetworkPolicy analysis as first-class output.** Other tools support writing such checks; Portal ships them out of the box (default-deny gaps, broad CIDRs, unreachable selectors) and emits them through the same PolicyReport/AlertManager pipeline.
- **Pluggable expression engine behind a stable rule schema.** Rule files do not embed engine choice; expr-lang today, CEL/Rego/starlark drop-in for v3 without invalidating existing rules. Only Kyverno comes close (multi-engine `validate`), and Kyverno's surrounding YAML is far more verbose.
- **Rule-corpus migration path from podwatcher-poc.** `portal migrate-rules` rewrites SpEL→expr-lang differences (`.contains()` → `in`, brace-sets → bracket-lists, `filter.namespace` → `match.namespaces`). 4/6 example rules transfer byte-identical; the other 2 are one-line tweaks. No other admission tool has a documented migration path from a different rule language.

### What Portal does *not* do (vs the field)

- **No kernel-level runtime enforcement.** Tetragon's `SIGKILL`/syscall-override territory is out of scope. Portal's v2 runtime story is K8s-API-audit-driven detection + response-based enforcement (Falco Talon class), not Tetragon class.
- **No mutation / generation / cleanup in v1.** Kyverno's bread and butter; explicit non-goals for Portal v1, possibly v3.
- **No image signature verification.** Kyverno (native) and OPA + sigstore Rego own this; Portal v1 doesn't.
- **No graduated CNCF stage.** Portal is greenfield. Maturity, scale evidence, and a rule library are all to-be-built.

### Honest positioning

Portal sits in the layer-4-to-6 band, with v2 extending into layer 7 detection + response. Closest competitor by *category* is **Kyverno**: both do admission + audit + (with v2) some runtime adjacency. Portal's bets vs Kyverno are (a) terser rule ergonomics via expr-lang + the pod sugar façade, (b) AlertManager-native output for Prometheus-first orgs, (c) built-in NetworkPolicy analysis, (d) generalised response actions. Kyverno's bets the other way are (a) mutation/generation/cleanup, (b) image signature verification, (c) graduation maturity + a large public policy library, (d) richer match/exclude semantics. The two are complementary in defense-in-depth (Portal alongside Falco/Tetragon is the natural runtime extension), but a cluster that already runs Kyverno *and* doesn't care about AlertManager-native output has little reason to add Portal.

## Documentation as a first-class deliverable

Documentation is not "after v1" — every phase ships with its docs, no merge without them. Underdocumented K8s tools become Falco-rule-cookbook-by-Stack-Overflow situations; we avoid that by making docs a release gate.

### Cross-cutting documentation rule

Every PR that introduces or changes a user-visible behaviour must include the corresponding doc change in the same PR. CI fails if a public-API symbol, CRD field, rule-schema field, Helm value, metric, or built-in action is added without its doc entry. Specifically:

- New rule-schema field → entry in `docs/reference/rule-schema.md`.
- New CRD field → regenerated `docs/reference/crd/{portalclusterrule,portalrule}.md` from the OpenAPI schema.
- New built-in action → entry in `docs/reference/actions.md` (params, RBAC, examples).
- New Prometheus metric → entry in `docs/reference/metrics.md` (type, labels, when emitted, alerting guidance).
- New Helm value → entry in `docs/reference/helm-values.md`.
- New `cluster.<gvk>.*` helper or expression-engine binding → entry in `docs/reference/expression-language.md`.
- New ADR-worthy decision → file under `docs/adr/NNNN-title.md`.

### Documentation site / structure

All docs live in-repo under `docs/`, versioned with code, reviewed in PRs. Rendered by mkdocs-material (or similar) and deployed via GitHub Pages from `main`. Structure:

```
docs/
├── README.md                            entry point, what Portal is, why
├── getting-started/
│   ├── quickstart-kind.md               install Portal on kind in 5 minutes
│   ├── install-helm.md                  production install with fail-closed semantics
│   └── first-rule.md                    write and apply your first PortalClusterRule
├── concepts/
│   ├── architecture.md                  layers, modules, dataflow + diagrams
│   ├── admission-vs-audit.md            when each fires, ordering, dryrun/warn/deny
│   ├── cross-resource.md                cluster.<gvk>.* model, dep tracking, consistency
│   ├── context-and-pod-sugar.md         object.* vs container.* — when to use which
│   ├── actions-and-rate-limiting.md     idempotency, rate-limit windows, audit log
│   └── fail-closed.md                   what it means, recovery, break-glass
├── reference/
│   ├── rule-schema.md                   every field, every default, every interaction
│   ├── expression-language.md           expr-lang in Portal — bound env, helpers, examples
│   ├── crd/portalclusterrule.md         auto-generated from OpenAPI
│   ├── crd/portalrule.md                auto-generated
│   ├── actions.md                       every built-in action: params, RBAC, examples
│   ├── metrics.md                       every Prometheus metric: type, labels, alerting
│   ├── helm-values.md                   every value: type, default, effect
│   └── cli.md                           `portal` + subcommands (migrate-rules, version, etc.)
├── cookbook/
│   ├── disallow-privileged.md           the classic; in admission + audit forms
│   ├── enforce-labels.md
│   ├── require-pdb-per-deployment.md    showcases cross-resource
│   ├── allowed-registries.md
│   ├── non-root-containers.md
│   ├── networkpolicy-default-deny.md
│   ├── quarantine-on-violation.md       label + alert action combo
│   └── revoke-sa-token-on-exec.md       v2 runtime preview
├── migration/
│   ├── from-podwatcher-poc.md           step-by-step, with portal migrate-rules walkthrough
│   ├── side-by-side-rule-syntax.md      SpEL → expr-lang differences with the 6 examples
│   └── coexistence-with-kyverno.md      when to use both, when to replace
├── operator/
│   ├── ha-and-leader-election.md
│   ├── rbac-scoping.md                  conditional permissions per enabled action
│   ├── certificate-management.md        cert-manager and built-in CA bootstrap
│   ├── upgrading.md                     version skew, CRD conversion, rollback
│   ├── troubleshooting.md               common errors, debug procedures, log levels
│   ├── observability.md                 dashboards, alerts, golden signals
│   └── recovery-from-self-lockout.md    break-glass for fail-closed misconfig
├── security/
│   ├── threat-model.md                  what Portal protects against, what it doesn't
│   ├── rbac-posture.md
│   ├── supply-chain.md                  image signing, SBOM, distroless base
│   └── responsible-disclosure.md
├── plugin-author/
│   ├── custom-action.md                 implement Action, register, ship
│   ├── custom-sink.md
│   ├── custom-expression-engine.md      e.g. adding CEL as alternate
│   └── interface-reference.md           auto-generated from internal/api Go doc
├── contributing/
│   ├── repo-layout.md
│   ├── module-boundaries.md
│   ├── testing-approach.md
│   └── release-process.md
├── adr/                                 architecture decision records
│   ├── 0001-go-and-exprlang.md          why not Java/SpEL; why not Go/CEL
│   ├── 0002-crds-primary-folder-fallback.md
│   ├── 0003-fail-closed-default.md
│   ├── 0004-no-mutation-no-ebpf.md      v1 scope boundaries
│   ├── 0005-cross-resource-full-v1.md
│   └── 0006-pod-sugar-narrow-facade.md
└── comparison/                          existing docs from portal/docs/ moved here
    ├── feature-matrix.md
    └── podwatcher-comparison.md
```

### Documentation that is *generated*, not hand-written

- **CRD reference** (`docs/reference/crd/*.md`): generated from CRD OpenAPI schemas via `crd-ref-docs` or similar. Regenerated in CI; commit diff if drift detected.
- **Go interface reference** (`docs/plugin-author/interface-reference.md`): generated from `internal/api/*.go` Godoc via `gomarkdoc`.
- **Metric reference**: generated from a registry in `internal/sink/prometheus/metrics.go` that pairs each metric with a description string. CI parses the file and checks for missing descriptions.
- **Helm values reference**: generated from `values.yaml` comments via `helm-docs`.
- **CLI reference**: generated from cobra `--help` output.

This keeps reference docs in sync with code mechanically; reviewers focus on the hand-written conceptual content.

### Diagrams

Architecture and sequence diagrams shipped as Mermaid in markdown — no proprietary tools, renders on GitHub natively. Minimum set:

- Layer model (4/5/6/7 across cluster).
- Admission request sequence (kubectl → API server → webhook → engine → response).
- Audit event sequence (informer → controller → engine → actions/sinks).
- Cross-resource dep-index lifecycle (rule eval records → resource changes → dependents requeued).
- Action dispatch with idempotency + rate limit (event → dispatcher → action → audit log).
- Fail-closed recovery procedure.

### Phase 8 — Documentation site (`docs/` finalisation)

While docs accumulate during phases 1–7, Phase 8 finalises the user-facing assets that span phases:

28. Quickstart on kind — copy-pasteable, tested in CI.
29. Migration guide from podwatcher-poc — walks through `portal migrate-rules` on the actual example folder.
30. Cookbook entries for every built-in action, every NetworkPolicy check, and at least three cross-resource patterns.
31. Threat model document — explicit about what Portal does and doesn't defend against.
32. ADRs for the load-bearing decisions (Go+expr-lang, CRDs primary, fail-closed default, no mutation, no eBPF, cross-resource v1, pod sugar narrow façade).
33. mkdocs config + GitHub Pages deploy from `main`.
34. CI gate: `make docs` must succeed (link-check, code-block syntax check, mermaid render check, generated-doc drift check).

### Documentation testing

- **Link check** — every internal link resolves; every external link returns 200 (cached, weekly run).
- **Code-block execution** — bash/yaml/expr-lang blocks marked `<!-- runnable -->` are executed in CI against a kind cluster; failures block merge.
- **Example-rule round-trip** — every cookbook rule is loaded by Portal in CI, evaluated against a fixture, asserted to produce the documented decision.
- **Generated-doc drift** — running the generator must produce no diff against committed files.

## Modularity principles (load-bearing)

Every layer must be independently buildable, testable, runnable, and disable-able.

- **One-way dependency graph.** Only `internal/api` (the interface/DTO package) is depended on by others. `admission`, `audit`, `network`, `actions` never depend on each other; communication is via interfaces declared in `internal/api`.
- **Per-layer toggles.** `--admission`, `--audit`, `--network` flags (and Helm `values.yaml`) gate startup of each layer. Portal in admission-only mode doesn't start informers; audit-only mode doesn't serve TLS. RBAC is conditional on flags.
- **Pluggable contracts** (Go interfaces, registered via package `init()` into a central registry):
  - `ExpressionEngine` — expr-lang today; CEL/starlark drop-in for v3 with no rule-schema change.
  - `Action` — adding a new action type (Slack, PagerDuty) is one struct + one `Register()` call.
  - `OutputSink` — AlertManager, PolicyReport, Prometheus, stdout-JSON behind one interface; each independently enable-able.
  - `EventSource` — admission webhook and audit informer are the v1 sources; v2's API-audit-log source implements the same interface.
  - `ContextBuilder` — pod sugar today; per-GVK builders pluggable.
- **Optional Deployment-per-layer.** Default chart ships one Deployment running every enabled layer. A `mode: split` Helm value deploys one Deployment per layer (webhook, audit, network) for independent scaling. Module boundaries make this deployment-time, not refactor-time.

## Modules and interfaces

Go module: `github.com/vilaca/portal` (or similar). Package layout:

| Package | Responsibility | Depends on | Exposes |
|---|---|---|---|
| `internal/api` | Pure interfaces + DTOs only. Zero K8s/expr-lang dependency. | — | `Rule`, `Violation`, `Decision`, `Context`, `EventMeta`, `ExpressionEngine`, `RuleEngine`, `EventSource`, `OutputSink`, `Action`, `ActionDispatcher`, `RateLimiter`, `IdempotencyStore`, `ContextBuilder` |
| `internal/rule/loader` | YAML loader + validator for the rule schema. Two sources behind one interface: folder loader (filesystem walk + watch) and CR loader (informer on `PortalClusterRule` + `PortalRule`). Same parsed `Rule` regardless of source. | `internal/api`, `sigs.k8s.io/yaml`, `client-go` informers | `RuleLoader` interface; `NewFolder(path)`, `NewCR(client)` |
| `internal/rule/crd` | Generated Go types for `PortalClusterRule` / `PortalRule` CRDs via `controller-gen`. Status reconciler writes `.status` back to each CR (eval count, last-applied, parseError). | `internal/api`, `client-go` | `Reconciler` |
| `internal/lookup` | Cluster lookup helpers exposed to expr-lang as `cluster.<gvk>.byName(ns,name)` / `cluster.<gvk>.list(ns,selector)`. Reverse-dependency index — records what each (rule, object) read; on referenced-resource change, enqueues re-eval of dependents. Cycle protection via bounded re-eval budget per object per window. Admission virtual-cluster view (inbound object materialised). | `internal/api`, audit's informer caches | `Lookup` (registered as expr-lang env extension) |
| `internal/expr/exprlang` | `expr-lang/expr` implementation of `ExpressionEngine`. Default engine. | `internal/api`, `github.com/expr-lang/expr` | `New() ExpressionEngine` (registered at init) |
| `internal/engine` | Rule dispatch: indexes rules by GVK, evaluates `Context`, produces `[]Violation`. | `internal/api` | `New(engines, rules) RuleEngine` |
| `internal/context/pod` | Pod-shaped `ContextBuilder` — façade preserving podwatcher-poc rule compatibility. | `internal/api`, `k8s.io/api/core/v1` | `New() ContextBuilder` |
| `internal/sink/alertmanager` | Re-implementation of podwatcher-poc's AM client in Go behind `OutputSink`. | `internal/api`, `net/http` | `New(cfg) OutputSink` |
| `internal/sink/policyreport` | `wgpolicyk8s.io/v1alpha2` emitter via dynamic client. | `internal/api`, `client-go/dynamic` | `New(client) OutputSink` |
| `internal/sink/prometheus` | Counters/gauges/histograms; serves `/metrics`. | `internal/api`, `prometheus/client_golang` | `New() OutputSink` |
| `internal/sink/stdout` | JSON log sink via `slog`. | `internal/api`, `log/slog` | `New() OutputSink` |
| `internal/admission` | TLS webhook server; `AdmissionReview` → `Context` → `EventSource.emit()`. | `internal/api`, `controller-runtime/webhook` | `New(engine, dispatcher, sinks) EventSource` |
| `internal/audit` | Informer-driven event source for any audited GVK; leader election. | `internal/api`, `client-go/informers`, `controller-runtime/manager` | `New(client, gvks, engine, dispatcher) EventSource` |
| `internal/network` | NetworkPolicy static analyser; emits synthetic violations through the same pipeline. | `internal/api`, audit's caches | `New(podLister, npLister) EventSource` |
| `internal/actions/engine` | Bounded dispatch pool + rate limit + idempotency cache; routes `Violation` to enabled `Action`s. | `internal/api` | `New(actions, limiter, idem) ActionDispatcher` |
| `internal/actions/label` | Add/remove labels on the violating object via server-side apply. | `internal/api`, `client-go` | `Action` |
| `internal/actions/annotate` | Add/remove annotations. | `internal/api`, `client-go` | `Action` |
| `internal/actions/evict` | `policy/v1.Eviction` on pods. | `internal/api`, `client-go` | `Action` |
| `internal/actions/patchnp` | Server-side apply on `NetworkPolicy`. | `internal/api`, `client-go` | `Action` |
| `internal/actions/revoketoken` | Delete SA token Secret, force rotation. | `internal/api`, `client-go` | `Action` |
| `cmd/portal` | Composition root — flag parsing, registry wiring, layer startup. Tiny. | every enabled module | `main()` |
| `deploy/helm` | Chart + CRDs + RBAC + ValidatingWebhookConfiguration. | — | Helm chart |
| `deploy/crds` | Vendored `wgpolicyk8s.io` PolicyReport CRD. | — | YAML |
| `deploy/test` | End-to-end kind tests. | everything | `go test` harness |

### Key interface signatures (all in `internal/api`)

```go
package api

type ExpressionEngine interface {
    Name() string                                            // "expr", "cel", "rego"
    Compile(expression string) (Program, error)
}

type Program interface {
    Eval(ctx Context) (bool, error)
}

type Action interface {
    Type() string                                            // "label", "evict", "alertmanager", ...
    Execute(ctx context.Context, v Violation, params map[string]any) error
    Idempotent() bool
    DefaultRateLimit() time.Duration
}

type OutputSink interface {
    Name() string                                            // "alertmanager", "policyreport", ...
    Emit(ctx context.Context, v Violation) error
    Close() error
}

type EventSource interface {
    Name() string                                            // "admission", "audit", "network", v2: "api-audit-log"
    Start(ctx context.Context, onEvent func(Context, EventMeta)) error
    Stop(ctx context.Context) error
}

type ContextBuilder interface {
    Supports(gvk schema.GroupVersionKind) bool
    Build(obj *unstructured.Unstructured) (Context, error)   // raw → rich
}

type RuleEngine interface {
    Evaluate(ctx Context, meta EventMeta) []Violation
}

type ActionDispatcher interface {
    Dispatch(ctx context.Context, v Violation)               // non-blocking; respects rate limit + idempotency
}
```

Implementations register themselves via `init()` into package-level registries (`api.RegisterEngine`, `api.RegisterAction`, `api.RegisterSink`). `cmd/portal` enumerates registries at boot, filters by enabled flags, and injects them. No reflection, no DI framework — idiomatic Go.

## Architecture

```
portal/
├── go.mod
├── cmd/
│   └── portal/                 main.go — composition root
├── internal/
│   ├── api/                    interfaces + DTOs (zero deps)
│   ├── rule/
│   │   ├── loader/             folder loader + CR loader (one RuleLoader iface)
│   │   └── crd/                generated PortalClusterRule/PortalRule types + status reconciler
│   ├── lookup/                 cluster.<gvk>.* helpers + reverse-dep index
│   ├── engine/                 DefaultRuleEngine (GVK index, eval)
│   ├── expr/
│   │   └── exprlang/           expr-lang adapter (default ExpressionEngine)
│   ├── context/
│   │   └── pod/                Pod-shaped ContextBuilder
│   ├── sink/
│   │   ├── alertmanager/
│   │   ├── policyreport/
│   │   ├── prometheus/
│   │   └── stdout/
│   ├── admission/              TLS webhook (EventSource)
│   ├── audit/                  Informer-driven (EventSource) + leader election
│   ├── network/                NetworkPolicy static analyser
│   └── actions/
│       ├── engine/             DefaultActionDispatcher (rate limit + idempotency)
│       ├── label/
│       ├── annotate/
│       ├── evict/
│       ├── patchnp/
│       └── revoketoken/
└── deploy/
    ├── helm/                   Chart, conditional RBAC, webhook config
    ├── crds/                   wgpolicyk8s.io PolicyReport (vendored)
    └── test/                   End-to-end kind tests
```

## Rule schema (extended, expr-lang syntax)

```yaml
name: privileged container
enabled: true
severity: critical
mode: [admission, audit]            # NEW — which loop(s) evaluate this rule
enforcementAction: deny             # NEW — deny | warn | dryrun (admission only)
match:                              # NEW — replaces filter.namespace as the canonical form
  gvk:
    - { group: "",   version: v1, kind: Pod }
    - { group: apps, version: v1, kind: Deployment }
  namespaces:
    include: [production]
rule: >
  container.securityContext.privileged == true
  || container.securityContext.allowPrivilegeEscalation == true
alert: insecure-workload            # shorthand for actions: [{type: alertmanager, template: ...}]
actions:                            # NEW — explicit list; merged with `alert` shorthand
  - { type: alertmanager, template: insecure-workload }
  - { type: label, key: portal.security/quarantine, value: "true", on: [audit] }
  - { type: evict, on: [audit], rateLimit: 5/min }
```

Migration of existing podwatcher-poc rules: a `portal migrate-rules` subcommand applies the SpEL→expr-lang fixups (`{...}.contains(x)` → `x in [...]`, `.contains('y')` → `'y' in ...`) and rewrites `filter.namespace` into `match.namespaces`. Idempotent.

## Context model

`Context` carries three layers:

1. **`object` — always populated, for every GVK including Pod.** Raw resource as `*unstructured.Unstructured` exposed to expr-lang as nested maps. Anything in the K8s schema is reachable: `object.spec.replicas`, `object.spec.volumes[0].hostPath`, `object.spec.tolerations`, `object.spec.containers[0].resources.limits.memory`, `object.metadata.ownerReferences`, custom CRD fields, anything. This is the universal escape hatch — no field is ever inaccessible.
2. **Pod-shaped sugar — populated only for Pods and resources with a `PodTemplateSpec`.** A *deliberately narrow* convenience façade covering exactly the fields podwatcher-poc exposes today: `container.{name,containerType,image.{registry,name,tag,sha256},command,args,ports,securityContext.{privileged,allowPrivilegeEscalation,readOnlyRootFilesystem,runAsUser,runAsGroup,runAsNonRoot,procMount,seccompProfileType,capabilities.{add,drop}}}`, `spec.{hostPID,hostNetwork,hostIPC,serviceAccountName,automountServiceAccountToken}`, `securityContext.{runAsUser,runAsGroup,runAsNonRoot,fsGroup,supplementalGroups,seccompProfileType}`, `metadata.{name,namespace,labels,annotations}`.

   This is **not** a typed mirror of `v1.Pod`. Volumes, env, probes, lifecycle, nodeSelector, tolerations, affinity, topology constraints, priorityClassName, runtimeClassName, dns*, restartPolicy, schedulerName, imagePullSecrets, hostAliases, and most of `v1.Pod` are **not** in the sugar. Rules that want those fields use `object.spec.<path>` directly. Sugar grows additively when real rules demand it.

   The sugar's load-bearing job (beyond terseness) is **multi-container iteration**: the engine evaluates each rule once per standard/init/ephemeral container with `container` rebound each pass. That's hard to express from raw `object.*` and is why sugar exists at all.
3. **`request.*` — admission only.** operation, userInfo, dryRun, oldObject.

Implementation detail: expr-lang takes a `map[string]any` env. The pod ContextBuilder produces a map whose top-level keys are `container`, `spec`, `securityContext`, `metadata`, `object`, `request`. Generic (non-pod) GVKs only get `object`, `metadata`, `request`.

## Outputs

| Channel | When | Payload |
|---|---|---|
| Admission response | Per-request | `AdmissionReview.response.allowed=false` + `status.message` (deny); `warnings[]` (warn); `allowed=true` always (dryrun) |
| PolicyReport CRD | Per rule, per resource | One `PolicyReport` per namespace, one `ClusterPolicyReport` cluster-scoped; results merged across rules |
| AlertManager | Per violation | Match podwatcher-poc's JSON schema exactly so existing AM routes keep working |
| Prometheus | Continuous | `portal_admission_requests_total{decision}`, `portal_admission_latency_seconds`, `portal_audit_violations`, `portal_actions_total{action,result}`, `portal_audit_watch_reconnects_total`, `portal_np_findings` |

## v1 implementation plan

### Phase 1 — Repo skeleton & core
1. `go mod init`, `cmd/portal/main.go` stub, `internal/api` interfaces + DTOs.
2. CRD design + generated types: `PortalClusterRule` (cluster-scoped) + `PortalRule` (namespaced). Schema mirrors the rule YAML schema below. Generated via `controller-gen` under `internal/rule/crd/`. CRD YAMLs live in `deploy/crds/`. Validation: OpenAPI structural schema constrains field types; the `rule:` expression field is a string regex-sanity-checked at admission and fully validated by Portal which writes parse errors back to `.status.parseError`.
3. `internal/rule/loader` — `RuleLoader` interface with two implementations: `NewFolder(path)` (filesystem walk + `fsnotify` for hot-reload) and `NewCR(client)` (informers on the two CRDs). Both feed into a single in-memory rule index. `--rules-folder` and CR mode are independently enable-able; both can run simultaneously (folder for bootstrap, CRs for everything else).
4. `internal/rule/crd` — status reconciler: each `Reconcile()` writes `.status.{evalCount,violationCount,lastApplied,parseError,activeOn}` back to the CR. Update interval bounded by a token bucket so noisy rules don't hammer the API server.
5. `internal/expr/exprlang` adapter: compile expr-lang programs, register helper functions (`startsWith`, `matches`) where expr-lang's stdlib doesn't already cover them.
6. `internal/context/pod` builder + `internal/engine` GVK-indexed evaluator.
7. `internal/sink/alertmanager` — port podwatcher-poc's AM client (OkHttp → `net/http`, same retry/backoff semantics, same JSON shape).
8. `internal/sink/prometheus` + `/metrics` + `/healthz`.
9. `cmd/portal migrate-rules` — converts podwatcher-poc rule files into `PortalClusterRule` CR manifests (default) or to the new folder format with `--format=folder`. Idempotent.

### Phase 2 — Admission webhook (layer 4)
10. `internal/admission` using `controller-runtime/pkg/webhook` — TLS, `AdmissionReview` v1, GVK dispatch.
11. Decision aggregation: `deny` short-circuits; `warn` accumulates into `response.warnings`; `dryrun` only records to PolicyReport + metrics.
12. Cert bootstrap: optional cert-manager `Certificate` (production) or self-signed CA generator that writes a `Secret` and patches the `ValidatingWebhookConfiguration.caBundle` on startup (dev).
13. **Fail-closed defaults (security posture):**
    - `failurePolicy: Fail` in the ValidatingWebhookConfiguration template.
    - `timeoutSeconds: 5` (tight enough not to stall API calls; loose enough for p99 rule eval).
    - `namespaceSelector` excludes `kube-system`, `kube-public`, `kube-node-lease`, and Portal's own namespace via `matchExpressions: [{key: kubernetes.io/metadata.name, operator: NotIn, values: [...]}]`. Non-negotiable — protects against self-lockout.
    - Break-glass: requests with annotation `portal.io/bypass=true` on the *namespace* (not the inbound object) short-circuit the webhook to `allowed=true`. Each bypass increments `portal_admission_bypass_total{namespace}` and emits a JSON audit log line. The annotation requires `patch` on namespaces, which is normally a privileged RBAC — by design.
    - Health: `/readyz` returns `notReady` on any sustained internal error (rule index unloaded, panic in last N requests). Failed readiness pulls the pod from the Service endpoints; if all pods are unready, the webhook is effectively absent and the `failurePolicy` decides.
14. Helm chart (full chart detail in Phase 7): `global.failClosed: true` is the default; setting it to `false` rewrites the WebhookConfiguration to `failurePolicy: Ignore` for teams who want podwatcher-poc's "if it dies, cluster is fine" property. The system-namespace exclusion is **always** applied regardless of this flag.

### Phase 3 — Continuous audit (layer 5, opt-in via `--audit`)

Hard requirement: **no polling**. Watch/informer-only.

11. `internal/audit` using `controller-runtime/pkg/manager` with `SharedInformerFactory` — one informer per unique GVK referenced by audit-enabled rules. The local caches are shared across audit + network modules for cross-resource lookups (no extra API calls).
12. Event handlers: `OnAdd`/`OnUpdate` evaluate rules whose `mode` includes `audit` and synchronously enqueue any violations into the action engine; `OnDelete` garbage-collects PolicyReport entries. Evaluation runs on a bounded worker pool, off the informer thread.
13. Lease-based leader election via `client-go/tools/leaderelection`. Informers run on every replica (cache warmth, fast failover); only the leader dispatches actions / writes PolicyReports.
14. Periodic resync (default 10 min, configurable) is a safety net only — main path is watch events. Resync count exposed as a counter so we can prove watch is doing the work.
15. Watch reconnect: client-go handles `Gone` / backoff transparently; we expose `portal_audit_watch_reconnects_total` to observe.

### Phase 4 — Cross-resource lookups (`cluster.<gvk>.*`)

Builds on Phase 3 informers; lands before actions/NP so they can use cluster helpers.

15a. `internal/lookup/api.go` — expose `cluster.<gvk>.byName(ns, name)` and `cluster.<gvk>.list(ns, selector)` as expr-lang env values. Backed by audit's shared informer caches — no extra API calls.
15b. **Static dependency extraction:** when each rule's expression compiles, walk the expr-lang AST to extract every `cluster.<gvk>.*` call → record `(gvk, lookupKind, argShape)` per rule. This tells Portal which GVKs each rule reads, so it can ensure informers for those GVKs are running.
15c. **Runtime dependency index:** when rule R evaluates object O and the lookup helper returns resource X, record `(referenced=X) → depends=(R, O)`. Stored as an inverse index in `internal/lookup/depindex.go`. Bounded LRU (default 500 k entries, configurable); on eviction the next informer event / 10-min resync covers correctness.
15d. **Reverse-dep re-evaluation:** on `OnAdd`/`OnUpdate`/`OnDelete` for any resource, look up the dep index → enqueue re-eval of every `(rule, object)` pair that read it. Goes through the same worker pool as audit events.
15e. **Cycle protection:** per `(rule, object)` pair, allow at most N (default 3) re-evals in any sliding W-second (default 10s) window. Excess triggers `portal_lookup_cycle_suppressed_total` and an audit-log entry naming the rule and object; correctness preserved by the 10-min resync.
15f. **Admission consistency / virtual cluster view:** at admission, the inbound object isn't in the informer cache yet (CREATE) or is stale (UPDATE / DELETE). The lookup helper wraps the cache with a per-request overlay that materialises the inbound object before reads. For rules that need a stronger guarantee (e.g. uniqueness checks), expose `cluster.consistent.<gvk>.byName(...)` which bypasses cache and does a direct API call — slower (one round-trip added), opt-in.
15g. **RBAC:** informers now require `get,list,watch` on every GVK any rule's lookup references. Helm `values.yaml` exposes a `watchedGvks: []` list; chart templates the ClusterRole accordingly.

### Phase 5 — Response-action engine
16. `internal/actions/engine` — bounded worker pool, idempotency key = `sha256(rule, gvk, namespace, name, actionType)` stored in an in-memory LRU (configurable size, default 100k entries; optional persistent backend in v2), sliding-window rate limiter per `(rule, target)` tuple.
17. `internal/actions/{label,annotate,evict,patchnp,revoketoken}` — each is a small `Action` impl. Server-side apply for label/annotate/patchnp to avoid races.
18. `internal/actions/engine` listens to violations from admission + audit; AlertManager is just another action (the legacy `alert:` field auto-expands).
19. Action audit log: each action attempt emits a JSON line via `slog` + a Prometheus counter `portal_actions_total{action,result}`. Never silently drop.

### Phase 6 — NetworkPolicy declarative analysis (layer 6)

"Declarative" = reason about the declared NP graph (CRs + pod labels) as opposed to observing live packet flows. **Not** "evaluate once" — the analyser is fully event-driven and findings appear/clear in real time as cluster state evolves.

20. `internal/network/model` — build pod→NP graph from the audit module's informer caches (shared, no extra API calls); ingress/egress rules expanded into a queryable structure.
21. Built-in checks emit through the same pipeline (synthetic violations with `gvk: Namespace` or `gvk: NetworkPolicy`):
    - `np.default-deny-missing` — namespace has pods but no NP selecting them with empty ingress.
    - `np.broad-cidr` — egress CIDR is `/0` or `/8` outside RFC1918.
    - `np.unreachable-selector` — NP selector matches no pods in the namespace.
    - `np.policy-without-targets` — NP has no `podSelector` matches.
22. Reactivity matrix:
    - `OnAdd`/`OnUpdate`/`OnDelete` on `Pod` → re-evaluate default-deny coverage for that namespace + any NP selectors that match the pod's old/new labels.
    - `OnAdd`/`OnUpdate`/`OnDelete` on `NetworkPolicy` → re-evaluate all four checks for the affected namespace; clear stale findings tied to deleted policies.
    - `OnAdd`/`OnDelete` on `Namespace` → re-evaluate default-deny.
23. Findings appear in PolicyReport sub-second after the triggering event; AlertManager alerts auto-resolve when fixes are applied (uses the existing `endsAt` mechanism from podwatcher-poc).
24. Periodic resync (10 min) is the safety net only; the main path is informer events.

### Phase 7 — Helm chart + deploy
23. Chart layout under `deploy/helm/portal/`:
    - **CRDs** (`templates/crds/` or `crds/` for Helm 3 hook-installed): `PortalClusterRule`, `PortalRule`, vendored `PolicyReport` / `ClusterPolicyReport`. Installed before Portal itself.
    - **Deployment**: `replicaCount: 2` default (required for fail-closed HA), anti-affinity across nodes (`requiredDuringScheduling` if ≥2 nodes available, else preferred), readiness/liveness probes on `/readyz` and `/healthz`.
    - **PodDisruptionBudget**: `minAvailable: 1`. Hard requirement when `global.failClosed: true`.
    - **Service** (TLS), **ServiceAccount**, **leader-election Lease**.
    - **ValidatingWebhookConfiguration** templated from configured GVKs. `failurePolicy` driven by `global.failClosed` (default `true` → `Fail`). `namespaceSelector` *always* excludes `kube-system`, `kube-public`, `kube-node-lease`, and the Portal install namespace.
    - **Cert source**: cert-manager `Certificate` if `certManager.enabled: true`; else built-in self-signed bootstrap.
    - **ServiceMonitor** for Prometheus Operator clusters (optional).
24. RBAC scope grows with response actions and lookups:
    - `get,list,watch` on every audited GVK + every GVK referenced by `cluster.<gvk>.*` rule helpers (computed from rule corpus at chart install / Portal startup; helm value `watchedGvks: []` lets operators override).
    - `get,list,watch` on `portalclusterrules.portal.io` and `portalrules.portal.io`; `patch` on their `.status`.
    - `create,update,patch` on `policyreports.wgpolicyk8s.io` and `clusterpolicyreports.wgpolicyk8s.io`.
    - `create` on `coordination.k8s.io/leases` (leader election).
    - Conditional on action types being enabled: `patch` on pods/deployments/etc. (label/annotate), `create` on `pods/eviction`, `patch` on `networkpolicies.networking.k8s.io`, `delete` on `secrets` (SA token revocation). Each gated by a Helm value.
25. **Self-lockout protection** (mandatory regardless of `failClosed`):
    - Portal install namespace excluded from its own webhook.
    - System namespaces excluded from its own webhook.
    - Chart README documents the recovery path if a misconfigured rule ever bricks admission: scale Portal's Deployment to 0 via `kubectl --validate=strict=false`, or delete the `ValidatingWebhookConfiguration` directly.
26. **Optional split-mode** (`global.mode: split`): one Deployment per layer (admission / audit / network). Each gets independent replica counts and resource limits. Same RBAC, divided by binary; same Helm chart, different value.
27. **Bootstrap rule set**: chart bundles a small `portalclusterrules` set (loaded via post-install hook) covering the migrated podwatcher-poc examples. Operators can disable via `bootstrapRules: false`.

## v2 implementation plan (sketch — not in v1)

- `internal/runtime` subscribing to K8s API audit log (webhook backend or audit policy file).
- Rule schema gains `mode: [..., runtime]` and `runtime.events: [exec, attach, port-forward, secret-read, ...]`.
- Runtime events feed the v1 action engine — no new action machinery, just a new `EventSource`.
- Optional Tetragon/Falco gRPC consumer as a second runtime source.

## What carries over from podwatcher-poc

| Source (Java) | Target (Go) | Treatment |
|---|---|---|
| `core/.../rule/Rule.java` | `internal/rule/rule.go` | Schema unchanged (additive fields). Rewritten in Go |
| `core/.../context/*.java` | `internal/api/context.go` + `internal/context/pod/builder.go` | Same shape; reimplemented in Go |
| `core/.../alertmanager/AlertManagerClient.java` | `internal/sink/alertmanager/client.go` | Port: same JSON, same retry/backoff, same auth |
| `core/.../metrics/*.java` | `internal/sink/prometheus/*.go` | Map simpleclient counters to `client_golang` |
| `core/.../health/HealthServer.java` | `internal/sink/prometheus` shares `/healthz` + `/metrics` server | Combined into one HTTP listener |
| `examples/rules/*.yaml` | `examples/rules/*.yaml` (migrated) | `portal migrate-rules` rewrites `.contains()` → `in` and `filter.namespace` → `match.namespaces` |
| `scanner-docker/` | dropped | K8s only |
| `webhook/` (stub) | replaced by `internal/admission` | Clean implementation |
| `scanner-k8s/` polling logic | replaced by `internal/audit` informers | Watch-based, no polling |

## Verification

- **Rule migration:** `portal migrate-rules examples/rules/` produces files that compile under `internal/expr/exprlang`. A unit test feeds each example pod fixture through both engines (where applicable) and asserts identical boolean outputs.
- **Admission webhook:** kind cluster, Portal Helm-installed, `kubectl apply` of a privileged pod is denied with the rule's message; same pod with `enforcementAction: warn` is admitted with a `kubectl` warning; `dryrun` is admitted silently but appears in PolicyReport. `kubectl create --dry-run=server` exercises the same path.
- **Audit immediacy:** with the rule pre-installed, `kubectl apply` a violating pod and assert the PolicyReport entry + action dispatch happen within **1 second** of pod creation. Kill the watch connection mid-test — verify recovery via `portal_audit_watch_reconnects_total`, not via the 10-min resync.
- **Network analyser reactivity:** namespace with no NP and a pod produces `np.default-deny-missing`. Add a default-deny NP; assert the finding clears within 1 s (informer event, no timer). Delete the NP; assert the finding re-fires within 1 s. Add an NP with `podSelector: {role: api}` and no matching pods; `np.unreachable-selector` fires. Add a pod with that label; finding clears.
- **Actions:** rule with `actions: [{type: label, ...}, {type: evict, ...}]` against an audit finding — label appears within one reconcile; eviction recorded in `portal_actions_total{action="evict",result="ok"}`; second identical finding within rate-limit window does *not* fire again; action audit log contains both attempts with reason.
- **Outputs:** AlertManager mocked in test receives JSON matching podwatcher-poc's schema byte-for-byte (regression vector); `PolicyReport` CR is created and updated, not duplicated; admission denial messages render in `kubectl` output.
- **HA + fail-closed:** scale Deployment to 3 replicas with `global.failClosed: true`; exactly one Lease holder; rolling restart of the leader transfers within ~15 s without duplicate alerts; during a rollout, admission requests to a workload namespace continue to succeed (PDB enforces ≥1 ready replica). Kill all Portal pods → assert API calls to a *workload* namespace are rejected with the webhook's failurePolicy message, while calls to `kube-system` and the Portal install namespace continue to succeed (namespaceSelector exclusion).
- **Cross-resource:** rule using `cluster.poddisruptionbudgets.list(object.metadata.namespace, selector={...})` against a Deployment without a PDB → violation. Add a matching PDB → dep-index re-evaluates the Deployment within 1 s → violation clears. Delete the PDB → violation re-fires within 1 s. With 100k cached objects, cycle protection caps re-evals at the configured budget; counter `portal_lookup_cycle_suppressed_total` is observable.
- **CRD rule loading:** `kubectl apply` of a malformed `PortalClusterRule` is rejected by the API server with a schema error; a syntactically-valid but expr-lang-uncompilable rule is accepted, and its `.status.parseError` is populated within 1 s; a fixed rule's status clears on next apply; rule deletion removes it from the engine's index without restart.
- **Bootstrap rule set:** Helm install with `bootstrapRules: true` creates the post-install hook → `kubectl get portalclusterrule` lists the migrated podwatcher-poc examples; disabling the hook removes them on next upgrade.
- **End-to-end golden test under `deploy/test/`:** spin up kind, install Helm chart, apply the migrated example rule bundle, apply a fixture of compliant + violating manifests, assert deny count, warn count, PolicyReport contents, AlertManager calls, Prometheus scrape.

## Critical files (to be created)

- `go.mod` — module `github.com/vilaca/portal`, Go 1.22, deps on `client-go`, `controller-runtime`, `expr-lang/expr`, `prometheus/client_golang`, `sigs.k8s.io/yaml`.
- `cmd/portal/main.go` — flag parsing, registry wiring, layer startup.
- `internal/api/*.go` — interfaces + DTOs.
- `internal/rule/loader.go` — YAML schema parser.
- `internal/expr/exprlang/engine.go` — expr-lang adapter.
- `internal/engine/dispatcher.go` — GVK-indexed rule evaluator.
- `internal/context/pod/builder.go` — pod-shaped ContextBuilder.
- `internal/sink/alertmanager/client.go` — port of podwatcher-poc's AM client.
- `internal/sink/policyreport/client.go` — PolicyReport CR emitter.
- `internal/admission/server.go` — TLS webhook.
- `internal/admission/cert.go` — self-signed bootstrap + cert-manager support.
- `internal/audit/controller.go` — informer-driven event source.
- `internal/audit/leader.go` — Lease-based leader election.
- `internal/network/analyser.go` — NetworkPolicy static checks.
- `internal/actions/engine/dispatcher.go` — action engine.
- `internal/actions/{label,annotate,evict,patchnp,revoketoken}/action.go` — action impls.
- `deploy/helm/portal/` — Helm chart.
- `deploy/crds/wgpolicyk8s.io_policyreports.yaml` — vendored CRD.
- `examples/rules/*.yaml` — migrated copies of podwatcher-poc rules.
- `cmd/portal/migrate.go` — `portal migrate-rules` subcommand.
- `internal/rule/crd/types.go` — generated `PortalClusterRule` / `PortalRule` Go types.
- `internal/rule/crd/reconciler.go` — `.status` writer for rule CRs.
- `internal/rule/loader/folder.go` — filesystem-folder rule loader.
- `internal/rule/loader/cr.go` — CR-informer rule loader.
- `internal/lookup/cluster.go` — `cluster.<gvk>.*` helpers for expr-lang.
- `internal/lookup/depindex.go` — reverse-dependency index + cycle protection.
- `internal/lookup/virtual.go` — admission-time virtual cluster view.
- `deploy/crds/portal.io_portalclusterrules.yaml` — generated CRD.
- `deploy/crds/portal.io_portalrules.yaml` — generated CRD.
- `deploy/helm/portal/values.yaml` — `global.failClosed`, `watchedGvks`, action-toggle gates, `replicaCount`, etc.
- `deploy/helm/portal/templates/validatingwebhookconfiguration.yaml` — fail-closed, namespaceSelector exclusions.
- `deploy/helm/portal/templates/poddisruptionbudget.yaml` — `minAvailable: 1`.
- `docs/` — the structure listed above; every PR adds the relevant doc lines alongside code.

## Out of scope for this plan

- Mutation, generation, cleanup (Kyverno parity) — explicit non-goals.
- eBPF / kernel-level enforcement — Tetragon territory.
- CEL or Rego engines — expr-lang stays; pluggable engine is a v3 consideration.
- UI / dashboard — PolicyReport feeds existing tools (policy-reporter, kyverno-ui).
