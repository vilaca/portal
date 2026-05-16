# Require a PodDisruptionBudget per Deployment

Cross-resource example: a Deployment violates if no PodDisruptionBudget selects its pods. Uses `cluster.poddisruptionbudgets.list(...)` from the informer cache (see [../concepts/cross-resource.md](../concepts/cross-resource.md)).

## Manifest

```yaml
apiVersion: portal.io/v1alpha1
kind: PortalClusterRule
metadata:
  name: deployments-require-pdb
spec:
  name: deployments-require-pdb
  enabled: true
  severity: medium
  mode: [admission, audit]
  enforcementAction: warn
  match:
    gvk:
      - {group: apps, version: v1, kind: Deployment}
  rule: >
    len(cluster.poddisruptionbudgets.list(
      object.metadata.namespace,
      object.spec.selector.matchLabels
    )) == 0
```

The rule asks the informer cache for every PDB in the Deployment's namespace whose selector matches the Deployment's pod template labels. Zero matches → violation.

## RBAC

Portal needs `get, list, watch` on `poddisruptionbudgets.policy`. Either rely on the chart's static-dependency extraction (rules referencing `cluster.poddisruptionbudgets.*` add the GVK automatically) or set:

```yaml
# values.yaml
watchedGvks:
  - "policy/v1/PodDisruptionBudget"
```

## Reverse-dependency behaviour

When a `PodDisruptionBudget` is created, updated, or deleted, every Deployment that referenced it via the lookup is re-enqueued for re-evaluation. The violation clears (or appears) within ~1 s of the PDB change. Cycle protection caps re-evals at 3 per `(rule, object)` per 10 s window; excess re-evals are counted in `portal_lookup_cycle_suppressed_total`.

## Test

```bash
kubectl create namespace pdb-demo
kubectl -n pdb-demo create deployment web --image=nginx
# admission: warn → admitted with kubectl warning

kubectl -n pdb-demo apply -f - <<'YAML'
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: web
spec:
  minAvailable: 1
  selector:
    matchLabels:
      app: web
YAML
# audit: violation clears for the Deployment within ~1 s
```
