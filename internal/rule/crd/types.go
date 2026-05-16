package crd

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RuleGVK is the marshalled shape of a single GroupVersionKind match entry.
// We re-declare it here (rather than reuse schema.GroupVersionKind) because
// kubebuilder markers don't transit through external types.
type RuleGVK struct {
	Group   string `json:"group"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
}

// NamespaceSelector mirrors api.NamespaceSelector for CR storage.
type NamespaceSelector struct {
	// +optional
	Include []string `json:"include,omitempty"`
	// +optional
	Exclude []string `json:"exclude,omitempty"`
}

// Matcher mirrors api.Matcher for CR storage.
type Matcher struct {
	GVK []RuleGVK `json:"gvk"`
	// +optional
	Namespaces NamespaceSelector `json:"namespaces,omitempty"`
}

// ActionSpec mirrors api.ActionSpec for CR storage.
type ActionSpec struct {
	Type string `json:"type"`
	// +optional
	On []string `json:"on,omitempty"`
	// +optional
	RateLimit string `json:"rateLimit,omitempty"`
	// Params is a free-form action parameter bag. Stored as opaque JSON in the
	// CR; converted to api.ActionSpec.Params (map[string]any) at runtime.
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	Params map[string]any `json:"params,omitempty"`
}

// RuleSpec mirrors api.Rule's serialisable fields (everything except Source,
// which is engine-internal). Re-declared verbatim so kubebuilder markers can
// be attached without embedding api.Rule.
type RuleSpec struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// +optional
	// +kubebuilder:validation:Enum=info;low;medium;high;critical
	Severity string `json:"severity,omitempty"`
	// +optional
	Mode []string `json:"mode,omitempty"`
	// +optional
	// +kubebuilder:validation:Enum=deny;warn;dryrun
	EnforcementAction string  `json:"enforcementAction,omitempty"`
	Match             Matcher `json:"match"`
	Expression        string  `json:"rule"`
	// +optional
	Alert string `json:"alert,omitempty"`
	// +optional
	Actions []ActionSpec `json:"actions,omitempty"`
}

// RuleStatus is the .status sub-resource shared by both CRDs.
type RuleStatus struct {
	// +optional
	EvalCount int64 `json:"evalCount,omitempty"`
	// +optional
	ViolationCount int64 `json:"violationCount,omitempty"`
	// +optional
	LastApplied metav1.Time `json:"lastApplied,omitempty"`
	// +optional
	ParseError string `json:"parseError,omitempty"`
	// +optional
	ActiveOn []string `json:"activeOn,omitempty"`
}

// PortalClusterRuleSpec is exported separately so convert.go can address it
// without an alias.
type PortalClusterRuleSpec = RuleSpec

// PortalRuleSpec mirrors PortalClusterRuleSpec; the spec is identical, the
// scope is what differs.
type PortalRuleSpec = RuleSpec

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=pcr
// +kubebuilder:subresource:status

// PortalClusterRule is the cluster-scoped form of a Portal rule.
type PortalClusterRule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PortalClusterRuleSpec `json:"spec,omitempty"`
	Status RuleStatus            `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PortalClusterRuleList is the list wrapper for PortalClusterRule.
type PortalClusterRuleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PortalClusterRule `json:"items"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=pr
// +kubebuilder:subresource:status

// PortalRule is the namespaced form of a Portal rule.
type PortalRule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PortalRuleSpec `json:"spec,omitempty"`
	Status RuleStatus     `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PortalRuleList is the list wrapper for PortalRule.
type PortalRuleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PortalRule `json:"items"`
}
