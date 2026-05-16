# Writing a custom Action

A custom action is one struct that implements `api.Action` (`internal/api/action.go`) plus an `init()` that calls `api.RegisterAction`. The dispatcher (`internal/actions/engine`) picks it up from the registry, applies rate-limiting and idempotency, and routes violations to its `Execute()` method.

## The interface

```go
// internal/api/action.go
type Action interface {
    Type() string
    Execute(ctx context.Context, v Violation, params map[string]any) error
    Idempotent() bool
    DefaultRateLimit() time.Duration
}
```

- **`Type()`** — the string a rule writes under `actions[].type`. Globally unique. The five built-ins are `label`, `annotate`, `evict`, `patch-networkpolicy`, `revoke-sa-token` (plus `alertmanager`).
- **`Execute()`** — the work. Receives the `Violation`, the rule's `actions[].params` bag, and a context. Return a non-nil error to mark the dispatch as `result="error"`.
- **`Idempotent()`** — `true` if re-running `Execute` is safe. The dispatcher uses this to decide whether the `IdempotencyStore` suppresses repeats.
- **`DefaultRateLimit()`** — fallback when the rule's `actions[].rateLimit` is empty.

## The Configure-on-client pattern

Actions that need a Kubernetes client cannot get it at `init()` time — the kubeconfig isn't loaded yet. The pattern (look at `internal/actions/label/action.go` for a full reference) is:

1. `init()` registers a placeholder factory that returns an action whose `Execute()` immediately returns `ErrNotConfigured`.
2. The composition root (`cmd/portal/wire.go`) calls a package-level `Configure(client)` once the client exists. This swaps the placeholder for the real action.

```go
package myaction

import (
    "context"
    "errors"
    "time"

    "github.com/vilaca/portal/internal/api"
)

const actionType = "myaction"

var ErrNotConfigured = errors.New("myaction action not configured")

func init() {
    api.RegisterAction(actionType, func() api.Action { return &action{} })
}

type action struct {
    client SomeClient
}

func Configure(c SomeClient) { defaultAction.client = c }
var defaultAction = &action{}

func (a *action) Type() string                  { return actionType }
func (a *action) Idempotent() bool              { return true }
func (a *action) DefaultRateLimit() time.Duration { return time.Minute }

func (a *action) Execute(ctx context.Context, v api.Violation, params map[string]any) error {
    if a.client == nil {
        return ErrNotConfigured
    }
    // ...real work...
    return nil
}
```

## Example — a hypothetical "slack" action

40 lines for a webhook-style notifier:

```go
// internal/actions/slack/action.go
package slack

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "net/http"
    "time"

    "github.com/vilaca/portal/internal/api"
)

const actionType = "slack"

var ErrNotConfigured = errors.New("slack action not configured")

func init() {
    api.RegisterAction(actionType, func() api.Action { return defaultAction })
}

type action struct {
    webhookURL string
    httpClient *http.Client
}

var defaultAction = &action{httpClient: &http.Client{Timeout: 5 * time.Second}}

// Configure is called from cmd/portal/wire.go once the URL is known.
func Configure(url string) { defaultAction.webhookURL = url }

func (a *action) Type() string                    { return actionType }
func (a *action) Idempotent() bool                { return false } // each call is a fresh notification
func (a *action) DefaultRateLimit() time.Duration { return 5 * time.Minute }

func (a *action) Execute(ctx context.Context, v api.Violation, params map[string]any) error {
    if a.webhookURL == "" {
        return ErrNotConfigured
    }
    channel, _ := params["channel"].(string)
    body, _ := json.Marshal(map[string]any{
        "channel": channel,
        "text":    fmt.Sprintf(":warning: *%s* on %s/%s: %s", v.Rule, v.GVK.Kind, v.Name, v.Message),
    })
    req, _ := http.NewRequestWithContext(ctx, http.MethodPost, a.webhookURL, bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    resp, err := a.httpClient.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode >= 300 {
        return fmt.Errorf("slack webhook returned %d", resp.StatusCode)
    }
    return nil
}
```

Wire-up:

```go
// cmd/portal/wire.go
import _ "github.com/vilaca/portal/internal/actions/slack"

// later, in runPortal:
if opts.slackURL != "" {
    slack.Configure(opts.slackURL)
}
```

Rule that uses it:

```yaml
actions:
  - type: slack
    params:
      channel: "#sec-alerts"
    rateLimit: 10/min
    on: [audit]
```

## RBAC implications

If your action talks to the Kubernetes API, you must also update the chart's `templates/clusterrole.yaml` to add the matching verbs, and gate the rule under a `rbac.actions.<name>` Helm value (see how the existing actions do it). Without RBAC the action returns errors and `portal_actions_total{action=...,result="error"}` increments — visible but not catastrophic.

Actions that only talk to **external** services (Slack, PagerDuty, a custom webhook) need no Kubernetes RBAC.

## Testing

Each built-in action has a small unit test under `internal/actions/<name>/action_test.go` — copy that pattern:

- Construct the action via the package factory.
- Stub the client interface.
- Call `Execute` directly and assert side effects.
- Cover `ErrNotConfigured` (action used before `Configure()`).
- Cover `Idempotent()` and `DefaultRateLimit()` return values.

End-to-end coverage lives in `deploy/test/`; new actions land integration tests there too.
