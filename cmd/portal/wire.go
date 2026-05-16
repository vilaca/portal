package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	ctrlmanager "sigs.k8s.io/controller-runtime/pkg/manager"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	alertmanagerAction "github.com/vilaca/portal/internal/actions/alertmanager_action"
	"github.com/vilaca/portal/internal/actions/annotate"
	"github.com/vilaca/portal/internal/actions/engine"
	"github.com/vilaca/portal/internal/actions/evict"
	"github.com/vilaca/portal/internal/actions/label"
	"github.com/vilaca/portal/internal/actions/patchnp"
	"github.com/vilaca/portal/internal/actions/policyreportgc"
	"github.com/vilaca/portal/internal/actions/revoketoken"
	"github.com/vilaca/portal/internal/admission"
	"github.com/vilaca/portal/internal/api"
	"github.com/vilaca/portal/internal/audit"
	_ "github.com/vilaca/portal/internal/context/generic"
	_ "github.com/vilaca/portal/internal/context/pod"
	ruleengine "github.com/vilaca/portal/internal/engine"
	"github.com/vilaca/portal/internal/expr/exprlang"
	"github.com/vilaca/portal/internal/network"
	"github.com/vilaca/portal/internal/rule"
	"github.com/vilaca/portal/internal/rule/loader"
	portalv1alpha1 "github.com/vilaca/portal/internal/rule/v1alpha1"
	"github.com/vilaca/portal/internal/sink/alertmanager"
	"github.com/vilaca/portal/internal/sink/policyreport"
	prommetrics "github.com/vilaca/portal/internal/sink/prometheus"
	"github.com/vilaca/portal/internal/sink/stdout"
)

