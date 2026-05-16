# Changelog

All notable changes to Portal are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions track
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Wave 0–5: complete v1 architecture (admission webhook, audit informer loop,
  NetworkPolicy analyser, response-action engine with six built-in actions,
  expr-lang rule engine, AlertManager + PolicyReport + Prometheus + stdout
  sinks).
- `portal init-certs` subcommand and Helm init-container, closing the
  self-signed CA-bundle injection gap for non-cert-manager installs.
- `policyreport-gc` action that drops stale `Result` entries from
  `PolicyReport` / `ClusterPolicyReport` when an audited object is deleted.
- Discovery-backed RESTMapper threaded through audit, network, and the
  `label` / `annotate` actions — replaces the naive `lowercase(kind)+"s"`
  pluraliser at six sites.
- controller-gen v0.17.3 wired through `make generate-crds`; CRD types and
  DeepCopy methods are now generated.
- Full `docs/` tree with mkdocs config (concepts, reference, cookbook,
  migration, operator, security, plugin-author, contributing, ADRs).
- `portal migrate-rules` subcommand that rewrites podwatcher-poc SpEL rules
  to expr-lang + `match.namespaces`.
- End-to-end kind harness under `deploy/test/`, gated by `//go:build e2e`.

### Fixed
- Audit `OnDelete` synthetic violation was leaking into every output sink
  (stdout, prometheus, AlertManager, PolicyReport), producing stray alerts
  and metric pollution. It now dispatches to the action engine only — see
  `TestEmitGCViolation_BypassesSinks`.
- `ActionSpec.Params` in the CRD types switched from `map[string]any` (which
  panicked controller-gen v0.17 DeepCopy generation) to
  `*runtime.RawExtension`.
- `--rules-cr=true` with `--admission` only used to silently load zero
  rules; wire-up now errors fast unless `--audit` or `--rules-folder` is
  also set.

### Changed
- `rules.bootstrap` Helm value defaulted to `false`. The hook to apply the
  example rule bundle is not in v1; operators copy the rules they want from
  `examples/rules/` and `kubectl apply` them.

## [0.1.0] — unreleased

The inaugural release will be tagged once the P1 items in
`docs/POC-TO-PRODUCTION.md` are landed and the e2e suite has been run on a
real kind cluster. This file will be split into `[0.1.0]` and `[Unreleased]`
sections at that point.
