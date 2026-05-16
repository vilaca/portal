# Portal

A Kubernetes admission webhook, informer-driven audit loop, and declarative
NetworkPolicy analyser — with a built-in response-action engine. Rules are
written in [expr-lang](https://github.com/expr-lang/expr) and shipped as
`PortalClusterRule` / `PortalRule` CRDs (or loaded from a folder).

> **Status:** v0.1.0-alpha. The CRD is `portal.io/v1alpha1`; breaking changes
> may land before GA. See [`docs/v1-followups.md`](docs/v1-followups.md) for
> the punchlist and known gaps.

## What it does

Three event sources, one rule language, one action pipeline:

- **Admission webhook** — `ValidatingWebhookConfiguration` with `failurePolicy: Fail`
  by default. Evaluates `mode: [admission]` rules synchronously and can
  `deny` / `warn` / `dryrun`.
- **Audit loop** — dynamic informers over a configurable GVK set. Evaluates
  `mode: [audit]` rules out-of-band and emits to every sink, plus emits
  synthetic `resolved` events when a rule stops firing.
- **NetworkPolicy analyser** — graph-shaped checks for default-deny-missing,
  broad-CIDR egress, unreachable selectors, policy-without-targets.

Outputs fan out to **AlertManager**, **PolicyReport** (`wgpolicyk8s.io/v1alpha2`),
**Prometheus** (`portal_*` metrics), and **stdout** simultaneously. Actions
(`label`, `annotate`, `evict`, `patch-networkpolicy`, `revoke-sa-token`,
`alertmanager`, `policyreport-gc`) run through a per-rule rate limiter +
LRU idempotency cache.

## Quick start

```bash
# Build + test
make build
make test

# Spin up a local kind cluster + run the full e2e suite
./deploy/test/kind.sh

# Install on an existing cluster
helm install portal deploy/helm/portal \
  --namespace portal-system --create-namespace \
  --set audit.enabled=true \
  --set network.enabled=true
```

Then apply a rule:

```yaml
apiVersion: portal.io/v1alpha1
kind: PortalClusterRule
metadata:
  name: disallow-privileged
spec:
  name: disallow-privileged
  enabled: true
  severity: high
  mode: [admission]
  enforcementAction: deny
  match:
    gvk:
      - {group: "", version: v1, kind: Pod}
  rule: container.securityContext.privileged == true
```

Full walkthrough: [`docs/getting-started/quickstart-kind.md`](docs/getting-started/quickstart-kind.md).

## Documentation

- **Design of record:** [`docs/POC-TO-PRODUCTION.md`](docs/POC-TO-PRODUCTION.md)
- **Documentation site:** [`docs/README.md`](docs/README.md) — concepts, reference,
  cookbook, migration from `podwatcher-poc`, operator runbooks, ADRs.
- **CRD + Helm value reference:** [`docs/reference/`](docs/reference/)
- **Migrating an existing rule corpus:** [`docs/migration/`](docs/migration/)
  and the `portal migrate-rules` subcommand.

## Project status

- **Unit tests:** 25 packages, all green (`make test`).
- **End-to-end suite:** 10 passing, 1 skipped, 0 failing on kind
  (`./deploy/test/kind.sh`).
- **Known gaps:** [`docs/v1-followups.md`](docs/v1-followups.md) §5.

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md). The repo is small enough that the
fastest path is `make test && ./deploy/test/kind.sh` before opening a PR.

## Security

Report vulnerabilities via [`SECURITY.md`](SECURITY.md) — please do **not**
file public issues for security reports.

## License

[Apache License 2.0](LICENSE).
