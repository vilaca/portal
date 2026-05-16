package network

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/vilaca/portal/internal/api"
)

func TestCheckDefaultDenyMissing(t *testing.T) {
	pods := &stubLister{items: []*unstructured.Unstructured{
		mkPod("a", "p1", nil),
	}}
	nps := &stubLister{items: []*unstructured.Unstructured{}}
	m, _ := BuildModel(pods, nps, nil, "")
	v := checkDefaultDenyMissing(m, "a")
	if len(v) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(v))
	}
	if v[0].Rule != "np.default-deny-missing" {
		t.Errorf("rule: %s", v[0].Rule)
	}
	if v[0].Mode != api.ModeNetwork {
		t.Errorf("mode: %s", v[0].Mode)
	}
}

func TestCheckDefaultDenyMissingClears(t *testing.T) {
	pods := &stubLister{items: []*unstructured.Unstructured{
		mkPod("a", "p1", nil),
	}}
	nps := &stubLister{items: []*unstructured.Unstructured{
		mkNP("a", "deny", map[string]any{"podSelector": map[string]any{}}),
	}}
	m, _ := BuildModel(pods, nps, nil, "")
	if got := checkDefaultDenyMissing(m, "a"); len(got) != 0 {
		t.Fatalf("expected 0 violations once deny is applied, got %d", len(got))
	}
}

func TestCheckBroadCIDR(t *testing.T) {
	nps := &stubLister{items: []*unstructured.Unstructured{
		mkNP("a", "broad", map[string]any{
			"podSelector": map[string]any{},
			"egress": []any{
				map[string]any{
					"to": []any{
						map[string]any{
							"ipBlock": map[string]any{"cidr": "0.0.0.0/0"},
						},
					},
				},
			},
		}),
		mkNP("a", "ok", map[string]any{
			"podSelector": map[string]any{},
			"egress": []any{
				map[string]any{
					"to": []any{
						map[string]any{
							"ipBlock": map[string]any{"cidr": "10.0.0.0/8"},
						},
					},
				},
			},
		}),
	}}
	m, _ := BuildModel(&stubLister{}, nps, nil, "")
	v := checkBroadCIDR(m, "a")
	if len(v) != 1 {
		t.Fatalf("expected 1 broad-cidr violation, got %d", len(v))
	}
	if !strings.Contains(v[0].Message, "0.0.0.0/0") {
		t.Errorf("message: %s", v[0].Message)
	}
}

func TestCheckUnreachableSelector(t *testing.T) {
	pods := &stubLister{items: []*unstructured.Unstructured{
		mkPod("a", "p1", map[string]string{"app": "x"}),
	}}
	nps := &stubLister{items: []*unstructured.Unstructured{
		mkNP("a", "useless", map[string]any{
			"podSelector": map[string]any{
				"matchLabels": map[string]any{"app": "missing"},
			},
		}),
	}}
	m, _ := BuildModel(pods, nps, nil, "")
	v := checkUnreachableSelector(m, "a")
	if len(v) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(v))
	}
}

func TestCheckPolicyWithoutTargets(t *testing.T) {
	nps := &stubLister{items: []*unstructured.Unstructured{
		mkNP("a", "useless", map[string]any{
			"podSelector": map[string]any{},
		}),
	}}
	m, _ := BuildModel(&stubLister{}, nps, nil, "")
	v := checkPolicyWithoutTargets(m, "a")
	if len(v) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(v))
	}

	// With policyTypes set, no violation.
	nps2 := &stubLister{items: []*unstructured.Unstructured{
		mkNP("a", "useless", map[string]any{
			"podSelector": map[string]any{},
			"policyTypes": []any{"Ingress"},
		}),
	}}
	m2, _ := BuildModel(&stubLister{}, nps2, nil, "")
	if got := checkPolicyWithoutTargets(m2, "a"); len(got) != 0 {
		t.Fatalf("expected 0 with policyTypes set, got %d", len(got))
	}
}

func TestIsBroadCIDR(t *testing.T) {
	cases := map[string]bool{
		"0.0.0.0/0":     true,
		"1.0.0.0/8":     true,
		"10.0.0.0/8":    false, // RFC1918
		"192.168.0.0/16": false,
		"10.5.0.0/16":   false,
	}
	for cidr, want := range cases {
		if got := isBroadCIDR(cidr); got != want {
			t.Errorf("isBroadCIDR(%s): want %v, got %v", cidr, want, got)
		}
	}
}
