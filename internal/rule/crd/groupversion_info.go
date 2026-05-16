// Package crd holds the kubebuilder-marker-annotated Go types for the
// PortalClusterRule (cluster-scoped) and PortalRule (namespaced) CRDs, plus
// the conversion helpers that turn their .Spec into the engine's canonical
// api.Rule shape.
//
// +kubebuilder:object:generate=true
// +groupName=portal.io
package crd

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupVersion is the API group / version this package's types belong to.
var GroupVersion = schema.GroupVersion{Group: "portal.io", Version: "v1alpha1"}

// SchemeBuilder is populated by AddToScheme below; controller-runtime callers
// register the package by calling AddToScheme on their *runtime.Scheme. In
// Wave 1 the slice is intentionally empty — Wave 2 will add the
// SchemeBuilder.Register calls for the two CR types alongside the
// controller-runtime manager wiring.
var SchemeBuilder = schemeBuilder{
	GroupVersion: GroupVersion,
}

// schemeBuilder is a tiny stand-in for sigs.k8s.io/controller-runtime's
// scheme builder so this package doesn't take a runtime-only dependency yet.
// Wave 2 will replace it with the real controller-runtime SchemeBuilder.
type schemeBuilder struct {
	GroupVersion schema.GroupVersion
}
