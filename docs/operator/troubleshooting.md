# Troubleshooting

A field guide to the common failure modes. Each section names the symptom, the diagnostic, and the fix.

## Webhook times out

**Symptom.** `kubectl apply` returns `Error from server (InternalError): admission webhook "portal.io" failed: timeout`. Logs show admission requests that took longer than 5 s.

**Diagnostic.**

- Inspect `portal_admission_latency_seconds` histogram. The p99 bucket should be well under 1 s.
- Check CPU throttling (`kubectl top pod -n portal-system`); throttled pods compile expression programs slowly under load.
- Count rules: `kubectl get portalclusterrule | wc -l`. A few hundred rules is fine, several thousand starts to push p99.

**Fix.**

- Raise CPU limits on the Portal Deployment.
- Audit your rule corpus for expressions that walk large object trees (e.g. iterating `object.spec.containers[*].volumeMounts[*]`); see `../reference/expression-language.md` for cheaper idioms.
- As a last resort, raise `webhook.timeoutSeconds` past the default 5 in the Helm chart. **Do not exceed 10** — `kube-apiserver` itself enforces a 30-second hard cap and you want headroom before that.

## Self-lockout (fail-closed + Portal pods Pending)

**Symptom.** Portal pods are `Pending` (image pull, taint, no capacity). Admission requests to non-excluded namespaces are rejected with the webhook's `failurePolicy: Fail` message.

**Fix.** See `recovery-from-self-lockout.md`. In short: `kubectl delete validatingwebhookconfiguration portal.io`, fix the underlying cause, then `helm upgrade` (or wait for the operator pod that the chart provides to recreate the webhook config).

## Rule `.status.parseError` populated

**Symptom.** A `PortalClusterRule` has been applied but never fires.

**Diagnostic.**

```bash
kubectl get portalclusterrule <name> -o jsonpath='{.status.parseError}'
```

The error message names the column where expr-lang choked — typically a SpEL construct that escaped migration (see `../migration/side-by-side-rule-syntax.md`).

**Fix.** Rewrite the offending expression and `kubectl apply -f` again. Status clears within ~1 s.

## `portal_audit_watch_reconnects_total` rises rapidly

**Symptom.** The counter increments every few seconds.

**Diagnostic.** This counter is incremented by `internal/audit/controller.go` whenever an informer's watch handler returns a non-nil error. The most common causes:

- API server itself is unhealthy / restarting.
- Network partition between Portal and `kube-apiserver`.
- RBAC was changed mid-flight and Portal's ServiceAccount lost `watch` permission on a GVK.

**Fix.** First check `kubectl get --raw /healthz` and `kubectl get componentstatuses`. If the API server is fine, run `kubectl auth can-i watch pods --as=system:serviceaccount:portal-system:portal` (substitute GVK) — if `no`, fix the `ClusterRole` (`operator/rbac-scoping.md`).

## High `portal_actions_total{result="dropped"}` or `result="error"`

**Symptom.** Actions stop firing or fire with errors. Alerting on this counter is recommended (`observability.md`).

**Diagnostic.**

- `result="dropped"` — the action dispatcher's queue is saturated. The bounded worker pool can't keep up with violations.
- `result="error"` — an action's `Execute` returned a non-nil error. Inspect Portal logs (`kubectl logs deployment/portal -n portal-system | grep -i action`).
- `result="ratelimited"` — not a failure; the per-`(rule,target)` rate limiter is doing its job. If you want more throughput, raise the rule's `actions[].rateLimit`.

**Fix.**

- For `dropped`: raise the action engine's `WorkerPoolSize` and `QueueSize` (via Helm values; defaults are tuned for ~hundreds of concurrent violations).
- For `error`: cross-reference the error with `internal/actions/<type>/action.go`. Common causes: missing RBAC (the conditional toggles in `rbac-scoping.md` aren't all on), missing namespace, target resource was deleted between violation and dispatch.

## Admission denies with no matching rule

**Symptom.** A workload is rejected but `kubectl get portalclusterrule` shows no obvious match.

**Diagnostic.** The deny message includes the rule name — `<rule-name>: <message>`. Compare against `kubectl get portalclusterrule <rule-name> -o yaml` to confirm `enabled: true` and verify the `match.gvk`/`match.namespaces` overlap.

If a rule has both `mode: [admission]` and `enforcementAction: deny`, it's an enforcing rule. To experiment without blocking, flip `enforcementAction: warn` and re-apply — warnings show up as `kubectl` warnings and in `portal_admission_requests_total{decision="warn"}` without blocking.

## `kubectl warning` flood

**Symptom.** Every `kubectl apply` prints multiple `Warning: ...` lines from Portal.

**Fix.** Each warning is a rule with `enforcementAction: warn` that matched. Either upgrade the rule to `enforcementAction: deny` (now you know it works) or to `dryrun` (silent; only `PolicyReport` and metrics record it). The third option — `enabled: false` — is for known-stale rules awaiting cleanup.

## Pod sugar fields surprisingly empty

**Symptom.** A rule uses `securityContext.runAsNonRoot` and never matches even on pods you expect to violate.

**Diagnostic.** The pod sugar (`internal/context/pod/`) is intentionally narrow — see `../concepts/context-and-pod-sugar.md`. If you need a field outside the sugar's surface, use `object.spec.<path>` directly. The full object is always reachable.

## Where the logs are

Portal logs to stderr via `slog` (`log/slog`), JSON-formatted at `LevelInfo` by default. Notable log lines:

- `"admission handler panic"` — a programming bug; the panic was caught (`internal/admission/handler.go` defer) but readiness will degrade if it persists.
- `"admission bypass annotation honoured"` — the `portal.io/bypass=true` namespace annotation was used. Every bypass produces an audit log line *and* increments `portal_admission_bypass_total{namespace}`. Alert on it.
- `"sink emit"` — a sink returned an error from `Emit()`. The handler logs and continues; other sinks are unaffected.
