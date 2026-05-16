package api

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ExpressionEngine compiles rule expressions into evaluable Programs. expr-lang
// is the default implementation; CEL/Rego/starlark can be added in v3 without
// touching the rule schema.
type ExpressionEngine interface {
	// Name is "expr", "cel", "rego" — used in metrics and the Rule's selector.
	Name() string
	// Compile parses one rule expression. Diagnostics include line/column when
	// available so PortalClusterRule.status.parseError is human-readable.
	Compile(expression string) (Program, error)
}

// Program is a compiled, reusable rule expression. It MUST be safe for
// concurrent Eval calls.
type Program interface {
	Eval(ctx Context) (bool, error)
}

// RuleEngine dispatches a Context to every rule indexed under that GVK and
// returns the produced Violations.
type RuleEngine interface {
	Evaluate(ctx Context, meta EventMeta) []Violation
}

// RuleIndex is the read-only view of the rule store that the engine consults.
// Loaders (folder, CR) write into a concrete index that implements this.
type RuleIndex interface {
	// ForGVK returns the rules whose Match.GVK includes gvk, filtered by enabled.
	ForGVK(gvk schema.GroupVersionKind) []Rule
	// All returns every enabled rule. Used for static dependency extraction.
	All() []Rule
}
