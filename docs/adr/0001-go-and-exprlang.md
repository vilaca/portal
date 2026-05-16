# ADR 0001 — Go runtime, expr-lang expression engine

**Status.** Accepted, implemented in v1.

## Context

`podwatcher-poc` is Java/SpEL. Portal is its productised successor — a layer-4+5+6 admission/audit/network tool that runs as a critical-path Kubernetes webhook. The choice of runtime language and expression engine sets the operational characteristics of the binary and the ergonomics of every rule operators will ever write.

## Decision

**Go (1.22+) and `github.com/expr-lang/expr`.**

## Rationale — why Go over Java/SpEL

- **Distroless static binary.** Go's static linking + cgo-disabled produces a single file with no runtime dependencies. The image is ~20 MB versus a JVM container's hundreds of MB; cold-start is sub-second versus several seconds. Critical-path webhooks cannot afford JVM warm-up.
- **`client-go` and `controller-runtime` are the native ecosystem.** Admission webhooks, informers, leader election, dynamic clients — all first-class in Go. Equivalent Java libraries exist (fabric8) but lag features by months, sometimes years.
- **Memory footprint.** Portal's webhook idles at ~30 MB. The JVM analogue starts at ~200 MB minimum.
- **Operability.** `pprof` for profiling, `go tool trace`, the runtime metrics on `/debug/vars` — all built-in. No JMX/JDK tooling required in production containers.
- **Type system and concurrency primitives** fit the problem (channel-based dispatch, sync primitives, context cancellation). Java has equivalents but the rate of allocations and the GC tuning costs of a high-throughput webhook are worse.

The Java pivot is paid in podwatcher-poc rule migration cost (mechanical, see `internal/rule/migrate/`) and in throwing away the existing podwatcher-poc Java code. The size of the existing codebase is small enough (POC scale) that this is acceptable.

## Rationale — why expr-lang over CEL

- **Terseness.** Side-by-side, expr-lang rules are roughly the same line count as the SpEL they replace; CEL is verbose by comparison for the same logical expressions. See `../comparison/feature-matrix.md` row "Block privileged container lines".
- **Null-safe operators.** expr-lang ships `?.` (safe navigation) and `??` (null-coalesce). CEL has `has(...)` but no operator-level safe navigation; rules end up nested in `has(...) && ...` chains.
- **Inline set literals.** `x in ['a', 'b']` in expr-lang. CEL has the same shape; SpEL had `{a, b}.contains(x)`. The migration cost from SpEL to expr-lang is **lower** than to CEL because the syntactic surface is closer — see `internal/rule/migrate/migrate.go` for the trivial transformations.
- **No type pre-declaration.** expr-lang reads from a `map[string]any` env; CEL requires upfront type declarations for every variable. Portal's evaluation env is dynamic (per-GVK, with optional cluster lookups, with admission-only `request.*`) — expr-lang's dynamic shape is a direct fit.
- **Compile performance.** expr-lang programs compile in microseconds. CEL is comparable but slightly slower per-compile; matters when Portal recompiles the entire corpus on every rule reload.

## Cost we accept

- **Smaller ecosystem.** CEL is K8s' native expression language (Gatekeeper, Kyverno, admission/CEL ValidatingAdmissionPolicy). Operators who already speak CEL face a small re-learning curve.
- **No standardised libraries** like CEL's `cel.@hasField`, `cel.bind`. Portal exposes its own helpers via `internal/lookup/` and the expr-lang builtin macros.

## Mitigation — the engine seam

`api.ExpressionEngine` is a registration interface (`internal/api/engine.go`). A CEL adapter can be added in v3 without touching the rule schema; rules don't embed engine choice. Per-rule engine selection is a future feature, not a v1 commitment. See `../plugin-author/custom-expression-engine.md`.

## Consequences

- The podwatcher-poc Java code is **not reused**. Behaviour parity is verified via golden-file regression tests in `internal/sink/alertmanager/testdata/`.
- Operators familiar with CEL must learn expr-lang's small syntactic differences. The migration guide (`docs/migration/side-by-side-rule-syntax.md`) covers them.
- The interface seam means a v3 engine-pluralism story is not a redesign — it's a new package.
