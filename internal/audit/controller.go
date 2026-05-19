// Package audit implements the informer-driven api.EventSource for Portal's
// layer-5 continuous-audit loop. It is opt-in via the --audit CLI flag.
//
// Architecture:
//
//   - One dynamic informer per audited GVK, all sharing one
//     dynamicinformer.DynamicSharedInformerFactory so the lookup and
//     network modules can reuse the caches with no extra API calls.
//   - OnAdd/OnUpdate handlers enqueue work into a controller-runtime-style
//     workqueue. A bounded worker pool dequeues, builds Context(s) via a
//     registered ContextBuilder, evaluates rules, dispatches resulting
//     violations to sinks and the ActionDispatcher.
//   - OnDelete emits a synthetic ModeAudit Violation tagged with action
//     "policyreport-gc" so the PolicyReport sink can GC its entries for
//     that object. Rule evaluation is NOT performed on delete — there is
//     no live object to evaluate against.
//   - Periodic resync (default 10m) is a safety net only; main path is
//     watch events. Watch errors increment portal_audit_watch_reconnects_total.
//   - Optional lease-based leader election (default on). Informers run on
//     every replica for cache warmth; only the leader executes worker-side
//     mutations (sink emit + dispatch). When LeaderElection=false workers
//     are always active (single-replica mode).
//
// Source contract: the constructor returns an api.EventSource. Start blocks
// until ctx is cancelled. The onEvent callback is invoked for every
// evaluated (Context, EventMeta) pair so wire-up code can observe the
// pipeline; it is OPTIONAL — workers also emit through sinks/dispatcher
// directly.
package audit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/util/workqueue"

	"github.com/vilaca/portal/internal/api"
	"github.com/vilaca/portal/internal/context/pod"
	prommetrics "github.com/vilaca/portal/internal/sink/prometheus"
)

// Defaults referenced from PLAN §"Phase 3 — Continuous audit".
const (
	DefaultResyncPeriod      = 10 * time.Minute
	DefaultWorkerPoolSize    = 8
	DefaultLeaseDuration     = 15 * time.Second
	DefaultRenewDeadline     = 10 * time.Second
	DefaultRetryPeriod       = 2 * time.Second
	DefaultLeaseLockName     = "portal-leader"
	policyReportGCActionType = "policyreport-gc"
)

// Options configures the audit controller.
type Options struct {
	// ResyncPeriod is the informer resync interval; default 10 minutes.
	// Resync is a safety net only — main path is watch events.
	ResyncPeriod time.Duration

	// WorkerPoolSize is the number of worker goroutines processing the queue;
	// default 8.
	WorkerPoolSize int

	// LeaderElection toggles lease-based leader election; default true.
	LeaderElection bool

	// LeaseLockNamespace is the namespace holding the Lease. Required when
	// LeaderElection is true.
	LeaseLockNamespace string

	// LeaseLockName is the Lease resource name; default "portal-leader".
	LeaseLockName string

	// Identity is the unique identity for this replica; default os.Hostname().
	Identity string

	// ContextBuilders is the ordered list of builders consulted by Supports().
	// Empty falls back to the default pod-shaped builder.
	ContextBuilders []api.ContextBuilder

	// ResourceForGVK is an optional override that maps GVK to GVR for the
	// dynamic informer factory. When non-nil it takes precedence over
	// RESTMapper. Nil-and-nil falls back to a naive lowercase+'s' rule.
	ResourceForGVK func(schema.GroupVersionKind) schema.GroupVersionResource

	// RESTMapper is the discovery-backed Kind→Resource resolver. Used to
	// derive GVRs for the dynamic informer factory and exposed via
	// Controller.RESTMapper() so the lookup/network/action modules can
	// reuse it. Nil falls back to ResourceForGVK or, if that is also nil,
	// the naive resolver — wrong on irregular plurals (NetworkPolicy →
	// networkpolicys).
	RESTMapper meta.RESTMapper
}

