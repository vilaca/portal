# ADR 0002 — CRDs primary, folder loader as fallback

**Status.** Accepted, implemented in v1.

## Context

`podwatcher-poc` distributed rules as files in a folder, loaded at startup. Portal must support a GitOps workflow, `kubectl apply` ergonomics, per-rule `.status` reporting, and ideally the same folder-based bootstrap path that podwatcher-poc users already know.

## Decision

- **`PortalClusterRule` (cluster-scoped) and `PortalRule` (namespaced) CRDs are the canonical surface.** `apiVersion: portal.io/v1alpha1`, defined in `internal/rule/crd/types.go`, deployed via the chart's `crds/` directory.
- **The folder loader is retained** behind the `--rules-folder` flag. Both can run simultaneously; the in-memory rule index is a merged view.
- **`portal migrate-rules` writes CRs by default** (`--format=cr`) and folder files on `--format=folder` (`internal/rule/migrate/migrate.go`).

## Rationale — CRDs as primary

- **`kubectl apply` is the contract.** Every K8s tool, every operator, every CI pipeline already knows it. No new file-distribution mechanism to ship.
- **K8s RBAC** scopes who can write rules: cluster admins for `PortalClusterRule`, namespace owners for `PortalRule`.
- **GitOps-native.** Argo CD / Flux already reconcile CRs; rules become regular declarative manifests under version control.
- **`.status` reporting.** Each CR has a `.status` subresource that the reconciler (`internal/rule/crd/reconciler.go`) writes with `evalCount`, `violationCount`, `lastApplied`, `parseError`, `activeOn`. Users see whether a rule is firing without grepping logs.
- **Validation at the API server.** The OpenAPI structural schema rejects malformed manifests before Portal ever sees them. Expression-level errors (expr-lang syntax) surface in `.status.parseError` post-apply.
- **Discoverability.** `kubectl get portalclusterrule` lists every active rule. `kubectl describe` gives operators everything they need without learning Portal-specific tooling.

## Rationale — folder loader retained

- **Bootstrap.** Some clusters install Portal from a tarball before the K8s API server is up enough to write CRs (cluster bootstrap, disaster recovery). Folder mode works without an API server.
- **Dev workflow.** `portal run --rules-folder=./examples/rules/ --kubeconfig=...` is the fast inner-loop. Edit a file, hit save, fsnotify picks it up — no CR roundtrip.
- **Migration from podwatcher-poc.** podwatcher-poc rules are in folders. The first migration step (run `portal migrate-rules ... --format=folder`) keeps them in folders so operators can verify behaviour parity before flipping to CRs.

## Both at once

Both loaders feed into the same `RuleIndex` (`internal/api/engine.go` — `RuleIndex`). Behavioural contract:

- Rules from both sources coexist; the index is a merged view.
- If two rules have the same `name`, the last-writer wins (folder loaders process in lexical order; CR loaders by informer event order). This is documented but not enforced — name collisions are an operator error.
- The migration story: install Portal with folder-only loading, verify parity, `portal migrate-rules` to CRs, `kubectl apply -f migrated-rules/`, then `helm upgrade` to drop the folder loader. No "all at once" requirement.

## Consequences

- Two rule sources to maintain. Both have unit tests under `internal/rule/loader/`. Both must produce the same `api.Rule` shape — folder uses YAML directly, CR loader extracts from `PortalClusterRule.spec`.
- The CRDs are versioned (`apiVersion: portal.io/v1alpha1`). Per `docs/operator/upgrading.md`, CRD upgrades are a separate step from `helm upgrade`. v1 only has one stored version; no conversion webhook.
- Documentation explicitly covers both modes (`docs/getting-started/first-rule.md` shows CR; `docs/getting-started/install-helm.md` covers `rulesFolder`).
