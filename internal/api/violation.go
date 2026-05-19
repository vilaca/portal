package api

import (
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Violation is the cross-cutting DTO emitted by rule evaluation. Every output
// channel (admission response, PolicyReport, AlertManager, Prometheus, action
// dispatcher) consumes Violations.
type Violation struct {
	Rule     string                  // Rule.Name
	Severity Severity                // copied from Rule
	GVK      schema.GroupVersionKind // resource that violated
	Namespace string
	Name     string

	Mode    Mode      // which loop produced this violation (admission/audit/network)
	Message string    // human-readable reason
	At      time.Time // creation timestamp

	// EnforcementAction tells admission how to respond. Empty outside admission.
	EnforcementAction EnforcementAction

	// Actions is the rule's action list, copied verbatim so the dispatcher
	// doesn't need to re-read the rule store. Pre-merged with the `alert:`
	// shorthand.
	Actions []ActionSpec

	// Source carries enough metadata for sinks to render context-rich messages
	// without re-fetching the object.
	Source ViolationSource

	// RuleSource is the origin of the rule that produced this Violation.
	// The action dispatcher reads RuleSource.Origin and RuleSource.Namespace
	// to enforce that namespace-scoped PortalRules cannot drive actions
	// against objects outside their own namespace.
	RuleSource RuleSource
}

// ViolationSource captures the originating event for downstream rendering.
type ViolationSource struct {
	EventID   string  // unique per emission, used for idempotency
	Operation string  // admission operation, if any
	Username  string  // admission requester, if any
	Container string  // when the violating context was a specific container in a multi-container Pod
}

// Decision is the admission-time aggregate response across all rules that fired
// for one AdmissionReview request.
type Decision struct {
	Allowed  bool
	Message  string
	Warnings []string
	// Violations is the full list this request produced, for PolicyReport + metrics.
	Violations []Violation
}
