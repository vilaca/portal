# Quickstart on kind

Stand up Portal on a local [kind](https://kind.sigs.k8s.io/) cluster in about five minutes.

## Prerequisites

This guide assumes the following are already installed and on `$PATH`:

- [`kind`](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) ≥ 0.20
- [`kubectl`](https://kubernetes.io/docs/tasks/tools/) ≥ 1.27
- [`helm`](https://helm.sh/docs/intro/install/) ≥ 3.13

## 1. Create the cluster

```bash
kind create cluster --name portal-quickstart
```

## 2. Install Portal

From the repo root:

```bash
helm install portal deploy/helm/portal \
  -n portal-system --create-namespace
```

The default values enable the admission webhook and CRD-mode rule loading; audit and network are off (see [reference/helm-values.md](../reference/helm-values.md)). The chart installs the `PortalClusterRule` / `PortalRule` CRDs and bootstraps a self-signed CA — no cert-manager required.

Wait for Portal to come up:

```bash
kubectl rollout status -n portal-system deployment/portal --timeout=2m
```

## 3. Apply a rule

Portal accepts rules from two sources: folder-format YAML (legacy podwatcher-poc compatibility, mounted via ConfigMap) and `PortalClusterRule` CRs (canonical). The repo's [examples/rules/](../../examples/rules/) folder ships folder-format manifests for `portal migrate-rules` round-trip testing — they are **not** valid `kubectl apply` input on their own.

### Option A — folder format (mount via ConfigMap)

```bash
kubectl create configmap portal-rules -n portal-system \
  --from-file=examples/rules/privileged-container.yaml
# then `helm upgrade portal deploy/helm/portal -n portal-system \
#   --set rules.folderConfigMap=portal-rules`
```

### Option B — PortalClusterRule (kubectl apply)

```yaml
apiVersion: portal.io/v1alpha1
kind: PortalClusterRule
metadata:
  name: privileged-container
spec:
  name: privileged-container
  enabled: true
  severity: critical
  mode: [admission, audit]
  enforcementAction: deny
  match:
    gvk:
      - {group: "", version: v1, kind: Pod}
  rule: >
    container.securityContext.privileged == true
    || container.securityContext.allowPrivilegeEscalation == true
  alert: insecure-workload
```

```bash
kubectl apply -f - <<EOF
$(cat above-manifest.yaml)
EOF
```

## 4. Verify the deny

```bash
kubectl run pwn --image=alpine --restart=Never \
  --overrides='{"spec":{"containers":[{"name":"pwn","image":"alpine","securityContext":{"privileged":true}}]}}'
# Error from server: admission webhook "portal.io" denied the request: privileged-container
```

Then tail the logs:

```bash
kubectl logs deployment/portal -n portal-system | grep privileged-container
```

You should see a JSON line naming the rule that fired and the violating pod.

## 5. Tear down

```bash
kind delete cluster --name portal-quickstart
```

## Where next

- Write your own rule: [first-rule.md](first-rule.md).
- Production install: [install-helm.md](install-helm.md).
- Browse worked examples: [../cookbook/](../cookbook/).
