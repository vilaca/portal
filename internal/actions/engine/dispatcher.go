// Package engine implements api.ActionDispatcher: a bounded worker pool that
// pulls Violations from an internal queue and runs each enabled action with
// mode filtering, idempotency suppression, rate limiting, audit logging, and
// portal_actions_total accounting.
//
// Construction is deliberately small: callers pass the action map (built
// once at composition time from the api.RegisterAction registry plus per-
// type Configure calls), the rate limiter, and the idempotency store. Drain
// closes the queue and waits for workers; this lets cmd/portal/run.go
// flush in-flight actions during graceful shutdown.
//
// Counter labels in portal_actions_total{action,result} follow the PLAN.md
// taxonomy exactly:
//   - ok          — action returned nil
//   - error       — action returned non-nil
//   - duplicate   — idempotency cache hit
//   - ratelimited — limiter said no
//   - unknown     — ActionSpec.Type not in actions map
//   - dropped     — queue was full at Dispatch time
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vilaca/portal/internal/api"
	prom "github.com/vilaca/portal/internal/sink/prometheus"
)

// Result label constants for portal_actions_total. Keeping them in one place
// makes it trivial to grep tests against the production code.
const (
	resultOK          = "ok"
	resultError       = "error"
	resultDuplicate   = "duplicate"
	resultRateLimited = "ratelimited"
	resultUnknown     = "unknown"
	resultDropped     = "dropped"
)

// Options controls dispatcher concurrency. Zero values fall back to sane
// defaults: 16 workers and a 1024-deep queue.
type Options struct {
	WorkerPoolSize int
	QueueSize      int
	// Logger is the slog handle used for audit lines. nil falls back to the
	// process default logger so tests can inject a custom handler.
	Logger *slog.Logger
}

// New constructs an ActionDispatcher. The limiter accepts the wider
// Allow(key, window, budget) signature defined in this package; see the
// Limiter docstring for the rationale.
func New(actions map[string]api.Action, limiter Limiter, idem api.IdempotencyStore, opts Options) api.ActionDispatcher {
	if opts.WorkerPoolSize <= 0 {
		opts.WorkerPoolSize = 16
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = 1024
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	d := &dispatcher{
		actions: actions,
		limiter: limiter,
		idem:    idem,
		queue:   make(chan api.Violation, opts.QueueSize),
		log:     opts.Logger,
	}
	d.wg.Add(opts.WorkerPoolSize)
	for i := 0; i < opts.WorkerPoolSize; i++ {
		go d.worker()
	}
	return d
}

type dispatcher struct {
	actions map[string]api.Action
	limiter Limiter
	idem    api.IdempotencyStore
	queue   chan api.Violation
	log     *slog.Logger

	wg       sync.WaitGroup
	closeMu  sync.Mutex
	closed   bool
}

// Dispatch enqueues v without blocking. A full queue is recorded as
// result="dropped" against the synthetic action label "*", which matches the
// label convention used by PLAN.md's audit-log section.
func (d *dispatcher) Dispatch(_ context.Context, v api.Violation) {
	d.closeMu.Lock()
	if d.closed {
		d.closeMu.Unlock()
		return
	}
	d.closeMu.Unlock()
	select {
	case d.queue <- v:
	default:
		prom.ActionsTotal.WithLabelValues("*", resultDropped).Inc()
		d.log.Warn("action dispatcher queue full; dropping violation",
			"rule", v.Rule, "gvk", v.GVK.String(), "namespace", v.Namespace, "name", v.Name)
	}
}

// Drain closes the queue and waits for workers to finish. Returns ctx.Err
// if the caller's deadline elapses first.
func (d *dispatcher) Drain(ctx context.Context) error {
	d.closeMu.Lock()
	if !d.closed {
		d.closed = true
		close(d.queue)
	}
	d.closeMu.Unlock()
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *dispatcher) worker() {
	defer d.wg.Done()
	for v := range d.queue {
		d.process(v)
	}
}

// process runs each ActionSpec in v.Actions through the
// filter→resolve→dedup→ratelimit→execute pipeline. Each stage may short-
// circuit with its own counter label.
func (d *dispatcher) process(v api.Violation) {
	for _, spec := range v.Actions {
		d.runSpec(v, spec)
	}
}

func (d *dispatcher) runSpec(v api.Violation, spec api.ActionSpec) {
	// 1. Mode filter.
	if len(spec.On) > 0 && !modeIn(spec.On, v.Mode) {
		return
	}

	// 2. Resolve action.
	action, ok := d.actions[spec.Type]
	if !ok {
		prom.ActionsTotal.WithLabelValues(spec.Type, resultUnknown).Inc()
		d.log.Warn("unknown action type", "rule", v.Rule, "type", spec.Type)
		return
	}

	// 3. Idempotency.
	key := idemKey(v.Rule, v.GVK.String(), v.Namespace, v.Name, spec.Type)
	if d.idem != nil && d.idem.Seen(key, action.DefaultRateLimit()*2) {
		prom.ActionsTotal.WithLabelValues(spec.Type, resultDuplicate).Inc()
		return
	}

	// 4. Rate limit.
	window, budget, ok := parseRateLimit(spec.RateLimit)
	if ok && d.limiter != nil {
		rateKey := v.Rule + "|" + v.Namespace + "/" + v.Name
		if !d.limiter.Allow(rateKey, window, budget) {
			prom.ActionsTotal.WithLabelValues(spec.Type, resultRateLimited).Inc()
			return
		}
	}

	// 5. Execute.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := action.Execute(ctx, v, spec.Params)
	result := resultOK
	if err != nil {
		result = resultError
	}
	prom.ActionsTotal.WithLabelValues(spec.Type, result).Inc()
	d.audit(v, spec, result, err)
}

// audit emits the JSON line described in PLAN.md item 19.
func (d *dispatcher) audit(v api.Violation, spec api.ActionSpec, result string, err error) {
	attrs := []any{
		"action", spec.Type,
		"rule", v.Rule,
		"gvk", v.GVK.String(),
		"ns", v.Namespace,
		"name", v.Name,
		"params", spec.Params,
		"result", result,
	}
	if err != nil {
		attrs = append(attrs, "error", err.Error())
		d.log.Error("action executed", attrs...)
		return
	}
	d.log.Info("action executed", attrs...)
}

func modeIn(modes []api.Mode, m api.Mode) bool {
	for _, x := range modes {
		if x == m {
			return true
		}
	}
	return false
}

// idemKey hashes the (rule, gvk, ns, name, action) tuple so it's a stable
// fixed-width string regardless of how exotic any single component is. The
// LRU stores hex.
func idemKey(rule, gvk, ns, name, actionType string) string {
	h := sha256.New()
	for _, s := range []string{rule, "|", gvk, "|", ns, "|", name, "|", actionType} {
		_, _ = h.Write([]byte(s))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// parseRateLimit accepts "<N>/<unit>" where unit is s|sec|m|min|h. Anything
// else returns ok=false so the dispatcher skips the limiter call.
func parseRateLimit(s string) (window time.Duration, budget int, ok bool) {
	if s == "" {
		return 0, 0, false
	}
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || n <= 0 {
		return 0, 0, false
	}
	unit := strings.ToLower(strings.TrimSpace(parts[1]))
	switch unit {
	case "s", "sec", "second", "seconds":
		window = time.Second
	case "m", "min", "minute", "minutes":
		window = time.Minute
	case "h", "hr", "hour", "hours":
		window = time.Hour
	default:
		return 0, 0, false
	}
	return window, n, true
}

