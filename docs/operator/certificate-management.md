# Certificate management

Portal's admission webhook is served over TLS. `kube-apiserver` validates the leaf against the `caBundle` declared in the `ValidatingWebhookConfiguration`. There are two supported sources for the cert + bundle.

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
- **Handles rotation**. When the leaf nears expiry, cert-manager re-issues; Portal picks up the new cert on next restart (or sooner if you front the cert with a file-watching reloader — not built in today).

This is the only mode with automatic rotation.

## Mode B — built-in self-signed bootstrap (default)

When `certManager.enabled: false`, Portal generates its own certificate on startup. From `internal/admission/cert.go` — `LoadOrGenerate`:

1. If `/etc/portal/certs/tls.crt` and `tls.key` already exist, they are reused. (The chart's `pre-install` Job creates a Secret with these files, mounted into Portal's pod.)
2. Otherwise Portal generates:
   - An **ECDSA P-256 CA** with a 1-year `NotAfter`, subject `CN=portal-admission-ca`.
   - An **ECDSA P-256 leaf** signed by that CA, with a 1-year `NotAfter`, DNS SANs including `localhost` plus whatever DNS names the wire-up code passes (the in-cluster Service FQDN).
3. The leaf is written to `tls.crt` / `tls.key` and the CA to `ca.crt`, all mode `0600` inside `--cert-dir` (default `/etc/portal/certs`).

### Honest caveat about the `caBundle`

The `ValidatingWebhookConfiguration.caBundle` must contain a base64-encoded copy of the CA that signed the leaf. The chart currently sets this field to `""`. For Mode A, cert-manager fills it in via injection. For Mode B (self-signed), the chart relies on a `pre-install` Job that:

1. Runs `LoadOrGenerate` once into a Secret.
2. Patches the `ValidatingWebhookConfiguration.webhooks[].clientConfig.caBundle` with the generated CA.

`internal/admission/cert.go`'s `PatchWebhookConfiguration` is a deliberate stub — it returns an error so wire-up code surfaces a clear message instead of silently failing. The patch is performed by the chart Job, not by the Portal process.

If you skip the chart Job (e.g. you're running Portal directly via `portal run` outside Helm), you must perform the patch by hand:

```bash
CA=$(base64 < /etc/portal/certs/ca.crt | tr -d '\n')
kubectl patch validatingwebhookconfiguration portal.io \
    --type=json \
    -p "[{\"op\":\"replace\",\"path\":\"/webhooks/0/clientConfig/caBundle\",\"value\":\"$CA\"}]"
```

This is documented openly because it's a real failure mode for first-time installers — please pick Mode A in production where possible.

## Rotation

- **Mode A (cert-manager)** — automatic. Tune `Certificate.spec.renewBefore` if you want a longer grace period.
- **Mode B (self-signed)** — manual. The CA and leaf both expire in 1 year. To rotate:

```bash
kubectl delete secret portal-webhook-tls -n portal-system
kubectl rollout restart deployment/portal -n portal-system
```

The next pod start regenerates the cert via `LoadOrGenerate`, the chart's reconciliation re-runs the pre-install Job (or you can re-run the patch manually). Plan rotation before the 1-year mark; the readiness probe will start failing once `kube-apiserver` rejects the expired leaf.

## Where the chart wires this up

- `deploy/helm/portal/templates/certmanager.yaml` — Mode A's `Certificate`.
- `deploy/helm/portal/templates/secret-bootstrap.yaml` — Mode B's Secret skeleton (populated by the pre-install Job).
- `deploy/helm/portal/templates/validatingwebhookconfiguration.yaml` — the `caBundle` placeholder.
