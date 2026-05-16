# Module boundaries

Portal's modularity is intentional and load-bearing: every layer must be independently buildable, testable, runnable, and disable-able. This page restates the rules.

## One-way dependency graph

Only `internal/api` (the interface/DTO package) is depended on by others. `internal/admission`, `internal/audit`, `internal/network`, `internal/actions/*` never depend on each other; communication is via interfaces declared in `internal/api`:

```
            internal/api
            ↑       ↑
   ┌────────┴───────┴────────┐
   ↑            ↑            ↑
admission     audit       network
actions/*     sink/*      lookup
            engine
            context/*
```

Practical consequences:

- A change to the admission webhook implementation cannot accidentally break the audit loop's compile.
- A new sink can be added without touching admission or audit.
- Tests for one module compile without pulling in client-go, expr-lang, prometheus — interfaces are mockable trivially.
- `go test ./internal/api/...` is the smallest unit; `go test ./internal/admission/...` is the next; the full `go test ./...` is the integration sweep.

If a refactor seems to require breaking the rule, the right move is almost always: move the shared functionality into a new file inside `internal/api`, or expose it via a new interface that both sides implement. The temptation to short-circuit via "I'll just import that helper" gets flagged in review.

## Plugin registration pattern

Implementations self-register at package `init()` into a central registry:

```go
// at package level
func init() {
    api.RegisterAction("label", func() api.Action { return &action{} })
}
```

The composition root (`cmd/portal/wire.go`) enumerates the registry, filters by enabled flags, injects the chosen implementations into constructors. No reflection, no DI framework.

This means **adding a new action / sink / engine / context-builder is exactly two things**:

1. A new struct that implements the interface, in its own package under `internal/actions/` (or `internal/sink/`, etc.).
2. A blank import of that package in `cmd/portal/wire.go` so its `init()` fires.

Anything more elaborate is a code smell.

## Layer toggles

Every layer must be independently startable:

- `--admission` — webhook only. No informers, no audit loop.
- `--audit` — informer-driven audit. Implies the dynamic client; opens the watch stream.
- `--network` — NetworkPolicy analyser. Implies `--audit` (shares its informer caches).
- `--rules-folder=<path>` — folder loader (additive to CR loader).
- `--rules-cr` — CR loader (default-on).

RBAC is conditional on flags: an admission-only deployment doesn't need watch on audited workloads; an audit-only deployment doesn't need to serve TLS. The Helm chart templates this surface (`rbac.actions.*` toggles, layer enable booleans).

The runtime expectation is that `portal run --admission` is a complete program — no half-initialised dependencies, no nil-pointer crashes from a code path that "almost" needed audit.

## Optional deployment-per-layer (`mode: split`)

The default chart ships one Deployment running every enabled layer. Setting `global.mode: split` deploys one Deployment per layer — independent replica counts, independent resource limits. Same RBAC, divided by binary; same chart, different value. The fact that modules don't cross-import makes this **deployment-time, not refactor-time**.

This split is the canonical scaling story for high-throughput clusters: admission can be sized for request rate, audit for cluster object count, network for namespace count.

## Where to put a new thing

- A new **action type** → `internal/actions/<name>/`. One package, one `action.go`, one `action_test.go`. Register in `init()`. Blank-import in `cmd/portal/wire.go`.
- A new **output sink** → `internal/sink/<name>/`. Same pattern.
- A new **expression engine** → `internal/expr/<name>/`. Same pattern.
- A new **`cluster.<gvk>.*` helper or expr-lang binding** → extend `internal/lookup/`. Document in `docs/reference/expression-language.md`.
- A new **per-GVK context-shape** → `internal/context/<name>/`. Add to the registry. Most rules cope with the generic builder; resist adding sugar until real rules demand it (per `docs/adr/0006-pod-sugar-narrow-facade.md`).
- A new **Helm value** → `deploy/helm/portal/values.yaml` (with a `# --` doc-comment for `helm-docs`), `deploy/helm/portal/templates/*` to consume it, and `docs/reference/helm-values.md` for the user-facing description.
- A new **rule-schema field** → `internal/api/rule.go` for the DTO, `internal/rule/crd/types.go` for the CR shape (with kubebuilder markers), `internal/rule/loader/` for parsing, `docs/reference/rule-schema.md` for the docs entry.

The cross-cutting rule (from `docs/PLAN.md` §"Documentation as a first-class deliverable"): a PR that introduces or changes a user-visible behaviour must include the doc change in the same PR. CI fails if a public-API symbol, CRD field, rule-schema field, Helm value, metric, or built-in action is added without its doc entry.
