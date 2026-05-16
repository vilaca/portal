# Write your first rule

A Portal rule is a small YAML document with a single `expr-lang` boolean expression. This walkthrough writes one from scratch, applies it as a `PortalClusterRule`, and exercises both `deny` and `warn` enforcement modes.

## 1. The manifest

```yaml
# rule-disallow-hostnetwork.yaml
apiVersion: portal.io/v1alpha1
kind: PortalClusterRule
metadata:
  name: disallow-hostnetwork
spec:
  name: disallow-hostnetwork
  enabled: true
  severity: high
  mode: [admission]
  enforcementAction: deny
  match:
    gvk:
      - {group: "", version: v1, kind: Pod}
  rule: spec.hostNetwork == true
  actions:
    - {type: alertmanager, template: hostnetwork-blocked}
```

### Field walkthrough

- `enabled` — boolean kill-switch. `false` parks the rule.
- `mode` — which loops evaluate this rule. Allowed: `admission`, `audit`, `network`, and (v2) `runtime`. Multiple modes are valid.
- `match.gvk` — the GroupVersionKind set the rule applies to. Required.
- `enforcementAction` — admission-only verdict: `deny` | `warn` | `dryrun`. Ignored in audit; see [../concepts/admission-vs-audit.md](../concepts/admission-vs-audit.md).
- `rule` — an `expr-lang` expression that must evaluate to `bool`. See [../reference/expression-language.md](../reference/expression-language.md).
- `actions` — list of `ActionSpec`s dispatched on violation. See [../reference/actions.md](../reference/actions.md).

The complete field reference is at [../reference/rule-schema.md](../reference/rule-schema.md).

## 2. Apply and inspect

```bash
kubectl apply -f rule-disallow-hostnetwork.yaml

kubectl get portalclusterrule disallow-hostnetwork
# NAME                    AGE
# disallow-hostnetwork    3s

kubectl describe portalclusterrule disallow-hostnetwork
```

The CRD has a `.status` subresource: `evalCount`, `violationCount`, `lastApplied`, `parseError`, `activeOn` (see [`internal/rule/crd/types.go`](../../internal/rule/crd/types.go)). If `spec.rule` fails to compile, `status.parseError` is populated within roughly one reconcile interval.

## 3. Trigger a deny

```bash
kubectl run hostnet --image=alpine --restart=Never \
  --overrides='{"spec":{"hostNetwork":true,"containers":[{"name":"hostnet","image":"alpine"}]}}'
# Error from server: admission webhook "portal.io" denied the request: disallow-hostnetwork
```

## 4. Flip to warn

```bash
kubectl edit portalclusterrule disallow-hostnetwork
# change enforcementAction: deny → warn
```

Re-run the same `kubectl run` command. The pod is now **admitted**, but `kubectl` prints a warning header. Audit logs and PolicyReport entries are recorded either way.

## What to read next

- [../concepts/admission-vs-audit.md](../concepts/admission-vs-audit.md) — when each loop fires.
- [../concepts/context-and-pod-sugar.md](../concepts/context-and-pod-sugar.md) — `container.*` vs `object.*`.
- [../cookbook/](../cookbook/) — worked examples for common policies.
