package api

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Context is the evaluation environment a rule sees. It carries three layers:
//
//   - Object: always populated. The raw resource as *unstructured.Unstructured,
//     exposed to expr-lang as nested maps. Anything reachable in the K8s schema
//     is reachable here — this is the universal escape hatch.
//   - Env: the rendered expr-lang environment (a map[string]any). Pod-shaped
//     sugar lives here under the keys "container", "spec", "securityContext",
//     "metadata", "object", "request". Non-pod GVKs get only "object",
//     "metadata", "request".
//   - Request: admission-only request metadata (Operation, UserInfo, DryRun,
//     OldObject).
type Context struct {
	GVK     schema.GroupVersionKind
	Object  *unstructured.Unstructured
	Env     map[string]any
	Request *AdmissionRequest
}

// AdmissionRequest is the subset of AdmissionReview.Request that rules can read.
type AdmissionRequest struct {
	Operation string
	UserInfo  UserInfo
	DryRun    bool
	OldObject *unstructured.Unstructured
}

// UserInfo identifies the requester at admission.
type UserInfo struct {
	Username string
	UID      string
	Groups   []string
	Extra    map[string][]string
}

// ContextBuilder converts a raw unstructured resource into an evaluation Context.
// One ContextBuilder is responsible for one GVK family — the pod-shaped builder
// covers Pod plus any GVK whose spec embeds PodTemplateSpec; a fallback builder
// handles everything else with only Object/Env(object,metadata,request).
type ContextBuilder interface {
	Supports(gvk schema.GroupVersionKind) bool
	Build(obj *unstructured.Unstructured) (Context, error)
}
