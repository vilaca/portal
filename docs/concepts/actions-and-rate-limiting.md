# Actions, idempotency, and rate limiting

Every violation produced by the rule engine flows into the `ActionDispatcher` ([`internal/actions/engine/dispatcher.go`](https://github.com/vilaca/portal/blob/main/internal/actions/engine/dispatcher.go)) which fans out to the per-type `Action` implementations.

## Built-in actions

| Type | Package | Idempotent | Default rate limit |
|------|---------|------------|--------------------|
| `alertmanager` | [`internal/actions/alertmanager_action`](https://github.com/vilaca/portal/blob/main/internal/actions/alertmanager_action/action.go) | yes | 5m |
| `label` | [`internal/actions/label`](https://github.com/vilaca/portal/blob/main/internal/actions/label/action.go) | yes | 5s |
| `annotate` | [`internal/actions/annotate`](https://github.com/vilaca/portal/blob/main/internal/actions/annotate/action.go) | yes | 5s |
| `evict` | [`internal/actions/evict`](https://github.com/vilaca/portal/blob/main/internal/actions/evict/action.go) | no | 30s |
| `patch-networkpolicy` | [`internal/actions/patchnp`](https://github.com/vilaca/portal/blob/main/internal/actions/patchnp/action.go) | yes | 30s |
| `revoke-sa-token` | [`internal/actions/revoketoken`](https://github.com/vilaca/portal/blob/main/internal/actions/revoketoken/action.go) | no | 60s |

Full reference: [../reference/actions.md](../reference/actions.md).

## Idempotency

The idempotency key is `sha256(rule | gvk | namespace | name | actionType)` ([`dispatcher.go`](https://github.com/vilaca/portal/blob/main/internal/actions/engine/dispatcher.go), function `idemKey`). Within the default window (`Action.DefaultRateLimit()`) a second identical attempt is suppressed and counted as `portal_actions_total{action,result="duplicate"}`.

Per-action `Idempotent()` returns whether the action is safe to repeat — `evict` and `revoke-sa-token` return `false`, the rest return `true`.

## Rate limiting

Per-rule overrides use the syntax `<N>/<unit>` where unit is `s`/`sec`/`second`(`s`), `m`/`min`/`minute`(`s`), `h`/`hr`/`hour`(`s`):

```yaml
actions:
  - {type: evict, rateLimit: 5/min}
  - {type: alertmanager, rateLimit: 1/hour}
```

The limiter is a sliding window per `(rule, target)` tuple. Implementation: [`internal/actions/engine/ratelimit.go`](https://github.com/vilaca/portal/blob/main/internal/actions/engine/ratelimit.go). When a request is rejected by the limiter the dispatcher records `portal_actions_total{result="ratelimited"}`.

## Audit log

Each action attempt emits a `slog` JSON line with the dispatcher fields (rule, action, target, result, latency) and increments `portal_actions_total{action,result}`. Possible `result` labels:

| Label | Meaning |
|-------|---------|
| `ok` | Action returned nil. |
| `error` | Action returned non-nil. |
| `duplicate` | Idempotency cache hit. |
| `ratelimited` | Limiter denied. |
| `unknown` | `ActionSpec.Type` not in the action map. |
| `dropped` | Queue full at `Dispatch` time. |

The dispatcher's queue is bounded (default 1024) and the worker pool is bounded (default 16). When the queue is full, the violation is logged + `dropped` is bumped — never silently lost.
