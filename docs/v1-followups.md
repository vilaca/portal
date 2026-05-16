# v1 follow-ups — post-kind-run punchlist

State at commit `0cf7cb7`. A fresh `./deploy/test/kind.sh` against a clean cluster delivers **4 PASS / 7 FAIL** on the e2e suite, plus 25 green unit-test packages and a clean `helm lint`. This document lists everything still outstanding, organised by priority so each item can be picked up independently after a context reset.

> The kind cluster from that run was left up with `SKIP_TEARDOWN=1`. To clean up:
> ```bash
> ./deploy/test/kind-teardown.sh
> ```

---

## 1. Quick wins — start here

These are the highest-likelihood, lowest-effort fixes. Each should unblock one or more e2e tests in well under an hour.

### 1.1 Bump the audit reconciler's status rate limiter

**File:** `internal/audit/reconciler.go`
**Line:** `NewRuleReconciler` constructs `rate.NewLimiter(rate.Limit(1), 10)` — 1/sec sustained, burst 10.

When `TestRuleMigrationCompileLoop` applies 6 rules in a tight burst the limiter starves status writes and the test's `wait for .status.lastApplied` helper times out after 30s. The 1/sec limit dates from when v1alpha1 was the status writer; now the audit reconciler is the sole writer and the rate-limit gate is the only thing slowing status writes. **Change to `rate.NewLimiter(rate.Limit(10), 50)` (or remove the gate entirely — Reconcile itself is already gated by controller-runtime's workqueue).**

Likely unsticks: `TestRuleMigrationCompileLoop`, `TestCRDRuleLoading`.

### 1.2 Add `policy/v1/PodDisruptionBudget` to the default audited GVKs

**File:** `cmd/portal/wire.go`
**Func:** `defaultAuditGVKs()`

Currently watches Pod, Namespace, Deployment, StatefulSet, DaemonSet, Job, NetworkPolicy. **`TestCrossResource`** uses `cluster.poddisruptionbudgets.list(...)` from an expr-lang rule. With no informer on PDBs the lookup cache returns nothing → no violation. Add `{Group: "policy", Version: "v1", Kind: "PodDisruptionBudget"}` to the list.

Better long term: have `internal/lookup` lazily start informers for any GVK a rule references (via `ExtractClusterRefs` at compile time + `audit.SharedInformerFactory().ForResource(gvr)`), so operators don't have to remember to add CLI flags.

Likely unsticks: `TestCrossResource`.

### 1.3 Force the engine to compile rules after `idx.Replace`

**File:** `internal/engine/dispatcher.go`

The engine pre-compiles every rule in `idx.All()` at construction — but `idx.Replace` (from the audit reconciler) bypasses that path. Lazy compile fires on the first `Evaluate`, but for admission-mode rules **no Evaluate runs until a pod admission request comes in**. So `.status.parseError` stays empty for any rule that hasn't yet faced an admission request, which breaks `TestCRDRuleLoading`.

**Fix:** expose `engine.Reload()` that walks `idx.All()` and pre-compiles, calling it from the audit reconciler right after `idx.Replace(rules)`. Records compile errors immediately. (Alternatively: implement a compile-on-store callback in the index.)

Likely unsticks: `TestCRDRuleLoading`.

### 1.4 Enable action RBAC for e2e

**File:** `deploy/test/kind.sh`

Action RBAC (`rbac.actions.label`, `.evict`, `.annotate`, `.patchnp`, `.revoketoken`) defaults to `false` in `values.yaml`. The e2e suite expects label + evict to work. Add `--set rbac.actions.label=true --set rbac.actions.evict=true` to the `helm upgrade --install` call. Same for any other action tests added later.

Likely unsticks: `TestActions`.

### 1.5 TestAdmissionDeny — investigate the namespace selector path

**Symptom:** rule status is set (audit reconciler ran, `.status.lastApplied` populated), but the pod is admitted instead of denied. Manual repro with the same rule shape works.

**Hypothesis:** Each subtest creates a unique namespace via `makeNamespace(t)` (e.g. `e2e-testadmissiondeny-123`). The rule's `match.namespaces.include` is set to just that namespace. The engine's `namespaceAllowed(matcher.Namespaces, ns)` check compares against the include list. If the engine has a stale rule index OR the rule's include list was somehow stringified differently, the namespace filter rejects all pods.

