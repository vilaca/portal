package network

import (
	"context"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"

	"github.com/vilaca/portal/internal/api"
)

// stubAuditCache satisfies network.AuditCache with the three listers we care
// about and no informer factory (so installHandlers is a no-op — tests drive
// re-evaluations manually via enqueue).
type stubAuditCache struct {
	pods *stubLister
	nps  *stubLister
	nss  *stubLister
}

func (s *stubAuditCache) Lister(gvk schema.GroupVersionKind) (cache.GenericLister, error) {
	switch gvk {
	case PodGVK:
		return s.pods, nil
	case NPGVK:
		return s.nps, nil
	case NSGVK:
		return s.nss, nil
	}
	return nil, &notFound{name: gvk.String()}
}

func (s *stubAuditCache) SharedInformerFactory() dynamicinformer.DynamicSharedInformerFactory {
	return nil
}

func TestAnalyserDefaultDenyMissingFiresAndResolves(t *testing.T) {
	ac := &stubAuditCache{
		pods: &stubLister{items: []*unstructured.Unstructured{
			mkPod("a", "p1", nil),
		}},
		nps: &stubLister{items: []*unstructured.Unstructured{}},
		nss: &stubLister{items: []*unstructured.Unstructured{mkNamespace("a")}},
	}
	es, err := New(ac, nil, nil, Options{WorkerPoolSize: 1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a := es.(*Analyser)

	var (
		mu    sync.Mutex
		emits []api.Violation
		wg    sync.WaitGroup
	)
	wg.Add(1)
	a.onEmitForTest = func(v api.Violation) {
		mu.Lock()
		defer mu.Unlock()
		emits = append(emits, v)
		if len(emits) == 1 {
			wg.Done()
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = es.Start(ctx, nil) }()

	// Wait for the default-deny-missing finding to fire.
	if err := waitWG(&wg, 2*time.Second); err != nil {
		t.Fatalf("did not observe initial finding: %v", err)
	}
	mu.Lock()
	if len(emits) != 1 || emits[0].Rule != "np.default-deny-missing" || emits[0].Message == "resolved" {
		t.Fatalf("unexpected first emit: %+v", emits)
	}
	mu.Unlock()

	// Apply a default-deny NP and re-enqueue. Use a second WG for the
	// "resolved" emission.
	var wg2 sync.WaitGroup
	wg2.Add(1)
	a.onEmitForTest = func(v api.Violation) {
		mu.Lock()
		defer mu.Unlock()
		emits = append(emits, v)
		if v.Message == "resolved" {
			wg2.Done()
		}
	}
	ac.nps.items = append(ac.nps.items, mkNP("a", "deny", map[string]any{
		"podSelector": map[string]any{},
		"policyTypes": []any{"Ingress"},
	}))
	a.enqueue(workItem{namespace: "a"})

	if err := waitWG(&wg2, 2*time.Second); err != nil {
		t.Fatalf("did not observe resolution: %v", err)
	}

	mu.Lock()
	var resolved bool
	for _, v := range emits {
		if v.Rule == "np.default-deny-missing" && v.Message == "resolved" {
			resolved = true
		}
	}
	mu.Unlock()
	if !resolved {
		t.Fatalf("expected a resolved default-deny-missing emission, got %+v", emits)
	}
}

func TestAnalyserBroadCIDRFires(t *testing.T) {
	ac := &stubAuditCache{
		pods: &stubLister{},
		nps: &stubLister{items: []*unstructured.Unstructured{
			mkNP("a", "broad", map[string]any{
				"podSelector": map[string]any{},
				"egress": []any{map[string]any{"to": []any{map[string]any{"ipBlock": map[string]any{"cidr": "0.0.0.0/0"}}}}},
			}),
		}},
		nss: &stubLister{items: []*unstructured.Unstructured{mkNamespace("a")}},
	}
	es, _ := New(ac, nil, nil, Options{WorkerPoolSize: 1})
	a := es.(*Analyser)

	var wg sync.WaitGroup
	wg.Add(1)
	var got api.Violation
	a.onEmitForTest = func(v api.Violation) {
		if v.Rule == "np.broad-cidr" {
			got = v
			wg.Done()
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = es.Start(ctx, nil) }()

	if err := waitWG(&wg, 2*time.Second); err != nil {
		t.Fatalf("broad-cidr never fired: %v", err)
	}
	if got.Severity != api.SeverityHigh {
		t.Errorf("severity: %s", got.Severity)
	}
}

func TestAnalyserUnreachableSelectorFires(t *testing.T) {
	ac := &stubAuditCache{
		pods: &stubLister{items: []*unstructured.Unstructured{mkPod("a", "p", map[string]string{"app": "x"})}},
		nps: &stubLister{items: []*unstructured.Unstructured{
			mkNP("a", "useless", map[string]any{
				"podSelector": map[string]any{"matchLabels": map[string]any{"app": "nope"}},
			}),
		}},
		nss: &stubLister{items: []*unstructured.Unstructured{mkNamespace("a")}},
	}
	es, _ := New(ac, nil, nil, Options{WorkerPoolSize: 1})
	a := es.(*Analyser)

	var wg sync.WaitGroup
	wg.Add(1)
	a.onEmitForTest = func(v api.Violation) {
		if v.Rule == "np.unreachable-selector" {
			wg.Done()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = es.Start(ctx, nil) }()

	if err := waitWG(&wg, 2*time.Second); err != nil {
		t.Fatalf("unreachable never fired: %v", err)
	}
}

func TestAnalyserPolicyWithoutTargetsFires(t *testing.T) {
	ac := &stubAuditCache{
		pods: &stubLister{},
		nps: &stubLister{items: []*unstructured.Unstructured{
			mkNP("a", "useless", map[string]any{
				"podSelector": map[string]any{},
			}),
		}},
		nss: &stubLister{items: []*unstructured.Unstructured{mkNamespace("a")}},
	}
	es, _ := New(ac, nil, nil, Options{WorkerPoolSize: 1})
	a := es.(*Analyser)

	var wg sync.WaitGroup
	wg.Add(1)
	a.onEmitForTest = func(v api.Violation) {
		if v.Rule == "np.policy-without-targets" {
			wg.Done()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = es.Start(ctx, nil) }()

	if err := waitWG(&wg, 2*time.Second); err != nil {
		t.Fatalf("policy-without-targets never fired: %v", err)
	}
}

func TestAnalyserDeleteClearsFinding(t *testing.T) {
	broad := mkNP("a", "broad", map[string]any{
		"podSelector": map[string]any{},
		"egress":      []any{map[string]any{"to": []any{map[string]any{"ipBlock": map[string]any{"cidr": "0.0.0.0/0"}}}}},
	})
	ac := &stubAuditCache{
		pods: &stubLister{},
		nps:  &stubLister{items: []*unstructured.Unstructured{broad}},
		nss:  &stubLister{items: []*unstructured.Unstructured{mkNamespace("a")}},
	}
	es, _ := New(ac, nil, nil, Options{WorkerPoolSize: 1})
	a := es.(*Analyser)

	var wg sync.WaitGroup
	wg.Add(1)
	a.onEmitForTest = func(v api.Violation) {
		if v.Rule == "np.broad-cidr" && v.Message != "resolved" {
			wg.Done()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = es.Start(ctx, nil) }()
	if err := waitWG(&wg, 2*time.Second); err != nil {
		t.Fatalf("never fired: %v", err)
	}

	var wg2 sync.WaitGroup
	wg2.Add(1)
	a.onEmitForTest = func(v api.Violation) {
		if v.Rule == "np.broad-cidr" && v.Message == "resolved" {
			wg2.Done()
		}
	}

	// Simulate delete: drop from lister then enqueue with deletedNP set.
	ac.nps.items = nil
	a.enqueue(workItem{namespace: "a", deletedNP: "broad"})

	if err := waitWG(&wg2, 2*time.Second); err != nil {
		t.Fatalf("never resolved: %v", err)
	}
}

func TestAnalyserAlertActionDecoration(t *testing.T) {
	ac := &stubAuditCache{
		pods: &stubLister{items: []*unstructured.Unstructured{mkPod("a", "p", nil)}},
		nps:  &stubLister{},
		nss:  &stubLister{items: []*unstructured.Unstructured{mkNamespace("a")}},
	}
	es, _ := New(ac, nil, nil, Options{WorkerPoolSize: 1, AlertOnFindings: true})
	a := es.(*Analyser)

	var wg sync.WaitGroup
	wg.Add(1)
	var got api.Violation
	a.onEmitForTest = func(v api.Violation) {
		got = v
		wg.Done()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = es.Start(ctx, nil) }()

	if err := waitWG(&wg, 2*time.Second); err != nil {
		t.Fatalf("no emit: %v", err)
	}
	if len(got.Actions) == 0 || got.Actions[0].Type != "alertmanager" {
		t.Fatalf("expected alertmanager action prepended, got %+v", got.Actions)
	}
}

// waitWG returns nil if wg drains before timeout, error otherwise.
func waitWG(wg *sync.WaitGroup, timeout time.Duration) error {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return context.DeadlineExceeded
	}
}
