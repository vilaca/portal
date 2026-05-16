package api

import "context"

// OutputSink writes violations somewhere visible. AlertManager, PolicyReport,
// Prometheus, stdout/JSON all implement this. Each sink is independently
// enable-able and must be safe for concurrent Emit calls.
type OutputSink interface {
	Name() string
	Emit(ctx context.Context, v Violation) error
	Close() error
}
