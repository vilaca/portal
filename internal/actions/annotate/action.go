// Package annotate implements api.Action "annotate": it adds (or
// overwrites) a single annotation on the violating object via server-side
// apply. Structure mirrors internal/actions/label exactly; the only
// behavioural difference is the targeted metadata sub-map.
package annotate

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
	actionType   = "annotate"
	fieldManager = "portal"
)

// ErrNotConfigured is returned when Execute runs before Configure().
var ErrNotConfigured = errors.New("annotate action not configured")

func init() {
	api.RegisterAction(actionType, func() api.Action { return &action{} })
}

// Configure swaps the registered factory for one bound to client and
// mapper. mapper may be nil; production wire-up always supplies a real
// RESTMapper.
func Configure(client dynamic.Interface, mapper meta.RESTMapper) {
	api.RegisterAction(actionType, func() api.Action { return NewWithMapper(client, mapper) })
}

// New constructs an annotate action bound to client. Falls back to the local
// pluraliser when no mapper is configured.
func New(client dynamic.Interface) api.Action {
	return &action{client: client}
}

// NewWithMapper is the production constructor — uses the discovery-backed
// RESTMapper for Kind→Resource.
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

// Execute applies metadata.annotations.<key>=<value> via SSA.
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
	obj := buildMetadataPatch(v, "annotations", key, value)
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

// guessGVR consults the RESTMapper first; falls back to pluralize() when
// no mapper is configured or the mapping isn't (yet) known to discovery.
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
