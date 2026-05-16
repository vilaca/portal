# Helm values

> Auto-generated. This file is regenerated from [`deploy/helm/portal/values.yaml`](../../deploy/helm/portal/values.yaml) by [`helm-docs`](https://github.com/norwoodj/helm-docs). The source of truth is the `# --` annotations inside `values.yaml`. Until the generator runs in CI, the hand-written table below covers the most-touched ~12 values.

## Most-touched values

| Key | Default | Description |
|-----|---------|-------------|
| `global.failClosed` | `true` | `failurePolicy: Fail` on the webhook. When false, the chart renders `Ignore`. |
| `global.mode` | `combined` | Deployment topology. `combined` runs all enabled layers in one Deployment. `split` (v1 stub) deploys one per layer. |
| `replicaCount` | `2` | Number of Portal replicas. Minimum 2 for fail-closed HA. |
| `image.repository` | `ghcr.io/vilaca/portal` | Container image repository. |
| `image.tag` | `""` | Container image tag. Defaults to `Chart.appVersion`. |
| `webhook.enabled` | `true` | Run the admission webhook (`portal run --admission`). |
| `webhook.timeoutSeconds` | `5` | `ValidatingWebhookConfiguration.webhooks[].timeoutSeconds`. |
| `webhook.rules` | (pods + workload kinds) | GVKs subject to admission. |
| `audit.enabled` | `false` | Run the audit informer loop (`--audit`). |
| `audit.leaderElection` | `true` | Lease-based leader election. Informers run on every replica; only the leader writes side effects. |
| `audit.resyncPeriod` | `10m` | Informer resync as a safety net. |
| `network.enabled` | `false` | Run the NetworkPolicy analyser (`--network`). Implies audit. |
| `policyReport.enabled` | `true` | Emit `wgpolicyk8s.io/v1alpha2` PolicyReport / ClusterPolicyReport CRs. |
| `alertmanager.url` | `""` | AlertManager v2 endpoint. Empty disables the sink. |
| `certManager.enabled` | `false` | Provision the webhook TLS Secret via cert-manager. |
| `rules.cr` | `true` | Watch PortalClusterRule + PortalRule CRs (`--rules-cr`). |
| `rules.folderConfigMap` | `""` | Mount this ConfigMap at `/etc/portal/rules` and pass `--rules-folder`. |
| `rules.bootstrap` | `true` | Install bootstrap PortalClusterRules via a post-install hook. |
| `watchedGvks` | `[]` | Additional GVKs (`group/version/Kind`) to start informers for. |
| `podDisruptionBudget.minAvailable` | `1` | `minAvailable` on the chart-provided PDB. Set 0 to skip the PDB. |
| `metrics.port` | `9090` | Prometheus / health listener port. |
| `serviceMonitor.enabled` | `false` | Create a Prometheus Operator `ServiceMonitor`. |
| `rbac.create` | `true` | Create the ClusterRole + ClusterRoleBinding. |
| `rbac.actions.label` | `false` | Grant `patch` on workload kinds for the `label` action. |
| `rbac.actions.annotate` | `false` | Grant `patch` on workload kinds for the `annotate` action. |
| `rbac.actions.evict` | `false` | Grant `create` on `pods/eviction`. |
| `rbac.actions.patchnp` | `false` | Grant `patch` on `networkpolicies.networking.k8s.io`. |
| `rbac.actions.revoketoken` | `false` | Grant `delete` on `secrets`. |

See [`deploy/helm/portal/values.yaml`](../../deploy/helm/portal/values.yaml) for the complete set including container security context, node selector, tolerations, affinity, extra env / volumes, and ServiceMonitor scrape interval / labels.
