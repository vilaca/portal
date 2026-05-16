# Writing a custom OutputSink

A sink consumes `api.Violation`s and writes them somewhere visible — AlertManager, PolicyReport CRDs, Prometheus counters, stdout JSON. Adding a new sink is one struct + one `init()`.

## The interface

```go
// internal/api/sink.go
type OutputSink interface {
    Name() string
    Emit(ctx context.Context, v Violation) error
    Close() error
}
```

- **`Name()`** — used for logging and metrics (e.g. `slog` warnings on `Emit` errors). Globally unique.
- **`Emit()`** — write the violation. Must be **safe for concurrent calls** because both the admission handler and audit workers fan out across all sinks. Return a non-nil error to surface a log line; the calling code logs and continues — a single sink failure must not cascade.
- **`Close()`** — flush and release. Called once at shutdown.

## Configure-on-client pattern

Sinks that need a remote endpoint or a Kubernetes client follow the same pattern as actions (see `custom-action.md`):

1. `init()` registers a no-op factory.
2. The composition root (`cmd/portal/wire.go`) calls a package-level `Configure(...)` once the URL/client/credentials are known.

For local sinks (stdout JSON, Prometheus counters) this isn't needed — they're fully self-contained.

## Example — a hypothetical "syslog" sink

```go
// internal/sink/syslog/sink.go
package syslog

import (
    "context"
    "errors"
    "fmt"
    "log/syslog"
    "sync"

    "github.com/vilaca/portal/internal/api"
)

func init() {
    api.RegisterSink("syslog", func() api.OutputSink { return defaultSink })
}

type sink struct {
    mu     sync.Mutex
    writer *syslog.Writer
}

var defaultSink = &sink{}

// Configure dials the syslog daemon. Called from wire-up once the address
// is known (Helm value `syslog.address`).
func Configure(network, raddr string, priority syslog.Priority, tag string) error {
    w, err := syslog.Dial(network, raddr, priority, tag)
    if err != nil {
        return err
    }
    defaultSink.mu.Lock()
    defer defaultSink.mu.Unlock()
    if defaultSink.writer != nil {
        _ = defaultSink.writer.Close()
    }
    defaultSink.writer = w
    return nil
}

func (s *sink) Name() string { return "syslog" }

func (s *sink) Emit(_ context.Context, v api.Violation) error {
    s.mu.Lock()
    w := s.writer
    s.mu.Unlock()
    if w == nil {
        return errors.New("syslog sink not configured")
    }
    line := fmt.Sprintf("portal violation rule=%q sev=%s gvk=%s ns=%s name=%s msg=%q",
        v.Rule, v.Severity, v.GVK, v.Namespace, v.Name, v.Message)
    return w.Warning(line)
}

func (s *sink) Close() error {
    s.mu.Lock()
    defer s.mu.Unlock()
    if s.writer == nil {
        return nil
    }
    err := s.writer.Close()
    s.writer = nil
    return err
}
```

Wire-up:

```go
// cmd/portal/wire.go
import _ "github.com/vilaca/portal/internal/sink/syslog"

// later:
if opts.syslogAddr != "" {
    if err := syslog.Configure("udp", opts.syslogAddr, syslog.LOG_LOCAL0, "portal"); err != nil {
        return fmt.Errorf("syslog configure: %w", err)
    }
}
```

## What the wire-up loop does with sinks

`cmd/portal/wire.go` enumerates the sink registry (`api.Sinks()`), filters by enabled flags, builds each into a slice, and passes the slice to the admission and audit constructors. They iterate the slice on every violation, calling `Emit()` in order. There's no fan-out across goroutines today — sinks are expected to be fast (microseconds). Sinks with blocking I/O should buffer internally.

## Testing

Pattern from `internal/sink/alertmanager/`:

- Spin up an `httptest.Server` that captures requests.
- Configure the sink against the test server URL.
- Call `Emit()` with a synthetic `api.Violation`.
- Assert the captured request matches expectations (golden-JSON file under `testdata/`).

For PolicyReport-class sinks (Kubernetes-CRD-emitting), use `controller-runtime/pkg/client/fake.NewClientBuilder()` to verify the emitted object shape.
