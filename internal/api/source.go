package api

import "context"

// EventSource produces evaluation events. The admission webhook, the audit
// informer loop, and the NetworkPolicy analyser each implement EventSource and
// feed the same RuleEngine + ActionDispatcher pipeline.
type EventSource interface {
	Name() string
	// Start blocks until ctx is cancelled or the source errors fatally. onEvent
	// is invoked for each event; the source is responsible for any internal
	// goroutines (informers, HTTP handlers).
	Start(ctx context.Context, onEvent func(Context, EventMeta)) error
	Stop(ctx context.Context) error
}
