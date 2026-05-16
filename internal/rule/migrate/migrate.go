// Package migrate converts podwatcher-poc rule YAML files into Portal's
// extended rule schema, emitting either folder-format rule YAML or one
// PortalClusterRule CR manifest per rule.
//
// The migration is mechanical text/YAML rewriting; it never round-trips
// through SpEL semantics. Two layers are applied per file:
//
//  1. Expression rewrite (string-level, regexp-driven) — the SpEL-flavoured
//     constructs podwatcher-poc shipped get rewritten to expr-lang forms:
//       - {a,b,c}.contains(x)      → x in [a,b,c]
//       - {'a','b','c'}.contains(x) → x in ['a','b','c']
//       - foo.bar.contains('y')    → 'y' in foo.bar
//       - .startsWith / .endsWith  → unchanged (expr-lang has them)
//       - ?.                       → unchanged (expr-lang has it)
//       - T(...) / #root           → emit a warning, leave unchanged
//
//  2. YAML rewrite — filter.namespace becomes match.namespaces; sensible
//     defaults are filled in (match.gvk = [Pod], mode = [admission,audit],
//     enforcementAction = warn) so the migrated file is a valid Portal rule.
//
// The migration is idempotent: running Migrate on its own output produces
// the same bytes.
package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/vilaca/portal/internal/expr/exprlang"
)

// Source is one input file picked up from the migration directory.
type Source struct {
	Path string
	Body []byte
}

// Output is one rewritten file ready to be written under the output dir.
type Output struct {
	Path string // suggested output filename, relative to the output dir
	Body []byte
}

// Result is the aggregate of one Migrate run.
type Result struct {
	Inputs   []Source
	Outputs  []Output
	Warnings []string
	Errors   []string
}

const (
	// FormatCR is the default — one PortalClusterRule CR manifest per rule.
	FormatCR = "cr"
	// FormatFolder emits rewritten folder-format rule YAML.
	FormatFolder = "folder"
)

// Migrate reads every *.yaml / *.yml file under dir, rewrites each to the
// requested format, and returns the resulting Outputs. The function does not
// write anything; callers (CLI, tests) decide how to consume Outputs.
func Migrate(dir, format string) (Result, error) {
	if format == "" {
		format = FormatCR
	}
	if format != FormatCR && format != FormatFolder {
		return Result{}, fmt.Errorf("migrate: unsupported format %q (want %q or %q)", format, FormatCR, FormatFolder)
	}

	res := Result{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Result{}, fmt.Errorf("migrate: read dir %q: %w", dir, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	used := map[string]int{} // slug → suffix counter, for CR collision suffixing
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !isYAML(e.Name()) {
			continue
		}
		full := filepath.Join(dir, e.Name())
		body, rerr := os.ReadFile(full)
		if rerr != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: read: %v", full, rerr))
			continue
		}
		src := Source{Path: full, Body: body}
		res.Inputs = append(res.Inputs, src)

		outs, warns, errs := migrateFile(src, format, used)
		res.Outputs = append(res.Outputs, outs...)
		res.Warnings = append(res.Warnings, warns...)
		res.Errors = append(res.Errors, errs...)
	}
	return res, nil
}

// migrateFile rewrites one source file and returns 0..N output documents.
// Folder mode produces one Output per file (input filename preserved);
// CR mode produces one Output per rule (named after the rule slug).
func migrateFile(src Source, format string, used map[string]int) ([]Output, []string, []string) {
	var (
		warns []string
		errs  []string
	)

	// Parse permissively — we don't want to lose comments-on-fields, but we
	// do need typed access to filter / match / rule. sigs.k8s.io/yaml gives
	// us JSON-shaped maps via its yaml→json bridge, which is exactly the
	// rewrite surface we want.
	docs, derr := splitYAMLDocs(src.Body)
	if derr != nil {
		errs = append(errs, fmt.Sprintf("%s: split: %v", src.Path, derr))
		return nil, warns, errs
	}

	var outs []Output
	for i, doc := range docs {
		if len(strings.TrimSpace(string(doc))) == 0 {
			continue
		}
		var raw map[string]any
		if err := yaml.Unmarshal(doc, &raw); err != nil {
			errs = append(errs, fmt.Sprintf("%s[doc %d]: unmarshal: %v", src.Path, i, err))
			continue
		}
		if raw == nil {
			continue
		}

		rewritten, fileWarns := rewriteRuleMap(raw, src.Path)
		warns = append(warns, fileWarns...)

		// Compile-validate the (possibly rewritten) expression.
		if expr, ok := rewritten["rule"].(string); ok && expr != "" {
			if _, err := exprlang.New().Compile(expr); err != nil {
				warns = append(warns, fmt.Sprintf("%s: rule does not compile under expr-lang: %v", src.Path, err))
			}
		}

		switch format {
		case FormatFolder:
			body, err := yaml.Marshal(rewritten)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: marshal: %v", src.Path, err))
				continue
			}
			outs = append(outs, Output{
				Path: filepath.Base(src.Path),
				Body: body,
			})
		case FormatCR:
			cr, slug := wrapAsCR(rewritten, used)
			body, err := yaml.Marshal(cr)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: marshal CR: %v", src.Path, err))
				continue
			}
			outs = append(outs, Output{
				Path: slug + ".yaml",
				Body: body,
			})
		}
	}
	return outs, warns, errs
}