// Controller is the concrete api.EventSource produced by New. The struct is
// exported so wire-up code can call SharedInformerFactory() and Lister(gvk)
// to share the cache with the lookup and network modules.
type Controller struct {
	cfg      *rest.Config
	dyn      dynamic.Interface
	kube     kubernetes.Interface
	gvks     []schema.GroupVersionKind
	opts     Options

	engine     api.RuleEngine
	dispatcher api.ActionDispatcher
	sinks      []api.OutputSink

	factory    dynamicinformer.DynamicSharedInformerFactory
	informers  map[schema.GroupVersionKind]cache.SharedIndexInformer
	listers    map[schema.GroupVersionKind]cache.GenericLister
	resForGVK  func(schema.GroupVersionKind) schema.GroupVersionResource

	queue   workqueue.TypedRateLimitingInterface[workItem]
	isLeader atomic.Bool

	mu        sync.Mutex
	stopCh    chan struct{}
	stoppedCh chan struct{}

	// activeViolations remembers which (object, rule) pairs produced a
	// violation on the last evaluation. When a rule stops firing for an
	// object that was previously in violation, the next evaluation emits a
	// synthetic Message="resolved" violation so sinks (e.g. PolicyReport)
	// can remove the stale entry. Keyed by object key
	// "<gvk>|<namespace>/<name>" → set of rule names.
	activeMu        sync.Mutex
	activeByObject  map[string]map[string]api.Violation
}

// workItem is what informer handlers enqueue.
type workItem struct {
	GVK       schema.GroupVersionKind
	Namespace string
	Name      string
	EventType string // "add", "update", "delete"
}

// New constructs an audit EventSource. cfg is required for dynamic client
// and (when LeaderElection=true) the coordination v1 client.
//
// gvks is the set of GVKs this controller will watch. One dynamic informer
// per unique GVK is created; the underlying factory is exposed via
// SharedInformerFactory() so the lookup/network modules can register
// additional event handlers without re-listing.
func New(
	cfg *rest.Config,
	gvks []schema.GroupVersionKind,
	engine api.RuleEngine,
	dispatcher api.ActionDispatcher,
	sinks []api.OutputSink,
	opts Options,
) (api.EventSource, error) {
	if cfg == nil {
		return nil, errors.New("audit.New: nil rest.Config")
	}
	if engine == nil {
		return nil, errors.New("audit.New: nil RuleEngine")
	}
	if len(gvks) == 0 {
		return nil, errors.New("audit.New: empty GVK list")
	}

	if opts.ResyncPeriod <= 0 {
		opts.ResyncPeriod = DefaultResyncPeriod
	}
	if opts.WorkerPoolSize <= 0 {
		opts.WorkerPoolSize = DefaultWorkerPoolSize
	}
	if opts.LeaseLockName == "" {
		opts.LeaseLockName = DefaultLeaseLockName
	}
	if opts.Identity == "" {
		h, _ := os.Hostname()
		if h == "" {
			h = fmt.Sprintf("portal-%d", os.Getpid())
		}
		opts.Identity = h
	}
	if len(opts.ContextBuilders) == 0 {
		opts.ContextBuilders = []api.ContextBuilder{pod.New()}
	}
	if opts.ResourceForGVK == nil {
		if opts.RESTMapper != nil {
			opts.ResourceForGVK = mapperBackedResolver(opts.RESTMapper)
		} else {
			opts.ResourceForGVK = defaultResourceForGVK
		}
	}

	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("audit.New: dynamic client: %w", err)
	}
	kube, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("audit.New: kube client: %w", err)
	}

	c := &Controller{
		cfg:            cfg,
		dyn:            dyn,
		kube:           kube,
		gvks:           gvks,
		opts:           opts,
		engine:         engine,
		dispatcher:     dispatcher,
		sinks:          sinks,
		activeByObject: map[string]map[string]api.Violation{},
		informers:      map[schema.GroupVersionKind]cache.SharedIndexInformer{},
		listers:    map[schema.GroupVersionKind]cache.GenericLister{},
		resForGVK:  opts.ResourceForGVK,
		queue: workqueue.NewTypedRateLimitingQueue(
			workqueue.DefaultTypedControllerRateLimiter[workItem](),
		),
	}

	c.factory = dynamicinformer.NewDynamicSharedInformerFactory(dyn, opts.ResyncPeriod)
	for _, gvk := range gvks {
		gvr := c.resForGVK(gvk)
		gi := c.factory.ForResource(gvr)
		inf := gi.Informer()
		_ = inf.SetWatchErrorHandlerWithContext(func(_ context.Context, _ *cache.Reflector, err error) {
			if err != nil {
				prommetrics.AuditWatchReconnectsTotal.Inc()
			}
		})
		// Capture gvk into the closure.
		captured := gvk
		_, err := inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    func(obj any) { c.enqueue(captured, obj, "add") },
			UpdateFunc: func(_, obj any) { c.enqueue(captured, obj, "update") },
			DeleteFunc: func(obj any) { c.enqueue(captured, obj, "delete") },
		})
		if err != nil {
			return nil, fmt.Errorf("audit.New: AddEventHandler %s: %w", gvk, err)
		}
		c.informers[gvk] = inf
		c.listers[gvk] = gi.Lister()
	}

	return c, nil
}