// runPortal is the composition root. Every internal/* module is wired here.
//
// Order matters: clients first → sinks/actions configured → rule index +
// engine → dispatcher → event sources → metrics server → ctx-bounded Start.
func runPortal(parentCtx context.Context, opts runOptions) error {
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if opts.network && !opts.audit {
		log.Warn("--network implies --audit (informers required); enabling")
		opts.audit = true
	}
	// CR loading depends on the audit controller's controller-runtime
	// Manager + cache. Running with --admission only + --rules-cr would
	// silently load zero rules; fail fast so operators see the misconfig
	// instead of an inert webhook.
	if opts.rulesCR && !opts.audit {
		if opts.rulesFolder == "" {
			return fmt.Errorf("--rules-cr requires --audit (or supply --rules-folder)")
		}
		log.Warn("--rules-cr without --audit is a no-op; rules will be loaded only from --rules-folder")
		opts.rulesCR = false
	}

	// 1. Kubernetes clients.
	needsClients := opts.admission || opts.audit || opts.network || opts.rulesCR
	var (
		restCfg     *rest.Config
		dynClient   dynamic.Interface
		typedClient kubernetes.Interface
		restMapper  meta.RESTMapper
	)
	if needsClients {
		var err error
		restCfg, err = loadRestConfig(opts.kubeconfig)
		if err != nil {
			return fmt.Errorf("kubeconfig: %w", err)
		}
		// Default in-cluster config is QPS=5 / Burst=10, which starves the
		// audit informer factory, lookup-cache list calls fired from
		// expr-lang rules, and the action dispatcher whenever a rule fans
		// out. Bump both well above the steady-state ceiling.
		restCfg.QPS = 100
		restCfg.Burst = 200
		dynClient, err = dynamic.NewForConfig(restCfg)
		if err != nil {
			return fmt.Errorf("dynamic client: %w", err)
		}
		typedClient, err = kubernetes.NewForConfig(restCfg)
		if err != nil {
			return fmt.Errorf("typed client: %w", err)
		}
		// Discovery-backed RESTMapper. Deferred so the cache populates on
		// first use; memory-cached so CRDs landing later are picked up via
		// Reset(). One mapper instance is shared across audit (informer
		// factory), network (analyser handlers), and the label/annotate
		// actions — see internal/audit, internal/network, internal/actions/*.
		disc, err := discovery.NewDiscoveryClientForConfig(restCfg)
		if err != nil {
			return fmt.Errorf("discovery client: %w", err)
		}
		restMapper = restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(disc))
	}

	// 2. Configure action clients (placeholder → real factories). label and
	// annotate take the RESTMapper so they handle irregular plurals + CRDs;
	// patchnp pins networkpolicies directly, evict/revoketoken use the
	// typed client.
	if needsClients {
		label.Configure(dynClient, restMapper)
		annotate.Configure(dynClient, restMapper)
		patchnp.Configure(dynClient)
		evict.Configure(typedClient)
		revoketoken.Configure(typedClient)
		policyreportgc.Configure(dynClient)
	}

	// 3. Configure remote sinks.
	if opts.alertmanagerURL != "" {
		alertmanager.Configure(alertmanager.Config{
			URL:           opts.alertmanagerURL,
			Timeout:       5 * time.Second,
			RetryAttempts: 3,
			RetryBackoff:  200 * time.Millisecond,
		})
		// Back the alertmanager-typed action with the same sink. Without
		// this, rules using `alert:` shorthand or an explicit alertmanager
		// action have the dispatcher log "unknown action type" — the sink
		// fires from the audit fan-out, but the action surface stays
		// unwired.
		if s := api.Sinks()["alertmanager"]; s != nil {
			alertmanagerAction.Configure(s())
		}
	}
	if dynClient != nil {
		policyreport.Configure(dynClient)
	}

	// 4. Build the sinks slice from the registry.
	sinks := materialiseSinks(opts)

	// 5. Rule index + loaders.
	idx := rule.NewIndex()
	loaderCtx, cancelLoaders := context.WithCancel(parentCtx)
	defer cancelLoaders()
	if opts.rulesFolder != "" {
		folderLoader := loader.NewFolder(opts.rulesFolder)
		if err := folderLoader.Start(loaderCtx, func(snap []api.Rule) { idx.Replace(snap) }); err != nil {
			return fmt.Errorf("folder loader: %w", err)
		}
	}
	// (CR loader is wired below, after the rule engine exists — the
	// reconciler needs the engine's ParseError accessor.)

	// 6. Expression engine + rule engine.
	expr := exprlang.New()
	ruleEng, err := ruleengine.New(idx, expr)
	if err != nil {
		return fmt.Errorf("rule engine: %w", err)
	}

	// 6a. CR loader — builds a controller-runtime Manager that reconciles
	// PortalClusterRule / PortalRule and pushes each snapshot into the
	// shared rule index. The same Manager also runs the status reconciler
	// that writes .status.parseError + .status.activeOn. Disabled when
	// --rules-cr is false (rules.cr Helm value).
	var crManager ctrlmanager.Manager
	if opts.rulesCR {
		parseSrc, ok := ruleEng.(portalv1alpha1.ParseErrorSource)
		if !ok {
			return errors.New("rule engine does not expose ParseError(name) — wire-up cannot reach the status reconciler")
		}
		scheme := runtime.NewScheme()
		if err := clientgoscheme.AddToScheme(scheme); err != nil {
			return fmt.Errorf("clientgo scheme: %w", err)
		}
		if err := portalv1alpha1.AddToScheme(scheme); err != nil {
			return fmt.Errorf("portal scheme: %w", err)
		}
		mgr, err := ctrlmanager.New(restCfg, ctrlmanager.Options{
			Scheme: scheme,
			// Disable the manager's own metrics endpoint — Portal serves
			// metrics from prommetrics on opts.metricsAddr.
			Metrics: ctrlmetrics.Options{BindAddress: "0"},
			// No leader election here; the audit controller owns that
			// concern. CR reconciliation is idempotent across replicas.
			LeaderElection: false,
		})
		if err != nil {
			return fmt.Errorf("controller-runtime manager: %w", err)
		}
		rr := audit.NewRuleReconciler(mgr.GetClient(), idx, parseSrc)
		if err := rr.SetupWithManager(mgr); err != nil {
			return fmt.Errorf("rule reconciler: %w", err)
		}
		// Note: the v1alpha1.SetupWithManager status reconciler is
		// deliberately NOT registered here. It would race the audit
		// reconciler — both write status, and v1alpha1's write doesn't
		// imply the index update has happened. Consumers (the e2e test,
		// `kubectl get portalclusterrule`) use status.lastApplied as the
		// "rule is live in the engine" signal, so the audit reconciler is
		// the sole writer.
		_ = portalv1alpha1.SetupWithManager // keep the symbol referenced
		crManager = mgr
	}

	// 7. Action dispatcher.
	actMap := map[string]api.Action{
		"label":               api.ActionFor("label"),
		"annotate":            api.ActionFor("annotate"),
		"patch-networkpolicy": api.ActionFor("patch-networkpolicy"),
		"evict":               api.ActionFor("evict"),
		"revoketoken":         api.ActionFor("revoketoken"),
		"alertmanager":        api.ActionFor("alertmanager"),
		"policyreport-gc":     api.ActionFor("policyreport-gc"),
	}
	for k, v := range actMap {
		if v == nil {
			delete(actMap, k)
		}
	}
	limiter := engine.NewLimiter()
	idem := engine.NewLRU(0)
	dispatcher := engine.New(actMap, limiter, idem, engine.Options{})

	// 8. Context builders snapshot.
	builders := make([]api.ContextBuilder, 0, len(api.ContextBuilders()))
	for _, ctor := range api.ContextBuilders() {
		builders = append(builders, ctor())
	}

	// 9. Event sources.
	var sources []api.EventSource
	if opts.admission {
		certFile, keyFile, err := admission.LoadOrGenerate(opts.certDir, opts.webhookDNSNames)
		if err != nil {
			return fmt.Errorf("admission certs: %w", err)
		}
		src, err := admission.New(ruleEng, dispatcher, sinks, admission.Options{
			Listen:           opts.listen,
			CertFile:         certFile,
			KeyFile:          keyFile,
			FailClosed:       opts.failClosed,
			ContextBuilders:  builders,
			InstallNamespace: opts.installNamespace,
		})
		if err != nil {
			return fmt.Errorf("admission: %w", err)
		}
		sources = append(sources, src)
	}

	var auditCtrl *audit.Controller
	if opts.audit {
		gvks := defaultAuditGVKs()
		gvks = append(gvks, parseGVKs(opts.watchedGvks)...)
		src, err := audit.New(restCfg, gvks, ruleEng, dispatcher, sinks, audit.Options{
			LeaderElection:     opts.leaderElection,
			LeaseLockNamespace: opts.installNamespace,
			ContextBuilders:    builders,
			RESTMapper:         restMapper,
		})
		if err != nil {
			return fmt.Errorf("audit: %w", err)
		}
		auditCtrl, _ = src.(*audit.Controller)
		sources = append(sources, src)
	}

	if opts.network {
		if auditCtrl == nil {
			return errors.New("network analyser requires --audit")
		}
		src, err := network.New(auditCtrl, dispatcher, sinks, network.Options{})
		if err != nil {
			return fmt.Errorf("network: %w", err)
		}
		sources = append(sources, src)
	}

	// 10. Metrics + health server.
	metricsSrv := &http.Server{Addr: opts.metricsAddr, Handler: prommetrics.Handler(), ReadHeaderTimeout: 5 * time.Second}

	// 11. errgroup-bounded lifecycle.
	rootCtx, cancel := signal.NotifyContext(parentCtx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	g, gctx := errgroup.WithContext(rootCtx)

	g.Go(func() error {
		log.Info("metrics listening", "addr", opts.metricsAddr)
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("metrics server: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		<-gctx.Done()
		shutdownCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		return metricsSrv.Shutdown(shutdownCtx)
	})

	for _, s := range sources {
		s := s
		g.Go(func() error {
			log.Info("event source starting", "name", s.Name())
			if err := s.Start(gctx, nil); err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("%s: %w", s.Name(), err)
			}
			return nil
		})
		g.Go(func() error {
			<-gctx.Done()
			shutdownCtx, c := context.WithTimeout(context.Background(), 10*time.Second)
			defer c()
			return s.Stop(shutdownCtx)
		})
	}

	if crManager != nil {
		g.Go(func() error {
			log.Info("controller-runtime manager starting", "for", "PortalClusterRule/PortalRule")
			if err := crManager.Start(gctx); err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("cr manager: %w", err)
			}
			return nil
		})
	}

	g.Go(func() error {
		<-gctx.Done()
		drainCtx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		return dispatcher.Drain(drainCtx)
	})

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	log.Info("portal shut down cleanly")
	return nil
}