**Investigation path:**
1. Enable verbose logging (slog Debug) in `internal/engine/dispatcher.go::Evaluate` to print `rule, ns, allowed`.
2. Have the test print the rule it just created via `kubectl get portalclusterrule -o yaml` right before applying the pod.
3. Compare the manual probe's flow to the test's flow side-by-side.

Likely unsticks: `TestAdmissionDeny`.

---

## 2. Harder / per-test investigations

### 2.1 TestAuditImmediacy — watch-reconnect metric never ticks

**Symptom:** test kills the audit pod and expects `portal_audit_watch_reconnects_total` to increment within 30s. It never does.

**Two possible causes:**

1. **Real bug.** `internal/audit/controller.go` installs `cache.WatchErrorHandler` via `cache.SetWatchErrorHandlerWithContext`. If the controller-runtime cache + informer-factory's watch-reconnect code doesn't actually surface to that handler (or to the metric), the metric stays zero. Check `internal/audit/controller.go::Start` for where the handler is registered and what it does.

2. **Test methodology flaw.** The metric is scraped via the apiserver `services/portal:metrics/proxy/metrics` endpoint, which **load-balances across replicas**. The pod that reconnected might not be the pod the scrape hits. Two replicas → 50% chance of scraping the wrong one.

**Fix path:**
- For #1: confirm the handler increments `prommetrics.AuditWatchReconnects` from `internal/sink/prometheus` on each call. Check that informer reconnects actually invoke the handler (could be a controller-runtime version mismatch — we bumped to v0.24.1).
- For #2: instead of Service-proxy, the test should pod-list label `app.kubernetes.io/name=portal`, port-forward to each individually, and sum the metric across pods.

### 2.2 TestNetworkAnalyserReactivity — finding doesn't clear after default-deny NP applied

**Symptom:** Test creates a namespace with no NP → `np.default-deny-missing` finding fires (good). Test applies a default-deny NP to that namespace → expects the finding to clear within 5s. It doesn't.

**Hypothesis:** `internal/network/analyser.go::onAdd` for NetworkPolicy enqueues a workItem for the namespace, but `internal/network/checks.go::CheckDefaultDenyMissing` may not recognise the just-applied NP as a "default-deny" pattern. A default-deny is `spec.podSelector: {}` (empty = selects all pods) plus `spec.policyTypes: [Ingress]` with no `ingress:` rules. Verify the check's matcher.

**Files to inspect:**
- `internal/network/checks.go` — search for "default-deny-missing" implementation
- `internal/network/analyser.go::installHandlers` — confirm the NP informer's UpdateFunc fires
- `internal/network/model.go` — verify `BuildModel` correctly classifies NPs

**Test the check in isolation:** add a unit test in `internal/network/checks_test.go` for "before: ns with pods, no NP → finding; after: same ns + a `podSelector:{}, policyTypes:[Ingress]` NP → no finding". If the unit test reproduces, fix the matcher.

### 2.3 TestAlertManagerJSON — currently passes, watch for flake

The receiver fixture works. But if you change the alertmanager sink's JSON shape (or the rule's action params), the test's structural assertion may still pass while the byte-equality golden in `internal/sink/alertmanager/testdata/expected_alert.json` regresses. Run **both** the unit test (byte-equality) and e2e (delivery) on any sink change.

---

## 3. Polish items found in passing

### 3.1 Dead code: `writeAllStatus` in `audit/reconciler.go`

After the v1alpha1 status reconciler was disabled, `writeAllStatus` is unreachable. Remove it; replace with a comment explaining the audit reconciler is the sole status writer. The unused `Engine` field used by writeAllStatus stays — it's referenced by `patchStatusForRequest`.

### 3.2 Unused: `internal/rule/loader/cr.go`

