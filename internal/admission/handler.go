package admission

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vilaca/portal/internal/api"
	promsink "github.com/vilaca/portal/internal/sink/prometheus"
)

// handler is the http.Handler that decodes an AdmissionReview, runs rules and
// renders a response. The struct is otherwise the runtime state shared across
// requests (excluded set, ring buffer).
type handler struct {
	engine     api.RuleEngine
	dispatcher api.ActionDispatcher
	sinks      []api.OutputSink
	builders   []api.ContextBuilder

	bypassAnnotation string
	excluded         map[string]struct{}
	nsLister         NamespaceListerFunc

	errorBuffer *errorRing
}

// podBuildAller is the supplemental contract the pod ContextBuilder satisfies
// for multi-container fan-out. We probe via type assertion.
type podBuildAller interface {
	BuildAll(*unstructured.Unstructured) ([]api.Context, error)
}

// ServeHTTP implements the AdmissionReview wire protocol.
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		promsink.AdmissionLatencySeconds.Observe(time.Since(start).Seconds())
	}()

	// Panic safety: never let a programming error inside rule evaluation
	// take the webhook process down. Record into the ring buffer; if the
	// error rate exceeds the threshold readiness flips false until later
	// successes restore it.
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("admission handler panic",
				"err", fmt.Sprint(rec),
			)
			h.recordOutcome(false)
			http.Error(w, "internal error", http.StatusInternalServerError)
			promsink.AdmissionRequestsTotal.WithLabelValues("error").Inc()
		}
	}()

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8*1024*1024))
	if err != nil {
		h.recordOutcome(false)
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	review := &admissionv1.AdmissionReview{}
	if err := json.Unmarshal(body, review); err != nil {
		h.recordOutcome(false)
		http.Error(w, "decode AdmissionReview: "+err.Error(), http.StatusBadRequest)
		return
	}
	if review.Request == nil {
		h.recordOutcome(false)
		http.Error(w, "AdmissionReview.Request is nil", http.StatusBadRequest)
		return
	}

	resp := h.process(r.Context(), review.Request)

	out := &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Response: resp,
	}
	h.recordOutcome(true)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		slog.Error("admission encode response", "err", err)
	}
}

