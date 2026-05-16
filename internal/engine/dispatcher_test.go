package engine

import (
	"errors"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vilaca/portal/internal/api"
)

// --- test doubles --------------------------------------------------------

type stubProgram struct {
	out bool
	err error
}

func (s *stubProgram) Eval(_ api.Context) (bool, error) { return s.out, s.err }

type stubEngine struct {
	// returned per expression text
	progs map[string]*stubProgram
	// expressions that fail to compile
	bad map[string]error
}

func (s *stubEngine) Name() string { return "stub" }
func (s *stubEngine) Compile(expr string) (api.Program, error) {
	if e, ok := s.bad[expr]; ok {
		return nil, e
	}
	if p, ok := s.progs[expr]; ok {
		return p, nil
	}
	return &stubProgram{out: false}, nil
}

type stubIndex struct{ rules []api.Rule }

func (s *stubIndex) ForGVK(gvk schema.GroupVersionKind) []api.Rule {
	out := []api.Rule{}
	for _, r := range s.rules {
		for _, g := range r.Match.GVK {
			if g == gvk {
				out = append(out, r)
				break
			}
		}
	}
	return out
}
func (s *stubIndex) All() []api.Rule { return s.rules }

// --- helpers -------------------------------------------------------------

var podGVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"}
var deployGVK = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}

func podCtx(ns, name string) api.Context {
	obj := &unstructured.Unstructured{}
	obj.SetName(name)
	obj.SetNamespace(ns)
	obj.SetGroupVersionKind(podGVK)
	return api.Context{
		GVK:    podGVK,
		Object: obj,
		Env: map[string]any{
			"container": map[string]any{"name": "main"},
		},
	}
}

// --- tests ---------------------------------------------------------------

func TestNewRejectsNil(t *testing.T) {
	if _, err := New(nil, &stubEngine{}); err == nil {
		t.Fatalf("expected error for nil index")
	}
	if _, err := New(&stubIndex{}, nil); err == nil {
		t.Fatalf("expected error for nil engine")
	}
}

func TestGVKRouting(t *testing.T) {
	idx := &stubIndex{rules: []api.Rule{
		{Name: "pod-rule", Expression: "always", Match: api.Matcher{GVK: []schema.GroupVersionKind{podGVK}}},
		{Name: "dep-rule", Expression: "always", Match: api.Matcher{GVK: []schema.GroupVersionKind{deployGVK}}},
	}}
	eng := &stubEngine{progs: map[string]*stubProgram{"always": {out: true}}}
	e, err := New(idx, eng)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	vs := e.Evaluate(podCtx("default", "p1"), api.EventMeta{Source: "admission"})
	if len(vs) != 1 || vs[0].Rule != "pod-rule" {
		t.Fatalf("expected pod-rule, got %+v", vs)
	}
}

func TestNamespaceInclude(t *testing.T) {
	idx := &stubIndex{rules: []api.Rule{
		{
			Name:       "prod-only",
			Expression: "always",
			Match: api.Matcher{
				GVK:        []schema.GroupVersionKind{podGVK},
				Namespaces: api.NamespaceSelector{Include: []string{"production"}},
			},
		},
	}}
	eng := &stubEngine{progs: map[string]*stubProgram{"always": {out: true}}}
	e, _ := New(idx, eng)

	if got := e.Evaluate(podCtx("default", "p1"), api.EventMeta{Source: "audit"}); len(got) != 0 {
		t.Errorf("expected 0 violations for non-matching namespace, got %d", len(got))
	}
	if got := e.Evaluate(podCtx("production", "p1"), api.EventMeta{Source: "audit"}); len(got) != 1 {
		t.Errorf("expected 1 violation for matching namespace, got %d", len(got))
	}
}

