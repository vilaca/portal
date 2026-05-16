# Migrating from `podwatcher-poc`

This guide walks through replacing a `podwatcher-poc` Java/SpEL scanner with Portal v1. It assumes you already have Portal installed (see `../getting-started/install-helm.md`).

The end state: every `podwatcher-poc` rule is running inside Portal as a `PortalClusterRule`, AlertManager keeps receiving the same JSON, and the old scanner Deployment can be deleted.

## What carries over byte-for-byte

| Source | Target | Treatment |
|---|---|---|
| `examples/rules/*.yaml` | `PortalClusterRule` manifests | rewritten by `portal migrate-rules` |
| AlertManager JSON shape | unchanged | `internal/sink/alertmanager` preserves the wire shape |
| `filter.namespace` field | `match.namespaces.include` | rewritten by the migrator |
| Prometheus counters | renamed (`portal_*` prefix) | document new names in your dashboards |

Per the design (see `POC-TO-PRODUCTION.md` "Language pivot"): of the six example rules in `podwatcher-poc`, **four translate byte-identical** and **two need the `.contains()` → `in` swap**, which `portal migrate-rules` performs automatically.

## Step 1 — Run `portal migrate-rules`

The default output format is `cr` (Custom Resources). Point it at your existing rule folder:

```bash
portal migrate-rules /path/to/podwatcher-poc/examples/rules/ \
    --format=cr \
    -o ./migrated-rules/
```

This produces one `PortalClusterRule` manifest per input rule, named after a slugified version of `metadata.name`. The migrator is idempotent — running it again on its output produces the same bytes.

For each input file the tool:

1. **Rewrites the expression** with regex passes:
   - `{a,b,c}.contains(x)` → `(x in [a,b,c])`
   - `foo.contains('y')` → `('y' in foo)`
2. **Rewrites the schema**:
   - `filter.namespace: ns` → `match.namespaces.include: [ns]`
3. **Backfills defaults** so the output is a valid Portal rule:
   - `match.gvk` → `[{group: "", version: v1, kind: Pod}]` (podwatcher-poc was pod-only)
   - `mode` → `[admission, audit]`
   - `enforcementAction` → `warn` (preserves "observe, don't block" semantics)

Source: `internal/rule/migrate/migrate.go`.

## Step 2 — Inspect the outputs

```bash
ls ./migrated-rules/
diff -r /path/to/podwatcher-poc/examples/rules/ ./migrated-rules/   # for shape comparison
```

Skim the migration warnings emitted on stderr. Warnings are not errors — the migrator never fails on rewritten SpEL — but they flag constructs that have no automatic equivalent:

- **`T(java.util.Arrays)`** and other SpEL Java-type references — Portal/expr-lang has no equivalent. Rewrite the rule by hand or replace with an inline list literal.
- **`#root`** — SpEL's context reference. expr-lang's env shape is different; consult `../reference/expression-language.md`.

For syntax-level differences see `side-by-side-rule-syntax.md`.

## Step 3 — Apply the rules

```bash
kubectl apply -f ./migrated-rules/
```

The CRD has an OpenAPI structural schema; the API server rejects malformed manifests immediately. expr-lang compile errors are surfaced later by Portal itself (see step 4).

## Step 4 — Verify `.status.parseError` is empty

Portal's rule-CR reconciler writes parse and evaluation diagnostics back into the CR's `.status`:

```bash
kubectl get portalclusterrule -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.parseError}{"\n"}{end}'
```

Every line should show only the rule name (empty parse error). If a rule compiled under SpEL but the migrator couldn't translate it cleanly, you'll see the expr-lang compiler error here. Fix the expression in the manifest, `kubectl apply -f` again, and watch the status clear within ~1 s (the reconciler runs on every CR update).

## Step 5 — Decommission `podwatcher-poc`

Once Portal is admitting and auditing with the migrated rule set, retire the old scanner:

```bash
kubectl delete deployment podwatcher-poc -n <its-namespace>
```

Portal's AlertManager output preserves `podwatcher-poc`'s JSON byte-for-byte, so your existing Prometheus routing keeps working without changes. Cross-reference: `internal/sink/alertmanager/`.

## What you gain by moving

- Admission-time enforcement (deny/warn/dryrun) — not just observability.
- Sub-second audit propagation (informers, not polling).
- Static NetworkPolicy analysis through the same pipeline.
- A response-action engine (`label`, `annotate`, `evict`, `patch-networkpolicy`, `revoke-sa-token`).
- PolicyReport CRD output for ecosystem tools.

For a full feature comparison see `../comparison/podwatcher-comparison.md`.