`NewCR` and `NewCRFromClient` were the original Wave 2 CR loader. They were never wired (wire.go's `--rules-cr` branch was a stub for a long time, and the eventual fix uses the audit RuleReconciler directly, not this loader). Delete the file and its test; the audit reconciler owns the CR loading concern.

### 3.3 No root-level `README.md`

The repo root has `LICENSE`, `CHANGELOG.md`, `CONTRIBUTING.md`, `SECURITY.md`, `Makefile`, `Dockerfile`, `.editorconfig`, `mkdocs.yml`, and `go.mod` — but no `README.md`. GitHub shows nothing on the repo landing page beyond the file list. Add a short README that:
- States what Portal is in one sentence.
- Links to `docs/POC-TO-PRODUCTION.md` for the canonical design.
- Links to `docs/README.md` (the docs site entry).
- Lists the four commands an operator types: `make build`, `make test`, `helm install`, `./deploy/test/kind.sh`.

### 3.4 Generated-docs drift on the docs CI gate

The `docs-generation-drift` CI job runs `make generate-docs` and `git diff --exit-code`. Today's commit (`0cf7cb7`) changed the rule schema layout (`actions[*].params`) and added the init-certs subcommand, both of which feed `docs/reference/cli.md` and `docs/reference/helm-values.md`. **Run `make generate-docs` locally before the next PR** and commit the regenerated content, or the gate will fail.

### 3.5 `rules.bootstrap` default

`deploy/helm/portal/values.yaml` has `rules.bootstrap: false` with a comment "Set to true once the chart ships a post-install bundle (not in v1)." There's no path to setting it to true today — the templates that would render the bundle don't exist. Either:
- Remove the value entirely (don't expose what doesn't work), or
- Add a `templates/bootstrap-rules-job.yaml` that kubectl-applies the migrated `examples/rules/*.yaml` via a post-install hook.

### 3.6 ADR header polish

The six ADRs under `docs/adr/` use `# Title` but lack the standard Nygard fields (Status, Context, Decision, Consequences). They read fine as prose but auditors / new contributors may want the structured form. One-pass conversion is mechanical.

### 3.7 `internal/audit/controller.go::defaultResourceForGVK`

After we wired the discovery-backed RESTMapper, this naive fallback is only used when the mapper isn't supplied (tests). The function is correct as a fallback, but the comment in the file still says "production wire-up supplies a RESTMapper-backed override." Update the comment to point at `Options.RESTMapper` since the production wiring is now mainline.

### 3.8 Test cleanup — rules leaking between subtests

Each subtest's `t.Cleanup` calls `Delete` on the PortalClusterRule it created. But the audit reconciler has no callback when the deletion finishes loading into the index — subsequent tests can briefly see the previous test's rule active. Add a `waitForRuleAbsent(name)` helper that polls `.status` until the rule is gone, and call it from `Cleanup`.

---

## 4. Real bugs that the kind run proved would have shipped broken

(All fixed in `0cf7cb7`. Kept here so future me knows the test corpus has historical reason to exist.)

1. **CRD version was `crd` instead of `v1alpha1`** — controller-gen derived it from the package name. Fixed by renaming `internal/rule/crd` → `internal/rule/v1alpha1`.
2. **`--rules-cr` was a log-only stub** — `cmd/portal/wire.go` never actually loaded rules from CRs. Fixed by building a controller-runtime Manager with the audit RuleReconciler attached.
3. **Engine snapshotted compiled rules at construction** — `idx.Replace` from the reconciler was invisible. Rewrote to read live from the index per `Evaluate`, with a per-expression compile cache.
4. **`EnsureCerts` tried to mutate immutable `Secret.Type`** — only mutate `Data` on update; set Type only on `Create`.
5. **`secret-bootstrap.yaml` was `kubernetes.io/tls` with empty data** — API server rejected. Now Opaque placeholder; `EnsureCerts` upgrades to `tls` on first write only via Create.
6. **Webhook entry name `portal.io` was 2 dot-segments** — K8s requires ≥3. Now `validate.portal.io`.
7. **AlertManager receiver fixture lacked `go.mod`** — `go build ./...` couldn't resolve a module.
8. **v1alpha1 status reconciler raced the audit reconciler** — v1alpha1 wrote `lastApplied` ahead of audit's `idx.Replace`. Disabled v1alpha1; audit is now sole status writer.
9. **Go toolchain mismatch** — `go.mod` was `1.26.0` but Dockerfiles + CI pinned `1.22`. Bumped all six pins.
10. **E2E harness:** `applyPortalClusterRule` missed `spec.name`; used wrong `--out` flag for migrate-rules; scraped metrics via `wget` (not in distroless); didn't gate on reconciler completion; didn't restore Portal after TestHAFailClosed; client-go REST QPS was too low.

---

## 5. v1 GA gate suggestion

Before tagging v0.1.0, **the 7 still-failing e2e tests need either a fix or a documented `t.Skip` with a Linear/issue link.** The 4 that pass already cover the load-bearing flows (admission TLS chain, fail-closed, sink delivery, dedup). Items 1.1 → 1.5 above should bring that to 9 PASS / 2 FAIL within an afternoon; the remaining two (`TestAuditImmediacy`, `TestNetworkAnalyserReactivity`) are real investigations that can ship as known-flake while v0.1.0 goes out.
