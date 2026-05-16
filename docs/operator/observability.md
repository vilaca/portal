# Observability

Portal exposes Prometheus metrics, a health probe, and a readiness probe. The full metric list is in `../reference/metrics.md` (parallel author) and the source of truth is `internal/sink/prometheus/sink.go`.

## Endpoints

- **`:9090/metrics`** — Prometheus exposition (default port; configurable via `--metrics-addr`).
- **`:9090/healthz`** — liveness; returns 200 unconditionally while the process is alive.
- **`:9090/readyz`** — readiness; returns 503 when Portal believes itself unable to serve admission correctly.

The HTTP server listens on the metrics address only — `:8443` (default) is the admission TLS port, which serves the webhook handler and nothing else.

## What `/readyz` actually checks

`readyz` flips to 503 when the admission handler's error ring buffer is **more than 50% errors over the last 100 requests** (`internal/admission/handler.go` — `errorRing.record`). The ring is sticky once tripped; readiness recovers when subsequent successes drop the error ratio below 50%.

This means a freshly-started Portal whose first 10 requests all fail will return 200 from `/readyz` (the buffer isn't yet full); behaviour stabilises once 100 requests have flowed.

## Metrics

Eight Portal-specific metrics, registered at package init via `promauto`:

| Metric | Type | Labels | Emits when |
|---|---|---|---|
| `portal_admission_requests_total` | counter | `decision` (`allow`/`deny`/`warn`/`dryrun`/`bypass`/`error`) | every admission webhook request |
| `portal_admission_latency_seconds` | histogram | — | every admission request, end-to-end |
| `portal_admission_bypass_total` | counter | `namespace` | a request was short-circuited via `portal.io/bypass=true` |
| `portal_audit_violations` | counter | `rule`, `severity`, `gvk` | the audit loop produces a violation |
| `portal_audit_watch_reconnects_total` | counter | — | an informer reconnect (`SetWatchErrorHandler` callback) |
| `portal_actions_total` | counter | `action`, `result` (`ok`/`error`/`skipped`/`ratelimited`/`dropped`) | an action executes (or is suppressed) |
| `portal_np_findings` | counter | `check` (e.g. `np.default-deny-missing`) | NetworkPolicy analyser produces a finding |
| `portal_lookup_cycle_suppressed_total` | counter | — | the dep-index re-eval budget capped a rule×object pair |

Plus standard Go runtime metrics (`go_*`, `process_*`) from `promhttp.Handler()`.

## Recommended Prometheus alerts

These are starting points; adjust thresholds to your environment.

```yaml
groups:
- name: portal
  rules:
  - alert: PortalDenyDecisionsObserved
    expr: |
      sum(rate(portal_admission_requests_total{decision="deny"}[5m])) > 0
    for: 5m
    labels: { severity: info }
    annotations:
      summary: Portal is denying admission requests (informational)
      description: |
        Confirms Portal is actively enforcing. Investigate when sustained,
        especially in unexpected namespaces.

  - alert: PortalDenyRateHigh
    expr: |
      sum(rate(portal_admission_requests_total{decision="deny"}[5m])) > 0.5
    for: 10m
    labels: { severity: warning }
    annotations:
      summary: Sustained high deny rate from Portal
      description: |
        Either a real attack/misconfiguration storm, or a rule is too broad.

  - alert: PortalWatchReconnectStorm
    expr: |
      rate(portal_audit_watch_reconnects_total[5m]) > 0.1
    for: 5m
    labels: { severity: warning }
    annotations:
      summary: Portal audit informers are reconnecting frequently
      description: |
        Likely apiserver health or network instability. Cross-check
        kube-apiserver own metrics.

  - alert: PortalActionsFailing
    expr: |
      sum(rate(portal_actions_total{result=~"error|dropped"}[5m])) > 0
    for: 10m
    labels: { severity: warning }
    annotations:
      summary: Portal actions are erroring or being dropped
      description: |
        result=error: action.Execute returned non-nil — check logs.
        result=dropped: dispatcher queue saturated — raise WorkerPoolSize.

  - alert: PortalAdmissionLatencyHigh
    expr: |
      histogram_quantile(0.99,
        sum(rate(portal_admission_latency_seconds_bucket[5m])) by (le)) > 0.5
    for: 10m
    labels: { severity: warning }
    annotations:
      summary: Portal admission p99 latency is approaching the timeout
      description: |
        Webhook timeoutSeconds defaults to 5s. Sustained > 500ms p99 is the
        warning zone — investigate rule complexity / CPU throttling.

  - alert: PortalBypassUsed
    expr: |
      sum(rate(portal_admission_bypass_total[5m])) > 0
    for: 0m
    labels: { severity: warning }
    annotations:
      summary: Portal admission bypass was used
      description: |
        A namespace carrying portal.io/bypass=true short-circuited the webhook.
        This is a break-glass control — confirm the operator intended it.
```

## ServiceMonitor

`deploy/helm/portal/templates/servicemonitor.yaml` ships a Prometheus Operator `ServiceMonitor` resource gated by `serviceMonitor.enabled`. Set `serviceMonitor.labels.release: <kube-prometheus-stack-release>` so the operator picks it up.

## Dashboard suggestions

There is no shipped Grafana dashboard in v1. A useful starter panel set:

1. Stacked decision rate (deny / warn / dryrun / allow / bypass).
2. p50 / p95 / p99 admission latency.
3. Action engine: rate by `result` (one panel) + rate by `action` (a second).
4. Watch reconnects vs cluster size — a healthy cluster sees occasional reconnects, not a sustained rate.
5. PolicyReport CR count by namespace (from a `kube_customresource_*` exporter) — useful for tracking enforcement coverage.
