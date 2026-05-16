// Package alertmanager_action wraps the alertmanager sink behind an
// api.Action so the rule's `alert:` shorthand (which the engine expands to
// ActionSpec{Type:"alertmanager",...}) flows through the same dispatcher
// pipeline as every other action — picking up idempotency suppression and
// rate limiting for free.
//
// The action is intentionally thin: Execute just delegates to the wrapped
// sink's Emit. params.template is read for compatibility with the rule
// shorthand but the rendering itself is the sink's responsibility (the
// alertmanager sink already pulls runbook_url etc. from v.Actions).
//
// Registration follows the same placeholder pattern as the other actions:
// init() registers a no-op factory; the composition root replaces it via
// Configure(sink) once the sink is built.
package alertmanager_action

import (
	"context"
	"errors"
	"time"

	"github.com/vilaca/portal/internal/api"
)

const actionType = "alertmanager"

// ErrNotConfigured is returned when Execute runs before Configure().
var ErrNotConfigured = errors.New("alertmanager action not configured")

func init() {
	api.RegisterAction(actionType, func() api.Action { return &action{} })
}

// Configure replaces the registered factory with one backed by sink.
func Configure(sink api.OutputSink) {
	api.RegisterAction(actionType, func() api.Action { return New(sink) })
}

// New constructs the action bound to sink. Passing nil yields a no-op
// action whose Execute returns ErrNotConfigured.
func New(sink api.OutputSink) api.Action {
	return &action{sink: sink}
}

type action struct {
	sink api.OutputSink
}

func (a *action) Type() string                    { return actionType }
func (a *action) Idempotent() bool                { return true }
func (a *action) DefaultRateLimit() time.Duration { return 5 * time.Minute }

// Execute delegates to the wrapped sink. params.template is not used here;
// the alertmanager sink builds the alert payload from v alone.
func (a *action) Execute(ctx context.Context, v api.Violation, _ map[string]any) error {
	if a.sink == nil {
		return ErrNotConfigured
	}
	return a.sink.Emit(ctx, v)
}