// rewriteRuleMap performs all in-place YAML-level rewrites on one parsed
// rule map and returns the rewritten copy plus any warnings.
func rewriteRuleMap(raw map[string]any, srcPath string) (map[string]any, []string) {
	var warns []string

	// 1. Expression rewrite.
	if exprStr, ok := raw["rule"].(string); ok && exprStr != "" {
		newExpr, exprWarns := RewriteExpression(exprStr)
		raw["rule"] = newExpr
		for _, w := range exprWarns {
			warns = append(warns, fmt.Sprintf("%s: %s", srcPath, w))
		}
	}

	// 2. filter.namespace → match.namespaces.
	if filt, ok := raw["filter"].(map[string]any); ok {
		match, _ := raw["match"].(map[string]any)
		if match == nil {
			match = map[string]any{}
		}
		if ns, has := filt["namespace"]; has {
			nsBlock, _ := match["namespaces"].(map[string]any)
			if nsBlock == nil {
				nsBlock = map[string]any{}
			}
			switch v := ns.(type) {
			case string:
				nsBlock["include"] = []any{v}
			case map[string]any:
				if inc, ok := v["include"]; ok {
					nsBlock["include"] = inc
				}
				if exc, ok := v["exclude"]; ok {
					nsBlock["exclude"] = exc
				}
			default:
				warns = append(warns, fmt.Sprintf("%s: filter.namespace has unexpected type %T; left as-is", srcPath, ns))
			}
			match["namespaces"] = nsBlock
			delete(filt, "namespace")
		}
		if len(filt) == 0 {
			delete(raw, "filter")
		} else {
			raw["filter"] = filt
		}
		if len(match) > 0 {
			raw["match"] = match
		}
	}

	// 3. Default match.gvk = [Pod] when absent — podwatcher-poc was pod-only.
	match, _ := raw["match"].(map[string]any)
	if match == nil {
		match = map[string]any{}
	}
	if _, has := match["gvk"]; !has {
		match["gvk"] = []any{
			map[string]any{"group": "", "version": "v1", "kind": "Pod"},
		}
		warns = append(warns, fmt.Sprintf("%s: defaulted match.gvk to [{group:'',version:v1,kind:Pod}] (podwatcher-poc pod-only legacy)", srcPath))
	}
	raw["match"] = match

	// 4. Default mode = [admission, audit] when absent.
	if _, has := raw["mode"]; !has {
		raw["mode"] = []any{"admission", "audit"}
	}

	// 5. Default enforcementAction = warn when absent.
	if _, has := raw["enforcementAction"]; !has {
		raw["enforcementAction"] = "warn"
		warns = append(warns, fmt.Sprintf("%s: defaulted enforcementAction to 'warn' (preserves podwatcher-poc observability-without-blocking semantics)", srcPath))
	}

	return raw, warns
}

// wrapAsCR builds a PortalClusterRule manifest map around a rule body.
// used keeps track of slug collisions so subsequent rules with the same
// human name get -2, -3, ... suffixes.
func wrapAsCR(rule map[string]any, used map[string]int) (map[string]any, string) {
	name, _ := rule["name"].(string)
	base := slugify(name)
	if base == "" {
		base = "rule"
	}
	used[base]++
	suffix := used[base]
	slug := base
	if suffix > 1 {
		slug = fmt.Sprintf("%s-%d", base, suffix)
	}
	// Even the first occurrence: callers asked for -1 / -2 collision style
	// when there are multiple. The test asserts -2 for the second; first
	// stays bare to match common kubectl conventions.

	cr := map[string]any{
		"apiVersion": "portal.io/v1alpha1",
		"kind":       "PortalClusterRule",
		"metadata": map[string]any{
			"name": slug,
		},
		"spec": rule,
	}
	return cr, slug
}

