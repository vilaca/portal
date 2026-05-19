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
	"github.com/vilaca/portal/internal/engine"
	"github.com/vilaca/portal/internal/expr/exprlang"
	"github.com/vilaca/portal/internal/rule"
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

// TestMalformedObjectFailsClosed ensures the handler refuses an
// AdmissionReview whose Object.Raw cannot be parsed as a JSON object. Prior
// behavior was to return Allowed=true, which let any caller bypass every
// rule by sending a JSON array as the object payload.
func TestMalformedObjectFailsClosed(t *testing.T) {
	eng := &stubEngine{}
	disp := &stubDispatcher{}
	s := newTestSource(t, eng, disp, nil, Options{})

	review := &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{APIVersion: "admission.k8s.io/v1", Kind: "AdmissionReview"},
		Request: &admissionv1.AdmissionRequest{
			UID:       types.UID("uid-malformed"),
			Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
			Resource:  metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: []byte(`[1,2,3]`)},
		},
	}
	resp := doRequest(t, s.Handler(), review)
	if resp.Response == nil || resp.Response.Allowed {
		t.Fatalf("expected Allowed=false on malformed object, got %+v", resp.Response)
	}
	if eng.callCount() != 0 {
		t.Errorf("engine should not have been called on decode failure, got %d calls", eng.callCount())
	}
	if len(disp.snapshot()) != 0 {
		t.Errorf("dispatcher should not have been called on decode failure")
	}
}

