package network

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"

	"github.com/vilaca/portal/internal/api"
)

// Defaults referenced from PLAN §"Phase 6 — NetworkPolicy declarative analysis".
const (
	DefaultResyncPeriod   = 10 * time.Minute
	DefaultWorkerPoolSize = 4
)

// AuditCache is the tiny shape this package depends on — same as
// lookup.AuditCache. Audit's controller satisfies it.
type AuditCache interface {
	Lister(gvk schema.GroupVersionKind) (cache.GenericLister, error)
	SharedInformerFactory() dynamicinformer.DynamicSharedInformerFactory
}

// Options configures the analyser.
type Options struct {
	// ResyncPeriod is the informer resync period. Default 10 minutes (safety net).
	ResyncPeriod time.Duration
	// WorkerPoolSize is the number of workers processing re-evaluations. Default 4.
	WorkerPoolSize int
	// AlertOnFindings prepends an alertmanager action to each emitted finding.
	// Off by default.
	AlertOnFindings bool
	// AutoPatchNP prepends a patchnp action to each emitted finding. Off by
	// default — flagged risky in PLAN.
	AutoPatchNP bool
	// ResourceForGVK resolves GVK → GVR for the factory. Nil uses the naive
	// pluraliser (correct for Pod/NetworkPolicy/Namespace).
	ResourceForGVK func(schema.GroupVersionKind) schema.GroupVersionResource
}

// Analyser implements api.EventSource. It is created via New().
type Analyser struct {
	audit      AuditCache
	dispatcher api.ActionDispatcher
	sinks      []api.OutputSink
	opts       Options
	resForGVK  func(schema.GroupVersionKind) schema.GroupVersionResource

	podLister cache.GenericLister
	npLister  cache.GenericLister
	nsLister  cache.GenericLister

	queue chan workItem

	mu             sync.Mutex
	activeFindings map[string]api.Violation // findingKey → current violation snapshot
	stopCh         chan struct{}
	stoppedCh      chan struct{}

	// onEmitForTest is set by tests to observe each emit (including
	// "resolved" emissions).
	onEmitForTest func(api.Violation)
}

type workItem struct {
	namespace string
	// affectedNP, when non-empty, narrows clear semantics on NP delete.
	deletedNP string
}

// New constructs a NetworkPolicy analyser. The audit AuditCache MUST already
// be watching Pods, NetworkPolicies, and Namespaces (or the constructor falls
// back to nil listers and the analyser emits no findings).
func New(audit AuditCache, dispatcher api.ActionDispatcher, sinks []api.OutputSink, opts Options) (api.EventSource, error) {
	if audit == nil {
		return nil, errors.New("network.New: nil AuditCache")
	}
	if opts.ResyncPeriod <= 0 {
		opts.ResyncPeriod = DefaultResyncPeriod
	}
	if opts.WorkerPoolSize <= 0 {
		opts.WorkerPoolSize = DefaultWorkerPoolSize
	}
	if opts.ResourceForGVK == nil {
		if m, ok := audit.(interface{ RESTMapper() meta.RESTMapper }); ok {
			if mapper := m.RESTMapper(); mapper != nil {
				opts.ResourceForGVK = mapperBackedResolver(mapper)
			}
		}
		if opts.ResourceForGVK == nil {
			opts.ResourceForGVK = defaultResourceForGVK
		}
	}
	a := &Analyser{
		audit:          audit,
		dispatcher:     dispatcher,
		sinks:          sinks,
		opts:           opts,
		resForGVK:      opts.ResourceForGVK,
		queue:          make(chan workItem, 1024),
		activeFindings: map[string]api.Violation{},
	}

	// Best-effort: fetch listers if available. Missing listers are tolerated —
	// the analyser still starts, just with no data.
	if l, err := audit.Lister(PodGVK); err == nil {
		a.podLister = l
	}
	if l, err := audit.Lister(NPGVK); err == nil {
		a.npLister = l
	}
	if l, err := audit.Lister(NSGVK); err == nil {
		a.nsLister = l
	}
	return a, nil
}

