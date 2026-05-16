package admission

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/vilaca/portal/internal/api"
	"github.com/vilaca/portal/internal/context/pod"
)

// --- test doubles --------------------------------------------------------

type stubEngine struct {
	mu     sync.Mutex
	calls  int
	violations []api.Violation
	perCallFn func(idx int, ctx api.Context) []api.Violation
}

func (s *stubEngine) Evaluate(ctx api.Context, _ api.EventMeta) []api.Violation {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.calls
	s.calls++
	if s.perCallFn != nil {
		return s.perCallFn(idx, ctx)
	}
	out := make([]api.Violation, len(s.violations))
	copy(out, s.violations)
	return out
}

func (s *stubEngine) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type stubDispatcher struct {
	mu   sync.Mutex
	got  []api.Violation
}

func (s *stubDispatcher) Dispatch(_ context.Context, v api.Violation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, v)
}
func (s *stubDispatcher) Drain(_ context.Context) error { return nil }
func (s *stubDispatcher) snapshot() []api.Violation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]api.Violation(nil), s.got...)
}

type stubSink struct {
	name string
	mu   sync.Mutex
	got  []api.Violation
}

func (s *stubSink) Name() string { return s.name }
func (s *stubSink) Emit(_ context.Context, v api.Violation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, v)
	return nil
}
func (s *stubSink) Close() error { return nil }
func (s *stubSink) snapshot() []api.Violation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]api.Violation(nil), s.got...)
}

// --- helpers -------------------------------------------------------------

var podGVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"}

func makePodObject(name, ns string, containerNames ...string) *unstructured.Unstructured {
	containers := make([]any, 0, len(containerNames))
	for _, c := range containerNames {
		containers = append(containers, map[string]any{
			"name":  c,
			"image": "nginx:1.21",
		})
	}
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]any{
				"name":      name,
				"namespace": ns,
			},
			"spec": map[string]any{
				"containers": containers,
			},
		},
	}
	obj.SetGroupVersionKind(podGVK)
	return obj
}

func buildReview(uid string, op admissionv1.Operation, obj *unstructured.Unstructured) *admissionv1.AdmissionReview {
	raw, _ := json.Marshal(obj.Object)
	return &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Request: &admissionv1.AdmissionRequest{
			UID:       types.UID(uid),
			Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
			Resource:  metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
			Name:      obj.GetName(),
			Namespace: obj.GetNamespace(),
			Operation: op,
			Object:    runtime.RawExtension{Raw: raw},
			UserInfo: authenticationv1.UserInfo{
				Username: "tester@example.com",
				Groups:   []string{"system:authenticated"},
			},
		},
	}
}

