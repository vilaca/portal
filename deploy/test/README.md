# Portal end-to-end test harness

This directory contains the Portal end-to-end suite. It spins up a disposable
[kind](https://kind.sigs.k8s.io/) cluster, installs the Helm chart from
`deploy/helm/portal/`, then runs a Go test binary tagged `e2e` that exercises
the eleven Verification scenarios listed in `docs/POC-TO-PRODUCTION.md`.

The harness is invoked from CI by `.github/workflows/ci.yml` and is intended to
be re-runnable on a developer laptop with the same single command.

## Prerequisites

| Tool       | Notes |
|------------|-------|
| `docker`   | Daemon must be reachable; the harness builds `portal:e2e` from the repo root. |
| `kind`     | ≥ 0.20; `kindest/node:v1.30.0` is the default image. |
| `kubectl`  | Any 1.27+ client. |
| `helm`     | 3.x. |
| `go`       | The same version pinned in `go.mod` (currently 1.22). |

`kind.sh` checks each tool with `command -v` and **exits 0 with a clear
message** if any of them are absent, so CI matrices that don't provision the
full toolchain can still run the script as a no-op. The whole Go suite sits
behind the `//go:build e2e` tag, so `go test ./...` never accidentally pulls
it in.

## Run

```sh
./deploy/test/kind.sh
```

That does, in order:

1. Asserts `docker`, `kind`, `kubectl`, `helm`, `go` are present.
2. Creates kind cluster `portal-e2e` using `deploy/test/kind-config.yaml`
   (1 control-plane + 3 workers; required by `TestHAFailClosed`).
3. Applies every CRD from `deploy/crds/`.
4. Builds `portal:e2e` from `deploy/test/Dockerfile.e2e` and `kind load`s it.
5. `helm upgrade --install portal deploy/helm/portal -n portal-system` with
   audit and network enabled and the image pinned to `portal:e2e`.
6. Waits for the Deployment to be Available.
7. Runs `go test -tags=e2e ./deploy/test/...` against the live cluster.
8. On exit, runs `kind delete cluster --name portal-e2e` (set
   `SKIP_TEARDOWN=1` to keep the cluster).

## Environment knobs

| Variable                | Default                          | Effect |
|-------------------------|----------------------------------|--------|
| `KIND_IMAGE`            | `kindest/node:v1.30.0`           | Override the kindest node image. |
| `KIND_CLUSTER_NAME`     | `portal-e2e`                     | Cluster name. |
| `KIND_CONFIG`           | `deploy/test/kind-config.yaml`   | kind cluster config. |
| `SKIP_TEARDOWN`         | unset                            | If `1`, leave the cluster running for inspection. |
| `KEEP_LOGS_DIR`         | `deploy/test/kind-logs`          | Where `kind export logs` writes on failure. |
| `E2E_GO_TEST_FLAGS`     | empty                            | Extra flags appended to `go test` (e.g. `-run TestActions`). |
| `PORTAL_E2E_NAMESPACE`  | `portal-system`                  | Namespace Portal is installed into. |

## Iterating on a single subtest

```sh
SKIP_TEARDOWN=1 ./deploy/test/kind.sh    # first run brings cluster up
KUBECONFIG="$(kind get kubeconfig-path --name portal-e2e 2>/dev/null \
              || echo $HOME/.kube/config)" \
  go test -tags=e2e -v -run TestAdmissionDeny ./deploy/test/
```

Once you're done:

```sh
./deploy/test/kind-teardown.sh
```

## Inspecting Portal while a test runs

```sh
kubectl -n portal-system get pods
kubectl -n portal-system logs deploy/portal -f
kubectl -n portal-system exec -it deploy/portal -- /portal --help
```

## Subtests ↔ Verification scenarios

Each subtest in `e2e_test.go` maps 1:1 to a scenario in `docs/POC-TO-PRODUCTION.md`
§Verification. The cross-reference:

| Subtest                          | Verification scenario |
|----------------------------------|-----------------------|
| `TestRuleMigrationCompileLoop`   | "Rule migration"      |
| `TestAdmissionDeny`              | "Admission webhook" (deny + warn) |
| `TestAdmissionDryRun`            | "Admission webhook" (dryrun) |
| `TestAuditImmediacy`             | "Audit immediacy" |
| `TestNetworkAnalyserReactivity`  | "Network analyser reactivity" |
| `TestActions`                    | "Actions" |
| `TestAlertManagerJSON`           | "Outputs" (AlertManager JSON shape) |
| `TestPolicyReport`               | "Outputs" (PolicyReport dedup) |
| `TestHAFailClosed`               | "HA + fail-closed" |
| `TestCrossResource`              | "Cross-resource" |
| `TestCRDRuleLoading`             | "CRD rule loading" |

## Known flakes

Be honest about which assertions are inherently racy on overloaded CI:

- **`TestAuditImmediacy`** — the "PolicyReport within 1 second" target is
  slack-allowed to 5 s in the assertion, but the watch-reconnect counter
  step is sensitive to apiserver scheduling. If it fails once on CI, retry
  the job before opening an issue.
- **`TestHAFailClosed`** — Lease transfer within 15 s is a soft target;
  on a busy worker node the test only `t.Logf`s when it sees more than one
  extra AlertManager action across the gap, rather than failing the run.

## Honest gaps

Documenting the limits of what this suite exercises so contributors don't
mistake green CI for full coverage:

- **No v2 runtime layer.** The K8s API audit-log event source and runtime
  enforcement are explicitly out of scope for v1, and therefore for this
  harness.
- **AlertManager JSON shape: skipped by default.** The byte-identical
  golden test belongs in this suite, but it requires reconfiguring the
  Portal sink at install time to point at an in-cluster receiver. The
  current implementation `t.Skip`s with a pointer to the unit tests in
  `internal/sink/alertmanager`, which already cover the JSON shape, retry
  semantics, auth headers, and TLS. The unit tests are the regression
  vector for that contract; the e2e test is reserved for delivery
  semantics once we add a chart value to override the sink URL.
- **No TLS / auth assertions against AlertManager.** Same reason as above
  — those are covered by unit tests in `internal/sink/alertmanager`. The
  e2e harness is exclusively about shape and delivery, not transport
  security.
- **HA Lease assertion needs 3 worker nodes.** The chart anti-affinity
  rules expect three nodes to schedule three replicas onto distinct hosts.
  `deploy/test/kind-config.yaml` provisions that topology; a stock
  `kind create cluster` does not.
- **Metric scrape uses `kubectl exec wget`.** No host-side port-forward
  means no flake risk from kernel-level TCP races, but it does mean the
  Portal image needs `wget` in PATH. The distroless image used here is
  static-only and *does* have `wget` via BusyBox — if the production
  image switches to true distroless, this scrape path needs port-forward.

## Wired into CI

`.github/workflows/ci.yml` has a dedicated `e2e` job that runs this harness
on every PR targeting `main` and every push to `main`. The job:

- installs `kind` via `helm/kind-action`,
- installs `helm` via `azure/setup-helm`,
- runs `./deploy/test/kind.sh`,
- uploads `kind-logs` as an artifact on failure (set by the `kind.sh`
  trap when `SKIP_TEARDOWN=0`).

The job is path-filtered: doc-only changes (`docs/**`, `**/*.md`) skip the
e2e job to keep the developer feedback loop tight for documentation PRs.

## Linting

`shellcheck deploy/test/*.sh` is recommended when iterating on the bash
scripts; CI installs shellcheck on Ubuntu and runs it. The Go file is
covered by the standard `go vet -tags=e2e ./deploy/test/...` check, which
also runs in CI.