// Name implements api.EventSource.
func (a *Analyser) Name() string { return "network" }

// Start implements api.EventSource. It is blocking until ctx is cancelled.
// The onEvent callback is invoked once per emitted Violation (including
// "resolved" emissions). It may be nil.
func (a *Analyser) Start(ctx context.Context, onEvent func(api.Context, api.EventMeta)) error {
	a.mu.Lock()
	if a.stopCh != nil {
		a.mu.Unlock()
		return errors.New("network.Start: already running")
	}
	a.stopCh = make(chan struct{})
	a.stoppedCh = make(chan struct{})
	stopCh := a.stopCh
	stoppedCh := a.stoppedCh
	a.mu.Unlock()
	defer close(stoppedCh)

	// Install informer handlers if a factory is available.
	if f := a.audit.SharedInformerFactory(); f != nil {
		if err := a.installHandlers(f); err != nil {
			return err
		}
	}

	// Worker pool.
	var wg sync.WaitGroup
	for i := 0; i < a.opts.WorkerPoolSize; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.runWorker(ctx, onEvent)
		}()
	}

	// Initial sweep across all namespaces.
	a.enqueue(workItem{namespace: ""})

	// Periodic resync (safety net).
	ticker := time.NewTicker(a.opts.ResyncPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			close(a.queue)
			wg.Wait()
			return nil
		case <-stopCh:
			close(a.queue)
			wg.Wait()
			return nil
		case <-ticker.C:
			a.enqueue(workItem{namespace: ""})
		}
	}
}

// Stop implements api.EventSource.
func (a *Analyser) Stop(ctx context.Context) error {
	a.mu.Lock()
	stopCh := a.stopCh
	stoppedCh := a.stoppedCh
	a.stopCh = nil
	a.mu.Unlock()
	if stopCh == nil {
		return nil
	}
	close(stopCh)
	if stoppedCh != nil {
		select {
		case <-stoppedCh:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// installHandlers wires informer event handlers for Pod/NP/Namespace.
func (a *Analyser) installHandlers(f dynamicinformer.DynamicSharedInformerFactory) error {
	if f == nil {
		return nil
	}
	for _, gvk := range []schema.GroupVersionKind{PodGVK, NPGVK, NSGVK} {
		gvr := a.resForGVK(gvk)
		inf := f.ForResource(gvr).Informer()
		captured := gvk
		_, err := inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    func(obj any) { a.onAdd(captured, obj) },
			UpdateFunc: func(_, obj any) { a.onUpdate(captured, obj) },
			DeleteFunc: func(obj any) { a.onDelete(captured, obj) },
		})
		if err != nil {
			return fmt.Errorf("network: AddEventHandler %s: %w", gvk, err)
		}
	}
	return nil
}

func (a *Analyser) onAdd(gvk schema.GroupVersionKind, obj any) {
	ns := nsOf(obj)
	a.enqueue(workItem{namespace: ns})
}

func (a *Analyser) onUpdate(gvk schema.GroupVersionKind, obj any) {
	ns := nsOf(obj)
	a.enqueue(workItem{namespace: ns})
}

func (a *Analyser) onDelete(gvk schema.GroupVersionKind, obj any) {
	if t, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = t.Obj
	}
	ns := nsOf(obj)
	if gvk == NPGVK {
		name := nameOf(obj)
		a.enqueue(workItem{namespace: ns, deletedNP: name})
		return
	}
	a.enqueue(workItem{namespace: ns})
}

func nsOf(obj any) string {
	if a, ok := obj.(metav1.Object); ok {
		return a.GetNamespace()
	}
	return ""
}

func nameOf(obj any) string {
	if a, ok := obj.(metav1.Object); ok {
		return a.GetName()
	}
	return ""
}

