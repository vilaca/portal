# RBAC posture

Portal's ServiceAccount asks for **the minimum verbs needed to do its job, on the GVKs it has been told to watch**. This page captures the philosophy; for the concrete `ClusterRole` rules and per-action toggles see `../operator/rbac-scoping.md`.

## Read scope is broad by design

To audit a GVK, Portal needs `get,list,watch` on it. That set spans:

- Every GVK in the `watchedGvks` Helm value.
- Every GVK referenced by a `cluster.<gvk>.*` lookup in any loaded rule.

For most production deployments that ends up looking like a fair chunk of the core/apps/networking APIs. **This is by design** — Portal cannot reason about what it cannot see. The mitigation is:

- Read-only verbs only. Portal never asks for `create,update,patch,delete` on GVKs it merely audits.
- Verbs are scoped to a known list; there is no `resources: ["*"]` rule.
- Minimum-viable installs can disable layers they don't use (no `--audit` means no informers means no `watch` permissions on audited workloads).

## Write scope is opt-in per action

Action RBAC is gated by Helm values. Defaults from `deploy/helm/portal/values.yaml`:

```yaml
rbac:
  actions:
    label:        false
    annotate:     false
    evict:        false
    patchnp:      false
    revoketoken:  false
```

Each toggle adds exactly one `rule:` block to the `ClusterRole`. With **all toggles off, Portal has zero write capability on cluster workloads** — only on its own status subresources, on PolicyReports, and on Leases.

The AlertManager action does not need cluster RBAC (it's a webhook call to AlertManager), so it's enabled by default.

## Portal does **not** ask for cluster-admin

Specifically, Portal never asks for:

- `*` on any apiGroup or resource.
- `update,patch,delete` on `roles`, `clusterroles`, `rolebindings`, `clusterrolebindings`.
- `escalate` on `clusterroles`.
- `impersonate` on users or groups.
- Write access to its own ServiceAccount or Deployment.

If a Portal upgrade ever introduces a new ClusterRole rule, it is reviewed against the principle above. The reviewer's question: "Is this verb on this resource strictly required to ship the documented feature?"

## Audit Portal itself

The cluster operator should audit Portal's effective permissions periodically:

```bash
kubectl auth can-i --list \
    --as=system:serviceaccount:portal-system:portal
```

The output should match what `operator/rbac-scoping.md` says it should. Deviations are either:

- A new action toggle you turned on and forgot. Audit your Helm values.
- Drift from a manual `kubectl apply -f` of a stale role. Re-run `helm upgrade portal -n portal-system` to reconcile.

## Hardening checklist

1. **Set `rbac.create: false`** if you bring your own ClusterRole (e.g. via Argo CD with a separately-templated role). Portal still works; the chart just doesn't render the role.
2. **Pin Portal's image** (`image.tag: vX.Y.Z`, not `latest`) and verify the digest. RBAC is only as strong as the binary using it.
3. **Restrict who can patch `validatingwebhookconfigurations`** — that's the off-switch.
4. **Restrict who can patch `Namespace` annotations** — that's how the `portal.io/bypass=true` annotation gets set.
5. **Monitor `portal_admission_bypass_total`** in Prometheus. Any non-zero rate is a break-glass that should be reviewed.

For the threat-model context see `threat-model.md`.