// TestNoObjectPayloadAllows preserves the allow-on-empty-payload behavior for
// legitimate sub-resource operations where neither Object nor OldObject is
// present.
func TestNoObjectPayloadAllows(t *testing.T) {
	eng := &stubEngine{}
	s := newTestSource(t, eng, nil, nil, Options{})

	review := &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{APIVersion: "admission.k8s.io/v1", Kind: "AdmissionReview"},
		Request: &admissionv1.AdmissionRequest{
			UID:       types.UID("uid-empty"),
			Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
			Resource:  metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
			Operation: admissionv1.Connect,
		},
	}
	resp := doRequest(t, s.Handler(), review)
	if resp.Response == nil || !resp.Response.Allowed {
		t.Fatalf("expected Allowed=true on empty payload, got %+v", resp.Response)
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

// --- Real-engine reproduction of TestAdmissionDeny ----------------------
//
// These tests mirror exactly what the e2e test deploys against kind:
//   - one PortalClusterRule with mode=[admission], enforcementAction=deny,
//     match.gvk=[Pod], match.namespaces.include=[<ns>],
//     rule=`container.securityContext.privileged == true`
//   - one inbound AdmissionRequest for a privileged-container pod
// and run them through the real api.RuleEngine + real pod ContextBuilder.
// If the e2e flake is caused by a Go-side bug in decode/build/eval, one
// of these will reproduce it locally; if they all pass, the bug must be
// outside the Go code path (apiserver delivery shape, reconciler timing,
// etc.).

func TestRealEngine_DeniesPrivilegedPod_BlockYAMLShape(t *testing.T) {
	runRealEngineDenyTest(t, privilegedPodJSONBlockStyle)
}

func TestRealEngine_DeniesPrivilegedPod_InlineYAMLShape(t *testing.T) {
	runRealEngineDenyTest(t, privilegedPodJSONInlineStyle)
}

// privilegedPodJSONBlockStyle is what the apiserver would deliver for the
// TestAdmissionDeny pod whose YAML uses the block-style containers list
// (`containers:\n  - name: app\n    ...`). After kubectl + apiserver
// serialisation it's the same canonical JSON either way; both literals
// are kept here to prove that explicitly.
const privilegedPodJSONBlockStyle = `{
  "apiVersion": "v1",
  "kind": "Pod",
  "metadata": {
    "name": "priv",
    "namespace": "e2e-testadmissiondeny-block"
  },
  "spec": {
    "containers": [
      {
        "name": "app",
        "image": "nginx",
        "securityContext": {"privileged": true}
      }
    ]
  }
}`

// privilegedPodJSONInlineStyle is the same pod as it would arrive when
// the YAML used inline-flow containers (`containers: [{name: app, ...}]`).
const privilegedPodJSONInlineStyle = `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"priv","namespace":"e2e-testadmissiondeny-inline"},"spec":{"containers":[{"name":"app","image":"nginx","securityContext":{"privileged":true}}]}}`

// privilegedPodJSONApiserverShape is the body the apiserver actually
// delivers when kubectl apply'ing the TestAdmissionDeny pod — captured
// from a local repro. Includes managedFields, defaulted fields,
// generated volumes (kube-api-access projection), the pod-level
// securityContext: {}, status.phase, etc. This is the shape my
// hand-rolled canonical JSONs missed.
const privilegedPodJSONApiserverShape = `{"kind":"Pod","apiVersion":"v1","metadata":{"name":"priv","namespace":"e2e-testadmissiondeny-apiserver","uid":"f1b14723-f873-4102-a400-610d8ac7521a","creationTimestamp":"2026-05-18T14:15:13Z","annotations":{"kubectl.kubernetes.io/last-applied-configuration":"{\"apiVersion\":\"v1\",\"kind\":\"Pod\",\"metadata\":{\"annotations\":{},\"name\":\"priv\",\"namespace\":\"e2e-testadmissiondeny-apiserver\"},\"spec\":{\"containers\":[{\"image\":\"nginx\",\"name\":\"app\",\"securityContext\":{\"privileged\":true}}]}}\n"},"managedFields":[{"manager":"kubectl-client-side-apply","operation":"Update","apiVersion":"v1","time":"2026-05-18T14:15:13Z","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:kubectl.kubernetes.io/last-applied-configuration":{}}},"f:spec":{"f:containers":{"k:{\"name\":\"app\"}":{".":{},"f:image":{},"f:imagePullPolicy":{},"f:name":{},"f:resources":{},"f:securityContext":{".":{},"f:privileged":{}},"f:terminationMessagePath":{},"f:terminationMessagePolicy":{}}},"f:dnsPolicy":{},"f:enableServiceLinks":{},"f:restartPolicy":{},"f:schedulerName":{},"f:securityContext":{},"f:terminationGracePeriodSeconds":{}}}}]},"spec":{"volumes":[{"name":"kube-api-access-qng4n","projected":{"sources":[{"serviceAccountToken":{"expirationSeconds":3607,"path":"token"}},{"configMap":{"name":"kube-root-ca.crt","items":[{"key":"ca.crt","path":"ca.crt"}]}},{"downwardAPI":{"items":[{"path":"namespace","fieldRef":{"apiVersion":"v1","fieldPath":"metadata.namespace"}}]}}],"defaultMode":420}}],"containers":[{"name":"app","image":"nginx","resources":{},"volumeMounts":[{"name":"kube-api-access-qng4n","readOnly":true,"mountPath":"/var/run/secrets/kubernetes.io/serviceaccount"}],"terminationMessagePath":"/dev/termination-log","terminationMessagePolicy":"File","imagePullPolicy":"Always","securityContext":{"privileged":true}}],"restartPolicy":"Always","terminationGracePeriodSeconds":30,"dnsPolicy":"ClusterFirst","serviceAccountName":"default","serviceAccount":"default","securityContext":{},"schedulerName":"default-scheduler","tolerations":[{"key":"node.kubernetes.io/not-ready","operator":"Exists","effect":"NoExecute","tolerationSeconds":300},{"key":"node.kubernetes.io/unreachable","operator":"Exists","effect":"NoExecute","tolerationSeconds":300}],"priority":0,"enableServiceLinks":true,"preemptionPolicy":"PreemptLowerPriority"},"status":{"phase":"Pending","qosClass":"BestEffort"}}`

func TestRealEngine_DeniesPrivilegedPod_ApiserverShape(t *testing.T) {
	runRealEngineDenyTest(t, privilegedPodJSONApiserverShape)
}

func runRealEngineDenyTest(t *testing.T, podJSON string) {
	t.Helper()
	// Real rule index + real expr-lang engine — same wiring as wire.go.
	idx := rule.NewIndex()
	idx.Replace([]api.Rule{{
		Name:              "e2e-deny-privileged",
		Enabled:           true,
		Severity:          api.SeverityCritical,
		Mode:              []api.Mode{api.ModeAdmission},
		EnforcementAction: api.EnforceDeny,
		Match: api.Matcher{
			GVK: []schema.GroupVersionKind{{Group: "", Version: "v1", Kind: "Pod"}},
			Namespaces: api.NamespaceSelector{
				Include: []string{namespaceFromJSON(t, podJSON)},
			},
		},
		Expression: "container.securityContext.privileged == true",
	}})
	eng, err := engine.New(idx, exprlang.New())
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}

	disp := &stubDispatcher{}
	s := newTestSource(t, eng, disp, nil, Options{})

	review := &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Request: &admissionv1.AdmissionRequest{
			UID:       types.UID("uid-real-engine"),
			Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
			Resource:  metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
			Name:      "priv",
			Namespace: namespaceFromJSON(t, podJSON),
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: []byte(podJSON)},
			UserInfo: authenticationv1.UserInfo{
				Username: "tester@example.com",
				Groups:   []string{"system:authenticated"},
			},
		},
	}
	resp := doRequest(t, s.Handler(), review)

	if resp.Response == nil {
		t.Fatalf("nil response")
	}
	if resp.Response.Allowed {
		t.Fatalf("expected Allowed=false; got Allowed=true. result=%+v", resp.Response.Result)
	}
	if resp.Response.Result == nil || !contains(resp.Response.Result.Message, "e2e-deny-privileged") {
		t.Errorf("expected deny message to name the rule, got: %+v", resp.Response.Result)
	}
}

