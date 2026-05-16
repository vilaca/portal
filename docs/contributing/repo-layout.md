# Repository layout

```
portal/
├── go.mod
├── Makefile
├── cmd/
│   └── portal/                # CLI: main.go, run.go, migrate.go, docgen.go, wire.go
├── internal/
│   ├── api/                   # interfaces + DTOs (zero K8s/expr-lang deps)
│   ├── rule/
│   │   ├── crd/               # generated PortalClusterRule / PortalRule types + status reconciler
│   │   ├── loader/            # folder + CR rule loaders behind one RuleLoader interface
│   │   └── migrate/           # podwatcher-poc rule migration (SpEL → expr-lang)
│   ├── lookup/                # cluster.<gvk>.* helpers + reverse-dep index + virtual cluster view
│   ├── engine/                # DefaultRuleEngine: GVK-indexed evaluation
│   ├── expr/
│   │   └── exprlang/          # expr-lang adapter (default ExpressionEngine)
│   ├── context/
│   │   ├── pod/               # pod-shaped ContextBuilder (sugar façade)
│   │   └── generic/           # fallback ContextBuilder for non-Pod GVKs
│   ├── sink/
│   │   ├── alertmanager/      # AlertManager JSON sink (podwatcher-poc-compatible wire shape)
│   │   ├── policyreport/      # wgpolicyk8s.io PolicyReport / ClusterPolicyReport sink
│   │   ├── prometheus/        # /metrics, /healthz, /readyz, the canonical metric registry
│   │   └── stdout/            # slog JSON sink
│   ├── admission/             # TLS webhook EventSource + self-signed cert bootstrap
│   ├── audit/                 # informer-driven EventSource + lease-based leader election
│   ├── network/               # NetworkPolicy declarative analyser
│   └── actions/
│       ├── engine/            # DefaultActionDispatcher: bounded pool + rate limit + idempotency
│       ├── alertmanager_action/  # action wrapper around the AlertManager sink
│       ├── label/             # server-side-apply label action
│       ├── annotate/          # server-side-apply annotation action
│       ├── evict/             # policy/v1 Eviction action
│       ├── patchnp/           # server-side-apply on NetworkPolicy
│       └── revoketoken/       # SA token Secret deletion
├── deploy/
│   ├── crds/                  # vendored PolicyReport + portal.io CRD YAMLs
│   ├── helm/portal/           # Helm chart (templates, values.yaml, Chart.yaml)
│   └── test/                  # end-to-end kind tests
├── docs/                      # this directory tree — versioned with code, mkdocs-built
└── examples/
    └── rules/                 # example PortalClusterRule manifests
```

## One-line description per top-level dir

| Dir | What lives here |
|---|---|
| `cmd/portal/` | Composition root. Flag parsing (`run.go`), registry wiring (`wire.go`), CLI subcommands (`migrate.go`, `docgen.go`). Tiny. |
| `internal/api/` | Pure interfaces and DTOs. The only package other `internal/*` packages may import. |
| `internal/rule/` | Rule data model, loaders (folder + CR), CRD types, migrator. |
| `internal/lookup/` | Cross-resource (`cluster.<gvk>.*`) helpers, dep index, admission virtual cluster view. |
| `internal/engine/` | GVK-indexed rule evaluator. |
| `internal/expr/exprlang/` | `expr-lang` adapter (default `ExpressionEngine`). |
| `internal/context/` | `ContextBuilder` implementations — pod sugar + generic fallback. |
| `internal/sink/` | `OutputSink` implementations. |
| `internal/admission/` | Admission webhook `EventSource` + TLS cert bootstrap. |
| `internal/audit/` | Informer-driven audit `EventSource` + leader election. |
| `internal/network/` | NetworkPolicy declarative analyser `EventSource`. |
| `internal/actions/` | Action engine + each built-in action. |
| `deploy/` | Everything that gets `kubectl apply`'d or `helm install`'d. |
| `docs/` | User-facing documentation, mkdocs source. |
| `examples/` | Hand-curated example rule manifests. |

## Module dependency rule

The load-bearing constraint of the codebase:

> **Only `internal/api` may be imported by other `internal/*` packages.**

This is enforced by code review and (in CI) by a `go list -deps` check. Concretely: `internal/admission` cannot `import "github.com/vilaca/portal/internal/audit"`, even if a refactor would be convenient. Instead, both depend on `internal/api`, exchanging `EventSource`, `RuleEngine`, `ActionDispatcher` etc. via interface.

Exceptions (deliberate, justified, finite):

- `internal/audit` exposes `SharedInformerFactory()` for `internal/lookup` and `internal/network` to consume — this is the agreed handshake for the shared informer cache.
- `internal/context/pod` is imported by `internal/audit` and `internal/admission` to get the default pod `ContextBuilder`. (Could be made registry-only, may be in a future refactor.)

Every other "obvious" import is a smell.

For the rationale see `module-boundaries.md`.