// Name implements api.EventSource.
func (c *Controller) Name() string { return "audit" }

// SharedInformerFactory exposes the underlying factory for the lookup and
// network modules to reuse.
func (c *Controller) SharedInformerFactory() dynamicinformer.DynamicSharedInformerFactory {
	return c.factory
}

// Lister returns the GenericLister for gvk, or an error if not watched.
func (c *Controller) Lister(gvk schema.GroupVersionKind) (cache.GenericLister, error) {
	l, ok := c.listers[gvk]
	if !ok {
		return nil, fmt.Errorf("audit: GVK %s not watched", gvk)
	}
	return l, nil
}

// WatchedGVKs returns every GVK this controller has an informer for. Order is
// unspecified. Used by the lookup module to advertise cluster.<gvk>.* helpers.
func (c *Controller) WatchedGVKs() []schema.GroupVersionKind {
	out := make([]schema.GroupVersionKind, 0, len(c.listers))
	for gvk := range c.listers {
		out = append(out, gvk)
	}
	return out
}

// RESTMapper returns the discovery-backed Kind→Resource resolver wired into
// this controller (or nil when none was supplied). Exported so the lookup,
// network, and action modules can reuse the same mapper instead of
// reinventing pluralisation.
func (c *Controller) RESTMapper() meta.RESTMapper {
	return c.opts.RESTMapper
}

// Start implements api.EventSource. It is blocking — the underlying
// goroutines (informer factory, leader election, worker pool) all run for
// the lifetime of ctx. onEvent may be nil; when non-nil it is invoked for
// every (Context, EventMeta) pair the workers process.
func (c *Controller) Start(ctx context.Context, onEvent func(api.Context, api.EventMeta)) error {
	c.mu.Lock()
	if c.stopCh != nil {
		c.mu.Unlock()
		return errors.New("audit.Start: already running")
	}
	c.stopCh = make(chan struct{})
	c.stoppedCh = make(chan struct{})
	stopCh := c.stopCh
	stoppedCh := c.stoppedCh
	c.mu.Unlock()

	defer close(stoppedCh)

	// Spin up informers.
	c.factory.Start(stopCh)
	c.factory.WaitForCacheSync(stopCh)

	// Workers gate on isLeader before dispatching; if LeaderElection is
	// disabled flip it immediately to true so workers actually do work.
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()

	if !c.opts.LeaderElection {
		c.isLeader.Store(true)
	}

	// Start the worker pool unconditionally; gating happens inside the loop.
	var wg sync.WaitGroup
	for i := 0; i < c.opts.WorkerPoolSize; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.runWorker(workerCtx, onEvent)
		}()
	}

	// Optionally run leader election.
	leaderDone := make(chan struct{})
	if c.opts.LeaderElection {
		go func() {
			defer close(leaderDone)
			c.runLeaderElection(workerCtx)
		}()
	} else {
		close(leaderDone)
	}

	// Block until ctx cancellation or stop signal.
	select {
	case <-ctx.Done():
	case <-stopCh:
	}

	c.queue.ShutDown()
	workerCancel()
	wg.Wait()
	<-leaderDone
	return nil
}

// Stop implements api.EventSource. Cancels Start's ctx-equivalent stop
// channel and waits for the goroutines to drain.
func (c *Controller) Stop(ctx context.Context) error {
	c.mu.Lock()
	stopCh := c.stopCh
	stoppedCh := c.stoppedCh
	c.stopCh = nil
	c.mu.Unlock()
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

// enqueue extracts metadata from the informer-supplied object and adds a
// workItem to the queue. Tombstones (cache.DeletedFinalStateUnknown) are
// resolved before extraction.
func (c *Controller) enqueue(gvk schema.GroupVersionKind, obj any, evType string) {
	if t, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = t.Obj
	}
	accessor, ok := obj.(metav1.Object)
	if !ok {
		return
	}
	c.queue.Add(workItem{
		GVK:       gvk,
		Namespace: accessor.GetNamespace(),
		Name:      accessor.GetName(),
		EventType: evType,
	})
}

