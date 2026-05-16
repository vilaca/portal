package api

import "sync"

// The registries below let modules self-register at package init() time. The
// composition root in cmd/portal/wire.go enumerates each registry, filters by
// enabled flags, and injects the chosen implementations. No reflection, no DI
// framework — idiomatic Go.
//
// Registration is global, write-once at startup, and read-many at composition
// time. The mutex covers tests that mutate the registry from setup code.

var (
	registryMu sync.RWMutex

	engines    = map[string]func() ExpressionEngine{}
	actions    = map[string]func() Action{}
	sinks      = map[string]func() OutputSink{}
	builders   = map[string]func() ContextBuilder{}
)

// RegisterEngine adds an ExpressionEngine factory. Last writer wins.
func RegisterEngine(name string, ctor func() ExpressionEngine) {
	registryMu.Lock()
	defer registryMu.Unlock()
	engines[name] = ctor
}

// Engine returns a constructed ExpressionEngine by name, or nil if absent.
func Engine(name string) ExpressionEngine {
	registryMu.RLock()
	ctor := engines[name]
	registryMu.RUnlock()
	if ctor == nil {
		return nil
	}
	return ctor()
}

// RegisterAction adds an Action factory.
func RegisterAction(typ string, ctor func() Action) {
	registryMu.Lock()
	defer registryMu.Unlock()
	actions[typ] = ctor
}

// ActionFor returns a constructed Action by type, or nil if absent.
func ActionFor(typ string) Action {
	registryMu.RLock()
	ctor := actions[typ]
	registryMu.RUnlock()
	if ctor == nil {
		return nil
	}
	return ctor()
}

// RegisterSink adds an OutputSink factory.
func RegisterSink(name string, ctor func() OutputSink) {
	registryMu.Lock()
	defer registryMu.Unlock()
	sinks[name] = ctor
}

// Sinks returns a snapshot of every registered sink factory.
func Sinks() map[string]func() OutputSink {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make(map[string]func() OutputSink, len(sinks))
	for k, v := range sinks {
		out[k] = v
	}
	return out
}

// RegisterContextBuilder adds a ContextBuilder factory.
func RegisterContextBuilder(name string, ctor func() ContextBuilder) {
	registryMu.Lock()
	defer registryMu.Unlock()
	builders[name] = ctor
}

// ContextBuilders returns a snapshot of every registered builder factory, in
// no particular order. The engine queries each in turn via Supports(gvk).
func ContextBuilders() map[string]func() ContextBuilder {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make(map[string]func() ContextBuilder, len(builders))
	for k, v := range builders {
		out[k] = v
	}
	return out
}