// process drives the rule pipeline for one AdmissionRequest and produces the
// AdmissionResponse to send back. UID is copied verbatim. Metric increments
// (allow/deny/warn/dryrun/bypass) happen here so every branch is counted.
func (h *handler) process(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	resp := &admissionv1.AdmissionResponse{
		UID:     req.UID,
		Allowed: true,
	}

	// (1) System-namespace / install-namespace exclusion. Short-circuit, no
	// engine call, no sinks.
	if _, excluded := h.excluded[req.Namespace]; excluded {
		promsink.AdmissionRequestsTotal.WithLabelValues("allow").Inc()
		return resp
	}

	// (2) Bypass annotation on the *namespace*.
	if h.namespaceBypassed(req) {
		promsink.AdmissionBypassTotal.WithLabelValues(req.Namespace).Inc()
		promsink.AdmissionRequestsTotal.WithLabelValues("bypass").Inc()
		slog.Warn("admission bypass annotation honoured",
			"rule", "bypass",
			"namespace", req.Namespace,
			"name", req.Name,
			"kind", req.Kind.Kind,
			"operation", string(req.Operation),
			"user", req.UserInfo.Username,
		)
		return resp
	}

	// (3) Decode the inbound object as *unstructured.Unstructured.
	obj, err := decodeObject(req)
	if err != nil {
		// Cannot decode — record as error (handler defer counts it), but be
		// permissive: render allow so we don't deadlock cluster bootstrap
		// for un-typed objects. The chart's failurePolicy decides the
		// fail-closed posture.
		slog.Warn("admission decode object", "err", err)
		promsink.AdmissionRequestsTotal.WithLabelValues("allow").Inc()
		return resp
	}

	// (4) Build contexts via the first matching builder. Prefer BuildAll for
	// pod-shaped builders so multi-container fan-out happens.
	gvk := schema.GroupVersionKind{
		Group:   req.Kind.Group,
		Version: req.Kind.Version,
		Kind:    req.Kind.Kind,
	}
	if obj.GroupVersionKind().Empty() {
		obj.SetGroupVersionKind(gvk)
	}

	contexts := h.buildContexts(obj, gvk)

	// Wire request metadata into every context.
	areq := &api.AdmissionRequest{
		Operation: string(req.Operation),
		DryRun:    req.DryRun != nil && *req.DryRun,
		UserInfo: api.UserInfo{
			Username: req.UserInfo.Username,
			UID:      req.UserInfo.UID,
			Groups:   append([]string(nil), req.UserInfo.Groups...),
			Extra:    convertUserExtra(req.UserInfo.Extra),
		},
		OldObject: decodeOld(req),
	}
	for i := range contexts {
		contexts[i].Request = areq
		// Also expose request.* in the expr env when the builder produced
		// one — this matches the pod sugar contract.
		if contexts[i].Env != nil {
			contexts[i].Env["request"] = map[string]any{
				"operation": areq.Operation,
				"dryRun":    areq.DryRun,
				"userInfo": map[string]any{
					"username": areq.UserInfo.Username,
					"uid":      areq.UserInfo.UID,
					"groups":   stringSliceToAny(areq.UserInfo.Groups),
				},
			}
		}
	}

	// (5) Evaluate every context. Aggregate violations.
	dry := areq.DryRun
	meta := api.EventMeta{
		Source:    "admission",
		EventID:   newEventID(),
		At:        time.Now(),
		DryRun:    dry,
		Operation: areq.Operation,
	}

	var allViolations []api.Violation
	for _, c := range contexts {
		v := h.engine.Evaluate(c, meta)
		if len(v) == 0 {
			continue
		}
		allViolations = append(allViolations, v...)
	}

	// Per-request summary at Debug so an admission decision can still be
	// traced when the engine log level is bumped, without spamming
	// production logs at the default INFO threshold.
	slog.Debug("admission request",
		"gvk", gvk.String(),
		"namespace", req.Namespace,
		"name", req.Name,
		"operation", string(req.Operation),
		"contexts", len(contexts),
		"violations", len(allViolations),
	)

	// (6) Aggregate decision.
	decision := aggregate(allViolations)

	// (7) Emit every violation to every sink + dispatcher. Dry-run violations
	// still go through so PolicyReport / metrics still observe them.
	for _, v := range allViolations {
		for _, sink := range h.sinks {
			if sink == nil {
				continue
			}
			if err := sink.Emit(ctx, v); err != nil {
				slog.Warn("sink emit", "sink", sink.Name(), "err", err)
			}
		}
		if h.dispatcher != nil {
			h.dispatcher.Dispatch(ctx, v)
		}
	}

	// (8) Render AdmissionResponse + count the chosen decision branch.
	if !decision.Allowed {
		resp.Allowed = false
		resp.Result = &metav1.Status{
			Status:  metav1.StatusFailure,
			Message: decision.Message,
			Reason:  metav1.StatusReasonForbidden,
			Code:    http.StatusForbidden,
		}
		promsink.AdmissionRequestsTotal.WithLabelValues("deny").Inc()
		return resp
	}
	if len(decision.Warnings) > 0 {
		resp.Warnings = decision.Warnings
		promsink.AdmissionRequestsTotal.WithLabelValues("warn").Inc()
		return resp
	}
	// dryrun: violations exist but none deny/warn (all dryrun-action).
	for _, v := range allViolations {
		if v.EnforcementAction == api.EnforceDryRun {
			promsink.AdmissionRequestsTotal.WithLabelValues("dryrun").Inc()
			return resp
		}
	}
	promsink.AdmissionRequestsTotal.WithLabelValues("allow").Inc()
	return resp
}

// namespaceBypassed reports whether the request's namespace carries the
// bypass annotation set to "true". If no lister is wired or the lookup
// fails, returns false (fail-secure: don't honour the bypass if we can't
// confirm it).
func (h *handler) namespaceBypassed(req *admissionv1.AdmissionRequest) bool {
	if h.nsLister == nil || req.Namespace == "" {
		return false
	}
	_, annotations, err := h.nsLister(req.Namespace)
	if err != nil {
		return false
	}
	v := annotations[h.bypassAnnotation]
	return strings.EqualFold(v, "true")
}

// buildContexts walks the registered builders and asks the first one whose
// Supports(gvk)==true to produce contexts. Multi-container (podBuildAller)
// builders are tried first across all registered builders, so a more-specific
// pod-shaped builder wins even if a permissive catch-all builder (e.g.
// internal/context/generic) is iterated earlier — h.builders comes from a
// Go map and has no guaranteed order. If no builder claims the GVK, a single
// fallback context is built containing the unstructured object.
func (h *handler) buildContexts(obj *unstructured.Unstructured, gvk schema.GroupVersionKind) []api.Context {
	for _, b := range h.builders {
		if b == nil || !b.Supports(gvk) {
			continue
		}
		multi, ok := b.(podBuildAller)
		if !ok {
			continue
		}
		if ctxs, err := multi.BuildAll(obj); err == nil && len(ctxs) > 0 {
			return ctxs
		}
	}
	for _, b := range h.builders {
		if b == nil || !b.Supports(gvk) {
			continue
		}
		if c, err := b.Build(obj); err == nil {
			return []api.Context{c}
		}
	}
	// Fallback: generic context with bare object/metadata. Rules whose
	// expressions only reference object.* still work.
	return []api.Context{{
		GVK:    gvk,
		Object: obj,
		Env: map[string]any{
			"object": obj.Object,
			"metadata": map[string]any{
				"name":        obj.GetName(),
				"namespace":   obj.GetNamespace(),
				"labels":      stringMapToAny(obj.GetLabels()),
				"annotations": stringMapToAny(obj.GetAnnotations()),
			},
			"request": nil,
		},
	}}
}

