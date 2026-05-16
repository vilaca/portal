# Revoke ServiceAccount token on suspicious exec (v2 preview)

> **v2 preview.** The `runtime` mode and the runtime EventSource are not enabled in v1. This entry sketches the intended use of [`internal/actions/revoketoken`](../../internal/actions/revoketoken/action.go) once Portal subscribes to the Kubernetes API audit log. See [POC-TO-PRODUCTION.md §v2 implementation plan (sketch — not in v1)](../POC-TO-PRODUCTION.md#v2-implementation-plan-sketch--not-in-v1).

## Intended manifest

```yaml
apiVersion: portal.io/v1alpha1
kind: PortalClusterRule
metadata:
  name: revoke-on-prod-exec
spec:
  name: revoke-on-prod-exec
  enabled: true
  severity: critical
  mode: [runtime]
  match:
    gvk:
      - {group: "", version: v1, kind: Pod}
  rule: >
    request.verb == "create"
    && request.subresource == "exec"
    && metadata.namespace == "production"
  actions:
    - type: revoke-sa-token
```

## Action behaviour today

The action implementation is in place: it deletes the ServiceAccount token Secret backing the violating pod's `spec.serviceAccountName`, forcing rotation. `Idempotent()` returns `false` (a recreated Secret can be deleted again on the next event); `DefaultRateLimit()` is 60 s.

It will not fire from `admission` or `audit` modes because the K8s API audit log carries exec/attach/port-forward events that the AdmissionReview pipeline never sees. In v1 the action is reachable only by direct dispatch in tests.

## RBAC

`rbac.actions.revoketoken: true` in `values.yaml` grants `delete` on `secrets`. See [../reference/helm-values.md](../reference/helm-values.md).

## When this lights up

When v2 lands, `portal run --runtime` will register an `EventSource` that reads the API server's audit log (webhook-backend or audit-policy file) and feeds the same dispatcher. The rule above will fire on every `pods/exec` event in `production` and rotate the SA token — a Falco-Talon-class response in one binary.
