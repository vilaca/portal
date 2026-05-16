#!/usr/bin/env bash
#
# kind.sh — orchestrates the Portal end-to-end suite against a disposable kind
# cluster. Intended to run on CI and on developer laptops that have the full
# toolchain. Gracefully no-ops when any required tool is missing so the same
# script can be wired into CI matrices that don't always provision kind.
#
# Env knobs:
#   KIND_IMAGE         Override the kindest/node image (default v1.30.0).
#   KIND_CLUSTER_NAME  Cluster name (default portal-e2e).
#   KIND_CONFIG        Path to kind config (default deploy/test/kind-config.yaml).
#   SKIP_TEARDOWN      If set to 1, keep the cluster on exit so you can debug.
#   KEEP_LOGS_DIR      Directory to dump `kind export logs` into on failure.
#   E2E_GO_TEST_FLAGS  Extra flags appended to `go test` (e.g. -run TestActions).
#
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"

KIND_IMAGE="${KIND_IMAGE:-kindest/node:v1.30.0}"
KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-portal-e2e}"
KIND_CONFIG="${KIND_CONFIG:-${SCRIPT_DIR}/kind-config.yaml}"
SKIP_TEARDOWN="${SKIP_TEARDOWN:-0}"
KEEP_LOGS_DIR="${KEEP_LOGS_DIR:-${SCRIPT_DIR}/kind-logs}"
E2E_GO_TEST_FLAGS="${E2E_GO_TEST_FLAGS:-}"

log() { printf '[kind.sh] %s\n' "$*"; }

require() {
  local name="$1"
  if ! command -v "${name}" >/dev/null 2>&1; then
    log "tool ${name} not installed; skipping e2e suite (exit 0)"
    log "install ${name} to run the full e2e suite — see deploy/test/README.md"
    exit 0
  fi
}

require kind
require kubectl
require helm
require docker
require go

# docker daemon reachable?
if ! docker info >/dev/null 2>&1; then
  log "docker daemon not reachable; skipping e2e suite (exit 0)"
  exit 0
fi

teardown() {
  local rc=$?
  if [[ "${SKIP_TEARDOWN}" == "1" ]]; then
    log "SKIP_TEARDOWN=1 — leaving cluster ${KIND_CLUSTER_NAME} running"
    log "to clean up later run: ${SCRIPT_DIR}/kind-teardown.sh"
    return
  fi
  if [[ ${rc} -ne 0 ]]; then
    log "test suite failed (rc=${rc}); exporting kind logs to ${KEEP_LOGS_DIR}"
    mkdir -p "${KEEP_LOGS_DIR}"
    kind export logs "${KEEP_LOGS_DIR}" --name "${KIND_CLUSTER_NAME}" || true
  fi
  log "deleting kind cluster ${KIND_CLUSTER_NAME}"
  kind delete cluster --name "${KIND_CLUSTER_NAME}" >/dev/null 2>&1 || true
}
trap teardown EXIT

# -----------------------------------------------------------------------------
# 1) Cluster up
# -----------------------------------------------------------------------------
if kind get clusters | grep -qx "${KIND_CLUSTER_NAME}"; then
  log "reusing existing kind cluster ${KIND_CLUSTER_NAME}"
else
  log "creating kind cluster ${KIND_CLUSTER_NAME} (image=${KIND_IMAGE})"
  if [[ -f "${KIND_CONFIG}" ]]; then
    kind create cluster \
      --name "${KIND_CLUSTER_NAME}" \
      --image "${KIND_IMAGE}" \
      --config "${KIND_CONFIG}" \
      --wait 120s
  else
    kind create cluster \
      --name "${KIND_CLUSTER_NAME}" \
      --image "${KIND_IMAGE}" \
      --wait 120s
  fi
fi

KUBECONFIG_PATH="${KUBECONFIG:-$(mktemp -t portal-kind.kubeconfig.XXXXXX)}"
kind get kubeconfig --name "${KIND_CLUSTER_NAME}" > "${KUBECONFIG_PATH}"
export KUBECONFIG="${KUBECONFIG_PATH}"
log "KUBECONFIG=${KUBECONFIG}"

# -----------------------------------------------------------------------------
# 2) CRDs
# -----------------------------------------------------------------------------
log "applying CRDs from deploy/crds/"
kubectl apply -f "${REPO_ROOT}/deploy/crds/"

# -----------------------------------------------------------------------------
# 3) Image build + load (portal + alertmanager-receiver fixture)
# -----------------------------------------------------------------------------
log "building Portal image portal:e2e"
docker build \
  -t portal:e2e \
  -f "${SCRIPT_DIR}/Dockerfile.e2e" \
  "${REPO_ROOT}"

log "building alertmanager-receiver fixture"
docker build \
  -t alertmanager-receiver:e2e \
  "${SCRIPT_DIR}/fixtures/alertmanager-receiver"

log "loading images into kind"
kind load docker-image portal:e2e --name "${KIND_CLUSTER_NAME}"
kind load docker-image alertmanager-receiver:e2e --name "${KIND_CLUSTER_NAME}"

# -----------------------------------------------------------------------------
# 4) Deploy receiver, then Portal
# -----------------------------------------------------------------------------
log "deploying alertmanager-receiver fixture"
kubectl apply -f "${SCRIPT_DIR}/fixtures/alertmanager-receiver/deploy.yaml"
kubectl wait --for=condition=Available deployment/alertmanager-receiver \
  -n portal-e2e --timeout=2m

log "installing Portal Helm chart"
helm upgrade --install portal "${REPO_ROOT}/deploy/helm/portal" \
  -n portal-system --create-namespace \
  --set image.repository=portal \
  --set image.tag=e2e \
  --set image.pullPolicy=Never \
  --set audit.enabled=true \
  --set network.enabled=true \
  --set rbac.actions.label=true \
  --set rbac.actions.annotate=true \
  --set rbac.actions.evict=true \
  --set rbac.actions.patchnp=true \
  --set rbac.actions.revoketoken=true \
  --set alertmanager.url=http://alertmanager-receiver.portal-e2e.svc:9093/api/v2/alerts \
  --wait --timeout 2m

log "waiting for Portal deployment to become Available"
kubectl wait --for=condition=Available deployment/portal \
  -n portal-system --timeout=2m

# -----------------------------------------------------------------------------
# 5) Go e2e tests
# -----------------------------------------------------------------------------
log "running e2e test suite (tag=e2e)"
cd "${REPO_ROOT}"
# shellcheck disable=SC2086
KUBECONFIG="${KUBECONFIG}" \
  PORTAL_E2E_NAMESPACE=portal-system \
  go test -tags=e2e -count=1 -v -timeout 30m ${E2E_GO_TEST_FLAGS} ./deploy/test/...

log "e2e suite passed"
