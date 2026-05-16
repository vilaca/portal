// Package label implements api.Action "label": it adds (or overwrites) a
// single label on the violating object via server-side apply.
//
// Server-side apply was chosen over a JSON-patch GET/MODIFY/PUT because:
//   - SSA owns only the fields we send (metadata.labels.<key>), so other
//     controllers' labels are left alone;
//   - retries on conflict are unnecessary — SSA reconciles instead;
//   - the field manager "portal" makes ownership visible in kubectl.
//
// The action takes the dynamic client by Configure() rather than at init()
// because the kubeconfig is not loaded until the composition root runs.
// init() registers a placeholder factory that returns an action whose
// Execute fails with ErrNotConfigured — same pattern as the alertmanager
// and policyreport sinks in Wave 1.
package label

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"github.com/vilaca/portal/internal/api"
)

const (
	actionType   = "label"
	fieldManager = "portal"
)

// ErrNotConfigured is returned when Execute runs before Configure().
var ErrNotConfigured = errors.New("label action not configured")

func init() {
	api.RegisterAction(actionType, func() api.Action { return &action{} })
}

// Configure swaps the registered factory for one bound to client and
// mapper. mapper may be nil for tests; production wire-up always supplies a
// discovery-backed RESTMapper so the action handles irregular plurals and
// CRDs correctly.
func Configure(client dynamic.Interface, mapper meta.RESTMapper) {
	api.RegisterAction(actionType, func() api.Action { return NewWithMapper(client, mapper) })
}

// New constructs a label action bound to client. The action falls back to
// the local pluralise() when invoked without a mapper.
func New(client dynamic.Interface) api.Action {
	return &action{client: client}
}

// NewWithMapper is the production constructor — the mapper resolves
// Kind→Resource via the discovery API instead of the local pluraliser.
func NewWithMapper(client dynamic.Interface, mapper meta.RESTMapper) api.Action {
	return &action{client: client, mapper: mapper}
}

type action struct {
	client dynamic.Interface
	mapper meta.RESTMapper
}

func (a *action) Type() string                    { return actionType }
func (a *action) Idempotent() bool                { return true }
func (a *action) DefaultRateLimit() time.Duration { return 5 * time.Second }

// Execute applies metadata.labels.<key>=<value> via SSA. Returns an error if
// params.key is missing or empty; the dispatcher logs the error and bumps
// portal_actions_total{result="error"}.
func (a *action) Execute(ctx context.Context, v api.Violation, params map[string]any) error {
	if a.client == nil {
		return ErrNotConfigured
	}
	key, _ := params["key"].(string)
	if key == "" {
		return fmt.Errorf("%s: params.key required", actionType)
	}
	value, _ := params["value"].(string)
	if value == "" {
		value = "true"
	}
	gvr := a.guessGVR(v.GVK)
	obj := buildMetadataPatch(v, "labels", key, value)
	data, err := obj.MarshalJSON()
	if err != nil {
		return fmt.Errorf("%s: marshal: %w", actionType, err)
	}
	_, err = a.client.Resource(gvr).Namespace(v.Namespace).Patch(
		ctx, v.Name, types.ApplyPatchType, data,
		metav1.PatchOptions{FieldManager: fieldManager, Force: ptrBool(true)},
	)
	return err
}

// buildMetadataPatch shapes the SSA payload. Only apiVersion, kind, name,
// (namespace, if any), and the targeted metadata sub-map are sent — this
// keeps SSA ownership minimal.
func buildMetadataPatch(v api.Violation, mapKey, k, val string) *unstructured.Unstructured {
	apiVersion := v.GVK.Version
	if v.GVK.Group != "" {
		apiVersion = v.GVK.Group + "/" + v.GVK.Version
	}
	o := &unstructured.Unstructured{}
	o.SetAPIVersion(apiVersion)
	o.SetKind(v.GVK.Kind)
	o.SetName(v.Name)
	if v.Namespace != "" {
		o.SetNamespace(v.Namespace)
	}
	_ = unstructured.SetNestedStringMap(o.Object, map[string]string{k: val}, "metadata", mapKey)
	return o
}

// guessGVR maps a GroupVersionKind to a GroupVersionResource. When a
// RESTMapper is configured, it wins; otherwise the local pluraliser is
// the fallback. The fallback path is exercised by unit tests; production
// always has a mapper.
func (a *action) guessGVR(gvk schema.GroupVersionKind) schema.GroupVersionResource {
	if a.mapper != nil {
		if mapping, err := a.mapper.RESTMapping(gvk.GroupKind(), gvk.Version); err == nil {
			return mapping.Resource
		}
	}
	return schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: pluralize(gvk.Kind),
	}
}

func pluralize(kind string) string {
	k := strings.ToLower(kind)
	switch k {
	case "networkpolicy":
		return "networkpolicies"
	case "ingress":
		return "ingresses"
	case "endpoints":
		return "endpoints"
	}
	if strings.HasSuffix(k, "s") || strings.HasSuffix(k, "x") || strings.HasSuffix(k, "ch") || strings.HasSuffix(k, "sh") {
		return k + "es"
	}
	if strings.HasSuffix(k, "y") {
		return strings.TrimSuffix(k, "y") + "ies"
	}
	return k + "s"
}

func ptrBool(b bool) *bool { return &b }
