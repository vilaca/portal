# ADR 0003 — Fail-closed default

**Status.** Accepted, implemented in v1.

## Context

A `ValidatingWebhookConfiguration` declares a `failurePolicy`: `Fail` or `Ignore`. With `Fail`, the apiserver rejects the request if the webhook is unreachable; with `Ignore`, it admits silently. This is the single biggest knob in admission control.

`podwatcher-poc` had no admission webhook — failure was always "the scanner died, the cluster is fine". Portal extends to admission and must pick a default.

## Decision

**`failurePolicy: Fail` by default.** The chart renders this in `deploy/helm/portal/templates/validatingwebhookconfiguration.yaml`. The Helm value `global.failClosed: true` is the documented default.

Operators can override with `global.failClosed: false`, which rewrites the rendered `failurePolicy` to `Ignore`.

## Rationale

For a **security** tool, silent admission absence is the worst failure mode. The scenarios:

- Portal pods are Pending. With `Ignore`: privileged pod admits, security team finds out the day after via audit log; meanwhile the pod is up. With `Fail`: privileged pod rejected, operators notice immediately, investigate.
- A bad rule corpus is denying everything. With `Ignore`: nothing is denied while Portal isn't running, but the rule corpus is still misconfigured. With `Fail`: everything is denied while Portal is wedged — visible loud failure.

The asymmetry is the point. `Fail` makes Portal a critical-path dependency, which is exactly what a deny-only admission tool should be when it's deployed; otherwise the deploy is theatre.

## Mitigations

The mandatory and recommended postures the chart enforces:

- **`replicaCount: 2` minimum.** Both replicas serve admission concurrently; one can fail and the other absorbs traffic.
- **`PodDisruptionBudget` with `minAvailable: 1`.** Node drains cannot take Portal to zero ready replicas.
- **`namespaceSelector` exclusions** — `kube-system`, `kube-public`, `kube-node-lease`, and Portal's install namespace are *always* excluded, regardless of `global.failClosed`. The exclusion is enforced both in the chart and at runtime (`internal/admission/server.go` — `DefaultExcludedNamespaces`).
- **Break-glass via the `portal.io/bypass=true` namespace annotation.** Setting this annotation on a Namespace causes Portal's webhook to short-circuit to `allowed=true` for that namespace's incoming requests. Every bypass is audited (`portal_admission_bypass_total{namespace}` + a slog warning).
- **`/readyz` probe.** When the admission handler's last-100-request error ring buffer trips 50% errors, `/readyz` returns 503 (`internal/admission/handler.go` — `errorRing`); the Service drops the unhealthy endpoint and `kube-apiserver` re-routes.

Together these make `Fail` survivable: at least one replica is normally ready, system namespaces always work, and operators have an audited break-glass.

## Honest counter-positioning

`podwatcher-poc`'s "cluster is fine if the scanner dies" property is genuinely attractive for teams whose risk model prefers availability over enforcement. We preserve it explicitly: `global.failClosed: false` flips the rendered `failurePolicy` to `Ignore`. The chart still enforces the system-namespace exclusions and the multi-replica defaults; only the apiserver-side fallback semantics change.

This is documented openly because pretending the choice doesn't have trade-offs would damage operator trust. The default is `Fail`; the escape hatch is documented; operators choose.

## Recovery procedure

Documented in `../operator/recovery-from-self-lockout.md`. Three steps: delete the webhook config, scale Portal to zero (works because `portal-system` is excluded from its own webhook), fix the underlying issue, restore.

## Consequences

- Operators **must** read `docs/operator/ha-and-leader-election.md` before deploying. The chart's `NOTES.txt` calls this out at install time.
- Operators **must** know the break-glass procedure. It's a one-page document we link from the chart's `README.md`.
- The fail-open path (`global.failClosed: false`) is a supported configuration, not deprecated, but documented as a posture-for-availability tradeoff.
