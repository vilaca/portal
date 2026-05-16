# Enforce a `team` label on Deployments

Require every Deployment to declare ownership via a `team` label.

## Manifest

```yaml
apiVersion: portal.io/v1alpha1
kind: PortalClusterRule
metadata:
  name: deployments-require-team-label
spec:
  name: deployments-require-team-label
  enabled: true
  severity: medium
  mode: [admission]
  enforcementAction: deny
  match:
    gvk:
      - {group: apps, version: v1, kind: Deployment}
  rule: not ("team" in keys(object.metadata.labels ?? {}))
```

`object.metadata.labels` may be `nil` on a freshly-defined Deployment, so the `?? {}` fallback gives `keys()` something to look at.

## Test

```bash
# Should be denied
kubectl create deployment nginx --image=nginx

# Should be admitted
kubectl create deployment nginx --image=nginx -o yaml --dry-run=client \
  | yq '.metadata.labels.team = "platform"' \
  | kubectl apply -f -
```

## Variant — audit existing Deployments

Add `audit` to `mode` and remove `enforcementAction` (or set it to `warn`) to soft-roll the rule:

```yaml
mode: [admission, audit]
enforcementAction: warn
```

Audit reports unlabelled Deployments via `portal_audit_violations` and PolicyReport without blocking anything. Once the count reaches zero, flip back to `deny`.
