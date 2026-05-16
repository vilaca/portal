// Package policyreportgc implements api.Action "policyreport-gc": it removes
// every PolicyReportResult that references the deleted object from the
// PolicyReport (namespaced) or ClusterPolicyReport (cluster-scoped). The
// audit controller emits a synthetic Violation tagged with this action's
// Type on every OnDelete event, so stale Results don't accumulate after
// objects leave the cluster.
//
// The action uses Update rather than server-side apply because we are
// shrinking the .results array — SSA's "owned fields" model would attempt to
// re-apply our previous additions, defeating the GC. A plain Update against
// the resource is the correct primitive.
//
// Naming: matches internal/sink/policyreport's reportName constant ("portal")
// in every namespace and cluster-wide. Changing one without the other will
// break GC.
package policyreportgc

import (
	"context"
	"errors"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/vilaca/portal/internal/api"
)

const (
	actionType = "policyreport-gc"
	// reportName mirrors internal/sink/policyreport.reportName. Kept as a
	// duplicate constant rather than imported so the action package stays
	// independent of the sink package's internals.
	reportName = "portal"
)

var (
	prGVR  = schema.GroupVersionResource{Group: "wgpolicyk8s.io", Version: "v1alpha2", Resource: "policyreports"}
	cprGVR = schema.GroupVersionResource{Group: "wgpolicyk8s.io", Version: "v1alpha2", Resource: "clusterpolicyreports"}
)

// ErrNotConfigured is returned when Execute runs before Configure().
var ErrNotConfigured = errors.New("policyreport-gc action not configured")

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
func (a *action) DefaultRateLimit() time.Duration { return 5 * time.Second }

// Execute drops every PolicyReportResult that references (v.GVK.Kind,
// v.Namespace, v.Name) from the relevant report. Missing report → no-op.
// Reports with no matching results → no-op (no write).
func (a *action) Execute(ctx context.Context, v api.Violation, _ map[string]any) error {
	if a.client == nil {
		return ErrNotConfigured
	}
	if v.Namespace == "" {
		return a.gcReport(ctx, cprGVR, "", v)
	}
	return a.gcReport(ctx, prGVR, v.Namespace, v)
}

func (a *action) gcReport(ctx context.Context, gvr schema.GroupVersionResource, ns string, v api.Violation) error {
	var ri dynamic.ResourceInterface
	if ns == "" {
		ri = a.client.Resource(gvr)
	} else {
		ri = a.client.Resource(gvr).Namespace(ns)
	}
	obj, err := ri.Get(ctx, reportName, metav1.GetOptions{})
	if err != nil {
		// Missing report (or any read error) → nothing to GC. We don't
		// distinguish NotFound from other errors here because the action's
		// purpose is best-effort cleanup; the dispatcher's audit log will
		// surface real failures if they recur.
		return nil
	}
	if !filterOut(obj, v.GVK.Kind, v.Namespace, v.Name) {
		return nil
	}
	_, err = ri.Update(ctx, obj, metav1.UpdateOptions{})
	return err
}

// filterOut walks obj.results and removes every entry whose resources[]
// includes a match on (kind, namespace, name). Returns true when at least one
// result was removed.
func filterOut(obj *unstructured.Unstructured, kind, namespace, name string) bool {
	results, found, err := unstructured.NestedSlice(obj.Object, "results")
	if !found || err != nil || len(results) == 0 {
		return false
	}
	keep := make([]any, 0, len(results))
	removed := false
	for _, r := range results {
		rm, ok := r.(map[string]any)
		if !ok {
			keep = append(keep, r)
			continue
		}
		if resultMatches(rm, kind, namespace, name) {
			removed = true
			continue
		}
		keep = append(keep, r)
	}
	if !removed {
		return false
	}
	_ = unstructured.SetNestedSlice(obj.Object, keep, "results")
	// Update the summary so policy-reporter-style consumers see a fresh
	// fail count. Other counters stay zero — the sink only emits fails.
	_ = unstructured.SetNestedField(obj.Object, int64(len(keep)), "summary", "fail")
	return true
}

func resultMatches(r map[string]any, kind, namespace, name string) bool {
	resources, ok := r["resources"].([]any)
	if !ok {
		return false
	}
	for _, res := range resources {
		rm, ok := res.(map[string]any)
		if !ok {
			continue
		}
		rk, _ := rm["kind"].(string)
		rn, _ := rm["name"].(string)
		rns, _ := rm["namespace"].(string)
		if rk == kind && rn == name && rns == namespace {
			return true
		}
	}
	return false
}
