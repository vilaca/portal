package api

import (
	"context"
	"testing"
	"time"
)

func TestRuleHasMode(t *testing.T) {
	r := Rule{Mode: []Mode{ModeAdmission, ModeAudit}}
	if !r.HasMode(ModeAdmission) {
		t.Errorf("expected HasMode(admission)=true")
	}
	if r.HasMode(ModeNetwork) {
		t.Errorf("expected HasMode(network)=false")
	}
}

func TestRegistryRoundtrip(t *testing.T) {
	// We only need to know that Register/Get round-trip; the concrete impls
	// land in their own packages.
	RegisterAction("noop-test", func() Action { return testAction{} })
	got := ActionFor("noop-test")
	if got == nil || got.Type() != "noop-test" {
		t.Fatalf("registry lost the action")
	}
}

type testAction struct{}

func (testAction) Type() string                                            { return "noop-test" }
func (testAction) Execute(_ context.Context, _ Violation, _ map[string]any) error { return nil }
func (testAction) Idempotent() bool                                        { return true }
func (testAction) DefaultRateLimit() time.Duration                         { return time.Second }
