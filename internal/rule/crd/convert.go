package crd

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vilaca/portal/internal/api"
)

// PortalClusterRuleSpecToRule converts a PortalClusterRuleSpec + its
// ObjectMeta into the engine's canonical api.Rule shape.
func PortalClusterRuleSpecToRule(spec PortalClusterRuleSpec, meta metav1.ObjectMeta) api.Rule {
	r := specToRule(spec)
	r.Source = api.RuleSource{
		Origin: "PortalClusterRule",
		Path:   meta.Name,
		UID:    string(meta.UID),
	}
	return r
}

// PortalRuleSpecToRule converts a PortalRuleSpec + its ObjectMeta into
// api.Rule. Path is "<namespace>/<name>" for namespaced CRs so log lines and
// status messages can disambiguate.
func PortalRuleSpecToRule(spec PortalRuleSpec, meta metav1.ObjectMeta) api.Rule {
	r := specToRule(spec)
	path := meta.Name
	if meta.Namespace != "" {
		path = meta.Namespace + "/" + meta.Name
	}
	r.Source = api.RuleSource{
		Origin: "PortalRule",
		Path:   path,
		UID:    string(meta.UID),
	}
	return r
}

// specToRule does the field-for-field copy. Source is filled in by callers.
func specToRule(spec RuleSpec) api.Rule {
	gvks := make([]schema.GroupVersionKind, 0, len(spec.Match.GVK))
	for _, g := range spec.Match.GVK {
		gvks = append(gvks, schema.GroupVersionKind{Group: g.Group, Version: g.Version, Kind: g.Kind})
	}

	modes := make([]api.Mode, 0, len(spec.Mode))
	for _, m := range spec.Mode {
		modes = append(modes, api.Mode(m))
	}

	actions := make([]api.ActionSpec, 0, len(spec.Actions))
	for _, a := range spec.Actions {
		on := make([]api.Mode, 0, len(a.On))
		for _, m := range a.On {
			on = append(on, api.Mode(m))
		}
		actions = append(actions, api.ActionSpec{
			Type:      a.Type,
			On:        on,
			RateLimit: a.RateLimit,
			Params:    a.Params,
		})
	}

	return api.Rule{
		Name:              spec.Name,
		Enabled:           spec.Enabled,
		Severity:          api.Severity(spec.Severity),
		Mode:              modes,
		EnforcementAction: api.EnforcementAction(spec.EnforcementAction),
		Match: api.Matcher{
			GVK: gvks,
			Namespaces: api.NamespaceSelector{
				Include: spec.Match.Namespaces.Include,
				Exclude: spec.Match.Namespaces.Exclude,
			},
		},
		Expression: spec.Expression,
		Alert:      spec.Alert,
		Actions:    actions,
	}
}
