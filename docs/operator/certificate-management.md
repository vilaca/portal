# Certificate management

Portal's admission webhook is served over TLS. `kube-apiserver` validates the leaf against the `caBundle` declared in the `ValidatingWebhookConfiguration`. There are two supported sources for the cert + bundle, selected by `certManager.enabled` in `values.yaml`.

## Mode A — cert-manager (recommended for production)

Set:

```yaml
certManager:
  enabled: true
  issuer:
    kind: ClusterIssuer
    name: my-internal-issuer
```

The chart renders a `Certificate` resource targeting the Portal webhook Service DNS names. cert-manager:

- Issues the leaf and stores it in a `Secret` mounted at `/etc/portal/certs` (`tls.crt`, `tls.key`).
- Stamps the `ValidatingWebhookConfiguration` with the issuer's CA via the `cert-manager.io/inject-ca-from` annotation handled by cert-manager's CA injector controller.
- **Handles rotation**. When the leaf nears expiry, cert-manager re-issues; Portal picks up the new cert on next restart.

This is the only mode with automatic rotation.

## Mode B — built-in self-signed bootstrap (default)

When `certManager.enabled: false`, the chart runs `portal init-certs` as a Pod-level init-container before the main webhook container starts. The init-certs subcommand is implemented at `cmd/portal/init_certs.go`, with the orchestrator logic at `internal/admission/initcerts.go::EnsureCerts`.

On each Pod start:

1. **Read the Secret.** The chart's `templates/secret-bootstrap.yaml` ships an empty Opaque Secret named `portal-webhook-tls` with `helm.sh/resource-policy: keep`. init-certs reads it and parses `tls.crt`, `tls.key`, `ca.crt`.
2. **Validate.** The cert must parse, chain to the CA, and be more than `renewBefore` away from its `NotAfter`. Default `renewBefore` is 30 days.
3. **Regenerate (if step 2 fails or the Secret is empty).** init-certs mints a fresh ECDSA P-256 CA + leaf (1-year validity each) and writes them back into the Secret with `type: kubernetes.io/tls`.
4. **Patch the WebhookConfiguration.** init-certs JSON-patches every `webhooks[].clientConfig.caBundle` in `ValidatingWebhookConfiguration/portal` to the (possibly unchanged) CA bundle. The patch is idempotent — a no-op when the field is already correct.
5. **Mirror to disk.** init-certs writes `tls.crt`, `tls.key`, `ca.crt` into `/etc/portal/certs` (a shared `emptyDir` mounted by both init and main containers). This bypasses kubelet's Secret-volume refresh timing — the main container reads the new material immediately.

After init exits successfully, the main `portal run` container starts, finds the cert files already in `/etc/portal/certs`, and serves TLS.

### Failure mode

If init-certs fails (RBAC missing, API unreachable, malformed Secret state) the Pod ends up in `Init:Error`, the Deployment is `Available: False`, no Pod backs the webhook Service, and the `failurePolicy: Fail` causes kube-apiserver to reject CREATE/UPDATE in workload namespaces. System namespaces stay reachable thanks to the chart's `namespaceSelector` exclusions. This is the desired loud-failure behaviour for a security webhook.

### RBAC scope

`templates/clusterrole.yaml` grants Portal's ServiceAccount:

- `get`, `patch`, `update` on `validatingwebhookconfigurations` with `resourceNames: [<chart fullname>]`. Scoped so the grant can only touch Portal's own WebhookConfig.
- `list`, `watch` on `validatingwebhookconfigurations` unscoped (Kubernetes RBAC doesn't honour `resourceNames` for list/watch). init-certs only uses `get`, so this is reserved for future reconciliation work.

`templates/role.yaml` grants in the install namespace:

- `get`, `update`, `patch` on the TLS Secret, again `resourceNames`-scoped to the chart's Secret name.
- `create` on Secrets (unscoped, namespace-bounded) for the first-install case where the bootstrap placeholder doesn't yet exist.

Both grants are emitted **only when `certManager.enabled: false`**. cert-manager has its own RBAC; Portal doesn't need either grant in Mode A.

## Rotation

- **Mode A (cert-manager)** — automatic. Tune `Certificate.spec.renewBefore` for a longer grace period.
- **Mode B (init-certs)** — semi-automatic. init-certs regenerates whenever the leaf's `NotAfter` is within 30 days on Pod start. Manual rotation:

```bash
kubectl rollout restart deployment/portal -n portal-system
```

The restart re-runs init-certs. Inside the 30-day window, the existing chain is reused (no regeneration). Outside the window, fresh chain + WebhookConfig patch. To force a rotation any time:

```bash
kubectl delete secret portal-webhook-tls -n portal-system
kubectl rollout restart deployment/portal -n portal-system
```

The next Pod start regenerates the chain, upserts the Secret, and re-patches the WebhookConfiguration.

## Where the chart wires this up

- `deploy/helm/portal/templates/deployment.yaml` — the init-container block (gated on `not .Values.certManager.enabled`) and the volume-type switch (Secret vs emptyDir).
- `deploy/helm/portal/templates/clusterrole.yaml` — the scoped WebhookConfig grant (also gated).
- `deploy/helm/portal/templates/role.yaml` — the scoped Secret grant (also gated).
- `deploy/helm/portal/templates/certmanager.yaml` — Mode A's `Certificate` + self-signed `Issuer`.
- `deploy/helm/portal/templates/secret-bootstrap.yaml` — the Opaque placeholder Secret that init-certs upgrades to `kubernetes.io/tls`.
- `deploy/helm/portal/templates/validatingwebhookconfiguration.yaml` — carries the `cert-manager.io/inject-ca-from` annotation (used in Mode A; ignored in Mode B because cert-manager isn't installed).

## Choosing a mode

| Concern | cert-manager | init-certs |
|---|---|---|
| Rotation | automatic | semi-automatic (30-day window auto-renew on Pod start; otherwise restart-driven) |
| Extra dependencies | cert-manager controller + CA injector | none |
| Air-gapped friendliness | requires cert-manager image mirrored | none |
| Portal's RBAC footprint | smaller (no webhook patch grant) | adds scoped `patch` on Portal's WebhookConfig |
| Helm install time | depends on cert-manager being ready | adds an init-container to Pod start (~few hundred ms) |

If you already run cert-manager, choose Mode A. If your security review treats the scoped WebhookConfig patch grant as acceptable, Mode B is fine — and it's the only option for air-gapped clusters without cert-manager.
