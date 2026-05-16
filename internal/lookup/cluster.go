package lookup

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"

	"github.com/vilaca/portal/internal/api"
)

// AuditCache is the tiny shape this package depends on. The audit controller
// satisfies it.
type AuditCache interface {
	Lister(gvk schema.GroupVersionKind) (cache.GenericLister, error)
	SharedInformerFactory() dynamicinformer.DynamicSharedInformerFactory
}

// clusterLookup implements api.Lookup over an AuditCache.
type clusterLookup struct {
	audit AuditCache
}

// New constructs an api.Lookup backed by audit's shared informer caches.
func New(audit AuditCache) api.Lookup {
	return &clusterLookup{audit: audit}
}

// ByName fetches a single object from the informer cache for gvk. Returns
// (nil, nil) when the object is not found; an error when the GVK is not
// watched or the underlying lister fails.
func (l *clusterLookup) ByName(gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error) {
	if l == nil || l.audit == nil {
		return nil, errors.New("lookup: nil AuditCache")
	}
	lister, err := l.audit.Lister(gvk)
	if err != nil {
		return nil, err
	}
	var obj runtime.Object
	if namespace == "" {
		obj, err = lister.Get(name)
	} else {
		obj, err = lister.ByNamespace(namespace).Get(name)
	}
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return asUnstructured(obj)
}

// List returns every object of gvk in namespace matching selector. An empty
// namespace means cluster-wide.
func (l *clusterLookup) List(gvk schema.GroupVersionKind, namespace string, selector map[string]string) ([]*unstructured.Unstructured, error) {
	if l == nil || l.audit == nil {
		return nil, errors.New("lookup: nil AuditCache")
	}
	lister, err := l.audit.Lister(gvk)
	if err != nil {
		return nil, err
	}
	sel := labels.Everything()
	if len(selector) > 0 {
		sel = labels.SelectorFromSet(labels.Set(selector))
	}
	var objs []runtime.Object
	if namespace == "" {
		objs, err = lister.List(sel)
	} else {
		objs, err = lister.ByNamespace(namespace).List(sel)
	}
	if err != nil {
		return nil, err
	}
	out := make([]*unstructured.Unstructured, 0, len(objs))
	for _, o := range objs {
		u, err := asUnstructured(o)
		if err != nil || u == nil {
			continue
		}
		out = append(out, u)
	}
	return out, nil
}

// Watched returns the GVKs covered by the shared factory.
//
// We use the factory's underlying informer registry to determine which GVRs
// are present. The factory does not expose its set directly, so callers that
// need the precise GVK set should query the AuditCache through other means.
// We provide a best-effort signal by probing well-known GVKs registered via
// Lister(); since the audit controller already records them, we expose them
// here via a small interface assertion.
func (l *clusterLookup) Watched() []schema.GroupVersionKind {
	if l == nil || l.audit == nil {
		return nil
	}
	if w, ok := l.audit.(interface {
		WatchedGVKs() []schema.GroupVersionKind
	}); ok {
		return w.WatchedGVKs()
	}
	return nil
}

func asUnstructured(o runtime.Object) (*unstructured.Unstructured, error) {
	if o == nil {
		return nil, nil
	}
	if u, ok := o.(*unstructured.Unstructured); ok {
		return u, nil
	}
	// GenericLister may wrap items in *metaonly or similar; coerce via accessor.
	a, err := meta.Accessor(o)
	if err != nil {
		return nil, err
	}
	out := &unstructured.Unstructured{}
	out.SetName(a.GetName())
	out.SetNamespace(a.GetNamespace())
	return out, nil
}

// isNotFound returns true when err is a k8s not-found error or matches the
// substring pattern emitted by cache.GenericLister's NewNotFound.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	type notFounder interface{ Status() metav1.Status }
	if nf, ok := err.(notFounder); ok {
		return nf.Status().Reason == metav1.StatusReasonNotFound
	}
	return strings.Contains(err.Error(), "not found")
}

// ────────────────────────────────────────────────────────────────────────────
// Expr-lang env binding
// ────────────────────────────────────────────────────────────────────────────

// gvkKey renders a GVK as the expr-lang key, e.g. "pods.v1." or
// "networkpolicies.v1.networking.k8s.io".
func gvkKey(gvk schema.GroupVersionKind) string {
	return fmt.Sprintf("%s.%s.%s", strings.ToLower(plural(gvk.Kind)), gvk.Version, gvk.Group)
}

// plural is a naive Kind→resource pluraliser matching audit.defaultResourceForGVK.
func plural(kind string) string {
	r := strings.ToLower(kind)
	if !strings.HasSuffix(r, "s") {
		r += "s"
	}
	return r
}

