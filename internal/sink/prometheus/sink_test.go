package prometheus

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vilaca/portal/internal/api"
)

func TestEmitAuditViolationIncrementsCounter(t *testing.T) {
	AuditViolations.Reset()
	s := New()
	v := api.Violation{
		Rule:     "privileged-container",
		Severity: api.SeverityHigh,
		GVK:      schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
		Mode:     api.ModeAudit,
	}
	if err := s.Emit(context.Background(), v); err != nil {
		t.Fatalf("emit: %v", err)
	}
	got := testutil.ToFloat64(AuditViolations.WithLabelValues("privileged-container", "high", "/v1, Kind=Pod"))
	if got != 1 {
		t.Errorf("AuditViolations = %v, want 1", got)
	}
}

func TestEmitNetworkViolationIncrementsNPFindings(t *testing.T) {
	NPFindings.Reset()
	s := New()
	v := api.Violation{
		Rule: "np.default-deny-missing",
		Mode: api.ModeNetwork,
	}
	if err := s.Emit(context.Background(), v); err != nil {
		t.Fatalf("emit: %v", err)
	}
	got := testutil.ToFloat64(NPFindings.WithLabelValues("np.default-deny-missing"))
	if got != 1 {
		t.Errorf("NPFindings = %v, want 1", got)
	}
}

func TestAllMetricsDocumented(t *testing.T) {
	// Every metric must have a description and a documented entry.
	for _, m := range Registry() {
		if strings.TrimSpace(m.Description) == "" {
			t.Errorf("metric %q has empty description", m.Name)
		}
		if m.Type == "" {
			t.Errorf("metric %q has empty type", m.Name)
		}
	}
}

func TestMetricsdocRenderNonEmpty(t *testing.T) {
	out := renderForTest()
	if !strings.Contains(out, "portal_admission_requests_total") {
		t.Errorf("rendered doc missing portal_admission_requests_total:\n%s", out)
	}
	if !strings.Contains(out, "| Name | Type | Labels | Description |") {
		t.Errorf("rendered doc missing header row")
	}
	for _, m := range Registry() {
		if !strings.Contains(out, m.Name) {
			t.Errorf("rendered doc missing metric %q", m.Name)
		}
	}
}

// renderForTest mirrors cmd/metricsdoc's Render() so tests don't depend on
// importing package main. The cmd binary covers the user-visible behaviour;
// this checks the underlying registry is iterable and non-empty.
func renderForTest() string {
	var b strings.Builder
	b.WriteString("| Name | Type | Labels | Description |\n")
	for _, m := range Registry() {
		b.WriteString("| " + m.Name + " | " + m.Type + " | ")
		if len(m.Labels) == 0 {
			b.WriteString("—")
		} else {
			b.WriteString(strings.Join(m.Labels, ","))
		}
		b.WriteString(" | " + m.Description + " |\n")
	}
	return b.String()
}

func TestRegisteredAtInit(t *testing.T) {
	if _, ok := api.Sinks()["prometheus"]; !ok {
		t.Fatalf("prometheus sink not registered at init")
	}
}

func TestServerEndpoints(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()

	// /metrics returns 200 and a body containing one of our metrics.
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("get /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/metrics status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	// Histograms register their _count/_sum series eagerly; counter vecs only
	// appear after WithLabelValues. Touch a label so the metric surfaces.
	AdmissionRequestsTotal.WithLabelValues("allow").Add(0)
	resp2, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("get /metrics 2: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if !strings.Contains(string(body2), "portal_admission_requests_total") {
		t.Errorf("/metrics body missing portal_admission_requests_total:\n%s", body2)
	}
	if !strings.Contains(string(body), "portal_admission_latency_seconds") {
		t.Errorf("/metrics body missing portal_admission_latency_seconds")
	}

	// /healthz returns 200 unconditionally.
	resp, err = http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("get /healthz: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/healthz status = %d", resp.StatusCode)
	}

	// /readyz returns 200 by default.
	resp, err = http.Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatalf("get /readyz: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/readyz initial status = %d", resp.StatusCode)
	}

	// Flip ready=false → 503.
	SetReady(false)
	defer SetReady(true)
	resp, err = http.Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatalf("get /readyz: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("/readyz after SetReady(false) status = %d, want 503", resp.StatusCode)
	}
}

func TestName(t *testing.T) {
	if got := New().Name(); got != "prometheus" {
		t.Errorf("Name() = %q, want prometheus", got)
	}
}
