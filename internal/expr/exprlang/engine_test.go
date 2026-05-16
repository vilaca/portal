package exprlang

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/vilaca/portal/internal/api"
)

func TestEngineName(t *testing.T) {
	e := New()
	if e.Name() != "expr" {
		t.Fatalf("Name=%q, want expr", e.Name())
	}
}

func TestEngineRegistered(t *testing.T) {
	if api.Engine("expr") == nil {
		t.Fatalf("engine not registered under name expr")
	}
}

func TestCompileError(t *testing.T) {
	e := New()
	_, err := e.Compile("this is (((not balanced")
	if err == nil {
		t.Fatalf("expected compile error")
	}
	// expr-lang emits line/col diagnostics; ensure something like that is in the message
	msg := err.Error()
	if !strings.Contains(msg, "compile:") {
		t.Errorf("expected wrapped compile error, got %q", msg)
	}
}

func TestEvalAgainstSyntheticContext(t *testing.T) {
	e := New()
	prog, err := e.Compile(`container.securityContext.privileged == true`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ctx := api.Context{
		Env: map[string]any{
			"container": map[string]any{
				"name": "main",
				"securityContext": map[string]any{
					"privileged": true,
				},
			},
		},
	}
	got, err := prog.Eval(ctx)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if !got {
		t.Errorf("expected true")
	}

	// flip
	ctx.Env["container"].(map[string]any)["securityContext"].(map[string]any)["privileged"] = false
	got, err = prog.Eval(ctx)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got {
		t.Errorf("expected false")
	}
}

func TestMatchesHelper(t *testing.T) {
	e := New()
	// expr-lang has a built-in `matches` operator; we use both that and our
	// regexMatch function-call helper to confirm both paths work.
	prog, err := e.Compile(`container.image.name matches "^nginx.*" && regexMatch(container.image.name, "ingress$")`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ctx := api.Context{
		Env: map[string]any{
			"container": map[string]any{
				"image": map[string]any{
					"name": "nginx-ingress",
				},
			},
		},
	}
	got, err := prog.Eval(ctx)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if !got {
		t.Errorf("expected match")
	}

	ctx.Env["container"].(map[string]any)["image"].(map[string]any)["name"] = "redis"
	got, err = prog.Eval(ctx)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got {
		t.Errorf("expected no match")
	}
}

func TestMatchesInvalidPattern(t *testing.T) {
	e := New()
	prog, err := e.Compile(`regexMatch("anything", "(((bad")`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = prog.Eval(api.Context{})
	if err == nil {
		t.Fatalf("expected runtime error for invalid pattern")
	}
}

func TestNonBoolExpressionFails(t *testing.T) {
	e := New()
	// expr.AsBool() ensures Compile rejects non-bool expressions at compile time.
	_, err := e.Compile(`"not-a-bool"`)
	if err == nil {
		t.Fatalf("expected compile error for non-bool expression")
	}
}

func TestEvalUsesObjectFallbackWhenEnvEmpty(t *testing.T) {
	e := New()
	prog, err := e.Compile(`object.spec.replicas > 1`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	obj := &unstructured.Unstructured{}
	obj.Object = map[string]any{
		"spec": map[string]any{
			"replicas": 3,
		},
	}
	got, err := prog.Eval(api.Context{Object: obj})
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if !got {
		t.Errorf("expected true")
	}
}

func TestStartsWithBuiltin(t *testing.T) {
	e := New()
	prog, err := e.Compile(`container.image.name startsWith "gcr.io/"`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ctx := api.Context{
		Env: map[string]any{
			"container": map[string]any{
				"image": map[string]any{"name": "gcr.io/foo/bar"},
			},
		},
	}
	got, err := prog.Eval(ctx)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if !got {
		t.Errorf("expected true")
	}
}
