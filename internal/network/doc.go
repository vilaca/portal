// Package network implements Portal's declarative NetworkPolicy analyser
// (PLAN §"Phase 6 — NetworkPolicy declarative analysis"). It is event-driven:
// re-evaluation triggers on Pod/NP/Namespace informer events. Findings clear
// when fixes are applied. No live packet/flow observation.
//
// Files:
//
//   - analyser.go         — api.EventSource implementation; wires informer
//                            handlers and dispatches re-evaluations through a
//                            bounded worker pool.
//   - model.go            — pod→NP graph builder from the audit informer caches.
//   - checks.go           — the four built-in checks:
//                              np.default-deny-missing
//                              np.broad-cidr
//                              np.unreachable-selector
//                              np.policy-without-targets
//   - dispatcher_glue.go  — fan-out to sinks + action dispatcher; tracks
//                            currently-active findings and emits synthetic
//                            "resolved" violations when a finding clears.
//
// Resolution semantics: when a finding "clears", the analyser emits a
// synthetic api.Violation with Message="resolved", same identifying fields,
// and an empty Actions slice. AlertManager sinks use this to set endsAt;
// PolicyReport sinks overwrite the prior Result entry per Wave 1 dedup.
package network
