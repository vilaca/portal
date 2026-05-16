# Side-by-side: SpEL → expr-lang

This page catalogues the syntactic differences between `podwatcher-poc`'s SpEL rules and Portal's expr-lang rules. The transformations are implemented in `internal/rule/migrate/migrate.go`; this doc is its user-facing companion.

## Expression syntax

| SpEL (`podwatcher-poc`) | expr-lang (Portal) | Notes |
|---|---|---|
| `{a, b, c}.contains(x)` | `x in [a, b, c]` | brace-set → bracket-list; the migrator wraps in parens to preserve unary `!` precedence: `(x in [a, b, c])` |
| `{'a', 'b'}.contains(x)` | `x in ['a', 'b']` | quoted literals preserved |
| `foo.contains('y')` | `'y' in foo` | bare-receiver form; the migrator inverts subject and predicate |
| `foo?.bar` | `foo?.bar` | safe-nav is **identical** in expr-lang — no rewrite |
| `foo ?: 'default'` | `foo ?? 'default'` | SpEL Elvis vs expr-lang null-coalesce; not auto-rewritten today, do it by hand |
| `.startsWith('p')` / `.endsWith('s')` | same | expr-lang ships these in stdlib |
| `T(java.util.Arrays).asList(...)` | **no equivalent** | migrator emits a warning; rewrite with `[...]` literal |
| `#root` | **no equivalent** | migrator emits a warning; rewrite using Portal's env (`object`, `container`, `spec`, ...) |

The two transformations the migrator actually performs come from `internal/rule/migrate/migrate.go`:

```go
// {…}.contains(arg) — non-greedy brace inner, forbids nesting
reBraceSetContains = regexp.MustCompile(`\{([^{}]*)\}\.contains\(([^)]+)\)`)

// receiver.contains('literal') — receiver is a dotted path with optional ?.
reReceiverContains = regexp.MustCompile(
    `([A-Za-z_][A-Za-z0-9_]*(?:(?:\?\.|\.)[A-Za-z_][A-Za-z0-9_]*)*)\.contains\(\s*('[^']*'|"[^"]*")\s*\)`)
```

The brace-set rewrite **must** run before the bare-receiver rewrite so `{a,b}.contains(x)` doesn't get matched as `{a,b}` (receiver) `.contains(x)` and produce invalid `x in {a,b}`.

## Schema differences

| `podwatcher-poc` field | Portal field | Notes |
|---|---|---|
| `filter.namespace: ns` | `match.namespaces.include: [ns]` | migrator handles scalar and map shapes |
| `filter.namespace: { include: [...], exclude: [...] }` | `match.namespaces: { include: [...], exclude: [...] }` | direct lift |
| _(implicit Pod scope)_ | `match.gvk: [{ group: "", version: v1, kind: Pod }]` | backfilled; podwatcher-poc was pod-only |
| _(implicit alert-only mode)_ | `mode: [admission, audit]` | backfilled; you can narrow afterwards |
| _(no admission concept)_ | `enforcementAction: warn` | backfilled; choose `deny`/`warn`/`dryrun` once you've validated the rule |

## What you might want to hand-edit after migration

The migrator preserves observability semantics (`warn`, both modes). You should consciously tighten:

- **`enforcementAction: deny`** — once a rule is proven, flip it from `warn` to deny so admission actually blocks.
- **`mode: [admission]`** — admission-only is cheaper than audit if the rule applies only to fresh objects.
- **`match.gvk`** — extend beyond `Pod` to cover `Deployment`, `StatefulSet`, etc. so violations are caught at the workload level, not on every spawned pod.

For the full rule schema see `../reference/rule-schema.md` (parallel author).
