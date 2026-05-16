// Package exprlang is the default api.ExpressionEngine implementation, backed
// by github.com/expr-lang/expr.
//
// It compiles a rule expression once into a reusable Program. The Program is
// safe for concurrent Eval calls because expr-lang's compiled VM programs are
// immutable and the env map produced per evaluation is fresh.
//
// The engine registers itself with api at package init() under the name "expr".
// Rules that omit an explicit engine selector default to this engine.
//
// Env contract:
//
//	The engine assumes the env map produced by api.ContextBuilders. For
//	pod-shaped contexts the keys are: container, spec, securityContext,
//	metadata, object, request. For generic contexts: object, metadata,
//	request. Expressions must tolerate nil values (use the ?. operator).
//
// Helper functions registered into every program:
//
//	regexMatch(s, pattern string) bool   — Go regexp partial-match on s.
//
// expr-lang's stdlib already provides startsWith, endsWith, contains, and a
// `matches` *operator* (s matches "pattern") that compiles to the same Go
// regexp engine. We expose regexMatch in function-call form for symmetry, but
// rule authors are encouraged to use the built-in operator.
package exprlang

import (
	"fmt"
	"regexp"
	"sync"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"

	"github.com/vilaca/portal/internal/api"
)

func init() {
	api.RegisterEngine("expr", func() api.ExpressionEngine { return New() })
}

// New returns a fresh ExpressionEngine.
func New() api.ExpressionEngine { return &engine{} }

type engine struct{}

func (e *engine) Name() string { return "expr" }

// Compile parses one rule expression into a reusable Program. The returned
// error includes line/column info when expr-lang surfaces it.
func (e *engine) Compile(expression string) (api.Program, error) {
	prog, err := expr.Compile(expression,
		expr.AsBool(),
		expr.AllowUndefinedVariables(),
		expr.Function("regexMatch", regexMatchFn, new(func(string, string) bool)),
	)
	if err != nil {
		return nil, fmt.Errorf("compile: %w", err)
	}
	return &program{prog: prog}, nil
}

type program struct {
	prog *vm.Program
}

// Eval runs the compiled program against the env layered into ctx.
func (p *program) Eval(ctx api.Context) (bool, error) {
	env := envFromContext(ctx)
	out, err := expr.Run(p.prog, env)
	if err != nil {
		return false, fmt.Errorf("eval: %w", err)
	}
	b, ok := out.(bool)
	if !ok {
		return false, fmt.Errorf("expression did not produce bool, got %T", out)
	}
	return b, nil
}

// envFromContext renders the expr-lang env. If the ContextBuilder already put
// a non-empty Env on the Context, we use it; otherwise we synthesise the
// generic env from Object + Request.
func envFromContext(ctx api.Context) map[string]any {
	if len(ctx.Env) > 0 {
		// Return a shallow copy so the caller can't be mutated by helpers.
		out := make(map[string]any, len(ctx.Env))
		for k, v := range ctx.Env {
			out[k] = v
		}
		return out
	}
	out := map[string]any{
		"object":          nil,
		"metadata":        nil,
		"request":         nil,
		"container":       nil,
		"spec":            nil,
		"securityContext": nil,
	}
	if ctx.Object != nil {
		out["object"] = ctx.Object.Object
		if md, found, _ := unstructuredGet(ctx.Object.Object, "metadata"); found {
			out["metadata"] = md
		}
	}
	if ctx.Request != nil {
		out["request"] = map[string]any{
			"operation": ctx.Request.Operation,
			"dryRun":    ctx.Request.DryRun,
			"userInfo": map[string]any{
				"username": ctx.Request.UserInfo.Username,
				"uid":      ctx.Request.UserInfo.UID,
				"groups":   ctx.Request.UserInfo.Groups,
				"extra":    ctx.Request.UserInfo.Extra,
			},
		}
	}
	return out
}

func unstructuredGet(m map[string]any, key string) (any, bool, error) {
	if m == nil {
		return nil, false, nil
	}
	v, ok := m[key]
	return v, ok, nil
}

// regexMatchFn is the implementation of regexMatch(s, pattern). The cache is
// global because rule expressions tend to reuse the same regex shape across
// many evaluations and recompiling every Eval is wasteful.
var (
	regexCacheMu sync.RWMutex
	regexCache   = map[string]*regexp.Regexp{}
)

func compileRegex(pattern string) (*regexp.Regexp, error) {
	regexCacheMu.RLock()
	re, ok := regexCache[pattern]
	regexCacheMu.RUnlock()
	if ok {
		return re, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	regexCacheMu.Lock()
	regexCache[pattern] = re
	regexCacheMu.Unlock()
	return re, nil
}

func regexMatchFn(params ...any) (any, error) {
	if len(params) != 2 {
		return nil, fmt.Errorf("regexMatch: expected 2 args, got %d", len(params))
	}
	s, ok := params[0].(string)
	if !ok {
		return nil, fmt.Errorf("regexMatch: first arg must be string, got %T", params[0])
	}
	pat, ok := params[1].(string)
	if !ok {
		return nil, fmt.Errorf("regexMatch: second arg must be string, got %T", params[1])
	}
	re, err := compileRegex(pat)
	if err != nil {
		return nil, fmt.Errorf("regexMatch: invalid pattern %q: %w", pat, err)
	}
	return re.MatchString(s), nil
}
