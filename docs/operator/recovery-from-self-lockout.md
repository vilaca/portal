# Recovery from self-lockout

A fail-closed admission webhook can lock you out of your own cluster. This page is the break-glass procedure. **Keep it bookmarked.**

## When this applies

You ran into one of:

- Portal pods are `Pending` (image pull, taint, node-pressure) and `failurePolicy: Fail` is rejecting workloads.
- A misconfigured rule corpus is denying everything, including the Portal Deployment's own pod recreates.
- A bad `helm upgrade` shipped a `ValidatingWebhookConfiguration` whose `clientConfig` is broken (wrong service, wrong port, wrong CA).

Common to all three: `kubectl apply -f ...` returns an admission failure for any non-excluded namespace, and you cannot heal forward.

## Why some things still work

The chart's `ValidatingWebhookConfiguration` always excludes:

- `kube-system`
- `kube-public`
- `kube-node-lease`
- Portal's own install namespace (`portal-system` by default)

So `kubectl` commands against those namespaces continue to succeed even when Portal itself is dead. This is **non-negotiable** — `internal/admission/server.go` enforces system-namespace exclusion regardless of caller-supplied options. The recovery procedure leans on this.

## Three-step break-glass

### 1. Disable the webhook

```bash
kubectl delete validatingwebhookconfiguration portal.io
```

If even this is rejected (rare, but possible if the webhook claims authority over `validatingwebhookconfigurations` somehow):

```bash
kubectl delete validatingwebhookconfiguration portal.io --validate=strict=false
```

If that still fails, jump to step 2 first — scale Portal to zero — and come back.

### 2. Scale Portal to zero

```bash
kubectl scale deployment/portal -n portal-system --replicas=0
```

`portal-system` is excluded from Portal's own webhook, so this command always succeeds. With no replicas, the webhook Service has no endpoints, and `failurePolicy: Fail` decides admission for any remaining `ValidatingWebhookConfiguration`. If you completed step 1, the webhook config is already gone and admission is fully open.

### 3. Fix the underlying cause, then restore

Choose the right repair for the failure mode:

- **Pods can't schedule** — fix node capacity / taint / image-pull issue. Scale back: `kubectl scale deployment/portal -n portal-system --replicas=2`.
- **Bad rule corpus** — find the rule with `kubectl get portalclusterrule` and either delete it or fix it:
  ```bash
  kubectl get portalclusterrule
  kubectl delete portalclusterrule <bad-rule>
  ```
  Then scale Portal back.
- **Bad Helm release** — `helm rollback portal <REVISION> -n portal-system`. Helm will re-create the `ValidatingWebhookConfiguration` and the Deployment from the prior good revision.

After the fix, confirm Portal is healthy before letting it gate workloads again:

```bash
kubectl rollout status deployment/portal -n portal-system
kubectl get endpoints portal-webhook -n portal-system   # should have ready endpoints
kubectl --raw "/readyz" --server "https://portal-webhook.portal-system.svc:443"  # 200 OK
```

Then re-apply the webhook configuration (Helm does this automatically; or `kubectl apply -f deploy/helm/portal/templates/validatingwebhookconfiguration.yaml` after rendering).

## Why the `namespaceSelector` exclusions exist

Two reasons:

1. **Bootstrap.** `kube-system` runs CoreDNS, kube-proxy, the metrics-server, etc. If Portal's webhook were to evaluate `kube-system` and reject one of those resources, the cluster would not be a cluster anymore.
2. **Recovery.** The commands in steps 1–3 above operate on `validatingwebhookconfigurations` (cluster-scoped, excluded by being cluster-scoped not namespaced) and on objects in `portal-system`. Both must succeed when Portal is broken.

Operators with `patch` on the `Namespace` resource for an arbitrary namespace can also add the `portal.io/bypass=true` annotation (`internal/admission/server.go` — `DefaultBypassAnnotation`), which short-circuits every request from that namespace to `allowed=true`. This is itself audited (`portal_admission_bypass_total{namespace}` + a slog warning), so detection is wired in by default.

## What you should never do

- **Do not** remove `kube-system`, `kube-public`, `kube-node-lease`, or `portal-system` from the namespace exclusion list. The chart treats this list as mandatory; the chart README documents it; the runtime enforces it.
- **Do not** set `global.failClosed: false` "just in case" — that flips the entire failure model and gives you fail-open by default. Use the bypass annotation per-namespace instead.
