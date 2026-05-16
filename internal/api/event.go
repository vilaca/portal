package api

import "time"

// EventMeta describes one event flowing through the engine. It is what
// EventSource produces and what ActionDispatcher / OutputSink consume alongside
// Violations.
type EventMeta struct {
	Source    string // "admission", "audit", "network", "api-audit-log" (v2)
	EventID   string // unique per event; used as the idempotency seed
	At        time.Time
	DryRun    bool
	Operation string // create/update/delete; "audit" or "network" outside admission
}
