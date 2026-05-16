package alertmanager_action

import (
	"context"
	"errors"
	"testing"

	"github.com/vilaca/portal/internal/api"
)

type fakeSink struct {
	calls int
	err   error
	last  api.Violation
}

func (s *fakeSink) Name() string { return "fake" }
func (s *fakeSink) Emit(_ context.Context, v api.Violation) error {
	s.calls++
	s.last = v
	return s.err
}
func (s *fakeSink) Close() error { return nil }

func TestAlertmanager_DelegatesToSink(t *testing.T) {
	s := &fakeSink{}
	a := New(s)
	v := api.Violation{Rule: "r1"}
	if err := a.Execute(context.Background(), v, map[string]any{"template": "ignored"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if s.calls != 1 || s.last.Rule != "r1" {
		t.Fatalf("expected one Emit with Rule=r1, got calls=%d last=%v", s.calls, s.last)
	}
}

func TestAlertmanager_PropagatesError(t *testing.T) {
	s := &fakeSink{err: errors.New("boom")}
	a := New(s)
	if err := a.Execute(context.Background(), api.Violation{}, nil); err == nil {
		t.Fatal("expected error propagation")
	}
}

func TestAlertmanager_NilSink(t *testing.T) {
	a := New(nil)
	if err := a.Execute(context.Background(), api.Violation{}, nil); err == nil {
		t.Fatal("expected ErrNotConfigured")
	}
}

func TestRegistered(t *testing.T) {
	if api.ActionFor(actionType) == nil {
		t.Fatal("alertmanager action not registered")
	}
}
