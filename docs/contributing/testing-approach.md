# Testing approach

Three test layers, three different gates.

## Unit tests — per package

Every Go package has a `_test.go` file. Examples:

- `internal/api/api_test.go` — interface contract tests.
- `internal/admission/handler_test.go` — admission decoder + handler tests.
- `internal/admission/cert_test.go` — cert bootstrap.
- `internal/audit/controller_test.go` — informer plumbing.
- `internal/network/checks_test.go`, `internal/network/analyser_test.go` — NetworkPolicy analysis.
- `internal/sink/<name>/sink_test.go` — sink-specific golden tests.
- `internal/actions/<name>/action_test.go` — per-action behaviour.
- `internal/rule/migrate/migrate_test.go` — migration rewrites are checked against the 6 podwatcher-poc example rules.

Run:

```bash
go test ./...
```

Race detector and explicit count to bypass test caching are the default:

```bash
go test -race -count=1 ./...
```

`make test` wraps this.

## Integration tests — per layer

These live alongside the unit tests but are tagged `//go:build integration` (or use a sub-test gate). They exercise full pipelines — admission handler with real engine + real sinks + fake clients; audit controller with a controller-runtime envtest harness.

Run:

```bash
go test -tags=integration ./internal/audit/... ./internal/admission/...
```

When envtest binaries are absent (a fresh checkout), the integration suite skips with a clear message rather than failing.

## End-to-end tests — kind-based

Under `deploy/test/`. The harness:

1. Spins up a `kind` cluster.
2. Installs the Helm chart via `helm install portal ./deploy/helm/portal/`.
3. Applies a fixture bundle of compliant + violating manifests.
4. Asserts deny count, warn count, PolicyReport contents, AlertManager calls (against a captured mock), Prometheus scrape.

Run:

```bash
go test -tags=e2e ./deploy/test/...
```

When `kind` is not on `$PATH`, the e2e tests gate on the build tag and skip; CI runs them in a job that installs `kind` explicitly.

## Fixture conventions

- Each package that needs fixtures has a `testdata/` subdirectory. Convention: one file per scenario, named after the test (`TestFoo_<scenario>.yaml`).
- Golden files are JSON, normalised to a canonical order so diffs are reviewable. The alertmanager sink's golden file (`internal/sink/alertmanager/testdata/expected_alert.json`) is the reference example — it asserts byte-for-byte compatibility with podwatcher-poc's wire shape.
- When a golden file legitimately needs to change, regenerate it with `go test -update` (the conventional flag idiom) and commit the new golden alongside the code change. CI's golden-diff check enforces no accidental drift.

## CI gates

The default GitHub Actions workflow (`.github/workflows/ci.yml`) runs:

1. **build** — `go build ./...`, `go vet ./...`, `go test -race -count=1 ./...`.
2. **lint** — `go vet` again (CI subset), `lychee` link-check on `docs/`.
3. **docs-generation-drift** — runs `make generate-docs` (`gomarkdoc` + `helm-docs` + CRD-ref generation) and fails if `git diff` is non-empty.

The e2e job is intentionally separate (heavier; gated by build tag) and runs on push to `main` and on `[e2e]` in a PR title.

## How to run only what you changed

```bash
# One package
go test ./internal/network/

# One test in one package
go test -run TestAnalyser_DefaultDenyMissing ./internal/network/

# With verbose output and race detector
go test -race -v -run TestAnalyser_DefaultDenyMissing ./internal/network/
```

For larger refactors, `go test -count=1 ./internal/...` (skip e2e) is the recommended pre-push sweep.

## What we don't test

- Real cluster integration. The kind-based e2e is the upper bound; we never assume access to a "live" cluster in CI.
- Performance benchmarks beyond a handful of `Benchmark*` functions on the hot paths (`internal/engine`, `internal/admission`). Latency is asserted at the integration layer (admission p99 < 20 ms is the design target, not a CI gate).
- UI / dashboard testing. Portal has no UI in v1.
