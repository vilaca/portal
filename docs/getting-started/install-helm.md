# Production install with Helm

## Fail-closed by default

Portal ships with `global.failClosed: true` — the rendered `ValidatingWebhookConfiguration` uses `failurePolicy: Fail`. If Portal is unreachable, the API server **rejects** the request. This is deliberate: a misconfigured Portal that fails open is silently absent. See [../concepts/fail-closed.md](../concepts/fail-closed.md) for the full discussion and break-glass procedure.

## Minimum supported topology

Fail-closed deployments **must** run at least two replicas behind a PodDisruptionBudget so rolling restarts and node drains don't drop the webhook:

```yaml
# values-prod.yaml
replicaCount: 2
podDisruptionBudget:
  minAvailable: 1
```

These are already the chart defaults; lowering them with `failClosed: true` is a self-lockout risk.

## Certificates

Two options, mutually exclusive:

| Option | When | How |
|--------|------|-----|
| Built-in self-signed | Quickstart / dev / air-gapped | Default. Portal generates a CA on startup and patches the webhook's `caBundle`. |
| cert-manager | Production | `certManager.enabled: true`. See [../operator/certificate-management.md](../operator/certificate-management.md). |

## System-namespace exclusion

The chart's `templates/validatingwebhookconfiguration.yaml` **always** excludes:

- `kube-system`
- `kube-public`
- `kube-node-lease`
- The Portal install namespace (`--install-namespace`, default `portal-system`)

This exclusion is non-negotiable and not driven by a Helm value. The rationale — preventing self-lockout and avoiding chicken-and-egg startup loops — is documented in [../security/threat-model.md](../security/threat-model.md).

## Most-touched values

Full table: [../reference/helm-values.md](../reference/helm-values.md). Source: [`deploy/helm/portal/values.yaml`](https://github.com/vilaca/portal/blob/main/deploy/helm/portal/values.yaml).

| Value | Default | Effect |
|-------|---------|--------|
| `global.failClosed` | `true` | `failurePolicy: Fail` in webhook config. |
| `replicaCount` | `2` | Portal deployment replicas. |
| `webhook.enabled` | `true` | Enable admission layer (`--admission`). |
| `audit.enabled` | `false` | Enable informer-driven audit (`--audit`). |
| `network.enabled` | `false` | Enable NetworkPolicy analyser (implies audit). |
| `policyReport.enabled` | `true` | Emit `wgpolicyk8s.io` PolicyReport CRs. |
| `alertmanager.url` | `""` | AlertManager v2 endpoint. Empty disables. |
| `certManager.enabled` | `false` | Use cert-manager for TLS. |
| `rules.cr` | `true` | Load `PortalClusterRule` / `PortalRule` CRs. |
| `rules.folderConfigMap` | `""` | Mount a ConfigMap of folder-format rules. |
| `watchedGvks` | `[]` | Extra GVKs to start informers for (`group/version/Kind`). |
| `rbac.actions.*` | all `false` | Conditional RBAC grants per action type. |

## Install

```bash
helm install portal deploy/helm/portal \
  -n portal-system --create-namespace \
  -f values-prod.yaml
```

## Verify

```bash
kubectl get validatingwebhookconfiguration portal.io -o jsonpath='{.webhooks[0].failurePolicy}'
# → Fail
kubectl get pdb -n portal-system
kubectl rollout status -n portal-system deployment/portal
```
