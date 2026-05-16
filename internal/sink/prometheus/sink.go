// Package prometheus implements the Prometheus OutputSink and exposes
// Portal's Prometheus metric registry. Counters/histograms defined here are
// the canonical Portal observability surface — every metric in this file
// MUST appear in docs/reference/metrics.md (generation handled by the
// cmd/metricsdoc tool below). The metricRegistry slice pairs each metric
// with a description so the generator and tests can verify completeness.
//
// The sink itself only emits portal_audit_violations and portal_np_findings;
// the other metrics are exposed as package-level vars for other Portal
// modules (admission, audit, actions, lookup) to increment directly.
package prometheus

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/vilaca/portal/internal/api"
)

func init() {
	api.RegisterSink("prometheus", func() api.OutputSink { return New() })
}

// MetricDoc describes a single Portal metric for the generator + tests.
type MetricDoc struct {
	Name        string
	Type        string // counter | histogram | gauge
	Labels      []string
	Description string
}

// metricRegistry is the source of truth for documented metrics. Adding a
// metric to the package without an entry here will fail TestAllMetricsDocumented.
var metricRegistry = []MetricDoc{
	{
		Name:        "portal_admission_requests_total",
		Type:        "counter",
		Labels:      []string{"decision"},
		Description: "Admission webhook decisions, partitioned by allow/deny/warn/dryrun.",
	},
	{
		Name:        "portal_admission_latency_seconds",
		Type:        "histogram",
		Labels:      nil,
		Description: "End-to-end admission webhook latency in seconds.",
	},
	{
		Name:        "portal_audit_violations",
		Type:        "counter",
		Labels:      []string{"rule", "severity", "gvk"},
		Description: "Audit-mode violations, partitioned by rule, severity, and GVK.",
	},
	{
		Name:        "portal_actions_total",
		Type:        "counter",
		Labels:      []string{"action", "result"},
		Description: "Action executions, partitioned by action type and result (ok/error/skipped).",
	},
	{
		Name:        "portal_audit_watch_reconnects_total",
		Type:        "counter",
		Labels:      nil,
		Description: "Number of times an audit informer reconnected to the API server.",
	},
	{
		Name:        "portal_np_findings",
		Type:        "counter",
		Labels:      []string{"check"},
		Description: "NetworkPolicy analyser findings, partitioned by check name.",
	},
	{
		Name:        "portal_admission_bypass_total",
		Type:        "counter",
		Labels:      []string{"namespace"},
		Description: "Admission requests short-circuited via portal.io/bypass annotation.",
	},
	{
		Name:        "portal_lookup_cycle_suppressed_total",
		Type:        "counter",
		Labels:      nil,
		Description: "Cross-resource re-evaluations suppressed by the cycle-protection budget.",
	},
}

// Registry returns a copy of the metricRegistry so callers (cmd/metricsdoc,
// tests, docs generator) can iterate without mutating the source of truth.
func Registry() []MetricDoc {
	out := make([]MetricDoc, len(metricRegistry))
	copy(out, metricRegistry)
	return out
}

// Package-level metric vars. Registered with the default registry so any
// promhttp.Handler() picks them up. promauto panics on duplicate registration,
// which is the behaviour we want — Portal runs as a single process.
var (
	AdmissionRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "portal_admission_requests_total",
		Help: "Admission webhook decisions, partitioned by allow/deny/warn/dryrun.",
	}, []string{"decision"})

	AdmissionLatencySeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "portal_admission_latency_seconds",
		Help:    "End-to-end admission webhook latency in seconds.",
		Buckets: prometheus.DefBuckets,
	})

	AuditViolations = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "portal_audit_violations",
		Help: "Audit-mode violations, partitioned by rule, severity, and GVK.",
	}, []string{"rule", "severity", "gvk"})

	ActionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "portal_actions_total",
		Help: "Action executions, partitioned by action type and result (ok/error/skipped).",
	}, []string{"action", "result"})

	AuditWatchReconnectsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "portal_audit_watch_reconnects_total",
		Help: "Number of times an audit informer reconnected to the API server.",
	})

	NPFindings = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "portal_np_findings",
		Help: "NetworkPolicy analyser findings, partitioned by check name.",
	}, []string{"check"})

	AdmissionBypassTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "portal_admission_bypass_total",
		Help: "Admission requests short-circuited via portal.io/bypass annotation.",
	}, []string{"namespace"})

	LookupCycleSuppressedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "portal_lookup_cycle_suppressed_total",
		Help: "Cross-resource re-evaluations suppressed by the cycle-protection budget.",
	})
)

// sink implements api.OutputSink. Audit violations bump
// portal_audit_violations; network-mode violations bump portal_np_findings.
// All other metrics are incremented directly by their owning module.
type sink struct{}

// New constructs the Prometheus sink. Metrics are registered on the default
// registry at package init via promauto.
func New() api.OutputSink { return &sink{} }

// Name implements api.OutputSink.
func (s *sink) Name() string { return "prometheus" }

// Emit implements api.OutputSink.
func (s *sink) Emit(_ context.Context, v api.Violation) error {
	switch v.Mode {
	case api.ModeNetwork:
		// Network checks reuse the rule name as the "check" label.
		NPFindings.WithLabelValues(v.Rule).Inc()
	default:
		AuditViolations.WithLabelValues(v.Rule, string(v.Severity), v.GVK.String()).Inc()
	}
	return nil
}

// Close implements api.OutputSink. Metrics outlive the sink.
func (s *sink) Close() error { return nil }
