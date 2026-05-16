# Allow only approved image registries

Block images pulled from registries outside an allow-list. Uses the pod sugar's parsed `container.image.registry` (see [../concepts/context-and-pod-sugar.md](../concepts/context-and-pod-sugar.md)).

## Manifest

```yaml
apiVersion: portal.io/v1alpha1
kind: PortalClusterRule
metadata:
  name: allowed-registries
spec:
  name: allowed-registries
  enabled: true
  severity: high
  mode: [admission, audit]
  enforcementAction: deny
  match:
    gvk:
      - {group: "", version: v1, kind: Pod}
  rule: >
    not (container.image.registry in ["ghcr.io", "registry.k8s.io", "quay.io"])
```

The pod ContextBuilder parses `.spec.containers[*].image` into `container.image.{registry,name,tag,sha256}`. An image like `nginx:latest` resolves to registry `docker.io` (the implicit default).

## Test

```bash
# Should be denied
kubectl run nginx --image=docker.io/nginx:latest

# Should be admitted
kubectl run pause --image=registry.k8s.io/pause:3.9
```

## Variant — pin by sha256 for production

```yaml
rule: >
  metadata.namespace == "production"
  && container.image.sha256 == ""
```

Denies any pod in the `production` namespace whose image isn't pinned by digest.