func (c *Controller) runWorker(ctx context.Context, onEvent func(api.Context, api.EventMeta)) {
	for {
		item, shutdown := c.queue.Get()
		if shutdown {
			return
		}
		// Workers gate on isLeader before doing anything that mutates state.
		// We still drain queue items to avoid unbounded growth; non-leaders
		// just discard.
		if !c.isLeader.Load() {
			c.queue.Done(item)
			continue
		}
		c.processItem(ctx, item, onEvent)
		c.queue.Done(item)
	}
}

func (c *Controller) processItem(ctx context.Context, w workItem, onEvent func(api.Context, api.EventMeta)) {
	now := time.Now()
	meta := api.EventMeta{
		Source:    "audit",
		EventID:   fmt.Sprintf("%s/%s/%s/%d", w.GVK, w.Namespace, w.Name, now.UnixNano()),
		At:        now,
		Operation: w.EventType,
	}

	if w.EventType == "delete" {
		c.emitGCViolation(ctx, w, meta)
		c.activeMu.Lock()
		delete(c.activeByObject, fmt.Sprintf("%s|%s/%s", w.GVK, w.Namespace, w.Name))
		c.activeMu.Unlock()
		return
	}

	// Look the live object up from the lister to avoid races between
	// enqueue and processing.
	lister, ok := c.listers[w.GVK]
	if !ok {
		return
	}
	var (
		obj runtime.Object
		err error
	)
	if w.Namespace == "" {
		obj, err = lister.Get(w.Name)
	} else {
		obj, err = lister.ByNamespace(w.Namespace).Get(w.Name)
	}
	if err != nil || obj == nil {
		return
	}
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}

	// Build context(s) via the first builder that supports the GVK; fall
	// back to a minimal generic builder if none matches.
	ctxs := c.buildContexts(u)
	objKey := fmt.Sprintf("%s|%s/%s", w.GVK, w.Namespace, w.Name)
	current := map[string]api.Violation{}
	for _, evalCtx := range ctxs {
		violations := c.engine.Evaluate(evalCtx, meta)
		// Always invoke onEvent so wire-up code can observe.
		if onEvent != nil {
			onEvent(evalCtx, meta)
		}
		for _, v := range violations {
			current[v.Rule] = v
			c.fanOut(ctx, v)
		}
	}
	// Diff against the previous active set to emit synthetic "resolved"
	// for rules that fired before but didn't this time. Sinks decide what
	// to do with the resolved emit (PolicyReport deletes the matching
	// Result; AlertManager fires a clear alert; stdout logs it).
	c.activeMu.Lock()
	prev := c.activeByObject[objKey]
	for rule, prevV := range prev {
		if _, still := current[rule]; still {
			continue
		}
		resolved := prevV
		resolved.Message = "resolved"
		resolved.At = now
		resolved.Actions = nil
		c.fanOut(ctx, resolved)
	}
	if len(current) == 0 {
		delete(c.activeByObject, objKey)
	} else {
		c.activeByObject[objKey] = current
	}
	c.activeMu.Unlock()
}

// buildContexts iterates registered ContextBuilders. Multi-container
// (pod-shaped) builders are tried first across all builders so the pod
// builder still wins even if a catch-all builder (e.g. internal/context/
// generic) appears earlier in the slice — wire.go populates this from a
// Go map, which has no guaranteed iteration order. Pod-shaped builders
// may produce multiple per-container contexts; non-pod builders produce
// exactly one. The dummy fallback at the end produces one when no
// registered builder claims the GVK.
func (c *Controller) buildContexts(u *unstructured.Unstructured) []api.Context {
	gvk := u.GroupVersionKind()
	for _, b := range c.opts.ContextBuilders {
		if !b.Supports(gvk) {
			continue
		}
		pb, ok := b.(*pod.Builder)
		if !ok {
			continue
		}
		if out, err := pb.BuildAll(u); err == nil && len(out) > 0 {
			return out
		}
	}
	for _, b := range c.opts.ContextBuilders {
		if !b.Supports(gvk) {
			continue
		}
		if ctx, err := b.Build(u); err == nil {
			return []api.Context{ctx}
		}
	}
	// Generic fallback.
	return []api.Context{{
		GVK:    gvk,
		Object: u,
		Env: map[string]any{
			"object":   u.Object,
			"metadata": metadataMap(u),
			"request":  nil,
		},
	}}
}

func metadataMap(u *unstructured.Unstructured) map[string]any {
	return map[string]any{
		"name":        u.GetName(),
		"namespace":   u.GetNamespace(),
		"labels":      stringMap(u.GetLabels()),
		"annotations": stringMap(u.GetAnnotations()),
	}
}

