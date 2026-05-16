package prometheus

import (
	"net/http"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ready is an atomic flag flipped by SetReady. Default is true (ready); the
// admission/audit composition root flips it false during graceful shutdown
// or sustained internal error so /readyz starts returning 503 and the pod
// is pulled from the Service endpoints.
var ready atomic.Bool

func init() {
	ready.Store(true)
}

// SetReady flips the package-level readiness flag. Used by composition-root
// shutdown and by failsafe code paths (panic in last N requests, rule index
// not loaded, etc.).
func SetReady(r bool) { ready.Store(r) }

// Handler returns an http.Handler that serves /metrics, /healthz, /readyz.
// Exposed so tests and the composition root can mount it independently of
// ListenAndServe.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if ready.Load() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready"))
	})
	return mux
}

// ListenAndServe starts an HTTP listener on addr serving /metrics, /healthz
// and /readyz. Intended for tests and simple deployments; the composition
// root may instead mount Handler() onto its own server.
func ListenAndServe(addr string) error {
	srv := &http.Server{Addr: addr, Handler: Handler()}
	return srv.ListenAndServe()
}
