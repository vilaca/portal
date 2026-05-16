// Package patchnp implements api.Action "patch-networkpolicy": it applies a
// caller-supplied JSON map as a server-side-apply patch against a
// NetworkPolicy object.
//
// Targeting rules:
//   - If the violation's GVK is NetworkPolicy, the patch targets that
//     object directly.
//   - Otherwise the action consults params.targetName /
//     params.targetNamespace and targets that NP instead. This lets a rule
//     fire on, say, a Pod violation and remediate by tightening a related
//     NP. params.targetNamespace defaults to the violation's namespace.
//
// Both branches use the dynamic client because NetworkPolicy is not in the
// kubernetes.Interface typed surface we care to vendor through this
// package.
package patchnp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"github.com/vilaca/portal/internal/api"
)

const (
	actionType   = "patch-networkpolicy"
	fieldManager = "portal"
)

// npGVR is the only resource this action ever patches.
var npGVR = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}

// ErrNotConfigured is returned when Execute runs before Configure().
var ErrNotConfigured = errors.New("patch-networkpolicy action not configured")

func init() {
	api.RegisterAction(actionType, func() api.Action { return &action{} })
}

// Configure swaps the registered factory for one backed by client.
func Configure(client dynamic.Interface) {
	api.RegisterAction(actionType, func() api.Action { return New(client) })
}

// New constructs the action bound to client.
func New(client dynamic.Interface) api.Action {
	return &action{client: client}
}

type action struct {
	client dynamic.Interface
}

func (a *action) Type() string                    { return actionType }
func (a *action) Idempotent() bool                { return true }
func (a *action) DefaultRateLimit() time.Duration { return 30 * time.Second }

// Execute reads params.patch (required, map[string]any), resolves the target
// NetworkPolicy, and applies the patch.
func (a *action) Execute(ctx context.Context, v api.Violation, params map[string]any) error {
	if a.client == nil {
		return ErrNotConfigured
	}
	patch, ok := params["patch"].(map[string]any)
	if !ok || patch == nil {
		return fmt.Errorf("%s: params.patch (map) required", actionType)
	}

	ns, name := resolveTarget(v, params)
	if name == "" {
		return fmt.Errorf("%s: target NetworkPolicy name not resolvable from violation or params", actionType)
	}

	// Compose the SSA payload: apiVersion + kind + name (+ namespace) +
	// caller-supplied patch fields. SSA owns only what we send.
	obj := map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
		},
	}
	mergePatchInto(obj, patch)
	data, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("%s: marshal: %w", actionType, err)
	}
	_, err = a.client.Resource(npGVR).Namespace(ns).Patch(
		ctx, name, types.ApplyPatchType, data,
		metav1.PatchOptions{FieldManager: fieldManager, Force: ptrBool(true)},
	)
	return err
}

// resolveTarget picks the (namespace, name) for the NP being patched.
// Violations against the NP itself take precedence; otherwise the params
// hold the target.
func resolveTarget(v api.Violation, params map[string]any) (ns, name string) {
	if v.GVK.Kind == "NetworkPolicy" && v.Name != "" {
		return v.Namespace, v.Name
	}
	name, _ = params["targetName"].(string)
	ns, _ = params["targetNamespace"].(string)
	if ns == "" {
		ns = v.Namespace
	}
	return ns, name
}

// mergePatchInto deep-copies patch into dst. The merge is shallow at the top
// level — we don't override apiVersion/kind/metadata.name, and any other key
// in patch wins. For nested map-of-map fields, the caller-supplied map
// replaces wholesale because SSA semantics already handle field-level merge
// at the server.
func mergePatchInto(dst, patch map[string]any) {
	for k, v := range patch {
		if k == "apiVersion" || k == "kind" {
			continue
		}
		// metadata is merged so the caller can add labels/annotations
		// alongside our hard-coded name/namespace; for other keys we
		// overwrite.
		if k == "metadata" {
			if existing, ok := dst["metadata"].(map[string]any); ok {
				if pm, ok := v.(map[string]any); ok {
					for mk, mv := range pm {
						if mk == "name" || mk == "namespace" {
							continue
						}
						existing[mk] = mv
					}
					continue
				}
			}
		}
		dst[k] = v
	}
}

// ensure unstructured import not flagged unused.
var _ = unstructured.Unstructured{}

func ptrBool(b bool) *bool { return &b }
