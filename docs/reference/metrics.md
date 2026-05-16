# Portal Prometheus metrics

Generated from `internal/sink/prometheus` — do not edit by hand.

| Name | Type | Labels | Description |
|------|------|--------|-------------|
| `portal_admission_requests_total` | counter | `decision` | Admission webhook decisions, partitioned by allow/deny/warn/dryrun. |
| `portal_admission_latency_seconds` | histogram | — | End-to-end admission webhook latency in seconds. |
| `portal_audit_violations` | counter | `rule`, `severity`, `gvk` | Audit-mode violations, partitioned by rule, severity, and GVK. |
| `portal_actions_total` | counter | `action`, `result` | Action executions, partitioned by action type and result (ok/error/skipped). |
| `portal_audit_watch_reconnects_total` | counter | — | Number of times an audit informer reconnected to the API server. |
| `portal_np_findings` | counter | `check` | NetworkPolicy analyser findings, partitioned by check name. |
| `portal_admission_bypass_total` | counter | `namespace` | Admission requests short-circuited via portal.io/bypass annotation. |
| `portal_lookup_cycle_suppressed_total` | counter | — | Cross-resource re-evaluations suppressed by the cycle-protection budget. |
