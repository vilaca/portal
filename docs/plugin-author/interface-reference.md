# Interface reference

> **Note.** This page is intended to be generated from `internal/api/*.go` Godoc via `gomarkdoc` and `make generate-docs`. Until that's wired into CI, the signatures below are hand-transcribed from the `api` package and reviewed in PR. CI drift-check will replace this content with the generated version.

The `internal/api` package is the only Portal package that other `internal/*` packages may import. It exports zero K8s/expr-lang dependencies — only Go interfaces and DTOs. This is the load-bearing module boundary described in `../contributing/module-boundaries.md`.

## ExpressionEngine

```go
type ExpressionEngine interface {
    // Name is "expr", "cel", "rego" — used in metrics and the Rule's selector.
    Name() string
    // Compile parses one rule expression. Diagnostics include line/column when
    // available so PortalClusterRule.status.parseError is human-readable.
    Compile(expression string) (Program, error)
}
```

## Program

```go
type Program interface {
    // Eval is safe for concurrent calls.
    Eval(ctx Context) (bool, error)
}
```

## RuleEngine

```go
type RuleEngine interface {
    // Evaluate dispatches a Context to every rule indexed under that GVK and
    // returns the produced Violations.
    Evaluate(ctx Context, meta EventMeta) []Violation
}
```

## RuleIndex

```go
type RuleIndex interface {
    // ForGVK returns the rules whose Match.GVK includes gvk, filtered by enabled.
    ForGVK(gvk schema.GroupVersionKind) []Rule
    // All returns every enabled rule. Used for static dependency extraction.
    All() []Rule
}
```

## EventSource

```go
type EventSource interface {
    Name() string
    // Start blocks until ctx is cancelled or the source errors fatally. onEvent
    // is invoked for each event; the source is responsible for any internal
    // goroutines (informers, HTTP handlers).
    Start(ctx context.Context, onEvent func(Context, EventMeta)) error
    Stop(ctx context.Context) error
}
```

## OutputSink

```go
type OutputSink interface {
    Name() string
    // Emit is safe for concurrent calls.
    Emit(ctx context.Context, v Violation) error
    Close() error
}
```

## Action

```go
type Action interface {
    Type() string
    Execute(ctx context.Context, v Violation, params map[string]any) error
    Idempotent() bool
    DefaultRateLimit() time.Duration
}
```

## ContextBuilder

```go
type ContextBuilder interface {
    Supports(gvk schema.GroupVersionKind) bool
    // Build converts a raw *unstructured.Unstructured into an evaluation
    // Context. Pod-shaped builders also implement BuildAll for the multi-
    // container fan-out (per std/init/ephemeral container).
    Build(obj *unstructured.Unstructured) (Context, error)
}
```

## ActionDispatcher

```go
type ActionDispatcher interface {
    // Dispatch is non-blocking; respects rate limit + idempotency.
    Dispatch(ctx context.Context, v Violation)
    // Drain blocks until in-flight dispatches finish or ctx is cancelled.
    Drain(ctx context.Context) error
}
```

## RateLimiter

```go
type RateLimiter interface {
    // Allow returns false to mean "drop"; the dispatcher counts these into
    // portal_actions_total{result="ratelimited"}.
    Allow(key string, window time.Duration) bool
}
```

## IdempotencyStore

```go
type IdempotencyStore interface {
    // Seen returns true if the key has been observed within ttl, otherwise
    // records it and returns false.
    Seen(key string, ttl time.Duration) bool
}
```

## RuleLoader

```go
type RuleLoader interface {
    Name() string
    Start(ctx context.Context, onUpdate func(snapshot []Rule)) error
    Stop(ctx context.Context) error
}
```

## Lookup

```go
type Lookup interface {
    // ByName fetches a single object from the cache. Returns nil if absent.
    ByName(gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error)
    // List returns every object of gvk in namespace matching selector. Empty
    // namespace means cluster-wide.
    List(gvk schema.GroupVersionKind, namespace string, selector map[string]string) ([]*unstructured.Unstructured, error)
    // Watched reports the set of GVKs this Lookup currently has informers for.
    Watched() []schema.GroupVersionKind
}
```

## DepRecorder

```go
type DepRecorder interface {
    // Record adds the edge (rule, observed) → referenced to the dep index.
    Record(rule string, observed, referenced ObjectRef)
    // Dependents returns every (rule, observed) pair that depends on the
    // referenced object. Called from informer event handlers to enqueue
    // re-evaluation of dependents.
    Dependents(referenced ObjectRef) []DepEntry
}
```

## Registration entry points

Implementations self-register at `init()` time:

```go
api.RegisterEngine(name, ctor)
api.RegisterAction(typ,  ctor)
api.RegisterSink(name,   ctor)
api.RegisterContextBuilder(name, ctor)
```

The composition root (`cmd/portal/wire.go`) reads each registry, filters by enabled flags, and injects the chosen implementations into constructors. No reflection; no DI framework.
