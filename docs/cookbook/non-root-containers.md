# Require non-root containers

Reject containers that run as root unless they explicitly opt in via `runAsNonRoot=true` or a non-zero `runAsUser`.

## Manifest

```yaml
apiVersion: portal.io/v1alpha1
kind: PortalClusterRule
metadata:
  name: require-non-root
spec:
  name: require-non-root
  enabled: true
  severity: high
  mode: [admission, audit]
  enforcementAction: deny
  match:
    gvk:
      - {group: "", version: v1, kind: Pod}
  rule: >
    container.securityContext?.runAsNonRoot != true
    && (container.securityContext?.runAsUser ?? 0) == 0
    && (securityContext?.runAsUser ?? 0) == 0
    && securityContext?.runAsNonRoot != true
```

The expression treats container-level settings as overriding the pod-level ones (`container.securityContext.*` first, then the pod's `securityContext.*`). The `?.` / `??` operators avoid NPEs when fields are nil.

## Test

```bash
# Should be denied: explicit root.
kubectl run pwn --image=alpine --restart=Never \
  --overrides='{"spec":{"containers":[{"name":"pwn","image":"alpine","securityContext":{"runAsUser":0}}]}}'

# Should be admitted: explicit non-root.
kubectl run ok --image=alpine --restart=Never \
  --overrides='{"spec":{"securityContext":{"runAsNonRoot":true,"runAsUser":65532},"containers":[{"name":"ok","image":"alpine"}]}}'
```

## Audit-only soft-launch

For existing clusters, start with `mode: [audit]` and no `enforcementAction`. Read `portal_audit_violations{rule="require-non-root"}` to size the cleanup effort before flipping to `deny`.
