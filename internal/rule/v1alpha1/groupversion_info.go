// Package crd holds the kubebuilder-marker-annotated Go types for the
// PortalClusterRule (cluster-scoped) and PortalRule (namespaced) CRDs, plus
// the conversion helpers that turn their .Spec into the engine's canonical
// api.Rule shape.
//
// +kubebuilder:object:generate=true
// +groupName=portal.io
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

// GroupVersion is the API group / version this package's types belong to.
var GroupVersion = schema.GroupVersion{Group: "portal.io", Version: "v1alpha1"}

// SchemeBuilder is the controller-runtime SchemeBuilder for this package.
// It registers PortalClusterRule, PortalClusterRuleList, PortalRule, and
// PortalRuleList into a *runtime.Scheme when AddToScheme is invoked.
var SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

// AddToScheme adds the Portal CRD types to the supplied scheme.
var AddToScheme = SchemeBuilder.AddToScheme

func init() {
	SchemeBuilder.Register(
		&PortalClusterRule{}, &PortalClusterRuleList{},
		&PortalRule{}, &PortalRuleList{},
	)
}
