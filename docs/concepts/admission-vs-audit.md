# Admission vs audit

Portal evaluates rules in three loops: **admission**, **audit**, and **network**. The `mode:` field on a rule (`[]api.Mode`, see [`internal/api/rule.go`](https://github.com/vilaca/portal/blob/main/internal/api/rule.go)) opts the rule into one or more.

## When admission fires

- Driven by `ValidatingWebhookConfiguration` — synchronous, pre-persistence.
- Has the inbound object in `request.object` and the prior state (UPDATE) in `request.oldObject`.
- Exposes `request.{operation,dryRun,userInfo}` in the expr-lang env (see [../reference/expression-language.md](../reference/expression-language.md)).
- `enforcementAction` is honoured: `deny` rejects the request, `warn` admits with a `kubectl` warning, `dryrun` admits silently but still records to sinks.
- Code path: [`internal/admission/handler.go`](https://github.com/vilaca/portal/blob/main/internal/admission/handler.go).

## When audit fires

- Driven by `client-go` informers; events arrive on already-persisted objects (`OnAdd`/`OnUpdate`/`OnDelete`).
- Asynchronous. `request.*` is **not bound** — only `object`, `metadata`, and pod-sugar keys are present.
- `enforcementAction` is **ignored**. Audit always feeds the violation to sinks and the action dispatcher.
- Code path: [`internal/audit/controller.go`](https://github.com/vilaca/portal/blob/main/internal/audit/controller.go).

## Picking modes

```yaml
mode: [admission]            # block at the door; nothing to audit
mode: [audit]                # observe-only; pair with deny+warn rule later
mode: [admission, audit]     # block new, surface existing
```

Audit is the safety net: a typo'd field or a missing sugar key in an admission rule still gives audit a chance to surface the problem on the next informer event. For high-confidence policies, run both — admission blocks the bleed-in, audit catches what landed before the rule was installed.

## Ordering of events

- A single `kubectl apply` can produce: an admission decision **then**, if admitted, an audit `OnAdd`. The two are independent invocations of the rule engine with different contexts.
- For an `UPDATE`, admission sees both `request.object` and `request.oldObject`; audit sees only the new state.
- Audit does **not** re-fire on a watch reconnect alone — only on object changes (and the configurable resync interval, default 10 min, used as a safety net).