func doRequest(t *testing.T, h http.Handler, review *admissionv1.AdmissionReview) *admissionv1.AdmissionReview {
	t.Helper()
	body, err := json.Marshal(review)
	if err != nil {
		t.Fatalf("marshal review: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/validate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	out := &admissionv1.AdmissionReview{}
	if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
		t.Fatalf("unmarshal response: %v; body=%s", err, w.Body.String())
	}
	return out
}

// newTestSource constructs an admission server bypassing TLS setup; tests
// drive Handler() directly.
func newTestSource(t *testing.T, engine api.RuleEngine, disp api.ActionDispatcher, sinks []api.OutputSink, opts Options) *server {
	t.Helper()
	if opts.ContextBuilders == nil {
		opts.ContextBuilders = []api.ContextBuilder{pod.New()}
	}
	src, err := New(engine, disp, sinks, opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return src.(*server)
}

// --- tests ---------------------------------------------------------------

func TestAllowWhenNoViolations(t *testing.T) {
	eng := &stubEngine{}
	disp := &stubDispatcher{}
	sink := &stubSink{name: "stub"}
	s := newTestSource(t, eng, disp, []api.OutputSink{sink}, Options{})

	review := buildReview("uid-allow", admissionv1.Create, makePodObject("p", "default", "main"))
	resp := doRequest(t, s.Handler(), review)

	if resp.Response == nil || !resp.Response.Allowed {
		t.Fatalf("expected Allowed=true, got %+v", resp.Response)
	}
	if string(resp.Response.UID) != "uid-allow" {
		t.Errorf("UID mismatch: got %q", resp.Response.UID)
	}
	if len(disp.snapshot()) != 0 {
		t.Errorf("expected 0 dispatches, got %d", len(disp.snapshot()))
	}
	if len(sink.snapshot()) != 0 {
		t.Errorf("expected 0 sink emits, got %d", len(sink.snapshot()))
	}
}

func TestDenyShortCircuitsAdmission(t *testing.T) {
	eng := &stubEngine{
		violations: []api.Violation{
			{
				Rule:              "no-privileged",
				EnforcementAction: api.EnforceDeny,
				Message:           "privileged pod",
			},
		},
	}
	disp := &stubDispatcher{}
	sink := &stubSink{name: "stub"}
	s := newTestSource(t, eng, disp, []api.OutputSink{sink}, Options{})

	review := buildReview("uid-deny", admissionv1.Create, makePodObject("p", "default", "main"))
	resp := doRequest(t, s.Handler(), review)

	if resp.Response.Allowed {
		t.Fatalf("expected Allowed=false")
	}
	if resp.Response.Result == nil || resp.Response.Result.Message == "" {
		t.Fatalf("expected result.message set, got %+v", resp.Response.Result)
	}
	if !contains(resp.Response.Result.Message, "no-privileged") {
		t.Errorf("expected rule name in message, got %q", resp.Response.Result.Message)
	}
	if len(sink.snapshot()) != 1 {
		t.Errorf("expected 1 sink emit, got %d", len(sink.snapshot()))
	}
	if len(disp.snapshot()) != 1 {
		t.Errorf("expected 1 dispatch, got %d", len(disp.snapshot()))
	}
}

func TestWarnAddsWarnings(t *testing.T) {
	eng := &stubEngine{
		violations: []api.Violation{
			{
				Rule:              "should-be-better",
				EnforcementAction: api.EnforceWarn,
				Message:           "no resource limits",
			},
		},
	}
	disp := &stubDispatcher{}
	sink := &stubSink{name: "stub"}
	s := newTestSource(t, eng, disp, []api.OutputSink{sink}, Options{})

	review := buildReview("uid-warn", admissionv1.Create, makePodObject("p", "default", "main"))
	resp := doRequest(t, s.Handler(), review)

	if !resp.Response.Allowed {
		t.Fatalf("expected Allowed=true")
	}
	if len(resp.Response.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %v", resp.Response.Warnings)
	}
	if !contains(resp.Response.Warnings[0], "should-be-better") {
		t.Errorf("warning missing rule name: %q", resp.Response.Warnings[0])
	}
}

func TestDryRunDoesNotAffectResponse(t *testing.T) {
	eng := &stubEngine{
		violations: []api.Violation{
			{
				Rule:              "future-rule",
				EnforcementAction: api.EnforceDryRun,
				Message:           "future enforcement",
			},
		},
	}
	disp := &stubDispatcher{}
	sink := &stubSink{name: "stub"}
	s := newTestSource(t, eng, disp, []api.OutputSink{sink}, Options{})

	review := buildReview("uid-dry", admissionv1.Create, makePodObject("p", "default", "main"))
	resp := doRequest(t, s.Handler(), review)

	if !resp.Response.Allowed {
		t.Fatalf("expected Allowed=true")
	}
	if len(resp.Response.Warnings) != 0 {
		t.Errorf("dryrun must not produce warnings, got %v", resp.Response.Warnings)
	}
	if len(sink.snapshot()) != 1 {
		t.Errorf("dryrun violation must still be emitted, got %d", len(sink.snapshot()))
	}
	if len(disp.snapshot()) != 1 {
		t.Errorf("dryrun violation must still be dispatched, got %d", len(disp.snapshot()))
	}
}

func TestBypassNamespaceShortCircuits(t *testing.T) {
	eng := &stubEngine{
		violations: []api.Violation{{Rule: "r", EnforcementAction: api.EnforceDeny, Message: "m"}},
	}
	disp := &stubDispatcher{}
	sink := &stubSink{name: "stub"}
	s := newTestSource(t, eng, disp, []api.OutputSink{sink}, Options{
		NamespaceLister: func(name string) (map[string]string, map[string]string, error) {
			if name == "team-a" {
				return nil, map[string]string{"portal.io/bypass": "true"}, nil
			}
			return nil, nil, nil
		},
	})

	review := buildReview("uid-by", admissionv1.Create, makePodObject("p", "team-a", "main"))
	resp := doRequest(t, s.Handler(), review)

	if !resp.Response.Allowed {
		t.Fatalf("expected Allowed=true for bypassed ns")
	}
	if eng.callCount() != 0 {
		t.Errorf("expected engine NOT called on bypass, got %d", eng.callCount())
	}
	if len(disp.snapshot()) != 0 || len(sink.snapshot()) != 0 {
		t.Errorf("expected no sink/dispatcher calls under bypass")
	}
}

func TestSystemNamespacesExcluded(t *testing.T) {
	eng := &stubEngine{
		violations: []api.Violation{{Rule: "r", EnforcementAction: api.EnforceDeny, Message: "m"}},
	}
	s := newTestSource(t, eng, nil, nil, Options{})
	for _, ns := range []string{"kube-system", "kube-public", "kube-node-lease"} {
		review := buildReview("uid-"+ns, admissionv1.Create, makePodObject("p", ns, "main"))
		resp := doRequest(t, s.Handler(), review)
		if !resp.Response.Allowed {
			t.Errorf("expected Allowed=true for %s, got Allowed=false", ns)
		}
	}
	if eng.callCount() != 0 {
		t.Errorf("expected engine NOT called for system namespaces, got %d", eng.callCount())
	}
}

func TestEmptyExcludedFallsBackToDefaults(t *testing.T) {
	eng := &stubEngine{
		violations: []api.Violation{{Rule: "r", EnforcementAction: api.EnforceDeny, Message: "m"}},
	}
	// Caller passes an explicitly empty list; defaults must still apply.
	s := newTestSource(t, eng, nil, nil, Options{ExcludedNamespaces: []string{}})
	review := buildReview("uid-x", admissionv1.Create, makePodObject("p", "kube-system", "main"))
	resp := doRequest(t, s.Handler(), review)
	if !resp.Response.Allowed {
		t.Fatalf("kube-system must always be excluded")
	}
}

func TestInstallNamespaceExcluded(t *testing.T) {
	eng := &stubEngine{
		violations: []api.Violation{{Rule: "r", EnforcementAction: api.EnforceDeny, Message: "m"}},
	}
	s := newTestSource(t, eng, nil, nil, Options{InstallNamespace: "portal-system"})
	review := buildReview("uid-i", admissionv1.Create, makePodObject("p", "portal-system", "main"))
	resp := doRequest(t, s.Handler(), review)
	if !resp.Response.Allowed {
		t.Fatalf("install namespace must be excluded")
	}
	if eng.callCount() != 0 {
		t.Errorf("engine must not be called for install namespace")
	}
}

func TestUIDCopiedVerbatim(t *testing.T) {
	eng := &stubEngine{}
	s := newTestSource(t, eng, nil, nil, Options{})
	review := buildReview("preserve-this-uid-12345", admissionv1.Create, makePodObject("p", "default", "main"))
	resp := doRequest(t, s.Handler(), review)
	if string(resp.Response.UID) != "preserve-this-uid-12345" {
		t.Fatalf("UID mismatch: got %q", resp.Response.UID)
	}
}

func TestPodWithTwoContainersBuildAllPath(t *testing.T) {
	eng := &stubEngine{
		violations: []api.Violation{{Rule: "r", EnforcementAction: api.EnforceWarn, Message: "m"}},
	}
	disp := &stubDispatcher{}
	sink := &stubSink{name: "stub"}
	s := newTestSource(t, eng, disp, []api.OutputSink{sink}, Options{})

	review := buildReview("uid-multi", admissionv1.Create, makePodObject("p", "default", "main", "sidecar"))
	resp := doRequest(t, s.Handler(), review)

	if eng.callCount() != 2 {
		t.Fatalf("expected engine called twice (once per container), got %d", eng.callCount())
	}
	if !resp.Response.Allowed {
		t.Fatalf("expected Allowed=true (warn)")
	}
	if len(resp.Response.Warnings) != 2 {
		t.Errorf("expected 2 warnings (one per container), got %v", resp.Response.Warnings)
	}
	if len(sink.snapshot()) != 2 {
		t.Errorf("expected sink emitted twice, got %d", len(sink.snapshot()))
	}
	if len(disp.snapshot()) != 2 {
		t.Errorf("expected dispatcher called twice, got %d", len(disp.snapshot()))
	}
}

func TestSinkAndDispatcherCalledForEveryViolation(t *testing.T) {
	eng := &stubEngine{
		violations: []api.Violation{
			{Rule: "r1", EnforcementAction: api.EnforceWarn, Message: "m1"},
			{Rule: "r2", EnforcementAction: api.EnforceDeny, Message: "m2"},
		},
	}
	disp := &stubDispatcher{}
	sink1 := &stubSink{name: "s1"}
	sink2 := &stubSink{name: "s2"}
	s := newTestSource(t, eng, disp, []api.OutputSink{sink1, sink2}, Options{})

	review := buildReview("uid-aggr", admissionv1.Create, makePodObject("p", "default", "main"))
	resp := doRequest(t, s.Handler(), review)

	if resp.Response.Allowed {
		t.Fatalf("expected Allowed=false (one deny)")
	}
	if got := len(sink1.snapshot()); got != 2 {
		t.Errorf("sink1: expected 2 emits, got %d", got)
	}
	if got := len(sink2.snapshot()); got != 2 {
		t.Errorf("sink2: expected 2 emits, got %d", got)
	}
	if got := len(disp.snapshot()); got != 2 {
		t.Errorf("dispatcher: expected 2 dispatches, got %d", got)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	eng := &stubEngine{}
	s := newTestSource(t, eng, nil, nil, Options{})
	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestMalformedBodyReturns400(t *testing.T) {
	eng := &stubEngine{}
	s := newTestSource(t, eng, nil, nil, Options{})
	req := httptest.NewRequest(http.MethodPost, "/validate", bytes.NewReader([]byte("not-json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAggregateMixedActions(t *testing.T) {
	d := aggregate([]api.Violation{
		{Rule: "warn1", EnforcementAction: api.EnforceWarn, Message: "w1"},
		{Rule: "deny1", EnforcementAction: api.EnforceDeny, Message: "d1"},
		{Rule: "dry1", EnforcementAction: api.EnforceDryRun, Message: "x1"},
		{Rule: "warn2", EnforcementAction: api.EnforceWarn, Message: "w2"},
	})
	if d.Allowed {
		t.Fatalf("expected Allowed=false (deny present)")
	}
	if !contains(d.Message, "deny1") {
		t.Errorf("deny message missing rule name: %q", d.Message)
	}
	if len(d.Warnings) != 2 {
		t.Errorf("expected 2 warnings, got %v", d.Warnings)
	}
}

func contains(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}
