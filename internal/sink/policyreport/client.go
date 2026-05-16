// Package policyreport implements an api.OutputSink that upserts
// wgpolicyk8s.io/v1alpha2 PolicyReport (namespaced) and ClusterPolicyReport
// (cluster-scoped) entries via a dynamic client. One report per namespace
// (or one ClusterPolicyReport for cluster-scoped objects), with Results
// deduplicated in-memory by (rule, namespace, name) so a re-emission
// replaces the prior entry rather than appending a duplicate.
//
// Server-side apply with fieldManager "portal" is used instead of
// read-modify-write — SSA + the lockstep dedup map gives correctness
// without a races-and-retries loop.
//
// Like alertmanager, registration is a placeholder at init() because the
// real factory needs a dynamic.Interface that isn't available until the
// composition root has loaded a kubeconfig. cmd/portal/wire.go calls
// Configure(client) in Wave 3 to swap in the real sink.
package policyreport

import (
	"context"
	"errors"
	"fmt"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"github.com/vilaca/portal/internal/api"
)

// Group/version/resource for the wgpolicyk8s.io v1alpha2 reports.
var (
	prGVR  = schema.GroupVersionResource{Group: "wgpolicyk8s.io", Version: "v1alpha2", Resource: "policyreports"}
	cprGVR = schema.GroupVersionResource{Group: "wgpolicyk8s.io", Version: "v1alpha2", Resource: "clusterpolicyreports"}
)

const (
	fieldManager  = "portal"
	reportName    = "portal"
	categoryLabel = "portal"
	sourceLabel   = "portal"
)

func init() {
	api.RegisterSink("policyreport", func() api.OutputSink { return &placeholder{} })
}

// ErrNotConfigured indicates Configure(client) was never called.
var ErrNotConfigured = errors.New("policyreport sink not configured")

type placeholder struct{}

func (placeholder) Name() string                                  { return "policyreport" }
func (placeholder) Emit(_ context.Context, _ api.Violation) error { return ErrNotConfigured }
func (placeholder) Close() error                                  { return nil }

// Configure swaps the registered factory for one backed by client.
func Configure(client dynamic.Interface) {
	api.RegisterSink("policyreport", func() api.OutputSink { return New(client) })
}

// resultKey deduplicates a single Result within a sink lifetime by the
// (rule, namespace, name) tuple — re-emitting the same violation replaces
// the prior Result rather than appending.
type resultKey struct {
	rule, namespace, name string
}

// sink is the configured client.
type sink struct {
	client dynamic.Interface

	mu        sync.Mutex
	nsResults map[string]map[resultKey]map[string]any // namespace → key → result
	clResults map[resultKey]map[string]any            // cluster-scoped results
}

// New constructs a PolicyReport sink backed by the supplied dynamic client.
func New(client dynamic.Interface) api.OutputSink {
	return &sink{
		client:    client,
		nsResults: map[string]map[resultKey]map[string]any{},
		clResults: map[resultKey]map[string]any{},
	}
}

// Name implements api.OutputSink.
func (s *sink) Name() string { return "policyreport" }

// Close implements api.OutputSink.
func (s *sink) Close() error { return nil }

// Emit upserts the Result for v into the appropriate (Cluster)PolicyReport.
// A violation with Message=="resolved" deletes the corresponding Result
// instead — that's how the network analyser tells consumers a previously
// active finding has cleared.
func (s *sink) Emit(ctx context.Context, v api.Violation) error {
	key := resultKey{rule: v.Rule, namespace: v.Namespace, name: v.Name}

	s.mu.Lock()
	if v.Message == "resolved" {
		if v.Namespace == "" {
			delete(s.clResults, key)
		} else if ns, ok := s.nsResults[v.Namespace]; ok {
			delete(ns, key)
		}
	} else {
		res := buildResult(v)
		if v.Namespace == "" {
			s.clResults[key] = res
		} else {
			ns, ok := s.nsResults[v.Namespace]
			if !ok {
				ns = map[resultKey]map[string]any{}
				s.nsResults[v.Namespace] = ns
			}
			ns[key] = res
		}
	}
	results := s.snapshotResults(v.Namespace)
	s.mu.Unlock()

	return s.apply(ctx, v.Namespace, results)
}

