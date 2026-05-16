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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"

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
	if opts.rulesCR {
		log.Info("--rules-cr is honoured by the audit controller's reconciler; ensure --audit is enabled to ingest CRs")
	}

	// 6. Expression engine + rule engine.
	expr := exprlang.New()
	ruleEng, err := ruleengine.New(idx, expr)
	if err != nil {
		return fmt.Errorf("rule engine: %w", err)
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