// aggregate folds a list of Violations into one Decision. Rules with
// EnforceDeny dominate; warns accumulate; dryruns don't affect the response.
func aggregate(violations []api.Violation) api.Decision {
	d := api.Decision{Allowed: true, Violations: violations}
	var denyMsgs []string
	for _, v := range violations {
		switch v.EnforcementAction {
		case api.EnforceDeny:
			d.Allowed = false
			denyMsgs = append(denyMsgs, fmt.Sprintf("%s: %s", v.Rule, v.Message))
		case api.EnforceWarn:
			d.Warnings = append(d.Warnings, fmt.Sprintf("%s: %s", v.Rule, v.Message))
		case api.EnforceDryRun:
			// no admission-response effect
		}
	}
	if !d.Allowed {
		d.Message = strings.Join(denyMsgs, "; ")
	}
	return d
}

// decodeObject unmarshals the inbound object on a CREATE/UPDATE request.
// For DELETE there's no inbound object — fall back to OldObject.
func decodeObject(req *admissionv1.AdmissionRequest) (*unstructured.Unstructured, error) {
	raw := req.Object.Raw
	if len(raw) == 0 {
		raw = req.OldObject.Raw
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("AdmissionRequest has no object payload")
	}
	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(raw); err != nil {
		return nil, err
	}
	return obj, nil
}

// decodeOld returns the OldObject if present, otherwise nil.
func decodeOld(req *admissionv1.AdmissionRequest) *unstructured.Unstructured {
	if len(req.OldObject.Raw) == 0 {
		return nil
	}
	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(req.OldObject.Raw); err != nil {
		return nil
	}
	return obj
}

// convertUserExtra widens the admission UserInfo.Extra type so it sits in our
// api.UserInfo.
func convertUserExtra(in map[string]authenticationv1.ExtraValue) map[string][]string {
	if in == nil {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = []string(v)
	}
	return out
}

// stringSliceToAny widens a []string for the expr env.
func stringSliceToAny(in []string) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

// stringMapToAny widens a map[string]string for the expr env.
func stringMapToAny(in map[string]string) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// newEventID returns a fresh ID for one admission event. We avoid pulling in
// uuid for a single use — 16 random bytes hex-encoded is plenty.
func newEventID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is fatal-class but admission must continue.
		// Use a fallback that's still unique-ish per-process.
		return fmt.Sprintf("evt-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// --- readiness ring buffer ----------------------------------------------

// errorRing is a fixed-size circular buffer of outcomes (true=ok, false=err)
// that flips readiness false when error ratio exceeds 50%.
type errorRing struct {
	mu      sync.Mutex
	size    int
	cursor  int
	values  []bool
	filled  bool
	errors  int

	// once we've ever flipped to not-ready, stay sticky until errors recede
	notReady atomic.Bool
}

func newErrorRing(size int) *errorRing {
	if size <= 0 {
		size = 100
	}
	return &errorRing{
		size:   size,
		values: make([]bool, size),
	}
}

// record stores the next outcome and updates the prometheus readiness flag
// when the error ratio crosses the 50% threshold.
func (e *errorRing) record(ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Subtract the value we're about to overwrite. Only slots written before
	// the buffer first filled hold real data — pre-fill slots default to
	// false which is NOT an error sample.
	if e.filled {
		prev := e.values[e.cursor]
		if !prev {
			e.errors--
		}
	}
	e.values[e.cursor] = ok
	if !ok {
		e.errors++
	}
	e.cursor++
	if e.cursor >= e.size {
		e.cursor = 0
		e.filled = true
	}

	// Only act once we have at least the window's worth of samples — flapping
	// on the first few requests is undesirable.
	if !e.filled {
		return
	}
	if e.errors*2 > e.size {
		if !e.notReady.Swap(true) {
			promsink.SetReady(false)
		}
	} else {
		if e.notReady.Swap(false) {
			promsink.SetReady(true)
		}
	}
}

// recordOutcome is the handler-facing shim around errorRing.record.
func (h *handler) recordOutcome(ok bool) {
	if h.errorBuffer == nil {
		return
	}
	h.errorBuffer.record(ok)
}