// snapshotResults returns the current Result list for the given scope.
// Iteration order is incidentally fine for SSA — apply replaces the whole
// list rather than diffing items.
func (s *sink) snapshotResults(namespace string) []any {
	var src map[resultKey]map[string]any
	if namespace == "" {
		src = s.clResults
	} else {
		src = s.nsResults[namespace]
	}
	out := make([]any, 0, len(src))
	for _, r := range src {
		out = append(out, r)
	}
	return out
}

// apply renders the SSA patch and posts it via the dynamic client.
func (s *sink) apply(ctx context.Context, namespace string, results []any) error {
	cluster := namespace == ""
	gvr := prGVR
	kind := "PolicyReport"
	if cluster {
		gvr = cprGVR
		kind = "ClusterPolicyReport"
	}
	obj := &unstructured.Unstructured{Object: map[string]any{}}
	obj.SetAPIVersion("wgpolicyk8s.io/v1alpha2")
	obj.SetKind(kind)
	obj.SetName(reportName)
	if !cluster {
		obj.SetNamespace(namespace)
	}
	obj.Object["results"] = results
	obj.Object["summary"] = summarise(results)

	data, err := obj.MarshalJSON()
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}

	var ri dynamic.ResourceInterface
	if cluster {
		ri = s.client.Resource(gvr)
	} else {
		ri = s.client.Resource(gvr).Namespace(namespace)
	}
	// Force=true: we own every field we declare in the SSA payload, even
	// if another fieldManager (e.g. a manual edit) claimed it first.
	force := true
	_, err = ri.Patch(ctx, reportName, types.ApplyPatchType, data, metav1.PatchOptions{
		FieldManager: fieldManager,
		Force:        &force,
	})
	if err == nil {
		return nil
	}
	// Some clients (notably client-go/dynamic/fake) don't implement SSA
	// create-on-missing. Fall back to Create when the report doesn't exist
	// yet; subsequent Emits will hit the Patch path. Real clusters never
	// reach this branch because Apply is create-or-update by design.
	if apierrors.IsNotFound(err) {
		if _, cerr := ri.Create(ctx, obj, metav1.CreateOptions{FieldManager: fieldManager}); cerr == nil {
			return nil
		} else if apierrors.IsAlreadyExists(cerr) {
			// Lost the race; another goroutine created it. Retry the SSA.
			_, perr := ri.Patch(ctx, reportName, types.ApplyPatchType, data, metav1.PatchOptions{
				FieldManager: fieldManager,
				Force:        &force,
			})
			if perr != nil {
				return fmt.Errorf("apply %s after create race: %w", kind, perr)
			}
			return nil
		} else {
			return fmt.Errorf("apply %s: %w (create fallback: %v)", kind, err, cerr)
		}
	}
	return fmt.Errorf("apply %s: %w", kind, err)
}

// silenceUnusedErrors keeps the errors import live for future use of
// errors.Is in retry branches; the fake-client compatibility path was
// removed in favour of a reactor-driven test client (see client_test.go).
var _ = errors.New

// buildResult renders one wgpolicyk8s.io/v1alpha2 PolicyReportResult.
func buildResult(v api.Violation) map[string]any {
	return map[string]any{
		"policy":   v.Rule,
		"rule":     v.Rule,
		"category": categoryLabel,
		"severity": string(v.Severity),
		"result":   "fail",
		"source":   sourceLabel,
		"message":  v.Message,
		"resources": []any{
			map[string]any{
				"apiVersion": gvkAPIVersion(v.GVK),
				"kind":       v.GVK.Kind,
				"name":       v.Name,
				"namespace":  v.Namespace,
			},
		},
		"timestamp": map[string]any{
			"seconds": v.At.UTC().Unix(),
			"nanos":   int64(v.At.UTC().Nanosecond()),
		},
	}
}

// gvkAPIVersion renders schema.GroupVersionKind into the "group/version" form
// the wgpolicyk8s.io API expects in resources[].apiVersion. Core (no group)
// renders as just the version, matching how kubectl writes it.
func gvkAPIVersion(g schema.GroupVersionKind) string {
	if g.Group == "" {
		return g.Version
	}
	return g.Group + "/" + g.Version
}

// summarise produces the PolicyReport.summary block. With result="fail" for
// every emission, "fail" tracks len(results); other counts stay zero.
func summarise(results []any) map[string]any {
	return map[string]any{
		"pass":  int64(0),
		"fail":  int64(len(results)),
		"warn":  int64(0),
		"error": int64(0),
		"skip":  int64(0),
	}
}