// enqueue is non-blocking; on a full queue we drop and rely on the next event
// or resync. Sized 1024 — exceeding that means churn that periodic resync
// covers anyway.
func (a *Analyser) enqueue(w workItem) {
	defer func() { _ = recover() }() // queue closed during shutdown — ignore
	select {
	case a.queue <- w:
	default:
	}
}

func (a *Analyser) runWorker(ctx context.Context, onEvent func(api.Context, api.EventMeta)) {
	for {
		select {
		case <-ctx.Done():
			return
		case w, ok := <-a.queue:
			if !ok {
				return
			}
			a.process(ctx, w, onEvent)
		}
	}
}

// process re-evaluates all checks for the affected namespace (or cluster-wide
// when ns==""), compares against the current activeFindings map, and emits
// new findings + synthetic "resolved" violations for cleared ones.
func (a *Analyser) process(ctx context.Context, w workItem, onEvent func(api.Context, api.EventMeta)) {
	m, err := BuildModel(a.podLister, a.npLister, a.nsLister, w.namespace)
	if err != nil {
		return
	}
	now := time.Now()
	current := Run(m, w.namespace)

	// Diff against active findings restricted to this scope.
	scopeMatches := func(key string) bool {
		if w.namespace == "" {
			return true
		}
		// findingKey shape: "<check>:<ns>:<name>"
		// or for namespace-level findings: "<check>:<ns>:<ns>"
		parts := strings.SplitN(key, ":", 3)
		if len(parts) < 3 {
			return false
		}
		return parts[1] == w.namespace
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	currentSet := map[string]api.Violation{}
	for _, v := range current {
		k := findingKey(v)
		currentSet[k] = v
	}

	// Resolved: was active, no longer in current.
	for k, prev := range a.activeFindings {
		if !scopeMatches(k) {
			continue
		}
		if _, still := currentSet[k]; still {
			continue
		}
		resolved := prev
		resolved.Message = "resolved"
		resolved.At = now
		resolved.Actions = nil
		a.emit(ctx, resolved, onEvent)
		delete(a.activeFindings, k)
	}

	// New + still-active: fire only if not previously active OR contents changed.
	for k, v := range currentSet {
		prev, was := a.activeFindings[k]
		if was && prev.Message == v.Message {
			continue
		}
		a.decorateActions(&v)
		a.emit(ctx, v, onEvent)
		a.activeFindings[k] = v
	}

	// NP delete: also clear any active findings keyed to the deleted NP.
	if w.deletedNP != "" {
		prefix := func(check string) string { return check + ":" + w.namespace + "/" + w.deletedNP }
		for k, prev := range a.activeFindings {
			if !(strings.HasPrefix(k, prefix(CheckBroadCIDR)) ||
				strings.HasPrefix(k, prefix(CheckUnreachableSelector)) ||
				strings.HasPrefix(k, prefix(CheckPolicyWithoutTargets))) {
				continue
			}
			if _, still := currentSet[k]; still {
				continue
			}
			resolved := prev
			resolved.Message = "resolved"
			resolved.At = now
			resolved.Actions = nil
			a.emit(ctx, resolved, onEvent)
			delete(a.activeFindings, k)
		}
	}
}

// findingKey is the deduplication key for active findings.
func findingKey(v api.Violation) string {
	switch v.Rule {
	case "np." + "default-deny-missing":
		return CheckDefaultDenyMissing + ":" + v.Namespace + ":" + v.Name
	default:
		// NP-keyed checks use a deterministic prefix; the EventID already
		// carries the rule index. We re-key here on namespace/name to keep
		// dedup stable across rebuilds.
		check := v.Rule
		// Map rule → check id by lookup.
		switch v.Rule {
		case "np." + "broad-cidr":
			check = CheckBroadCIDR
		case "np." + "unreachable-selector":
			check = CheckUnreachableSelector
		case "np." + "policy-without-targets":
			check = CheckPolicyWithoutTargets
		}
		// Use the EventID directly when set — it already encodes rule indices.
		if v.Source.EventID != "" {
			return v.Source.EventID
		}
		return check + ":" + v.Namespace + "/" + v.Name
	}
}