// loadRestConfig resolves a *rest.Config from --kubeconfig, $KUBECONFIG, or the
// in-cluster ServiceAccount.
func loadRestConfig(explicit string) (*rest.Config, error) {
	if explicit != "" {
		return clientcmd.BuildConfigFromFlags("", explicit)
	}
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
}

// materialiseSinks returns one OutputSink per registered factory, skipping
// remote sinks whose Configure was never called.
func materialiseSinks(opts runOptions) []api.OutputSink {
	out := []api.OutputSink{stdout.New(), prommetrics.New()}
	if opts.alertmanagerURL != "" {
		if s := api.Sinks()["alertmanager"]; s != nil {
			out = append(out, s())
		}
	}
	if opts.policyReport {
		if s := api.Sinks()["policyreport"]; s != nil {
			out = append(out, s())
		}
	}
	return out
}

// defaultAuditGVKs is the v1 default set of resources the audit loop watches
// when no --watched-gvk overrides are provided. Covers the surface that the
// migrated podwatcher-poc rule corpus needs plus the NetworkPolicy analyser's
// inputs.
func defaultAuditGVKs() []schema.GroupVersionKind {
	return []schema.GroupVersionKind{
		{Group: "", Version: "v1", Kind: "Pod"},
		{Group: "", Version: "v1", Kind: "Namespace"},
		{Group: "apps", Version: "v1", Kind: "Deployment"},
		{Group: "apps", Version: "v1", Kind: "StatefulSet"},
		{Group: "apps", Version: "v1", Kind: "DaemonSet"},
		{Group: "batch", Version: "v1", Kind: "Job"},
		{Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy"},
		{Group: "policy", Version: "v1", Kind: "PodDisruptionBudget"},
	}
}

// parseGVKs parses repeated --watched-gvk strings of form "group/version/kind"
// (empty group for core, e.g. "/v1/ConfigMap"). Invalid entries are skipped
// with a warning to stderr.
func parseGVKs(entries []string) []schema.GroupVersionKind {
	var out []schema.GroupVersionKind
	for _, e := range entries {
		parts := strings.SplitN(e, "/", 3)
		if len(parts) != 3 {
			fmt.Fprintf(os.Stderr, "portal: ignoring malformed --watched-gvk %q (want group/version/kind)\n", e)
			continue
		}
		out = append(out, schema.GroupVersionKind{Group: parts[0], Version: parts[1], Kind: parts[2]})
	}
	return out
}
