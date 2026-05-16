#!/usr/bin/env bash
#
# kind-teardown.sh — delete the e2e cluster left behind by `SKIP_TEARDOWN=1`.
#
set -euo pipefail

KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-portal-e2e}"

if ! command -v kind >/dev/null 2>&1; then
  echo "[kind-teardown.sh] kind not installed; nothing to do"
  exit 0
fi

if ! kind get clusters | grep -qx "${KIND_CLUSTER_NAME}"; then
  echo "[kind-teardown.sh] cluster ${KIND_CLUSTER_NAME} not present; nothing to do"
  exit 0
fi

echo "[kind-teardown.sh] deleting cluster ${KIND_CLUSTER_NAME}"
kind delete cluster --name "${KIND_CLUSTER_NAME}"
