package admission

import (
	"context"
	"crypto/tls"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vilaca/portal/internal/api"
)

func TestNewRejectsNilEngine(t *testing.T) {
	if _, err := New(nil, nil, nil, Options{}); err == nil {
		t.Fatalf("expected error for nil engine")
	}
}

func TestNewDefaultsListenAndBypass(t *testing.T) {
	src, err := New(&stubEngine{}, nil, nil, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s := src.(*server)
	if s.opts.Listen != DefaultListen {
		t.Errorf("expected default Listen %q, got %q", DefaultListen, s.opts.Listen)
	}
	if s.opts.BypassAnnotation != DefaultBypassAnnotation {
		t.Errorf("expected default BypassAnnotation %q, got %q", DefaultBypassAnnotation, s.opts.BypassAnnotation)
	}
	if s.Name() != "admission" {
		t.Errorf("Name=%q", s.Name())
	}
}

func TestStartFailsOnBadCert(t *testing.T) {
	src, err := New(&stubEngine{}, nil, nil, Options{
		Listen:   "127.0.0.1:0",
		CertFile: "/nonexistent/tls.crt",
		KeyFile:  "/nonexistent/tls.key",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = src.Start(ctx, func(api.Context, api.EventMeta) {})
	if err == nil {
		t.Fatalf("expected Start to fail with missing certs")
	}
}

func TestStopWithoutStartIsNoop(t *testing.T) {
	src, _ := New(&stubEngine{}, nil, nil, Options{})
	if err := src.Stop(context.Background()); err != nil {
		t.Errorf("Stop without Start should be no-op, got %v", err)
	}
}

func TestReadinessRingFlipsOnSustainedErrors(t *testing.T) {
	ring := newErrorRing(10)
	// All-good fill: stays ready.
	for i := 0; i < 10; i++ {
		ring.record(true)
	}
	if ring.notReady.Load() {
		t.Errorf("expected ready after all-good fill")
	}
	// Now 6/10 are errors -> not ready.
	for i := 0; i < 6; i++ {
		ring.record(false)
	}
	if !ring.notReady.Load() {
		t.Errorf("expected not-ready after >50%% errors")
	}
	// Restore: push successes; eventually ready again.
	for i := 0; i < 10; i++ {
		ring.record(true)
	}
	if ring.notReady.Load() {
		t.Errorf("expected ready after recovery")
	}
}

func TestReadinessRingNoFlipBeforeFilled(t *testing.T) {
	ring := newErrorRing(10)
	// 4 errors out of 4 = 100% error ratio, but window not yet filled.
	for i := 0; i < 4; i++ {
		ring.record(false)
	}
	if ring.notReady.Load() {
		t.Errorf("readiness must not flip until window is filled")
	}
}

// Just smoke-test that a nil dispatcher and nil-sinks slice don't NPE.
func TestNilDispatcherAndSinksTolerated(t *testing.T) {
	_, err := New(&stubEngine{}, nil, nil, Options{})
	if err != nil {
		t.Fatalf("New must accept nil dispatcher and sinks: %v", err)
	}
}

// Probe that the (api.EventSource) Stop on a never-started server uses
// errors.Is on http.ErrServerClosed correctly — actually a noop here.
var _ = errors.Is // keep imports tidy if Stop's Shutdown semantics change

// writeSelfSignedBundle writes a fresh CA+leaf+key to dir and returns the
// paths. Reuses internal/admission/cert.go's generateSelfSigned so we don't
// duplicate cert assembly.
func writeSelfSignedBundle(t *testing.T, dir string) (certFile, keyFile, caFile string) {
	t.Helper()
	caPEM, leafPEM, leafKeyPEM, err := generateSelfSigned([]string{"localhost"})
	if err != nil {
		t.Fatalf("generateSelfSigned: %v", err)
	}
	certFile = filepath.Join(dir, "tls.crt")
	keyFile = filepath.Join(dir, "tls.key")
	caFile = filepath.Join(dir, "ca.crt")
	for _, w := range []struct {
		path string
		data []byte
	}{
		{certFile, leafPEM},
		{keyFile, leafKeyPEM},
		{caFile, caPEM},
	} {
		if err := os.WriteFile(w.path, w.data, 0o600); err != nil {
			t.Fatalf("write %s: %v", w.path, err)
		}
	}
	return certFile, keyFile, caFile
}

func TestLoadTLS_EmptyReturnsNil(t *testing.T) {
	cfg, err := loadTLS("", "", "")
	if err != nil {
		t.Fatalf("loadTLS: %v", err)
	}
	if cfg != nil {
		t.Errorf("expected nil config for empty inputs, got %+v", cfg)
	}
}

func TestLoadTLS_NoClientCAKeepsLegacyBehavior(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, _ := writeSelfSignedBundle(t, dir)
	cfg, err := loadTLS(certFile, keyFile, "")
	if err != nil {
		t.Fatalf("loadTLS: %v", err)
	}
	if cfg == nil {
		t.Fatalf("expected non-nil tls.Config")
	}
	if cfg.ClientAuth != tls.NoClientCert {
		t.Errorf("ClientAuth = %v, want NoClientCert (legacy behavior)", cfg.ClientAuth)
	}
	if cfg.ClientCAs != nil {
		t.Errorf("ClientCAs should be nil when no client CA configured")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %v, want TLS 1.2", cfg.MinVersion)
	}
}

func TestLoadTLS_ClientCABundleRequiresClientCert(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, caFile := writeSelfSignedBundle(t, dir)
	cfg, err := loadTLS(certFile, keyFile, caFile)
	if err != nil {
		t.Fatalf("loadTLS: %v", err)
	}
	if cfg == nil {
		t.Fatalf("expected non-nil tls.Config")
	}
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("ClientAuth = %v, want RequireAndVerifyClientCert", cfg.ClientAuth)
	}
	if cfg.ClientCAs == nil {
		t.Fatalf("ClientCAs is nil — bundle did not load")
	}
}

func TestLoadTLS_ClientCAEmptyPEMErrors(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, _ := writeSelfSignedBundle(t, dir)
	emptyCA := filepath.Join(dir, "empty-ca.crt")
	if err := os.WriteFile(emptyCA, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("write empty CA: %v", err)
	}
	if _, err := loadTLS(certFile, keyFile, emptyCA); err == nil {
		t.Fatalf("expected error for client CA file with no PEM certificates")
	}
}

func TestLoadTLS_ClientCAMissingFileErrors(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, _ := writeSelfSignedBundle(t, dir)
	if _, err := loadTLS(certFile, keyFile, filepath.Join(dir, "does-not-exist.crt")); err == nil {
		t.Fatalf("expected error for missing client CA file")
	}
}

// Smoke: New + Start with ClientCAFile populated should not error on TLS
// config assembly. We don't actually open a listener here; Start does, so we
// just verify loadTLS works against a real bundle.
func TestNewWithClientCAFile(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, caFile := writeSelfSignedBundle(t, dir)
	src, err := New(&stubEngine{}, nil, nil, Options{
		Listen:       "127.0.0.1:0",
		CertFile:     certFile,
		KeyFile:      keyFile,
		ClientCAFile: caFile,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cfg, err := loadTLS(src.(*server).opts.CertFile, src.(*server).opts.KeyFile, src.(*server).opts.ClientCAFile)
	if err != nil {
		t.Fatalf("loadTLS: %v", err)
	}
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("expected RequireAndVerifyClientCert, got %v", cfg.ClientAuth)
	}
}
