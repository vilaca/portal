package admission

import (
	"context"
	"errors"
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
