# Portal documentation

Portal is the productised successor to `podwatcher-poc`: a Go admission webhook, informer-driven audit loop, and declarative NetworkPolicy analyser, with a built-in response-action engine. Rules are written in [`expr-lang/expr`](https://github.com/expr-lang/expr); rule distribution is `PortalClusterRule` / `PortalRule` CRDs with a folder-loader fallback.

## Documentation map

| Folder | What lives there |
|--------|------------------|
| [getting-started/](getting-started/) | Install on kind, install on production, write your first rule |
| [concepts/](concepts/) | Architecture, admission vs audit, cross-resource model, pod sugar, actions, fail-closed |
| [reference/](reference/) | Rule schema, expression language, CRDs, actions, metrics, Helm values, CLI |
| [cookbook/](cookbook/) | Worked examples — privileged container, label enforcement, PDB cross-resource, etc. |
| [operator/](operator/) | HA, leader election, RBAC scoping, certificates, upgrades, troubleshooting, observability, recovery |
| [security/](security/) | Threat model, RBAC posture, supply chain, responsible disclosure |
| [plugin-author/](plugin-author/) | Add a custom action, sink, or expression engine |
| [contributing/](contributing/) | Repo layout, module boundaries, testing, release process |
| [adr/](adr/) | Architecture Decision Records |
| [migration/](migration/) | From `podwatcher-poc`; side-by-side rule syntax; coexistence with Kyverno |
| [comparison/](comparison/) | Feature matrix vs the field; Portal-vs-podwatcher comparison |

## 5-minute start

See [getting-started/quickstart-kind.md](getting-started/quickstart-kind.md).

## Source of truth

The canonical v1 design lives in [PLAN.md](PLAN.md). When this site and PLAN.md disagree, PLAN.md wins until the docs are corrected.