// TestRealEngine_ConcurrentReloadDuringAdmission stresses the same code
// path while a concurrent goroutine keeps Index.Replace / Reload-cycling.
// If the CI flake is a race between the audit reconciler reloading the
// engine and the admission handler reading from it, this test should
// surface it. Runs for ~1 s with admission requests at ~kHz and reloads
// at ~10/s — matches the rough cadence of the e2e burst.
func TestRealEngine_ConcurrentReloadDuringAdmission(t *testing.T) {
	ns := "e2e-stress"
	idx := rule.NewIndex()
	denyRule := api.Rule{
		Name:              "e2e-deny-privileged",
		Enabled:           true,
		Severity:          api.SeverityCritical,
		Mode:              []api.Mode{api.ModeAdmission},
		EnforcementAction: api.EnforceDeny,
		Match: api.Matcher{
			GVK: []schema.GroupVersionKind{{Group: "", Version: "v1", Kind: "Pod"}},
			Namespaces: api.NamespaceSelector{
				Include: []string{ns},
			},
		},
		Expression: "container.securityContext.privileged == true",
	}
	idx.Replace([]api.Rule{denyRule})

	eng, err := engine.New(idx, exprlang.New())
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	reloader, _ := eng.(interface{ Reload() })

	disp := &stubDispatcher{}
	s := newTestSource(t, eng, disp, nil, Options{})
	hh := s.Handler()

	podJSON := []byte(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"priv","namespace":"` + ns + `"},"spec":{"containers":[{"name":"app","image":"nginx","securityContext":{"privileged":true}}]}}`)

	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				idx.Replace([]api.Rule{denyRule})
				if reloader != nil {
					reloader.Reload()
				}
			}
		}
	}()
	defer close(stop)

	var (
		denied  int
		allowed int
	)
	for i := 0; i < 200; i++ {
		review := &admissionv1.AdmissionReview{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "admission.k8s.io/v1",
				Kind:       "AdmissionReview",
			},
			Request: &admissionv1.AdmissionRequest{
				UID:       types.UID("uid-stress"),
				Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
				Resource:  metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
				Name:      "priv",
				Namespace: ns,
				Operation: admissionv1.Create,
				Object:    runtime.RawExtension{Raw: podJSON},
				UserInfo: authenticationv1.UserInfo{
					Username: "tester@example.com",
				},
			},
		}
		resp := doRequest(t, hh, review)
		if resp.Response.Allowed {
			allowed++
		} else {
			denied++
		}
	}

	if allowed != 0 {
		t.Fatalf("under concurrent Reload: %d/200 admissions returned allowed (expected 0)", allowed)
	}
	if denied != 200 {
		t.Fatalf("expected 200 denials, got %d", denied)
	}
}

// namespaceFromJSON extracts metadata.namespace from a pod JSON literal,
// so the rule's Include list matches the AdmissionRequest's namespace
// without test-side string juggling.
func namespaceFromJSON(t *testing.T, podJSON string) string {
	t.Helper()
	var probe struct {
		Metadata struct {
			Namespace string `json:"namespace"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(podJSON), &probe); err != nil {
		t.Fatalf("namespaceFromJSON: %v", err)
	}
	if probe.Metadata.Namespace == "" {
		t.Fatalf("namespaceFromJSON: pod JSON has no metadata.namespace")
	}
	return probe.Metadata.Namespace
}

