package v1alpha1

import (
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vilaca/portal/internal/api"
)

// raw is a tiny helper that wraps a JSON object literal as a *runtime.RawExtension.
func raw(s string) *runtime.RawExtension { return &runtime.RawExtension{Raw: []byte(s)} }

func TestPortalClusterRuleSpecToRule(t *testing.T) {
	spec := RuleSpec{
		Name:              "privileged",
		Enabled:           true,
		Severity:          "critical",
		Mode:              []string{"admission", "audit"},
		EnforcementAction: "deny",
		Match: Matcher{
			GVK: []RuleGVK{
				{Group: "", Version: "v1", Kind: "Pod"},
				{Group: "apps", Version: "v1", Kind: "Deployment"},
			},
			Namespaces: NamespaceSelector{Include: []string{"production"}, Exclude: []string{"kube-system"}},
		},
		Expression: "container.securityContext.privileged == true",
		Alert:      "insecure-workload",
		Actions: []ActionSpec{
			{Type: "alertmanager", Params: raw(`{"template":"insecure-workload"}`)},
			{Type: "label", On: []string{"audit"}, RateLimit: "5/min", Params: raw(`{"key":"portal.security/quarantine","value":"true"}`)},
		},
	}
	meta := metav1.ObjectMeta{Name: "privileged", UID: types.UID("uid-123")}

	got := PortalClusterRuleSpecToRule(spec, meta)

	want := api.Rule{
		Name:              "privileged",
		Enabled:           true,
		Severity:          api.SeverityCritical,
		Mode:              []api.Mode{api.ModeAdmission, api.ModeAudit},
		EnforcementAction: api.EnforceDeny,
		Match: api.Matcher{
			GVK: []schema.GroupVersionKind{
				{Group: "", Version: "v1", Kind: "Pod"},
				{Group: "apps", Version: "v1", Kind: "Deployment"},
			},
			Namespaces: api.NamespaceSelector{Include: []string{"production"}, Exclude: []string{"kube-system"}},
		},
		Expression: "container.securityContext.privileged == true",
		Alert:      "insecure-workload",
		Actions: []api.ActionSpec{
			{Type: "alertmanager", On: []api.Mode{}, Params: map[string]any{"template": "insecure-workload"}},
			{Type: "label", On: []api.Mode{api.ModeAudit}, RateLimit: "5/min", Params: map[string]any{"key": "portal.security/quarantine", "value": "true"}},
		},
		Source: api.RuleSource{Origin: "PortalClusterRule", Path: "privileged", UID: "uid-123"},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("conversion mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestPortalRuleSpecToRule_NamespacedPath(t *testing.T) {
	spec := RuleSpec{
		Name:    "ns-rule",
		Enabled: true,
		Match: Matcher{
			GVK: []RuleGVK{{Group: "", Version: "v1", Kind: "Pod"}},
		},
		Expression: "true",
	}
	meta := metav1.ObjectMeta{Name: "ns-rule", Namespace: "prod", UID: types.UID("uid-9")}

	got := PortalRuleSpecToRule(spec, meta)

	if got.Source.Origin != "PortalRule" {
		t.Errorf("Source.Origin = %q, want %q", got.Source.Origin, "PortalRule")
	}
	if got.Source.Path != "prod/ns-rule" {
		t.Errorf("Source.Path = %q, want %q", got.Source.Path, "prod/ns-rule")
	}
	if got.Source.UID != "uid-9" {
		t.Errorf("Source.UID = %q, want %q", got.Source.UID, "uid-9")
	}
	if got.Name != "ns-rule" || !got.Enabled || got.Expression != "true" {
		t.Errorf("core fields didn't round-trip: %#v", got)
	}
	if len(got.Match.GVK) != 1 || got.Match.GVK[0].Kind != "Pod" {
		t.Errorf("GVK didn't round-trip: %#v", got.Match.GVK)
	}
}
