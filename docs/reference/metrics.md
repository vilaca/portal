# Prometheus metrics

> Auto-generated. This file is regenerated from `metricRegistry` in [`internal/sink/prometheus/sink.go`](../../internal/sink/prometheus/sink.go) by `make generate-docs`, which invokes the renderer at [`internal/sink/prometheus/cmd/metricsdoc/main.go`](../../internal/sink/prometheus/cmd/metricsdoc/main.go). Drift between the generator output and this committed file fails CI.

The generator writes its output here (`docs/reference/metrics.md`). The table below is hand-maintained in lockstep until the generator runs in CI.

| Name | Type | Labels | Description |
|------|------|--------|-------------|
| `portal_admission_requests_total` | counter | `decision` | Admission webhook decisions, partitioned by allow/deny/warn/dryrun. |
| `portal_admission_latency_seconds` | histogram | — | End-to-end admission webhook latency in seconds. |
| `portal_audit_violations` | counter | `rule`, `severity`, `gvk` | Audit-mode violations, partitioned by rule, severity, and GVK. |
| `portal_actions_total` | counter | `action`, `result` | Action executions, partitioned by action type and result (ok/error/skipped). |
| `portal_audit_watch_reconnects_total` | counter | — | Number of times an audit informer reconnected to the API server. |
| `portal_np_findings` | counter | `check` | NetworkPolicy analyser findings, partitioned by check name. |
| `portal_admission_bypass_total` | counter | `namespace` | Admission requests short-circuited via `portal.io/bypass` annotation. |
| `portal_lookup_cycle_suppressed_total` | counter | — | Cross-resource re-evaluations suppressed by the cycle-protection budget. |

The dispatcher emits `result` labels `ok`, `error`, `duplicate`, `ratelimited`, `unknown`, `dropped` on `portal_actions_total` (see [`internal/actions/engine/dispatcher.go`](../../internal/actions/engine/dispatcher.go)). The admission handler additionally writes `decision="error"` on `portal_admission_requests_total` for panicked handlers.
