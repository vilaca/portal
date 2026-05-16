// Package alertmanager implements the AlertManager v2 alerts client behind
// api.OutputSink. The JSON shape MUST match podwatcher-poc's exactly — that
// regression vector is what lets existing Prometheus routes keep working
// when teams upgrade to Portal. testdata/expected_alert.json is the golden
// file enforcing byte-for-byte parity.
//
// The constructor takes a Config (URL, basic-auth, TLS, retry budget) so the
// composition root can wire it from Helm values at startup. At init() we
// register only a "configured-later" placeholder factory — the real factory
// is wired by cmd/portal/wire.go in Wave 3.
package alertmanager

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/vilaca/portal/internal/api"
)

func init() {
	// Placeholder registration: the real sink needs runtime Config (URL,
	// auth, TLS) that isn't available at init(). cmd/portal/wire.go will
	// overwrite this with api.RegisterSink("alertmanager", ...) once Config
	// is loaded from flags/Helm. Using a placeholder keeps the registry
	// surface consistent — callers see "alertmanager" as a registered name
	// even before Configure() runs — while making the no-op behaviour
	// obvious if someone forgets to wire it.
	api.RegisterSink("alertmanager", func() api.OutputSink { return &placeholder{} })
}

// Configure replaces the registered "alertmanager" factory with a real,
// configured client. cmd/portal/wire.go calls this once it has parsed flags
// and Helm values. Choosing this over a global-mutable Config keeps the
// no-op safe (Emit returns ErrNotConfigured) and the configured sink fully
// immutable after construction.
func Configure(cfg Config) {
	api.RegisterSink("alertmanager", func() api.OutputSink { return New(cfg) })
}

// ErrNotConfigured is returned by Emit on the placeholder sink. It signals a
// wiring bug — the binary started without calling Configure().
var ErrNotConfigured = errors.New("alertmanager sink not configured")

type placeholder struct{}

func (placeholder) Name() string                              { return "alertmanager" }
func (placeholder) Emit(_ context.Context, _ api.Violation) error { return ErrNotConfigured }
func (placeholder) Close() error                              { return nil }

// BasicAuth is optional HTTP basic auth for AlertManager.
type BasicAuth struct {
	User string
	Pass string
}

// Config configures the AlertManager client. Defaults match podwatcher-poc.
type Config struct {
	URL            string
	BasicAuth      *BasicAuth
	TLS            *tls.Config
	Timeout        time.Duration
	RetryAttempts  int
	RetryBackoff   time.Duration
}

// sink is the configured client.
type sink struct {
	cfg    Config
	client *http.Client
}

// New constructs an AlertManager sink. Caller-supplied zero values fall back
// to safe defaults (5s timeout, 3 attempts, 200ms initial backoff doubling).
func New(cfg Config) api.OutputSink {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.RetryAttempts <= 0 {
		cfg.RetryAttempts = 3
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = 200 * time.Millisecond
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.TLS != nil {
		tr.TLSClientConfig = cfg.TLS
	}
	return &sink{
		cfg: cfg,
		client: &http.Client{
			Transport: tr,
			Timeout:   cfg.Timeout,
		},
	}
}

// Name implements api.OutputSink.
func (s *sink) Name() string { return "alertmanager" }

// Close implements api.OutputSink. No persistent state to flush.
func (s *sink) Close() error { return nil }

// Emit posts a single AlertManager v2 alert. Retries 5xx and transport errors
// up to RetryAttempts with exponential backoff. 4xx responses are not retried
// — they indicate a malformed request and re-sending won't help.
func (s *sink) Emit(ctx context.Context, v api.Violation) error {
	body, err := json.Marshal([]alert{buildAlert(v)})
	if err != nil {
		return fmt.Errorf("marshal alert: %w", err)
	}

	backoff := s.cfg.RetryBackoff
	var lastErr error
	for attempt := 0; attempt < s.cfg.RetryAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
		err := s.post(ctx, body)
		if err == nil {
			return nil
		}
		lastErr = err
		// 4xx: don't retry.
		var hErr *httpStatusError
		if errors.As(err, &hErr) && hErr.code >= 400 && hErr.code < 500 {
			return err
		}
	}
	return fmt.Errorf("alertmanager: gave up after %d attempts: %w", s.cfg.RetryAttempts, lastErr)
}

func (s *sink) post(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.cfg.BasicAuth != nil {
		req.SetBasicAuth(s.cfg.BasicAuth.User, s.cfg.BasicAuth.Pass)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return &httpStatusError{code: resp.StatusCode}
}

type httpStatusError struct{ code int }

func (e *httpStatusError) Error() string { return fmt.Sprintf("alertmanager: status %d", e.code) }

// alert is the JSON shape AlertManager v2 expects, locked to podwatcher-poc's
// exact field set. Do not add fields without updating testdata and
// docs/migration.
type alert struct {
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     string            `json:"startsAt"`
	EndsAt       string            `json:"endsAt,omitempty"`
	GeneratorURL string            `json:"generatorURL"`
}

func buildAlert(v api.Violation) alert {
	labels := map[string]string{
		"alertname": v.Rule,
		"severity":  string(v.Severity),
		"namespace": v.Namespace,
		"kind":      v.GVK.Kind,
		"name":      v.Name,
		"mode":      string(v.Mode),
		"rule":      v.Rule,
	}
	ann := map[string]string{
		"description": v.Message,
		"summary":     v.Rule,
		"source":      v.Source.EventID,
	}
	if v.Source.Container != "" {
		ann["container"] = v.Source.Container
	}
	if v.Source.Operation != "" {
		ann["operation"] = v.Source.Operation
	}
	// Optional runbook_url from any alertmanager-typed action's params.
	for _, a := range v.Actions {
		if a.Type != "alertmanager" {
			continue
		}
		if rb, ok := a.Params["runbook_url"].(string); ok && rb != "" {
			ann["runbook_url"] = rb
			break
		}
	}
	return alert{
		Labels:       labels,
		Annotations:  ann,
		StartsAt:     v.At.UTC().Format(time.RFC3339Nano),
		GeneratorURL: "",
	}
}