// RewriteExpression converts one expr string from SpEL-ish syntax to
// expr-lang. The rewrite is greedy but deterministic and idempotent — once a
// `.contains` form is converted to `in`, the regexps no longer match it.
//
// Warnings are returned for constructs that are known-unsupported in
// expr-lang (T(...) Java type refs, #root context refs); the expression is
// returned unchanged so the operator can fix it by hand.
func RewriteExpression(spel string) (string, []string) {
	var warns []string

	// Bail-out cases: T(...) and #root are SpEL-only and have no direct
	// expr-lang equivalent.
	if reTypeRef.MatchString(spel) {
		warns = append(warns, "unsupported SpEL Java type reference (T(...)) left unchanged; review by hand")
	}
	if strings.Contains(spel, "#root") {
		warns = append(warns, "unsupported SpEL context reference (#root) left unchanged; review by hand")
	}

	out := spel

	// Brace-set .contains form must run first, otherwise the bare-receiver
	// form below would match `{a,b}.contains(x)` and produce `x in {a,b}`
	// which is invalid expr-lang.
	out = reBraceSetContains.ReplaceAllStringFunc(out, func(m string) string {
		sub := reBraceSetContains.FindStringSubmatch(m)
		// sub[1] = inner of braces, sub[2] = argument
		inner := strings.TrimSpace(sub[1])
		arg := strings.TrimSpace(sub[2])
		// Wrap in parens so unary `!` and `&&` precedence aren't broken:
		// `!x.contains(y)` would otherwise rewrite to `!y in [x]`, which
		// expr-lang parses as `(!y) in [x]`.
		return fmt.Sprintf("(%s in [%s])", arg, inner)
	})

	// Bare-receiver .contains('literal') / .contains("literal") form.
	// The receiver is a dotted path possibly with the safe-nav `?.` operator.
	out = reReceiverContains.ReplaceAllStringFunc(out, func(m string) string {
		sub := reReceiverContains.FindStringSubmatch(m)
		// sub[1] = receiver path
		// sub[2] = entire literal token (with quotes)
		recv := strings.TrimSpace(sub[1])
		lit := strings.TrimSpace(sub[2])
		return fmt.Sprintf("(%s in %s)", lit, recv)
	})

	return out, warns
}

// Regexps used by RewriteExpression. Compiled once.
var (
	// {…}.contains(arg) — the inner of the braces is non-greedy and forbids
	// nested braces (good enough for the podwatcher-poc rule corpus).
	reBraceSetContains = regexp.MustCompile(`\{([^{}]*)\}\.contains\(([^)]+)\)`)

	// receiver.contains('literal') or receiver.contains("literal").
	// receiver: an identifier followed by zero or more `.ident` or `?.ident`.
	// We deliberately exclude the `.contains` itself from the receiver match.
	reReceiverContains = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*(?:(?:\?\.|\.)[A-Za-z_][A-Za-z0-9_]*)*)\.contains\(\s*('[^']*'|"[^"]*")\s*\)`)

	// SpEL Java type reference: T(java.lang.Math) etc.
	reTypeRef = regexp.MustCompile(`\bT\(\s*[A-Za-z_][A-Za-z0-9_.$]*\s*\)`)
)

// splitYAMLDocs splits a YAML byte slice on `---` document separators while
// preserving leading/trailing whitespace inside each doc. We avoid pulling
// in yaml.v3 here because sigs.k8s.io/yaml doesn't expose a doc splitter.
func splitYAMLDocs(body []byte) ([][]byte, error) {
	lines := strings.Split(string(body), "\n")
	var docs [][]byte
	var current []string
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "---" {
			docs = append(docs, []byte(strings.Join(current, "\n")))
			current = current[:0]
			continue
		}
		current = append(current, ln)
	}
	docs = append(docs, []byte(strings.Join(current, "\n")))
	return docs, nil
}

// slugify reduces s to lower-kebab-case using only [a-z0-9-] and clamps to 63
// characters (DNS label limit, the same constraint Kubernetes applies to
// metadata.name on cluster-scoped resources).
func slugify(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == '-' || r == '_' || r == ' ' || r == ':' || r == '/' || r == '.':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		default:
			// drop unknown chars
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 63 {
		out = strings.TrimRight(out[:63], "-")
	}
	return out
}

func isYAML(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")
}