func TestNamespaceExclude(t *testing.T) {
	idx := &stubIndex{rules: []api.Rule{
		{
			Name:       "not-system",
			Expression: "always",
			Match: api.Matcher{
				GVK:        []schema.GroupVersionKind{podGVK},
				Namespaces: api.NamespaceSelector{Exclude: []string{"kube-system"}},
			},
		},
	}}
	eng := &stubEngine{progs: map[string]*stubProgram{"always": {out: true}}}
	e, _ := New(idx, eng)

	if got := e.Evaluate(podCtx("kube-system", "p1"), api.EventMeta{Source: "audit"}); len(got) != 0 {
		t.Errorf("expected exclusion to suppress violation, got %d", len(got))
	}
	if got := e.Evaluate(podCtx("default", "p1"), api.EventMeta{Source: "audit"}); len(got) != 1 {
		t.Errorf("expected violation in non-excluded ns, got %d", len(got))
	}
}

func TestAlertShorthandExpansion(t *testing.T) {
	idx := &stubIndex{rules: []api.Rule{
		{
			Name:       "alert-shorthand",
			Expression: "always",
			Alert:      "insecure-workload",
			Actions:    []api.ActionSpec{{Type: "label", Params: map[string]any{"key": "x"}}},
			Match:      api.Matcher{GVK: []schema.GroupVersionKind{podGVK}},
		},
	}}
	eng := &stubEngine{progs: map[string]*stubProgram{"always": {out: true}}}
	e, _ := New(idx, eng)
	vs := e.Evaluate(podCtx("default", "p1"), api.EventMeta{Source: "audit"})
	if len(vs) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(vs))
	}
	v := vs[0]
	if len(v.Actions) != 2 {
		t.Fatalf("expected 2 actions (alert + label), got %d", len(v.Actions))
	}
	if v.Actions[0].Type != "alertmanager" {
		t.Errorf("expected alertmanager first, got %q", v.Actions[0].Type)
	}
	if v.Actions[0].Params["template"] != "insecure-workload" {
		t.Errorf("expected template insecure-workload, got %v", v.Actions[0].Params["template"])
	}
	if v.Actions[1].Type != "label" {
		t.Errorf("expected label second, got %q", v.Actions[1].Type)
	}
}

func TestMultiViolationAggregation(t *testing.T) {
	idx := &stubIndex{rules: []api.Rule{
		{Name: "r1", Expression: "yes", Match: api.Matcher{GVK: []schema.GroupVersionKind{podGVK}}},
		{Name: "r2", Expression: "yes", Match: api.Matcher{GVK: []schema.GroupVersionKind{podGVK}}},
		{Name: "r3", Expression: "no", Match: api.Matcher{GVK: []schema.GroupVersionKind{podGVK}}},
	}}
	eng := &stubEngine{progs: map[string]*stubProgram{
		"yes": {out: true},
		"no":  {out: false},
	}}
	e, _ := New(idx, eng)
	vs := e.Evaluate(podCtx("default", "p1"), api.EventMeta{Source: "audit"})
	if len(vs) != 2 {
		t.Fatalf("expected 2 violations, got %d", len(vs))
	}
}

func TestCompileErrorsSurface(t *testing.T) {
	idx := &stubIndex{rules: []api.Rule{
		{Name: "good", Expression: "ok", Match: api.Matcher{GVK: []schema.GroupVersionKind{podGVK}}},
		{Name: "broken", Expression: "BAD", Match: api.Matcher{GVK: []schema.GroupVersionKind{podGVK}}},
	}}
	eng := &stubEngine{
		progs: map[string]*stubProgram{"ok": {out: true}},
		bad:   map[string]error{"BAD": errors.New("syntax error at line 1")},
	}
	e, err := New(idx, eng)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d := e.(*dispatcher)
	if got := d.ParseError("broken"); got == "" {
		t.Errorf("expected ParseError for broken rule")
	}
	if got := d.ParseError("good"); got != "" {
		t.Errorf("expected no ParseError for good rule, got %q", got)
	}
	errs := d.CompileErrors()
	if _, ok := errs["broken"]; !ok {
		t.Errorf("expected CompileErrors map to contain broken")
	}

	// And evaluation should still work for the good rule, not panic on broken.
	vs := e.Evaluate(podCtx("default", "p1"), api.EventMeta{Source: "audit"})
	if len(vs) != 1 || vs[0].Rule != "good" {
		t.Fatalf("expected only good to fire, got %+v", vs)
	}
}

