# Contributing to Portal

Thanks for considering a contribution. Portal is Apache-2.0 licensed; by
opening a PR you agree your changes are licensed under the same terms.

## Quick links

The detailed guides live under `docs/contributing/`:

- [Repo layout](docs/contributing/repo-layout.md) — the directory tree and what each subtree owns.
- [Module boundaries](docs/contributing/module-boundaries.md) — the one-way dependency graph (`internal/api` is the only shared package).
- [Testing approach](docs/contributing/testing-approach.md) — unit / integration / e2e layers.
- [Release process](docs/contributing/release-process.md) — tagging, CHANGELOG, CI gates.

The canonical v1 design and roadmap is [`docs/POC-TO-PRODUCTION.md`](docs/POC-TO-PRODUCTION.md).

## Development loop

```bash
make build         # go build ./...
make test          # go test -race -count=1 ./...
make vet           # go vet ./...
make generate      # controller-gen CRDs + DeepCopy + helm-docs + gomarkdoc
helm lint deploy/helm/portal
```

Install the build-time tools once:

```bash
make tools         # controller-gen, helm-docs, gomarkdoc
```

## Pull-request expectations

- **Tests.** Every behavioural change has a test next to the code. Race
  detection is on by default — `make test` runs `-race`.
- **Docs.** Per `docs/POC-TO-PRODUCTION.md` §"Documentation as a first-class
  deliverable", every user-visible change ships with its doc entry in the
  same PR. CI fails if a new metric, action type, CRD field, Helm value, or
  expression-language binding is added without its reference entry.
- **Generated artifacts.** Run `make generate` and commit the output. CI
  fails on uncommitted generator drift.
- **Module boundaries.** `internal/api` is the only package depended on by
  other `internal/*` packages. PRs that introduce a new cross-package
  import need a justification.
- **Commit messages.** Conventional-Commits style not required, but
  imperative subject + a body explaining *why* is. Reference
  `docs/contributing/release-process.md` for the CHANGELOG convention.

## Reporting bugs / asking questions

GitHub issues for bugs, feature requests, and design questions. For security
issues, see [`SECURITY.md`](SECURITY.md).