// ToExprEnv constructs the expr-lang env map for the "cluster" namespace. Each
// watched GVK becomes a pseudo-object with two callables: byName(ns,name) and
// list(ns,selector).
//
// Two keys are emitted per GVK so rule expressions can address resources by
// either the disambiguated flat form (`cluster.<resource>.<version>.<group>`,
// dot-separated single key — needed when two GVKs share a resource name
// across groups) or the short simple-name alias (`cluster.<resource>` —
// natural in expr-lang chain syntax). The alias is added only when the
// simple name isn't already taken by another GVK.
//
// The result is intended to be merged into the per-evaluation env map under the
// key "cluster". Callers that also want strong-consistency lookups should call
// ToConsistentExprEnv(client, watched) and merge it under "consistentCluster".
func ToExprEnv(lookup api.Lookup) map[string]any {
	out := map[string]any{}
	if lookup == nil {
		return out
	}
	for _, gvk := range lookup.Watched() {
		captured := gvk
		obj := pseudoObject(lookup, captured)
		out[gvkKey(gvk)] = obj
		short := strings.ToLower(plural(gvk.Kind))
		if _, taken := out[short]; !taken {
			out[short] = obj
		}
	}
	return out
}

// pseudoObject is the {byName,list} pair for one GVK.
func pseudoObject(l api.Lookup, gvk schema.GroupVersionKind) map[string]any {
	return map[string]any{
		"byName": func(args ...any) any {
			ns, name := twoStringArgs(args)
			u, err := l.ByName(gvk, ns, name)
			if err != nil || u == nil {
				return nil
			}
			return u.Object
		},
		"list": func(args ...any) any {
			ns, sel := nsAndSelectorArgs(args)
			us, err := l.List(gvk, ns, sel)
			if err != nil {
				return nil
			}
			items := make([]any, 0, len(us))
			for _, u := range us {
				if u == nil {
					continue
				}
				items = append(items, u.Object)
			}
			return items
		},
	}
}

func twoStringArgs(args []any) (string, string) {
	var a, b string
	if len(args) > 0 {
		a, _ = args[0].(string)
	}
	if len(args) > 1 {
		b, _ = args[1].(string)
	}
	return a, b
}

func nsAndSelectorArgs(args []any) (string, map[string]string) {
	var ns string
	var sel map[string]string
	if len(args) > 0 {
		ns, _ = args[0].(string)
	}
	if len(args) > 1 {
		switch s := args[1].(type) {
		case map[string]string:
			sel = s
		case map[string]any:
			sel = make(map[string]string, len(s))
			for k, v := range s {
				if vs, ok := v.(string); ok {
					sel[k] = vs
				}
			}
		}
	}
	return ns, sel
}

// ────────────────────────────────────────────────────────────────────────────
// Consistent (live) lookup
// ────────────────────────────────────────────────────────────────────────────

// ConsistentLookup is the strong-consistency variant. Each call is one live
// API round-trip — slower than the cache-backed Lookup. Use for narrow cases
// like uniqueness checks where the cache may be stale.
type ConsistentLookup struct {
	dyn             dynamic.Interface
	resourceForGVK  func(schema.GroupVersionKind) schema.GroupVersionResource
	watched         []schema.GroupVersionKind
}

// NewConsistent constructs a strong-consistency Lookup over a dynamic.Interface.
// If resourceForGVK is nil a naive Kind→resource pluraliser is used.
func NewConsistent(dyn dynamic.Interface, watched []schema.GroupVersionKind, resourceForGVK func(schema.GroupVersionKind) schema.GroupVersionResource) *ConsistentLookup {
	if resourceForGVK == nil {
		resourceForGVK = func(gvk schema.GroupVersionKind) schema.GroupVersionResource {
			return schema.GroupVersionResource{Group: gvk.Group, Version: gvk.Version, Resource: plural(gvk.Kind)}
		}
	}
	cp := make([]schema.GroupVersionKind, len(watched))
	copy(cp, watched)
	return &ConsistentLookup{dyn: dyn, resourceForGVK: resourceForGVK, watched: cp}
}

