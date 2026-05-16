# Changelog

All notable changes to Portal are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions track
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Nothing yet.

## [0.1.0-alpha.1]

First public alpha. The e2e suite runs **10 PASS / 1 SKIP / 0 FAIL** against
a fresh kind cluster; unit tests are green across 25 packages; `helm lint`
is clean. CRDs are `portal.io/v1alpha1`; breaking changes may land before
v0.1.0 GA. See [`docs/v1-followups.md`](docs/v1-followups.md) for the
punchlist and the one documented product gap (§5.1, cross-resource
re-evaluation).

### Added
- Wave 0–5: complete v1 architecture (admission webhook, audit informer loop,
  NetworkPolicy analyser, response-action engine with six built-in actions,
  expr-lang rule engine, AlertManager + PolicyReport + Prometheus + stdout
  sinks).
- `portal init-certs` subcommand and Helm init-container, closing the
  self-signed CA-bundle injection gap for non-cert-manager installs.
- `policyreport-gc` action that drops stale `Result` entries from
  `PolicyReport` / `ClusterPolicyReport` when an audited object is deleted.
- Discovery-backed RESTMapper threaded through audit, network, and the
  `label` / `annotate` actions — replaces the naive `lowercase(kind)+"s"`
  pluraliser at six sites.
- controller-gen v0.17.3 wired through `make generate-crds`; CRD types and
  DeepCopy methods are now generated.
- Full `docs/` tree with mkdocs config (concepts, reference, cookbook,
  migration, operator, security, plugin-author, contributing, ADRs).
- `portal migrate-rules` subcommand that rewrites podwatcher-poc SpEL rules
  to expr-lang + `match.namespaces`.
- End-to-end kind harness under `deploy/test/`, gated by `//go:build e2e`.
- `engine.Reload()` so admission-only rules surface compile errors via
  `.status.parseError` immediately on CR apply rather than waiting for a
  matching admission request. The audit reconciler calls it after every
  `idx.Replace`.
- `internal/lookup` is now wired into the engine. Rule expressions can call
  `cluster.<resource>.list(ns, selector)` / `.byName(ns, name)`; the audit
  controller's informer cache is the backing store. `ToExprEnv` emits a
  simple-name alias so the short syntax works in expr-lang chain form.
- Audit controller now tracks `(object, rule)` active violations per replica
  and emits a synthetic `Message="resolved"` violation when a rule stops
  firing for an object that previously triggered it. The PolicyReport sink
  deletes the matching `Result` on resolved emits, so stale findings clear
  on the next audit pass.
- `policy/v1/PodDisruptionBudget` added to the default audited GVK set so
  cross-resource rules referencing PDBs hit a populated informer cache.
- `waitForRuleAbsent` helper in the e2e harness so a deleted rule doesn't
  leak into the next subtest's reconcile snapshot.
- Root `README.md` (this is the file you read first).

### Fixed
- **CA-divergence race in `EnsureCerts`.** Both replicas' init-containers
  found the empty bootstrap Secret, generated independent CAs, and
  last-write-wins on the Secret left one replica serving a leaf signed by a
  CA the webhook caBundle no longer trusted. Rewritten as a claim-or-adopt
  loop with `IsAlreadyExists` / `IsConflict` retries; the first writer wins
  and losers re-read the Secret and adopt the winner's material.
- Audit `OnDelete` synthetic violation was leaking into every output sink
  (stdout, prometheus, AlertManager, PolicyReport), producing stray alerts
  and metric pollution. It now dispatches to the action engine only — see
  `TestEmitGCViolation_BypassesSinks`.
- `ActionSpec.Params` in the CRD types switched from `map[string]any` (which
  panicked controller-gen v0.17 DeepCopy generation) to
  `*runtime.RawExtension`.
- `--rules-cr=true` with `--admission` only used to silently load zero
  rules; wire-up now errors fast unless `--audit` or `--rules-folder` is
  also set.
- `internal/actions/alertmanager_action` was never imported by
  `cmd/portal/wire.go`, so its `init()` never ran and every rule with an
  `alert:` shorthand dispatched as "unknown action type". Imported and
  `Configure(sink)` called when the alertmanager URL is set.
- Action dispatcher rate-limit key was `(rule, namespace, name)` — every
  fresh target got a fresh budget, redundant with idempotency. Keyed by
  `(rule, action)` so the rate-limit value caps cluster-wide per rule.
- Portal binary kept the default in-cluster client-go QPS=5 / Burst=10,
  starving the audit informer factory and the action dispatcher under load.
  Bumped to 100/200.
- `audit.RuleReconciler` status-write rate limiter was 1/sec, burst 10 —
  bursts of CR applies blew the budget and the e2e harness timed out
  waiting on `.status.lastApplied`. Bumped to 10/sec, burst 50.
- PolicyReport sink upserted on every emit, so when a network-analyser
  finding cleared the resolved emission left a `Result` with
  `message=resolved` in the report instead of deleting it. Resolved emits
  now drop the matching `Result`.
- `TestRuleMigrationCompileLoop` kubectl-applied every example rule —
  including a deny-mode `privileged-container` — and never cleaned up,
  blocking later tests. Cleanup now deletes every applied rule and waits
  for absence.

### Changed
- Removed the `rules.bootstrap` Helm value. No template ever consumed it
  and there's no path to set it to `true` in v1; operators copy the rules
  they want from `examples/rules/` and `kubectl apply` them.
- Removed `internal/rule/loader/cr.go` and its test. The audit
  `RuleReconciler` owns CR loading end-to-end; the older loader was never
  wired.
- Removed the dead `writeAllStatus` shim from `internal/audit/reconciler.go`.
  The audit reconciler is the sole writer of `PortalClusterRule.status` /
  `PortalRule.status`.

### Known issues
- **Cross-resource re-evaluation.** A rule reading
  `cluster.poddisruptionbudgets.list(...)` only re-fires when its
  match-target changes — adding or deleting a PDB doesn't propagate to the
  Deployment's audit pass, so PolicyReport entries based on the old
  cross-resource state linger until the dependent object is touched. See
  [`docs/v1-followups.md`](docs/v1-followups.md) §5.1.
- `TestAuditImmediacy`'s watch-reconnect assertion is `t.Skip`-ed: killing
  one replica doesn't disrupt the surviving replica's watch, and the
  killed pod's counter dies with it. The first half of the test still
  asserts audit-to-PolicyReport latency.
