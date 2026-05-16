# Disallow privileged containers

Block privileged containers at admission **and** surface existing ones via audit. Mirrors [`examples/rules/privileged-container.yaml`](https://github.com/vilaca/portal/blob/main/examples/rules/privileged-container.yaml).

## Manifest

```yaml
apiVersion: portal.io/v1alpha1
kind: PortalClusterRule
metadata:
  name: disallow-privileged
spec:
  name: disallow-privileged
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
  actions:
    - {type: label, key: portal.security/violation, value: "privileged", on: [audit]}
```

The rule iterates per container (see [../concepts/context-and-pod-sugar.md](../concepts/context-and-pod-sugar.md)); init and ephemeral containers are evaluated as well.

## Expected behaviour

- **On apply (admission mode):** every CREATE/UPDATE of a Pod whose security context sets `privileged: true` or `allowPrivilegeEscalation: true` is rejected with the rule name in the deny message.
- **On audit:** pre-existing violating pods produce a `portal_audit_violations` increment + a PolicyReport entry + (when `rbac.actions.label: true`) a `portal.security/violation=privileged` label applied via server-side apply.

## Test

```bash
kubectl apply -f rule.yaml

# Should be denied
kubectl run pwn --image=alpine --restart=Never \
  --overrides='{"spec":{"containers":[{"name":"pwn","image":"alpine","securityContext":{"privileged":true}}]}}'

# Should be admitted
kubectl run ok --image=alpine --restart=Never \
  --command -- sleep 60
```

## Notes

The audit-side label requires `rbac.actions.label: true` in the Helm chart — see [../reference/helm-values.md](../reference/helm-values.md).