// ByName implements api.Lookup with a direct API call.
func (c *ConsistentLookup) ByName(gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error) {
	if c == nil || c.dyn == nil {
		return nil, errors.New("consistent: nil dynamic client")
	}
	gvr := c.resourceForGVK(gvk)
	var ri dynamic.ResourceInterface
	if namespace == "" {
		ri = c.dyn.Resource(gvr)
	} else {
		ri = c.dyn.Resource(gvr).Namespace(namespace)
	}
	u, err := ri.Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

// List implements api.Lookup with a direct API call.
func (c *ConsistentLookup) List(gvk schema.GroupVersionKind, namespace string, selector map[string]string) ([]*unstructured.Unstructured, error) {
	if c == nil || c.dyn == nil {
		return nil, errors.New("consistent: nil dynamic client")
	}
	gvr := c.resourceForGVK(gvk)
	lo := metav1.ListOptions{}
	if len(selector) > 0 {
		lo.LabelSelector = labels.SelectorFromSet(labels.Set(selector)).String()
	}
	var ri dynamic.ResourceInterface
	if namespace == "" {
		ri = c.dyn.Resource(gvr)
	} else {
		ri = c.dyn.Resource(gvr).Namespace(namespace)
	}
	ulist, err := ri.List(context.Background(), lo)
	if err != nil {
		return nil, err
	}
	out := make([]*unstructured.Unstructured, 0, len(ulist.Items))
	for i := range ulist.Items {
		out = append(out, &ulist.Items[i])
	}
	return out, nil
}

// Watched mirrors the configured set.
func (c *ConsistentLookup) Watched() []schema.GroupVersionKind {
	cp := make([]schema.GroupVersionKind, len(c.watched))
	copy(cp, c.watched)
	return cp
}

// ToConsistentExprEnv builds the "consistentCluster" pseudo-env from a
// ConsistentLookup.
func ToConsistentExprEnv(c *ConsistentLookup) map[string]any {
	if c == nil {
		return map[string]any{}
	}
	return ToExprEnv(c)
}

// ────────────────────────────────────────────────────────────────────────────
// Static dep extraction
// ────────────────────────────────────────────────────────────────────────────

// ExtractClusterRefs walks the expression's AST and returns every GVK
// referenced via `cluster.<gvk>.byName/list(...)` or
// `consistentCluster.<gvk>.byName/list(...)`. The returned slice is
// de-duplicated and stable-ordered by GVK string.
//
// GVK shape in expressions is `cluster.<resource>.<version>.<group>...`. For
// core group resources the group is empty and authors write `cluster.pods.v1.` —
// note the trailing dot. We accept any non-identifier terminator (e.g.
// `cluster.pods.v1.byName(...)` is the actual call where the empty group is
// implicit). To keep this simple we accept either form:
//   - cluster.<resource>.<version>.<group>.byName/list(...)
//   - cluster.<resource>.<version>.byName/list(...)        (core group)
func ExtractClusterRefs(expression string) ([]schema.GroupVersionKind, error) {
	tree, err := parser.Parse(expression)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	v := &refVisitor{seen: map[schema.GroupVersionKind]struct{}{}}
	node := tree.Node
	ast.Walk(&node, v)
	out := make([]schema.GroupVersionKind, 0, len(v.seen))
	for g := range v.seen {
		out = append(out, g)
	}
	// Deterministic ordering.
	sortGVKs(out)
	return out, nil
}

type refVisitor struct {
	seen map[schema.GroupVersionKind]struct{}
}

func (r *refVisitor) Visit(n *ast.Node) {
	call, ok := (*n).(*ast.CallNode)
	if !ok {
		return
	}
	mn, ok := call.Callee.(*ast.MemberNode)
	if !ok {
		return
	}
	// Method name must be byName or list.
	method, ok := stringProperty(mn.Property)
	if !ok {
		return
	}
	if method != "byName" && method != "list" {
		return
	}
	// Walk back through the member chain collecting identifiers.
	parts := collectChain(mn.Node)
	if len(parts) < 3 {
		return
	}
	root := parts[0]
	if root != "cluster" && root != "consistentCluster" {
		return
	}
	// Possible shapes after stripping root:
	//   [resource, version, group, ...] (group may be multi-dot like networking.k8s.io)
	//   [resource, version]              (core group)
	tail := parts[1:]
	if len(tail) < 2 {
		return
	}
	resource := tail[0]
	version := tail[1]
	group := strings.Join(tail[2:], ".")
	gvk := schema.GroupVersionKind{Group: group, Version: version, Kind: kindFromResource(resource)}
	r.seen[gvk] = struct{}{}
}

// collectChain walks an a.b.c MemberNode tree and returns ["a","b","c"].
func collectChain(n ast.Node) []string {
	var out []string
	cur := n
	for {
		switch v := cur.(type) {
		case *ast.MemberNode:
			s, ok := stringProperty(v.Property)
			if !ok {
				return nil
			}
			out = append([]string{s}, out...)
			cur = v.Node
		case *ast.IdentifierNode:
			out = append([]string{v.Value}, out...)
			return out
		case *ast.ChainNode:
			cur = v.Node
		default:
			return nil
		}
	}
}

func stringProperty(p ast.Node) (string, bool) {
	if s, ok := p.(*ast.StringNode); ok {
		return s.Value, true
	}
	if id, ok := p.(*ast.IdentifierNode); ok {
		return id.Value, true
	}
	return "", false
}

// kindFromResource is the inverse of plural(): "pods" → "Pod",
// "networkpolicies" → "NetworkPolicy". Naïve — production wire-up should use
// a RESTMapper-backed override and pass GVKs explicitly via Watched().
func kindFromResource(r string) string {
	r = strings.TrimSuffix(r, "s")
	switch r {
	case "networkpolicie":
		return "NetworkPolicy"
	case "policie":
		return "Policy"
	case "endpoint":
		return "Endpoints"
	}
	if r == "" {
		return ""
	}
	return strings.ToUpper(r[:1]) + r[1:]
}

func sortGVKs(in []schema.GroupVersionKind) {
	// Tiny insertion sort to avoid an extra sort import in tests.
	for i := 1; i < len(in); i++ {
		j := i
		for j > 0 && in[j-1].String() > in[j].String() {
			in[j-1], in[j] = in[j], in[j-1]
			j--
		}
	}
}
