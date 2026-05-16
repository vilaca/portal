package loader

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vilaca/portal/internal/api"
)

// captureLoader collects callback invocations.
type captureLoader struct {
	mu    sync.Mutex
	snaps [][]api.Rule
	cond  *sync.Cond
}

func newCapture() *captureLoader {
	c := &captureLoader{}
	c.cond = sync.NewCond(&c.mu)
	return c
}

func (c *captureLoader) cb(s []api.Rule) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]api.Rule, len(s))
	copy(cp, s)
	c.snaps = append(c.snaps, cp)
	c.cond.Broadcast()
}

// waitFor blocks until at least n callbacks have fired or the deadline trips.
func (c *captureLoader) waitFor(t *testing.T, n int, timeout time.Duration) [][]api.Rule {
	t.Helper()
	deadline := time.Now().Add(timeout)
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.snaps) < n {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("only %d snapshots after %s, want >=%d", len(c.snaps), timeout, n)
		}
		// sync.Cond has no timeout; emulate with a watchdog goroutine.
		done := make(chan struct{})
		go func() {
			time.Sleep(remaining)
			c.mu.Lock()
			c.cond.Broadcast()
			c.mu.Unlock()
			close(done)
		}()
		c.cond.Wait()
	}
	out := make([][]api.Rule, len(c.snaps))
	copy(out, c.snaps)
	return out
}

func TestFolder_InitialEmitAndUnion(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "single.yaml"), `
name: single-rule
enabled: true
match:
  gvk:
    - { group: "", version: v1, kind: Pod }
rule: "true"
`)

	writeFile(t, filepath.Join(dir, "list.yaml"), `
- name: list-rule-a
  enabled: true
  match:
    gvk:
      - { group: "", version: v1, kind: Pod }
  rule: "true"
- name: list-rule-b
  enabled: false
  match:
    gvk:
      - { group: apps, version: v1, kind: Deployment }
  rule: "true"
`)

	cap := newCapture()
	f := NewFolder(dir)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := f.Start(ctx, cap.cb); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = f.Stop(context.Background()) })

	snaps := cap.waitFor(t, 1, time.Second)
	first := snaps[0]
	if len(first) != 3 {
		t.Fatalf("initial snapshot has %d rules, want 3 (1 single + 2 in list)", len(first))
	}
	names := ruleNames(first)
	wantNames := map[string]bool{"single-rule": true, "list-rule-a": true, "list-rule-b": true}
	for _, n := range names {
		if !wantNames[n] {
			t.Errorf("unexpected rule name %q in snapshot", n)
		}
	}
	for _, r := range first {
		if r.Source.Origin != "folder" {
			t.Errorf("rule %q: Source.Origin = %q, want %q", r.Name, r.Source.Origin, "folder")
		}
		if r.Source.Path == "" {
			t.Errorf("rule %q: Source.Path is empty", r.Name)
		}
	}
}

func TestFolder_TouchTriggersReemit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "r.yaml")
	writeFile(t, p, `
name: r
enabled: true
match:
  gvk:
    - { group: "", version: v1, kind: Pod }
rule: "true"
`)

	cap := newCapture()
	f := NewFolder(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.Start(ctx, cap.cb); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = f.Stop(context.Background()) })

	cap.waitFor(t, 1, time.Second)

	// Rewrite the file to change a field; debounced re-emit should arrive.
	writeFile(t, p, `
name: r-touched
enabled: true
match:
  gvk:
    - { group: "", version: v1, kind: Pod }
rule: "true"
`)

	snaps := cap.waitFor(t, 2, 2*time.Second)
	last := snaps[len(snaps)-1]
	if len(last) != 1 || last[0].Name != "r-touched" {
		t.Fatalf("after touch, got %v, want [r-touched]", ruleNames(last))
	}
}

func TestFolder_MalformedFileSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "good.yaml"), `
name: good
enabled: true
match:
  gvk:
    - { group: "", version: v1, kind: Pod }
rule: "true"
`)
	writeFile(t, filepath.Join(dir, "bad.yaml"), `not: [valid yaml: : :`)

	cap := newCapture()
	f := NewFolder(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.Start(ctx, cap.cb); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = f.Stop(context.Background()) })

	snaps := cap.waitFor(t, 1, time.Second)
	first := snaps[0]
	if len(first) != 1 || first[0].Name != "good" {
		t.Fatalf("snapshot = %v, want [good] (malformed file should be skipped)", ruleNames(first))
	}
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func ruleNames(rs []api.Rule) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return out
}
