package network

import (
	"fmt"
	"net"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vilaca/portal/internal/api"
)

// Check IDs — kept as constants so rules and tests reference one source of truth.
const (
	CheckDefaultDenyMissing  = "np.default-deny-missing"
	CheckBroadCIDR           = "np.broad-cidr"
	CheckUnreachableSelector = "np.unreachable-selector"
	CheckPolicyWithoutTargets = "np.policy-without-targets"
)

// Check is one declarative analyser. The Model is the read-only graph
// snapshot; ns is the namespace to evaluate (or "" for cluster-wide).
type Check func(m *Model, ns string) []api.Violation

// allChecks is the ordered list run on each re-evaluation.
var allChecks = []Check{
	checkDefaultDenyMissing,
	checkBroadCIDR,
	checkUnreachableSelector,
	checkPolicyWithoutTargets,
}

// Run executes all built-in checks against m for ns and returns the
// flattened violations.
func Run(m *Model, ns string) []api.Violation {
	if m == nil {
		return nil
	}
	out := []api.Violation{}
	for _, c := range allChecks {
		out = append(out, c(m, ns)...)
	}
	return out
}

// ---------------------------------------------------------------------------
// check 1: default-deny-missing
// ---------------------------------------------------------------------------

func checkDefaultDenyMissing(m *Model, ns string) []api.Violation {
	if ns == "" {
		var out []api.Violation
		for _, n := range m.Namespaces {
			out = append(out, checkDefaultDenyMissing(m, n)...)
		}
		return out
	}
	if len(m.PodsByNamespace[ns]) == 0 {
		return nil
	}
	if m.DefaultDenyApplies(ns) {
		return nil
	}
	return []api.Violation{{
		Rule:     "np." + "default-deny-missing",
		Severity: api.SeverityMedium,
		GVK:      schema.GroupVersionKind{Version: "v1", Kind: "Namespace"},
		Namespace: ns,
		Name:     ns,
		Mode:     api.ModeNetwork,
		Message:  fmt.Sprintf("namespace %q has %d pods but no default-deny NetworkPolicy", ns, len(m.PodsByNamespace[ns])),
		At:       time.Now(),
		Source:   api.ViolationSource{EventID: CheckDefaultDenyMissing + ":" + ns},
	}}
}

// ---------------------------------------------------------------------------
// check 2: broad-cidr
// ---------------------------------------------------------------------------

func checkBroadCIDR(m *Model, ns string) []api.Violation {
	var out []api.Violation
	walk := func(n string) {
		for _, np := range m.NetworkPoliciesByNamespace[n] {
			spec, _, _ := unstructuredMap(np.Object, "spec")
			if spec == nil {
				continue
			}
			egress, _ := spec["egress"].([]any)
			for ri, rule := range egress {
				rmap, ok := rule.(map[string]any)
				if !ok {
					continue
				}
				tos, _ := rmap["to"].([]any)
				for ti, to := range tos {
					toMap, ok := to.(map[string]any)
					if !ok {
						continue
					}
					ipb, _ := toMap["ipBlock"].(map[string]any)
					if ipb == nil {
						continue
					}
					cidr, _ := ipb["cidr"].(string)
					if cidr == "" {
						continue
					}
					if !isBroadCIDR(cidr) {
						continue
					}
					out = append(out, api.Violation{
						Rule:      "np." + "broad-cidr",
						Severity:  api.SeverityHigh,
						GVK:       NPGVK,
						Namespace: np.GetNamespace(),
						Name:      np.GetName(),
						Mode:      api.ModeNetwork,
						Message:   fmt.Sprintf("NetworkPolicy %s/%s egress rule[%d].to[%d] permits broad CIDR %s", np.GetNamespace(), np.GetName(), ri, ti, cidr),
						At:        time.Now(),
						Source:    api.ViolationSource{EventID: fmt.Sprintf("%s:%s/%s:%d:%d", CheckBroadCIDR, np.GetNamespace(), np.GetName(), ri, ti)},
					})
				}
			}
		}
	}
	if ns == "" {
		for _, n := range m.Namespaces {
			walk(n)
		}
	} else {
		walk(ns)
	}
	return out
}

