package lookup

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vilaca/portal/internal/api"
)

func TestDepIndexRecordDependents(t *testing.T) {
	di := NewDepIndex(0) // default capacity

	ref := api.ObjectRef{GVK: schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, Namespace: "ns", Name: "x"}
	obs := api.ObjectRef{GVK: schema.GroupVersionKind{Version: "v1", Kind: "Deployment"}, Namespace: "ns", Name: "d"}

	di.Record("rule-A", obs, ref)
	di.Record("rule-B", obs, ref)
	di.Record("rule-A", obs, ref) // duplicate — should not double

	deps := di.Dependents(ref)
	if len(deps) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(deps))
	}
}

func TestDepIndexLRUEviction(t *testing.T) {
	di := NewDepIndex(2).(*depIndex)

	obs := api.ObjectRef{GVK: schema.GroupVersionKind{Version: "v1", Kind: "Deployment"}, Namespace: "ns", Name: "d"}

	r1 := api.ObjectRef{GVK: schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, Namespace: "n", Name: "one"}
	r2 := api.ObjectRef{GVK: schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, Namespace: "n", Name: "two"}
	r3 := api.ObjectRef{GVK: schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, Namespace: "n", Name: "three"}

	di.Record("R", obs, r1)
	di.Record("R", obs, r2)
	// Touch r1 to make r2 the LRU.
	_ = di.Dependents(r1)
	di.Record("R", obs, r3) // should evict r2

	if got := di.Dependents(r2); len(got) != 0 {
		t.Fatalf("expected r2 evicted, got %d", len(got))
	}
	if got := di.Dependents(r1); len(got) != 1 {
		t.Fatalf("expected r1 still present, got %d", len(got))
	}
	if got := di.Dependents(r3); len(got) != 1 {
		t.Fatalf("expected r3 present, got %d", len(got))
	}
}

func TestDepIndexEmptyReturnsNil(t *testing.T) {
	di := NewDepIndex(8)
	got := di.Dependents(api.ObjectRef{Name: "nope"})
	if got != nil {
		t.Fatalf("expected nil for missing, got %v", got)
	}
}
