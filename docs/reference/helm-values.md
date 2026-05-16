# portal

![Version: 0.1.0](https://img.shields.io/badge/Version-0.1.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.1.0](https://img.shields.io/badge/AppVersion-0.1.0-informational?style=flat-square)

Portal — an admission webhook, informer-driven audit loop, and declarative
NetworkPolicy analyser, with a built-in response-action engine. Successor to
podwatcher-poc; AlertManager-compatible, PolicyReport-native, expr-lang rules.

**Homepage:** <https://github.com/vilaca/portal>

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| Joao Vilaca | <joao.vilaca@relexsolutions.com> |  |

## Source Code

* <https://github.com/vilaca/portal>

## Requirements

Kubernetes: `>=1.27.0-0`

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` | Affinity rules applied to Portal pods. The chart adds a preferred pod anti-affinity by default — overridden when this value is non-empty. |
| alertmanager.url | string | `""` | AlertManager v2 alerts endpoint. Empty disables the AlertManager sink. |
| audit.enabled | bool | `false` |  |
| audit.leaderElection | bool | `true` | Enable lease-based leader election. Informers run on every replica; only the leader dispatches actions / writes PolicyReports. |
| audit.resyncPeriod | string | `"10m"` | Informer resync period. Watch events are the main path; resync is a safety net only. |
| certManager.enabled | bool | `false` | Use cert-manager to provision the webhook TLS Secret. When false the binary bootstraps a self-signed CA on startup and patches the ValidatingWebhookConfiguration.caBundle itself. |
| certManager.issuerKind | string | `""` | Issuer kind to use when certManager.enabled. When empty an in-chart self-signed Issuer is created. |
| certManager.issuerName | string | `""` | Issuer name to use when certManager.enabled. When empty an in-chart self-signed Issuer is created. |
| extraEnv | list | `[]` | Extra environment variables for the Portal container. |
| extraVolumeMounts | list | `[]` | Extra volume mounts on the Portal container. |
| extraVolumes | list | `[]` | Extra volumes mounted in the Portal pod. |
| global.failClosed | bool | `true` | Whether the ValidatingWebhookConfiguration uses failurePolicy=Fail. When true, API server requests are rejected if Portal is unavailable — ≥2 replicas + PDB become mandatory. System namespaces are always excluded regardless of this flag (see templates/validatingwebhookconfiguration.yaml). |
| global.mode | string | `"combined"` | Deployment topology. "combined" runs every enabled layer in one Deployment. "split" deploys one Deployment per layer (admission / audit / network) for independent scaling. v1 ships combined only; the split-mode templates are stubbed for forward compat. |
| image.pullPolicy | string | `"IfNotPresent"` | Container image pull policy. |
| image.pullSecrets | list | `[]` | Image pull secrets to attach to the ServiceAccount. |
| image.repository | string | `"ghcr.io/vilaca/portal"` | Container image repository. |
| image.tag | string | `""` | Container image tag. Defaults to the chart appVersion when empty. |
| metrics.port | int | `9090` | Container port the Prometheus /metrics + /healthz + /readyz listener serves on. |
| network.enabled | bool | `false` |  |
| nodeSelector | object | `{}` | Node selector applied to Portal pods. |
| podDisruptionBudget.minAvailable | int | `1` | Minimum available replicas during voluntary disruption. ≥1 is required when failClosed=true. Set to 0 to disable the PDB entirely. |
| podSecurityContext.fsGroup | int | `65532` | Pod-level fsGroup. 65532 is the distroless `nonroot` group. |
| podSecurityContext.runAsNonRoot | bool | `true` | Reject pods whose container security context allows root. |
| podSecurityContext.seccompProfile.type | string | `"RuntimeDefault"` | Seccomp profile applied at pod level. |
| policyReport.enabled | bool | `true` | Emit wgpolicyk8s.io/v1alpha2 PolicyReport / ClusterPolicyReport CRs. |
| rbac.actions.annotate | bool | `false` | Grant `patch` on workload kinds so the annotate action can mutate metadata.annotations via server-side apply. |
| rbac.actions.evict | bool | `false` | Grant `create` on pods/eviction so the evict action can drain pods. |
| rbac.actions.label | bool | `false` | Grant `patch` on workload kinds so the label action can mutate metadata.labels via server-side apply. |
| rbac.actions.patchnp | bool | `false` | Grant `patch` on networkpolicies.networking.k8s.io for the patch-NP action. |
| rbac.actions.revoketoken | bool | `false` | Grant `delete` on secrets so the revoke-sa-token action can force ServiceAccount token rotation. |
| rbac.create | bool | `true` | Create the ClusterRole + ClusterRoleBinding. |
| replicaCount | int | `2` | Number of Portal replicas. 2 is the minimum for fail-closed HA; the PodDisruptionBudget keeps ≥1 healthy during rollouts so workload-namespace admission requests continue to succeed. |
| resources.limits.cpu | string | `"500m"` | CPU limit per Portal pod. |
| resources.limits.memory | string | `"256Mi"` | Memory limit per Portal pod. |
| resources.requests.cpu | string | `"100m"` | CPU request per Portal pod. |
| resources.requests.memory | string | `"128Mi"` | Memory request per Portal pod. |
| rules.bootstrap | bool | `false` | Install a small bootstrap set of PortalClusterRules covering the migrated podwatcher-poc examples. Off by default — operators copy the rules they want from examples/rules/ and kubectl apply them. Set to true once the chart ships a post-install bundle (not in v1). |
| rules.cr | bool | `true` |  |
| rules.folderConfigMap | string | `""` | Name of an existing ConfigMap holding rule YAML files. Mounted at /etc/portal/rules and passed via --rules-folder. Empty disables folder loading. |
| securityContext.allowPrivilegeEscalation | bool | `false` | Disallow privilege escalation. |
| securityContext.capabilities.drop | list | `["ALL"]` | Drop all Linux capabilities; Portal needs none. |
| securityContext.readOnlyRootFilesystem | bool | `true` | Mount the root filesystem read-only. |
| securityContext.runAsGroup | int | `65532` | Run as the distroless nonroot group. |
| securityContext.runAsNonRoot | bool | `true` | Enforce non-root execution at container level. |
| securityContext.runAsUser | int | `65532` | Run as the distroless nonroot user. |
| serviceAccount.annotations | object | `{}` | Extra annotations on the ServiceAccount (useful for IRSA / Workload Identity). |
| serviceAccount.create | bool | `true` | Create a ServiceAccount for Portal. Set to false to reuse an existing one. |
| serviceAccount.name | string | `""` | Name of the ServiceAccount. Defaults to portal.fullname. |
| serviceMonitor.enabled | bool | `false` | Create a Prometheus Operator ServiceMonitor. |
| serviceMonitor.interval | string | `"30s"` | Scrape interval. |
| serviceMonitor.labels | object | `{}` | Extra labels added to the ServiceMonitor (typically the Prometheus `release` label for kube-prometheus-stack). |
| tolerations | list | `[]` | Tolerations applied to Portal pods. |
| watchedGvks | list | `[]` | Additional GVKs to start informers for, in "group/version/Kind" form. Empty group renders as "/v1/ConfigMap" etc. These extend the set computed from the audited rule corpus. |
| webhook.caBundle | string | `""` | Optional pre-baked caBundle (base64). When empty, Portal patches the ValidatingWebhookConfiguration at startup with its self-signed CA, or cert-manager populates it via the Certificate's caBundle injection. |
| webhook.enabled | bool | `true` |  |
| webhook.port | int | `8443` | Container port the webhook TLS server listens on. |
| webhook.rules | list | `[{"apiGroups":[""],"apiVersions":["v1"],"operations":["CREATE","UPDATE"],"resources":["pods"]},{"apiGroups":["apps"],"apiVersions":["v1"],"operations":["CREATE","UPDATE"],"resources":["deployments","statefulsets","daemonsets","replicasets"]},{"apiGroups":["batch"],"apiVersions":["v1"],"operations":["CREATE","UPDATE"],"resources":["jobs","cronjobs"]},{"apiGroups":["networking.k8s.io"],"apiVersions":["v1"],"operations":["CREATE","UPDATE"],"resources":["networkpolicies"]}]` | GVKs subject to admission. Each entry is {apiGroups, apiVersions, resources}. The system-namespace exclusion is layered on top of this via namespaceSelector. |
| webhook.timeoutSeconds | int | `5` | Webhook timeoutSeconds in the ValidatingWebhookConfiguration. |