// isBroadCIDR returns true if cidr is 0.0.0.0/0 OR a /8 prefix that is not
// RFC1918 (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16).
func isBroadCIDR(cidr string) bool {
	if cidr == "0.0.0.0/0" {
		return true
	}
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}
	ones, _ := ipnet.Mask.Size()
	if ones <= 8 {
		// Treat as broad unless in RFC1918.
		return !rfc1918Contains(ip)
	}
	return false
}

func rfc1918Contains(ip net.IP) bool {
	for _, c := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		_, ipnet, _ := net.ParseCIDR(c)
		if ipnet.Contains(ip) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// check 3: unreachable-selector
// ---------------------------------------------------------------------------

func checkUnreachableSelector(m *Model, ns string) []api.Violation {
	var out []api.Violation
	walk := func(n string) {
		for _, np := range m.NetworkPoliciesByNamespace[n] {
			sel := m.Selectors[n+"/"+np.GetName()]
			if sel == nil {
				continue
			}
			// "empty selector matches everything" — don't flag those.
			if sel.Empty() {
				continue
			}
			matches := m.PodsMatching(n, sel)
			if len(matches) > 0 {
				continue
			}
			out = append(out, api.Violation{
				Rule:      "np." + "unreachable-selector",
				Severity:  api.SeverityLow,
				GVK:       NPGVK,
				Namespace: np.GetNamespace(),
				Name:      np.GetName(),
				Mode:      api.ModeNetwork,
				Message:   fmt.Sprintf("NetworkPolicy %s/%s podSelector matches no pods in namespace", np.GetNamespace(), np.GetName()),
				At:        time.Now(),
				Source:    api.ViolationSource{EventID: fmt.Sprintf("%s:%s/%s", CheckUnreachableSelector, np.GetNamespace(), np.GetName())},
			})
		}
	}
	if ns == "" {
		for _, n := range m.Namespaces {
			walk(n)
		}
	} else {
		walk(ns)
	}
	return out
}

// ---------------------------------------------------------------------------
// check 4: policy-without-targets
// ---------------------------------------------------------------------------

func checkPolicyWithoutTargets(m *Model, ns string) []api.Violation {
	var out []api.Violation
	walk := func(n string) {
		for _, np := range m.NetworkPoliciesByNamespace[n] {
			spec, _, _ := unstructuredMap(np.Object, "spec")
			if spec == nil {
				continue
			}
			ps, _ := spec["podSelector"].(map[string]any)
			if !isEmptySelector(ps) {
				continue
			}
			ing, _ := spec["ingress"].([]any)
			egr, _ := spec["egress"].([]any)
			if len(ing) > 0 || len(egr) > 0 {
				continue
			}
			// Empty podSelector + no rules. The default-deny pattern is the
			// useful version of this; if policyTypes include both Ingress
			// and Egress with no rules, that's deny-all and is fine. Flag
			// only when neither policyType produces meaningful coverage:
			// here we consider the structure useless when both lists are
			// absent AND policyTypes is empty.
			pt, _ := spec["policyTypes"].([]any)
			if len(pt) > 0 {
				continue
			}
			out = append(out, api.Violation{
				Rule:      "np." + "policy-without-targets",
				Severity:  api.SeverityLow,
				GVK:       NPGVK,
				Namespace: np.GetNamespace(),
				Name:      np.GetName(),
				Mode:      api.ModeNetwork,
				Message:   fmt.Sprintf("NetworkPolicy %s/%s has empty podSelector and no rules", np.GetNamespace(), np.GetName()),
				At:        time.Now(),
				Source:    api.ViolationSource{EventID: fmt.Sprintf("%s:%s/%s", CheckPolicyWithoutTargets, np.GetNamespace(), np.GetName())},
			})
		}
	}
	if ns == "" {
		for _, n := range m.Namespaces {
			walk(n)
		}
	} else {
		walk(ns)
	}
	return out
}
