package network

import (
	"context"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vilaca/portal/internal/api"
)

// decorateActions optionally prepends alertmanager / patchnp actions per
// Options. Both are off by default; PLAN flags AutoPatchNP as risky.
func (a *Analyser) decorateActions(v *api.Violation) {
	if a.opts.AlertOnFindings {
		v.Actions = append([]api.ActionSpec{{
			Type: "alertmanager",
			On:   []api.Mode{api.ModeNetwork},
			Params: map[string]any{
				"alertname": v.Rule,
				"summary":   v.Message,
				"severity":  string(v.Severity),
			},
		}}, v.Actions...)
	}
	if a.opts.AutoPatchNP && v.GVK == (schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy"}) {
		v.Actions = append([]api.ActionSpec{{
			Type: "patchnp",
			On:   []api.Mode{api.ModeNetwork},
			Params: map[string]any{
				"namespace": v.Namespace,
				"name":      v.Name,
				"patch":     synthesisedPatchFor(v),
			},
		}}, v.Actions...)
	}
}

// synthesisedPatchFor builds a tiny JSON patch hint. Real wire-up would
// compute a server-side-apply payload; here we just stash an annotation that
// records the check that fired.
func synthesisedPatchFor(v *api.Violation) map[string]any {
	return map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]any{
				"portal.io/finding": v.Rule,
			},
		},
	}
}

// emit fans out one violation to every configured sink and the action
// dispatcher (when present). Bumps no metrics directly — the Prometheus sink
// in the slice does that for us.
func (a *Analyser) emit(ctx context.Context, v api.Violation, onEvent func(api.Context, api.EventMeta)) {
	for _, s := range a.sinks {
		_ = s.Emit(ctx, v)
	}
	if a.dispatcher != nil && v.Message != "resolved" {
		// Resolved emissions don't trigger actions — they're informational
		// for sinks that support resolution semantics.
		a.dispatcher.Dispatch(ctx, v)
	}
	if onEvent != nil {
		onEvent(api.Context{GVK: v.GVK}, api.EventMeta{
			Source:  "network",
			EventID: v.Source.EventID,
			At:      v.At,
			Operation: opOf(v),
		})
	}
	if a.onEmitForTest != nil {
		a.onEmitForTest(v)
	}
}

func opOf(v api.Violation) string {
	if strings.EqualFold(v.Message, "resolved") {
		return "resolved"
	}
	return "network"
}

// defaultResourceForGVK is the dumb fallback used when no RESTMapper is
// reachable through the AuditCache. It special-cases NetworkPolicy (the only
// irregular plural in the v1 network analyser's input set) and lowercase+'s'
// for everything else.
func defaultResourceForGVK(gvk schema.GroupVersionKind) schema.GroupVersionResource {
	r := strings.ToLower(gvk.Kind)
	switch gvk.Kind {
	case "NetworkPolicy":
		r = "networkpolicies"
	default:
		if !strings.HasSuffix(r, "s") {
			r += "s"
		}
	}
	return schema.GroupVersionResource{Group: gvk.Group, Version: gvk.Version, Resource: r}
}

// mapperBackedResolver mirrors the audit package's helper: prefer the
// discovery-backed mapper, fall through to defaultResourceForGVK on miss so
// a transient cache lookup doesn't stall the analyser.
func mapperBackedResolver(m meta.RESTMapper) func(schema.GroupVersionKind) schema.GroupVersionResource {
	return func(gvk schema.GroupVersionKind) schema.GroupVersionResource {
		mapping, err := m.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return defaultResourceForGVK(gvk)
		}
		return mapping.Resource
	}
}

// Compile-time: Analyser implements api.EventSource.
var _ api.EventSource = (*Analyser)(nil)
