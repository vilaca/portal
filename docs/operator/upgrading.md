# Upgrading

Portal is shipped as a Helm chart (`deploy/helm/portal/`) plus a small handful of `CustomResourceDefinition`s (`deploy/helm/portal/crds/` and `deploy/crds/`). Upgrades are a Helm operation; the CRD set has its own lifecycle.

## Standard upgrade workflow

```bash
# 1. Fetch the new chart version.
helm repo update

# 2. Render and review (dry-run) — read the diff before applying.
helm upgrade --dry-run portal portal/portal --version v0.2.0 -n portal-system

# 3. Apply.
helm upgrade portal portal/portal --version v0.2.0 -n portal-system
```

Wait for the new replicas to roll out (`kubectl rollout status deployment/portal -n portal-system`). The `PodDisruptionBudget` ensures admission stays available throughout — but verify with `kubectl get events -n portal-system` if the upgrade stalls.

## CRD version skew

CRDs in `deploy/helm/portal/crds/` are picked up by `helm install` but **not** upgraded by `helm upgrade`. This is Helm's documented behaviour, not a Portal quirk. Between Portal versions that change CRD schemas you must explicitly apply:

```bash
kubectl apply -f deploy/crds/
kubectl apply -f deploy/helm/portal/crds/
```

Or, equivalently, fetch the new chart bundle and:

```bash
helm pull portal/portal --version v0.2.0 --untar
kubectl apply -f portal/crds/
```

Then run `helm upgrade` as above. The order matters: CRDs must be present before Portal pods that depend on them start. Existing CRs are unaffected by additive schema changes; the API server applies the new structural schema on next write.

## CRD conversion

Portal currently ships **one stored version** of each CRD (`v1alpha1`). No conversion webhook is required. When the schema graduates to `v1beta1` or `v1`, a conversion webhook will land alongside it; until then, no extra work.

## Rollback

```bash
helm history portal -n portal-system
helm rollback portal <REVISION> -n portal-system
```

Two known-good break-glass procedures if Helm rollback gets stuck:

### Stuck because the new `ValidatingWebhookConfiguration` is wrong

If a misconfigured upgrade made the webhook itself reject the rollback's API calls (rare, but possible if `failurePolicy: Fail` plus a self-conflicting `objectSelector`):

```bash
# Delete the broken webhook config — this temporarily makes admission absent
# for audited namespaces. System namespaces are unaffected.
kubectl delete validatingwebhookconfiguration portal.io

# Roll back the chart.
helm rollback portal <REVISION> -n portal-system

# The chart re-creates the webhook config from the rolled-back version.
```

See `recovery-from-self-lockout.md` for the full procedure when even `kubectl` is being rejected.

### Stuck because new pods can't start (image pull, ConfigMap missing)

`helm rollback` is normally enough — the previous ReplicaSet scales back up. If both ReplicaSets are at 0:

```bash
kubectl scale deployment/portal --replicas=2 -n portal-system
```

…to nudge the rollback scheduler.

## Pre-upgrade checklist

1. **Read the release notes** — Portal is greenfield; v1.x → v1.(x+1) is the supported step. Skipping minor versions is best-effort.
2. **Check `.status.parseError` is empty on every CR** before upgrading. A rule that compiles under the current expr-lang version but not the next is the most likely upgrade surprise. Run `kubectl get portalclusterrule -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.parseError}{"\n"}{end}'`.
3. **Confirm `replicaCount >= 2`** and the PDB is in place if the upgrade ships new admission code.
4. **Have the break-glass `kubectl delete validatingwebhookconfiguration portal.io` command handy** in a copy-paste buffer.