func TestEvalErrorRecorded(t *testing.T) {
	idx := &stubIndex{rules: []api.Rule{
		{Name: "boom", Expression: "boom", Match: api.Matcher{GVK: []schema.GroupVersionKind{podGVK}}},
	}}
	eng := &stubEngine{progs: map[string]*stubProgram{
		"boom": {err: errors.New("nil deref")},
	}}
	e, _ := New(idx, eng)
	vs := e.Evaluate(podCtx("default", "p1"), api.EventMeta{Source: "audit"})
	if len(vs) != 0 {
		t.Errorf("expected no violations on eval error, got %d", len(vs))
	}
	d := e.(*dispatcher)
	if got := d.ParseError("boom"); got == "" {
		t.Errorf("expected ParseError to record eval failure")
	}
}

func TestModeMappingAndEnforcement(t *testing.T) {
	rule := api.Rule{
		Name:              "r",
		Expression:        "yes",
		EnforcementAction: api.EnforceDeny,
		Severity:          api.SeverityHigh,
		Match:             api.Matcher{GVK: []schema.GroupVersionKind{podGVK}},
	}
	idx := &stubIndex{rules: []api.Rule{rule}}
	eng := &stubEngine{progs: map[string]*stubProgram{"yes": {out: true}}}
	e, _ := New(idx, eng)

	t.Run("admission carries enforcement", func(t *testing.T) {
		vs := e.Evaluate(podCtx("default", "p1"), api.EventMeta{
			Source: "admission", EventID: "evt-1", Operation: "CREATE", At: time.Unix(1, 0),
		})
		if len(vs) != 1 {
			t.Fatalf("want 1 violation")
		}
		v := vs[0]
		if v.Mode != api.ModeAdmission {
			t.Errorf("Mode=%q", v.Mode)
		}
		if v.EnforcementAction != api.EnforceDeny {
			t.Errorf("EnforcementAction=%q", v.EnforcementAction)
		}
		if v.Source.EventID != "evt-1" {
			t.Errorf("EventID=%q", v.Source.EventID)
		}
		if v.Source.Operation != "CREATE" {
			t.Errorf("Operation=%q", v.Source.Operation)
		}
		if v.Source.Container != "main" {
			t.Errorf("Container=%q", v.Source.Container)
		}
		if v.Severity != api.SeverityHigh {
			t.Errorf("Severity=%q", v.Severity)
		}
		if v.Message != "rule violated: r" {
			t.Errorf("Message=%q", v.Message)
		}
	})

	t.Run("audit drops enforcement", func(t *testing.T) {
		vs := e.Evaluate(podCtx("default", "p1"), api.EventMeta{Source: "audit"})
		if len(vs) != 1 {
			t.Fatalf("want 1 violation")
		}
		if vs[0].EnforcementAction != "" {
			t.Errorf("expected empty enforcement outside admission, got %q", vs[0].EnforcementAction)
		}
		if vs[0].Mode != api.ModeAudit {
			t.Errorf("Mode=%q", vs[0].Mode)
		}
	})

	t.Run("network mode", func(t *testing.T) {
		vs := e.Evaluate(podCtx("default", "p1"), api.EventMeta{Source: "network"})
		if len(vs) != 1 || vs[0].Mode != api.ModeNetwork {
			t.Fatalf("expected ModeNetwork, got %+v", vs)
		}
	})
}

func TestUnknownGVKNoViolations(t *testing.T) {
	idx := &stubIndex{rules: []api.Rule{
		{Name: "r", Expression: "yes", Match: api.Matcher{GVK: []schema.GroupVersionKind{podGVK}}},
	}}
	eng := &stubEngine{progs: map[string]*stubProgram{"yes": {out: true}}}
	e, _ := New(idx, eng)
	other := api.Context{GVK: deployGVK, Object: &unstructured.Unstructured{}}
	if vs := e.Evaluate(other, api.EventMeta{Source: "audit"}); len(vs) != 0 {
		t.Errorf("expected no violations for unknown GVK, got %d", len(vs))
	}
}
