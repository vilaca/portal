package api

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Severity classifies a rule for downstream routing and alerting.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// Mode names a rule-evaluation loop. A rule may opt in to multiple modes.
type Mode string

const (
	ModeAdmission Mode = "admission"
	ModeAudit     Mode = "audit"
	ModeNetwork   Mode = "network"
	ModeRuntime   Mode = "runtime" // v2
)

// EnforcementAction controls how admission responds to a violation.
type EnforcementAction string

const (
	EnforceDeny   EnforcementAction = "deny"
	EnforceWarn   EnforcementAction = "warn"
	EnforceDryRun EnforcementAction = "dryrun"
)

// NamespaceSelector is the include/exclude shape used by Rule.Match.Namespaces.
type NamespaceSelector struct {
	Include []string `json:"include,omitempty" yaml:"include,omitempty"`
	Exclude []string `json:"exclude,omitempty" yaml:"exclude,omitempty"`
}

// Matcher narrows the set of resources a rule applies to.
type Matcher struct {
	GVK        []schema.GroupVersionKind `json:"gvk" yaml:"gvk"`
	Namespaces NamespaceSelector         `json:"namespaces,omitempty" yaml:"namespaces,omitempty"`
}

// ActionSpec is one entry in Rule.Actions.
type ActionSpec struct {
	Type      string         `json:"type" yaml:"type"`
	On        []Mode         `json:"on,omitempty" yaml:"on,omitempty"`
	RateLimit string         `json:"rateLimit,omitempty" yaml:"rateLimit,omitempty"`
	Params    map[string]any `json:"params,omitempty" yaml:",inline"`
}

// Rule is the canonical parsed shape of a Portal rule, regardless of whether
// it originated from a folder YAML file or a PortalClusterRule / PortalRule CR.
type Rule struct {
	Name              string            `json:"name" yaml:"name"`
	Enabled           bool              `json:"enabled" yaml:"enabled"`
	Severity          Severity          `json:"severity,omitempty" yaml:"severity,omitempty"`
	Mode              []Mode            `json:"mode,omitempty" yaml:"mode,omitempty"`
	EnforcementAction EnforcementAction `json:"enforcementAction,omitempty" yaml:"enforcementAction,omitempty"`
	Match             Matcher           `json:"match" yaml:"match"`
	Expression        string            `json:"rule" yaml:"rule"`
	Alert             string            `json:"alert,omitempty" yaml:"alert,omitempty"`
	Actions           []ActionSpec      `json:"actions,omitempty" yaml:"actions,omitempty"`

	// Source identifies where this rule came from; used by status reporting and tests.
	Source RuleSource `json:"-" yaml:"-"`
}

// RuleSource records the origin of a Rule for diagnostics.
type RuleSource struct {
	Origin    string // "folder", "PortalClusterRule", "PortalRule"
	Path      string // file path or CR name/namespace
	UID       string // CR UID when applicable
	Generated bool   // true if produced by migrate-rules
}

// HasMode reports whether the rule opts into the given evaluation mode.
func (r Rule) HasMode(m Mode) bool {
	for _, x := range r.Mode {
		if x == m {
			return true
		}
	}
	return false
}
