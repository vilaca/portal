package alertmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vilaca/portal/internal/api"
)

func fixtureViolation() api.Violation {
	return api.Violation{
		Rule:     "privileged-container",
		Severity: api.SeverityCritical,
		GVK:      schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
		Namespace: "production",
		Name:      "api-server",
		Mode:      api.ModeAudit,
		Message:   "container is privileged",
		At:        time.Date(2025, 1, 2, 3, 4, 5, 6, time.UTC),
		Source: api.ViolationSource{
			EventID:   "evt-42",
			Operation: "UPDATE",
			Container: "main",
		},
		Actions: []api.ActionSpec{{
			Type:   "alertmanager",
			Params: map[string]any{"runbook_url": "https://runbooks.example/privileged"},
		}},
	}
}

// canonicalise re-encodes JSON with sorted keys + indentation so the test can
// compare bytes against the golden file regardless of map ordering.
func canonicalise(t *testing.T, raw []byte) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("canonicalise unmarshal: %v\nraw=%s", err, raw)
	}
	// Re-marshal: encoding/json sorts map keys for map[string]X already, but
	// arrays of maps need a normalised pass. Indent for byte-equality with
	// the testdata file.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatalf("canonicalise marshal: %v", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func TestEmitMatchesGoldenJSON(t *testing.T) {
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := New(Config{URL: srv.URL})
	if err := s.Emit(context.Background(), fixtureViolation()); err != nil {
		t.Fatalf("emit: %v", err)
	}

	golden, err := os.ReadFile(filepath.Join("testdata", "expected_alert.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	gotC := canonicalise(t, captured)
	wantC := canonicalise(t, golden)
	if !bytes.Equal(gotC, wantC) {
		t.Errorf("AlertManager JSON drift.\n--- got ---\n%s\n--- want ---\n%s", gotC, wantC)
	}
}

func TestRetryOn5xxThenSuccess(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := New(Config{URL: srv.URL, RetryAttempts: 5, RetryBackoff: time.Millisecond})
	if err := s.Emit(context.Background(), fixtureViolation()); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got := n.Load(); got != 3 {
		t.Errorf("expected 3 attempts (2 fail + 1 success), got %d", got)
	}
}

func TestNoRetryOn4xx(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	s := New(Config{URL: srv.URL, RetryAttempts: 5, RetryBackoff: time.Millisecond})
	err := s.Emit(context.Background(), fixtureViolation())
	if err == nil {
		t.Fatalf("expected error on 400")
	}
	if got := n.Load(); got != 1 {
		t.Errorf("expected exactly 1 attempt on 4xx, got %d", got)
	}
}

func TestPlaceholderUntilConfigured(t *testing.T) {
	// At init() a placeholder is registered. It returns ErrNotConfigured.
	sinks := api.Sinks()
	ctor, ok := sinks["alertmanager"]
	if !ok {
		t.Fatalf("alertmanager not registered at init")
	}
	s := ctor()
	err := s.Emit(context.Background(), fixtureViolation())
	if err == nil {
		t.Fatalf("expected ErrNotConfigured from placeholder")
	}
}

func TestConfigureReplacesFactory(t *testing.T) {
	t.Cleanup(func() {
		// restore placeholder for subsequent tests / package consumers
		api.RegisterSink("alertmanager", func() api.OutputSink { return &placeholder{} })
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	Configure(Config{URL: srv.URL})
	ctor := api.Sinks()["alertmanager"]
	s := ctor()
	if err := s.Emit(context.Background(), fixtureViolation()); err != nil {
		t.Fatalf("configured sink should succeed: %v", err)
	}
}

func TestBasicAuthSent(t *testing.T) {
	var gotUser, gotPass string
	var ok bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, ok = r.BasicAuth()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	s := New(Config{URL: srv.URL, BasicAuth: &BasicAuth{User: "u", Pass: "p"}})
	if err := s.Emit(context.Background(), fixtureViolation()); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !ok || gotUser != "u" || gotPass != "p" {
		t.Errorf("basic auth not sent: user=%q pass=%q ok=%v", gotUser, gotPass, ok)
	}
}

func TestName(t *testing.T) {
	s := New(Config{URL: "http://example"})
	if s.Name() != "alertmanager" {
		t.Errorf("Name() = %q", s.Name())
	}
}
