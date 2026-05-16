// Package admission implements the api.EventSource for Kubernetes admission
// webhooks. It receives AdmissionReview v1 requests over HTTPS, builds api.Contexts
// via registered ContextBuilders, evaluates rules through a RuleEngine, dispatches
// resulting Violations to sinks and the ActionDispatcher, and renders an
// AdmissionReview response with allowed/warnings/status.message populated per the
// aggregation rules in /docs/PLAN.md (Phase 2).
//
// Fail-closed posture: the actual ValidatingWebhookConfiguration.failurePolicy
// lives in the Helm chart; this package does not gate behaviour on FailClosed
// beyond surfacing it via Options. System-namespace exclusion and bypass
// short-circuits are enforced here unconditionally.
//
// The handler is deliberately a direct net/http implementation rather than the
// controller-runtime webhook package; the wire shape (AdmissionReview JSON
// encoding, response.uid copied from request.uid) matches what kube-apiserver
// expects, and the lighter surface area is easier to test.
package admission

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/vilaca/portal/internal/api"
)

// DefaultListen is the default :PORT the admission webhook listens on.
const DefaultListen = ":8443"

// DefaultBypassAnnotation is the namespace annotation that short-circuits the
// webhook to allowed=true.
const DefaultBypassAnnotation = "portal.io/bypass"

// DefaultExcludedNamespaces enumerates the namespaces that must never be
// gated by the webhook. The wire-up layer should extend (not replace) this
// list with Portal's own install namespace.
var DefaultExcludedNamespaces = []string{
	"kube-system",
	"kube-public",
	"kube-node-lease",
}

// NamespaceListerFunc returns the labels and annotations for a namespace.
// Wire-up passes an informer-backed implementation; tests can pass nil (then
// the bypass annotation can't fire) or a stub.
type NamespaceListerFunc func(name string) (labels, annotations map[string]string, err error)

// Options configures the admission webhook server.
type Options struct {
	// Listen is the bind address. Defaults to ":8443".
	Listen string

	// CertFile and KeyFile are the TLS material served on Listen.
	CertFile, KeyFile string

	// FailClosed is advisory only — the real failurePolicy lives in the Helm
	// chart. Kept here so wire-up can log a warning if the chart and process
	// posture diverge.
	FailClosed bool

	// BypassAnnotation is the namespace annotation that short-circuits to
	// allowed=true. Defaults to portal.io/bypass.
	BypassAnnotation string

	// ContextBuilders is the ordered list of builders queried per request.
	// The first whose Supports(gvk)==true wins.
	ContextBuilders []api.ContextBuilder

	// NamespaceLister resolves a namespace's labels and annotations. May be
	// nil in tests; then bypass annotation can't fire.
	NamespaceLister NamespaceListerFunc

	// ExcludedNamespaces are namespaces that bypass the rule engine entirely.
	// Empty falls back to DefaultExcludedNamespaces. System-namespace exclusion
	// is mandatory — callers cannot opt out by passing an empty list.
	ExcludedNamespaces []string

	// InstallNamespace is Portal's own install namespace, automatically appended
	// to ExcludedNamespaces. Empty means no automatic addition.
	InstallNamespace string
}

// New constructs an api.EventSource that runs the admission webhook.
func New(engine api.RuleEngine, dispatcher api.ActionDispatcher, sinks []api.OutputSink, opts Options) (api.EventSource, error) {
	if engine == nil {
		return nil, errors.New("admission.New: nil RuleEngine")
	}
	// dispatcher and sinks may legitimately be nil/empty in degenerate
	// configurations; we still want to render decisions.

	if opts.Listen == "" {
		opts.Listen = DefaultListen
	}
	if opts.BypassAnnotation == "" {
		opts.BypassAnnotation = DefaultBypassAnnotation
	}

	// System-namespace exclusion is mandatory. Empty list = use defaults.
	ex := opts.ExcludedNamespaces
	if len(ex) == 0 {
		ex = append(ex, DefaultExcludedNamespaces...)
	}
	if opts.InstallNamespace != "" {
		ex = append(ex, opts.InstallNamespace)
	}
	excludedSet := map[string]struct{}{}
	for _, n := range ex {
		excludedSet[n] = struct{}{}
	}

	h := &handler{
		engine:           engine,
		dispatcher:       dispatcher,
		sinks:            sinks,
		builders:         opts.ContextBuilders,
		bypassAnnotation: opts.BypassAnnotation,
		excluded:         excludedSet,
		nsLister:         opts.NamespaceLister,
		errorBuffer:      newErrorRing(100),
	}

	s := &server{
		opts:    opts,
		handler: h,
	}
	return s, nil
}

// server implements api.EventSource.
type server struct {
	opts    Options
	handler *handler

	mu      sync.Mutex
	httpSrv *http.Server
}

// Name implements api.EventSource.
func (s *server) Name() string { return "admission" }

// Start implements api.EventSource. It blocks until ctx is cancelled or the
// HTTP server returns a fatal error. The onEvent callback is currently
// unused — admission emits via sinks/dispatcher directly and renders a
// synchronous AdmissionReview response; it is kept on the interface for
// audit/network parity.
func (s *server) Start(ctx context.Context, _ func(api.Context, api.EventMeta)) error {
	mux := http.NewServeMux()
	mux.Handle("/validate", s.handler)
	mux.Handle("/", s.handler) // permissive — kube-apiserver hits whatever path the WebhookConfiguration specifies.

	tlsCfg, err := loadTLS(s.opts.CertFile, s.opts.KeyFile)
	if err != nil {
		return fmt.Errorf("admission.Start: load TLS: %w", err)
	}

	srv := &http.Server{
		Addr:              s.opts.Listen,
		Handler:           mux,
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: 5 * time.Second,
	}

	s.mu.Lock()
	s.httpSrv = srv
	s.mu.Unlock()

	errCh := make(chan error, 1)
	go func() {
		// Empty cert/key strings cause ListenAndServeTLS to fall back to the
		// TLSConfig.Certificates we already wired up.
		errCh <- srv.ListenAndServeTLS(s.opts.CertFile, s.opts.KeyFile)
	}()

	select {
	case <-ctx.Done():
		return s.Stop(context.Background())
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Stop implements api.EventSource.
func (s *server) Stop(ctx context.Context) error {
	s.mu.Lock()
	srv := s.httpSrv
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

// loadTLS loads the leaf certificate/key into a *tls.Config. When the inputs
// are empty (tests), it returns a nil config so the caller may serve plain
// HTTP (httptest.NewServer scenarios).
func loadTLS(certFile, keyFile string) (*tls.Config, error) {
	if certFile == "" && keyFile == "" {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// Handler exposes the underlying http.Handler so tests (and the composition
// root if it wants to mount admission onto a shared mux) can use it without
// going through Start.
func (s *server) Handler() http.Handler { return s.handler }
