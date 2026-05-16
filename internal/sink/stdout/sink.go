// Package stdout implements an api.OutputSink that writes one JSON line per
// violation to os.Stdout using log/slog. It is the default development sink
// and the cheapest verification channel — humans can read the lines, CI can
// grep them, and downstream log shippers can ingest them.
package stdout

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/vilaca/portal/internal/api"
)

func init() {
	api.RegisterSink("stdout", func() api.OutputSink { return New() })
}

// sink writes structured violation events as JSON.
type sink struct {
	log *slog.Logger
}

// New constructs the stdout sink with a JSON slog handler over os.Stdout.
func New() api.OutputSink {
	return newWithWriter(os.Stdout)
}

// newWithWriter is the testable constructor. Public callers go through New().
func newWithWriter(w io.Writer) api.OutputSink {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})
	return &sink{log: slog.New(h)}
}

// Name implements api.OutputSink.
func (s *sink) Name() string { return "stdout" }

// Emit implements api.OutputSink. One slog record per violation.
func (s *sink) Emit(_ context.Context, v api.Violation) error {
	lvl := severityLevel(v.Severity)
	actionTypes := make([]string, 0, len(v.Actions))
	for _, a := range v.Actions {
		actionTypes = append(actionTypes, a.Type)
	}
	s.log.LogAttrs(context.Background(), lvl, v.Message,
		slog.String("rule", v.Rule),
		slog.Group("gvk",
			slog.String("group", v.GVK.Group),
			slog.String("version", v.GVK.Version),
			slog.String("kind", v.GVK.Kind),
		),
		slog.String("namespace", v.Namespace),
		slog.String("name", v.Name),
		slog.String("mode", string(v.Mode)),
		slog.String("message", v.Message),
		slog.String("severity", string(v.Severity)),
		slog.Group("source",
			slog.String("event_id", v.Source.EventID),
			slog.String("operation", v.Source.Operation),
			slog.String("container", v.Source.Container),
		),
		slog.Any("action_types", actionTypes),
	)
	return nil
}

// Close implements api.OutputSink. No-op — slog flushes per record.
func (s *sink) Close() error { return nil }

// severityLevel maps Portal severities to slog levels.
//
// critical/high → ERROR, medium → WARN, low/info/unspecified → INFO. This
// keeps "what humans should look at first" sorted by the log level rather
// than only by a string label.
func severityLevel(sev api.Severity) slog.Level {
	switch sev {
	case api.SeverityCritical, api.SeverityHigh:
		return slog.LevelError
	case api.SeverityMedium:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}
