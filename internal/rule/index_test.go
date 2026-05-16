package rule

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vilaca/portal/internal/api"
)

var (
	gvkPod        = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"}
	gvkDeployment = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
)

func mkRule(name string, enabled bool, gvks ...schema.GroupVersionKind) api.Rule {
	return api.Rule{
		Name:    name,
		Enabled: enabled,
		Match:   api.Matcher{GVK: gvks},
	}
}

func TestIndex_GVKRouting(t *testing.T) {
	idx := NewIndex()
	idx.Replace([]api.Rule{
		mkRule("a", true, gvkPod),
		mkRule("b", true, gvkPod, gvkDeployment),
		mkRule("c", true, gvkDeployment),
	})

	pods := idx.ForGVK(gvkPod)
	if len(pods) != 2 {
		t.Fatalf("ForGVK(Pod) = %d, want 2", len(pods))
	}
	deps := idx.ForGVK(gvkDeployment)
	if len(deps) != 2 {
		t.Fatalf("ForGVK(Deployment) = %d, want 2", len(deps))
	}
	if got := len(idx.All()); got != 3 {
		t.Fatalf("All() = %d, want 3", got)
	}

	// Returned slice must be a copy: mutating it must not affect the index.
	pods[0] = api.Rule{Name: "tampered"}
	pods2 := idx.ForGVK(gvkPod)
	if pods2[0].Name == "tampered" {
		t.Fatal("ForGVK returned the internal slice; expected a copy")
	}
}

func TestIndex_DisabledRulesExcluded(t *testing.T) {
	idx := NewIndex()
	idx.Replace([]api.Rule{
		mkRule("enabled-pod", true, gvkPod),
		mkRule("disabled-pod", false, gvkPod),
		mkRule("disabled-dep", false, gvkDeployment),
	})

	pods := idx.ForGVK(gvkPod)
	if len(pods) != 1 || pods[0].Name != "enabled-pod" {
		t.Fatalf("ForGVK(Pod) = %v, want [enabled-pod]", pods)
	}
	if got := idx.ForGVK(gvkDeployment); len(got) != 0 {
		t.Fatalf("ForGVK(Deployment) = %v, want empty", got)
	}
	all := idx.All()
	if len(all) != 1 || all[0].Name != "enabled-pod" {
		t.Fatalf("All() = %v, want [enabled-pod]", all)
	}
}

func TestIndex_ConcurrentReadersAndWriters(t *testing.T) {
	idx := NewIndex()
	idx.Replace([]api.Rule{mkRule("seed", true, gvkPod)})

	deadline := time.Now().Add(200 * time.Millisecond)
	var stop atomic.Bool
	var wg sync.WaitGroup

	// readers
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				_ = idx.ForGVK(gvkPod)
				_ = idx.All()
			}
		}()
	}
	// writers
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			i := 0
			for !stop.Load() {
				idx.Replace([]api.Rule{
					mkRule("a", true, gvkPod),
					mkRule("b", i%2 == 0, gvkDeployment),
				})
				i++
			}
		}(w)
	}

	time.Sleep(time.Until(deadline))
	stop.Store(true)
	wg.Wait()
}
