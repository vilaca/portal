package stdout

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vilaca/portal/internal/api"
)

func TestEmitProducesJSONLine(t *testing.T) {
	var buf bytes.Buffer
	s := newWithWriter(&buf)

	v := api.Violation{
		Rule:     "privileged-container",
		Severity: api.SeverityCritical,
		GVK:      schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
		Namespace: "production",
		Name:      "api-server",
		Mode:      api.ModeAudit,
		Message:   "container is privileged",
		At:        time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		Source: api.ViolationSource{
			EventID:   "evt-1",
			Operation: "UPDATE",
			Container: "main",
		},
		Actions: []api.ActionSpec{{Type: "alertmanager"}, {Type: "label"}},
	}
	if err := s.Emit(context.Background(), v); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	line := bytes.TrimSpace(buf.Bytes())
	if len(line) == 0 {
		t.Fatalf("expected one JSON line, got empty buffer")
	}
	var got map[string]any
	if err := json.Unmarshal(line, &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, line)
	}

	wantString := map[string]string{
		"rule":      "privileged-container",
		"namespace": "production",
		"name":      "api-server",
		"mode":      "audit",
		"message":   "container is privileged",
		"severity":  "critical",
		"level":     "ERROR",
	}
	for k, want := range wantString {
		gv, ok := got[k].(string)
		if !ok || gv != want {
			t.Errorf("field %q = %v, want %q", k, got[k], want)
		}
	}

	gvk, ok := got["gvk"].(map[string]any)
	if !ok {
		t.Fatalf("gvk group missing or wrong shape: %v", got["gvk"])
	}
	if gvk["kind"] != "Pod" || gvk["version"] != "v1" {
		t.Errorf("gvk fields wrong: %v", gvk)
	}

	src, ok := got["source"].(map[string]any)
	if !ok {
		t.Fatalf("source group missing or wrong shape: %v", got["source"])
	}
	if src["event_id"] != "evt-1" || src["operation"] != "UPDATE" || src["container"] != "main" {
		t.Errorf("source fields wrong: %v", src)
	}

	at, ok := got["action_types"].([]any)
	if !ok {
		t.Fatalf("action_types missing: %v", got["action_types"])
	}
	if len(at) != 2 || at[0] != "alertmanager" || at[1] != "label" {
		t.Errorf("action_types wrong: %v", at)
	}

	if _, ok := got["time"].(string); !ok {
		t.Errorf("time field missing or wrong shape: %v", got["time"])
	}
}

func TestSeverityLevelMapping(t *testing.T) {
	cases := map[api.Severity]string{
		api.SeverityCritical: "ERROR",
		api.SeverityHigh:     "ERROR",
		api.SeverityMedium:   "WARN",
		api.SeverityLow:      "INFO",
		api.SeverityInfo:     "INFO",
		api.Severity(""):     "INFO",
	}
	for sev, want := range cases {
		var buf bytes.Buffer
		s := newWithWriter(&buf)
		_ = s.Emit(context.Background(), api.Violation{Severity: sev, Rule: "r"})
		var got map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &got); err != nil {
			t.Fatalf("severity %q: bad JSON: %v", sev, err)
		}
		if got["level"] != want {
			t.Errorf("severity %q: level = %v, want %q", sev, got["level"], want)
		}
	}
}

func TestRegisteredAtInit(t *testing.T) {
	sinks := api.Sinks()
	if _, ok := sinks["stdout"]; !ok {
		t.Fatalf("stdout sink not registered at init()")
	}
}

func TestName(t *testing.T) {
	if got := New().Name(); got != "stdout" {
		t.Errorf("Name() = %q, want stdout", got)
	}
}