// catchAllBuilder is a test double for a non-pod ContextBuilder whose
// Supports() returns true for every GVK — same shape as the generic
// builder in internal/context/generic. Its Build() returns a context
// with NO container key, mirroring how a catch-all would handle a Pod.
type catchAllBuilder struct{}

func (catchAllBuilder) Supports(_ schema.GroupVersionKind) bool { return true }

func (catchAllBuilder) Build(obj *unstructured.Unstructured) (api.Context, error) {
	return api.Context{
		GVK:    obj.GroupVersionKind(),
		Object: obj,
		Env: map[string]any{
			"object":   obj.Object,
			"metadata": map[string]any{"name": obj.GetName(), "namespace": obj.GetNamespace()},
		},
	}, nil
}

// TestRealEngine_PodBuilderWinsOverCatchAllRegardlessOfOrder is the
// regression test for the production flake where wire.go iterated
// api.ContextBuilders() (a Go map, unordered) into handler.builders.
// If the catch-all builder happened to come first, handler.buildContexts
// returned its container-less context, the rule eval hit a nil container,
// the privileged pod was admitted, and TestAdmissionDeny failed roughly
// 80% of the time. The fix in handler.buildContexts prefers any
// podBuildAller across all builders before falling back to single-context
// builders. This test asserts that invariant in both possible orderings.
func TestRealEngine_PodBuilderWinsOverCatchAllRegardlessOfOrder(t *testing.T) {
	cases := []struct {
		name     string
		builders []api.ContextBuilder
	}{
		{"catchAllFirst", []api.ContextBuilder{catchAllBuilder{}, pod.New()}},
		{"podFirst", []api.ContextBuilder{pod.New(), catchAllBuilder{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idx := rule.NewIndex()
			idx.Replace([]api.Rule{{
				Name:              "e2e-deny-privileged",
				Enabled:           true,
				Severity:          api.SeverityCritical,
				Mode:              []api.Mode{api.ModeAdmission},
				EnforcementAction: api.EnforceDeny,
				Match: api.Matcher{
					GVK: []schema.GroupVersionKind{{Group: "", Version: "v1", Kind: "Pod"}},
					Namespaces: api.NamespaceSelector{
						Include: []string{namespaceFromJSON(t, privilegedPodJSONApiserverShape)},
					},
				},
				Expression: "container.securityContext.privileged == true",
			}})
			eng, err := engine.New(idx, exprlang.New())
			if err != nil {
				t.Fatalf("engine.New: %v", err)
			}
			s := newTestSource(t, eng, &stubDispatcher{}, nil, Options{ContextBuilders: tc.builders})

			review := &admissionv1.AdmissionReview{
				TypeMeta: metav1.TypeMeta{APIVersion: "admission.k8s.io/v1", Kind: "AdmissionReview"},
				Request: &admissionv1.AdmissionRequest{
					UID:       types.UID("uid-order-" + tc.name),
					Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
					Resource:  metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
					Name:      "priv",
					Namespace: namespaceFromJSON(t, privilegedPodJSONApiserverShape),
					Operation: admissionv1.Create,
					Object:    runtime.RawExtension{Raw: []byte(privilegedPodJSONApiserverShape)},
					UserInfo: authenticationv1.UserInfo{
						Username: "tester@example.com",
						Groups:   []string{"system:authenticated"},
					},
				},
			}
			resp := doRequest(t, s.Handler(), review)
			if resp.Response == nil {
				t.Fatalf("nil response")
			}
			if resp.Response.Allowed {
				t.Fatalf("expected Allowed=false; got Allowed=true. result=%+v", resp.Response.Result)
			}
		})
	}
}
