// Code in this file is hand-written but mirrors what controller-gen would
// produce. controller-gen isn't part of the build path in Wave 2; we
// implement the minimum DeepCopy / DeepCopyObject surface required by
// k8s.io/apimachinery's runtime.Object interface and by controller-runtime's
// caches / clients.

package crd

import (
	"k8s.io/apimachinery/pkg/runtime"
)

// --- PortalClusterRule ---------------------------------------------------

// DeepCopyInto copies into out.
func (in *PortalClusterRule) DeepCopyInto(out *PortalClusterRule) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy returns a deep copy.
func (in *PortalClusterRule) DeepCopy() *PortalClusterRule {
	if in == nil {
		return nil
	}
	out := new(PortalClusterRule)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object.
func (in *PortalClusterRule) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// --- PortalClusterRuleList -----------------------------------------------

// DeepCopyInto copies into out.
func (in *PortalClusterRuleList) DeepCopyInto(out *PortalClusterRuleList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]PortalClusterRule, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopy returns a deep copy.
func (in *PortalClusterRuleList) DeepCopy() *PortalClusterRuleList {
	if in == nil {
		return nil
	}
	out := new(PortalClusterRuleList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object.
func (in *PortalClusterRuleList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// --- PortalRule ----------------------------------------------------------

// DeepCopyInto copies into out.
func (in *PortalRule) DeepCopyInto(out *PortalRule) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy returns a deep copy.
func (in *PortalRule) DeepCopy() *PortalRule {
	if in == nil {
		return nil
	}
	out := new(PortalRule)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object.
func (in *PortalRule) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// --- PortalRuleList ------------------------------------------------------

// DeepCopyInto copies into out.
func (in *PortalRuleList) DeepCopyInto(out *PortalRuleList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]PortalRule, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopy returns a deep copy.
func (in *PortalRuleList) DeepCopy() *PortalRuleList {
	if in == nil {
		return nil
	}
	out := new(PortalRuleList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object.
func (in *PortalRuleList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// --- RuleSpec / RuleStatus / Matcher / ActionSpec / etc ------------------

// DeepCopyInto copies into out.
func (in *RuleSpec) DeepCopyInto(out *RuleSpec) {
	*out = *in
	if in.Mode != nil {
		out.Mode = make([]string, len(in.Mode))
		copy(out.Mode, in.Mode)
	}
	in.Match.DeepCopyInto(&out.Match)
	if in.Actions != nil {
		out.Actions = make([]ActionSpec, len(in.Actions))
		for i := range in.Actions {
			in.Actions[i].DeepCopyInto(&out.Actions[i])
		}
	}
}

// DeepCopyInto copies into out.
func (in *RuleStatus) DeepCopyInto(out *RuleStatus) {
	*out = *in
	in.LastApplied.DeepCopyInto(&out.LastApplied)
	if in.ActiveOn != nil {
		out.ActiveOn = make([]string, len(in.ActiveOn))
		copy(out.ActiveOn, in.ActiveOn)
	}
}

// DeepCopyInto copies into out.
func (in *Matcher) DeepCopyInto(out *Matcher) {
	*out = *in
	if in.GVK != nil {
		out.GVK = make([]RuleGVK, len(in.GVK))
		copy(out.GVK, in.GVK)
	}
	in.Namespaces.DeepCopyInto(&out.Namespaces)
}

// DeepCopyInto copies into out.
func (in *NamespaceSelector) DeepCopyInto(out *NamespaceSelector) {
	*out = *in
	if in.Include != nil {
		out.Include = make([]string, len(in.Include))
		copy(out.Include, in.Include)
	}
	if in.Exclude != nil {
		out.Exclude = make([]string, len(in.Exclude))
		copy(out.Exclude, in.Exclude)
	}
}

// DeepCopyInto copies into out.
func (in *ActionSpec) DeepCopyInto(out *ActionSpec) {
	*out = *in
	if in.On != nil {
		out.On = make([]string, len(in.On))
		copy(out.On, in.On)
	}
	if in.Params != nil {
		out.Params = make(map[string]any, len(in.Params))
		for k, v := range in.Params {
			out.Params[k] = v
		}
	}
}
