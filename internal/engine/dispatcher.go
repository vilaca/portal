// Package engine is the default api.RuleEngine. It indexes rules by GVK at
// construction, compiles each rule's expression once, and on Evaluate() walks
// the rules registered for the Context's GVK and produces api.Violations.
//
// Compile errors are not fatal. Rules whose expressions fail to compile are
// skipped at evaluation time and recorded in an internal error map keyed by
// rule name. The CRD status reconciler (later wave) reads these via
// ParseError(rule) / CompileErrors().
//
// Multi-container iteration is the responsibility of the ContextBuilder. The
// engine evaluates each (rule, Context) pair exactly once.
package engine

import (
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vilaca/portal/internal/api"
)

// New constructs a RuleEngine by compiling every rule in idx and indexing the
// result by GVK. A nil idx or nil eng is a programming error.
func New(idx api.RuleIndex, eng api.ExpressionEngine) (api.RuleEngine, error) {
	if idx == nil {
		return nil, fmt.Errorf("engine.New: nil RuleIndex")
	}
	if eng == nil {
		return nil, fmt.Errorf("engine.New: nil ExpressionEngine")
	}
	d := &dispatcher{
		byGVK: map[schema.GroupVersionKind][]compiledRule{},
	}
	for _, r := range idx.All() {
		prog, err := eng.Compile(r.Expression)
		if err != nil {
			d.compileErrors.Store(r.Name, err.Error())
			continue
		}
		cr := compiledRule{rule: r, prog: prog}
		for _, gvk := range r.Match.GVK {
			d.byGVK[gvk] = append(d.byGVK[gvk], cr)
		}
	}
	return d, nil
}

type compiledRule struct {
	rule api.Rule
	prog api.Program
}

type dispatcher struct {
	byGVK         map[schema.GroupVersionKind][]compiledRule
	compileErrors sync.Map // rule name -> error string
}

// ParseError returns the compile error message for rule, or "" if the rule
// compiled cleanly (or doesn't exist).
func (d *dispatcher) ParseError(rule string) string {
	v, ok := d.compileErrors.Load(rule)
	if !ok {
		return ""
	}
	return v.(string)
}

// CompileErrors returns a snapshot of every compile error keyed by rule name.
func (d *dispatcher) CompileErrors() map[string]string {
	out := map[string]string{}
	d.compileErrors.Range(func(k, v any) bool {
		out[k.(string)] = v.(string)
		return true
	})
	return out
}

// Evaluate runs every rule registered for ctx.GVK against ctx and returns the
// produced Violations. Rules with namespace filters that don't match are
// skipped. Rules whose Eval returns an error are skipped — the error is
// recorded under the same compileErrors map (prefix "eval:") so test code can
// observe it.
func (d *dispatcher) Evaluate(ctx api.Context, meta api.EventMeta) []api.Violation {
	rules := d.byGVK[ctx.GVK]
	if len(rules) == 0 {
		return nil
	}
	ns, name := metadataFromObject(ctx)
	mode := modeFromSource(meta.Source)
	container := containerNameFromEnv(ctx.Env)

	out := make([]api.Violation, 0, len(rules))
	for _, cr := range rules {
		if !namespaceAllowed(cr.rule.Match.Namespaces, ns) {
			continue
		}
		ok, err := cr.prog.Eval(ctx)
		if err != nil {
			d.compileErrors.Store(cr.rule.Name, "eval: "+err.Error())
			continue
		}
		if !ok {
			continue
		}
		out = append(out, buildViolation(cr.rule, ctx, meta, mode, ns, name, container))
	}
	return out
}

func buildViolation(
	rule api.Rule,
	ctx api.Context,
	meta api.EventMeta,
	mode api.Mode,
	ns, name, container string,
) api.Violation {
	at := meta.At
	if at.IsZero() {
		at = time.Now()
	}

	// Pre-merge the alert: shorthand with the rule's action list.
	var actions []api.ActionSpec
	if rule.Alert != "" {
		actions = append(actions, api.ActionSpec{
			Type:   "alertmanager",
			Params: map[string]any{"template": rule.Alert},
		})
	}
	actions = append(actions, rule.Actions...)

	enforcement := api.EnforcementAction("")
	if mode == api.ModeAdmission {
		enforcement = rule.EnforcementAction
	}

	src := api.ViolationSource{
		EventID:   meta.EventID,
		Operation: meta.Operation,
		Container: container,
	}
	if ctx.Request != nil {
		src.Username = ctx.Request.UserInfo.Username
	}

	return api.Violation{
		Rule:              rule.Name,
		Severity:          rule.Severity,
		GVK:               ctx.GVK,
		Namespace:         ns,
		Name:              name,
		Mode:              mode,
		Message:           "rule violated: " + rule.Name,
		At:                at,
		EnforcementAction: enforcement,
		Actions:           actions,
		Source:            src,
	}
}

// metadataFromObject pulls namespace and name off ctx.Object.
func metadataFromObject(ctx api.Context) (ns, name string) {
	if ctx.Object == nil {
		return "", ""
	}
	return ctx.Object.GetNamespace(), ctx.Object.GetName()
}

// containerNameFromEnv pulls container.name out of the expr env, if present.
func containerNameFromEnv(env map[string]any) string {
	if env == nil {
		return ""
	}
	c, ok := env["container"].(map[string]any)
	if !ok || c == nil {
		return ""
	}
	n, _ := c["name"].(string)
	return n
}

// modeFromSource maps EventMeta.Source to an api.Mode.
func modeFromSource(src string) api.Mode {
	switch src {
	case "admission":
		return api.ModeAdmission
	case "audit":
		return api.ModeAudit
	case "network":
		return api.ModeNetwork
	default:
		return api.Mode(src)
	}
}

// namespaceAllowed implements the include/exclude semantics on Matcher.Namespaces.
func namespaceAllowed(sel api.NamespaceSelector, ns string) bool {
	if len(sel.Include) > 0 {
		for _, n := range sel.Include {
			if n == ns {
				return true
			}
		}
		return false
	}
	if len(sel.Exclude) > 0 {
		for _, n := range sel.Exclude {
			if n == ns {
				return false
			}
		}
	}
	return true
}