func stringMap(in map[string]string) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// fanOut emits a violation to every sink and routes its actions through
// the dispatcher. Sink errors are swallowed — the stdout sink would have
// logged already and a single sink failure must not abort the others.
func (c *Controller) fanOut(ctx context.Context, v api.Violation) {
	for _, s := range c.sinks {
		_ = s.Emit(ctx, v)
	}
	if c.dispatcher != nil {
		c.dispatcher.Dispatch(ctx, v)
	}
}

// emitGCViolation produces the synthetic "policyreport-gc" violation that
// tells the action dispatcher to GC PolicyReport entries for the deleted
// object. We deliberately do not call engine.Evaluate — no live object
// exists — and we deliberately do NOT call fanOut: this is a control
// message, not a finding. Sending it through the sinks would emit a stray
// AlertManager alert, increment portal_audit_violations, and cause the
// PolicyReport sink to add a stray Result that this very action is meant
// to clean up.
func (c *Controller) emitGCViolation(ctx context.Context, w workItem, meta api.EventMeta) {
	if c.dispatcher == nil {
		return
	}
	v := api.Violation{
		Rule:      "__audit_object_deleted__",
		Severity:  api.SeverityInfo,
		GVK:       w.GVK,
		Namespace: w.Namespace,
		Name:      w.Name,
		Mode:      api.ModeAudit,
		Message:   "object deleted; policyreport entries should be garbage-collected",
		At:        meta.At,
		Actions:   []api.ActionSpec{{Type: policyReportGCActionType}},
		Source:    api.ViolationSource{EventID: meta.EventID, Operation: "delete"},
	}
	c.dispatcher.Dispatch(ctx, v)
}

// runLeaderElection runs the lease loop. OnStartedLeading flips isLeader
// to true; OnStoppedLeading flips it back. The function blocks until ctx
// cancellation.
func (c *Controller) runLeaderElection(ctx context.Context) {
	if c.opts.LeaseLockNamespace == "" {
		// Misconfigured: log and refuse to lead. Workers stay idle.
		// We don't surface a hard error because Start has already returned.
		_ = wait.PollUntilContextCancel(ctx, time.Minute, false, func(context.Context) (bool, error) {
			return false, nil
		})
		return
	}

	lock, err := resourcelock.New(
		resourcelock.LeasesResourceLock,
		c.opts.LeaseLockNamespace,
		c.opts.LeaseLockName,
		c.kube.CoreV1(),
		c.kube.CoordinationV1(),
		resourcelock.ResourceLockConfig{
			Identity: c.opts.Identity,
			// EventRecorder is optional; left nil.
		},
	)
	if err != nil {
		return
	}

	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   DefaultLeaseDuration,
		RenewDeadline:   DefaultRenewDeadline,
		RetryPeriod:     DefaultRetryPeriod,
		Name:            "portal-audit",
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(_ context.Context) {
				c.isLeader.Store(true)
			},
			OnStoppedLeading: func() {
				c.isLeader.Store(false)
			},
		},
	})
}

// defaultResourceForGVK is the dumb fallback that lowercases Kind and
// appends 's'. It is wrong for irregular plurals (NetworkPolicy,
// Endpoints, Ingress, ...) and for any CRD whose .spec.names.plural is
// not derivable from the Kind. Production wire-up supplies a
// RESTMapper-backed override via Options.RESTMapper; tests fall through
// here when no mapper is provided.
func defaultResourceForGVK(gvk schema.GroupVersionKind) schema.GroupVersionResource {
	r := strings.ToLower(gvk.Kind)
	if !strings.HasSuffix(r, "s") {
		r += "s"
	}
	return schema.GroupVersionResource{Group: gvk.Group, Version: gvk.Version, Resource: r}
}

// mapperBackedResolver returns a GVK→GVR function that consults a
// meta.RESTMapper first and falls through to defaultResourceForGVK when
// the mapper has no entry (transient discovery cache miss or unknown
// kind). The fallthrough keeps the informer factory able to make
// progress on already-loaded GVKs while a CRD lands.
func mapperBackedResolver(m meta.RESTMapper) func(schema.GroupVersionKind) schema.GroupVersionResource {
	return func(gvk schema.GroupVersionKind) schema.GroupVersionResource {
		mapping, err := m.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return defaultResourceForGVK(gvk)
		}
		return mapping.Resource
	}
}

// Compile-time: Controller implements api.EventSource.
var _ api.EventSource = (*Controller)(nil)

