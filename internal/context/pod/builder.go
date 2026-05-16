// Package pod implements the pod-shaped api.ContextBuilder. It turns a raw
// Pod (or workload kind whose .spec.template.spec is a PodSpec — Deployment,
// StatefulSet, DaemonSet, Job, CronJob, ReplicaSet, ReplicationController)
// into one Context per container, exposing the deliberately-narrow pod sugar
// described in /docs/POC-TO-PRODUCTION.md (Context model).
//
// Multi-container semantics:
//
//	api.ContextBuilder.Build returns a single Context. Pods commonly have
//	multiple containers (standard + init + ephemeral) and rules expect to be
//	evaluated against each. The audit/admission integration code is expected
//	to call BuildAll, which is the supplemental method defined on this type
//	but NOT part of the api.ContextBuilder interface. Build() returns the
//	first context (useful for tests, single-container cases, or callers that
//	only care about Pod-level fields).
//
// Map shape:
//
//	The Env produced for each container has top-level keys: container, spec,
//	securityContext, metadata, object, request. Missing fields are present as
//	nil rather than absent so expr-lang's optional chaining works.
package pod

import (
	"fmt"
	"log/slog"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vilaca/portal/internal/api"
)

// SupportedGVKs is the set of kinds this builder recognises. It is a package
// variable (not a const) so tests and admission/audit configuration code can
// extend it before registration if needed.
var SupportedGVKs = []schema.GroupVersionKind{
	{Group: "", Version: "v1", Kind: "Pod"},
	{Group: "apps", Version: "v1", Kind: "Deployment"},
	{Group: "apps", Version: "v1", Kind: "StatefulSet"},
	{Group: "apps", Version: "v1", Kind: "DaemonSet"},
	{Group: "apps", Version: "v1", Kind: "ReplicaSet"},
	{Group: "batch", Version: "v1", Kind: "Job"},
	{Group: "batch", Version: "v1", Kind: "CronJob"},
	{Group: "", Version: "v1", Kind: "ReplicationController"},
}

func init() {
	api.RegisterContextBuilder("pod", func() api.ContextBuilder { return New() })
}

// Builder is the concrete pod-shaped ContextBuilder. The exported type lets
// callers use BuildAll without a type assertion.
type Builder struct{}

// New returns a fresh pod builder.
func New() *Builder { return &Builder{} }

// Supports reports whether gvk is one of the recognised pod-shaped kinds.
func (b *Builder) Supports(gvk schema.GroupVersionKind) bool {
	for _, g := range SupportedGVKs {
		if g == gvk {
			return true
		}
	}
	return false
}

// Build returns the first per-container Context, or — if there are no
// containers — a Context with container=nil. The audit/admission paths should
// prefer BuildAll.
func (b *Builder) Build(obj *unstructured.Unstructured) (api.Context, error) {
	ctxs, err := b.BuildAll(obj)
	if err != nil {
		return api.Context{}, err
	}
	if len(ctxs) == 0 {
		return b.emptyContext(obj), nil
	}
	return ctxs[0], nil
}

// BuildAll returns one Context per container (standard, then init, then
// ephemeral). For workload kinds the pod spec is read from
// .spec.template.spec; for Pods it is read directly from .spec.
//
// If the object has zero containers, BuildAll returns a single Context with
// container=nil. This preserves rule applicability for pod-level rules that
// don't reference container.*.
func (b *Builder) BuildAll(obj *unstructured.Unstructured) ([]api.Context, error) {
	if obj == nil {
		return nil, fmt.Errorf("pod.Builder.BuildAll: nil object")
	}
	gvk := obj.GroupVersionKind()
	podSpec := locatePodSpec(obj)

	std, _, stdErr := unstructured.NestedSlice(podSpec, "containers")
	initC, _, _ := unstructured.NestedSlice(podSpec, "initContainers")
	eph, _, _ := unstructured.NestedSlice(podSpec, "ephemeralContainers")

	if len(std) == 0 && len(initC) == 0 && len(eph) == 0 {
		// Empty container set is anomalous for a pod-shaped GVK. Trace at
		// Debug so it can be surfaced via log-level bump without producing
		// per-request noise in production. Bump up the level on the binary
		// (-log-level=debug) when investigating a "rule didn't fire" or
		// "container is nil" report.
		var podKeys, specKeys []string
		for k := range obj.Object {
			podKeys = append(podKeys, k)
		}
		for k := range podSpec {
			specKeys = append(specKeys, k)
		}
		slog.Debug("pod builder: no containers found",
			"gvk", gvk.String(),
			"name", obj.GetName(),
			"namespace", obj.GetNamespace(),
			"podKeys", podKeys,
			"specKeys", specKeys,
			"podSpecNil", podSpec == nil,
			"containersErr", stdErr,
		)
	}

	specEnv := buildSpecEnv(podSpec)
	scEnv := buildPodSecurityContextEnv(podSpec)
	mdEnv := buildMetadataEnv(obj)

	makeCtx := func(containerEnv map[string]any) api.Context {
		env := map[string]any{
			"container":       containerEnv,
			"spec":            specEnv,
			"securityContext": scEnv,
			"metadata":        mdEnv,
			"object":          obj.Object,
			"request":         nil,
		}
		return api.Context{
			GVK:    gvk,
			Object: obj,
			Env:    env,
		}
	}

	var out []api.Context
	for _, c := range std {
		if m, ok := c.(map[string]any); ok {
			out = append(out, makeCtx(buildContainerEnv(m, "standard")))
		}
	}
	for _, c := range initC {
		if m, ok := c.(map[string]any); ok {
			out = append(out, makeCtx(buildContainerEnv(m, "init")))
		}
	}
	for _, c := range eph {
		if m, ok := c.(map[string]any); ok {
			out = append(out, makeCtx(buildContainerEnv(m, "ephemeral")))
		}
	}
	if len(out) == 0 {
		out = append(out, b.emptyContext(obj))
	}
	return out, nil
}

func (b *Builder) emptyContext(obj *unstructured.Unstructured) api.Context {
	var (
		podSpec map[string]any
		gvk     schema.GroupVersionKind
		raw     map[string]any
	)
	if obj != nil {
		gvk = obj.GroupVersionKind()
		podSpec = locatePodSpec(obj)
		raw = obj.Object
	}
	return api.Context{
		GVK:    gvk,
		Object: obj,
		Env: map[string]any{
			"container":       nil,
			"spec":            buildSpecEnv(podSpec),
			"securityContext": buildPodSecurityContextEnv(podSpec),
			"metadata":        buildMetadataEnv(obj),
			"object":          raw,
			"request":         nil,
		},
	}
}

// locatePodSpec returns the PodSpec map. For Pods this is .spec; for workload
// kinds it is .spec.template.spec. Returns nil if not present.
func locatePodSpec(obj *unstructured.Unstructured) map[string]any {
	if obj == nil {
		return nil
	}
	gvk := obj.GroupVersionKind()
	if gvk.Group == "" && gvk.Kind == "Pod" {
		spec, _, _ := unstructured.NestedMap(obj.Object, "spec")
		return spec
	}
	// CronJob nests one extra level: .spec.jobTemplate.spec.template.spec
	if gvk.Group == "batch" && gvk.Kind == "CronJob" {
		spec, _, _ := unstructured.NestedMap(obj.Object, "spec", "jobTemplate", "spec", "template", "spec")
		return spec
	}
	spec, _, _ := unstructured.NestedMap(obj.Object, "spec", "template", "spec")
	return spec
}

// --- env builders --------------------------------------------------------

// buildContainerEnv returns the container.* sugar map for one container.
func buildContainerEnv(c map[string]any, kind string) map[string]any {
	if c == nil {
		return nil
	}
	name, _ := c["name"].(string)
	imgRef, _ := c["image"].(string)
	command, _ := c["command"].([]any)
	args, _ := c["args"].([]any)
	ports, _ := c["ports"].([]any)
	sc, _ := c["securityContext"].(map[string]any)

	return map[string]any{
		"name":            name,
		"containerType":   kind,
		"image":           parseImageRef(imgRef),
		"command":         command,
		"args":            args,
		"ports":           ports,
		"securityContext": buildContainerSecurityContextEnv(sc),
	}
}

// parseImageRef splits a Docker-style image reference into registry, name,
// tag, sha256. Defaults: registry "docker.io", tag "latest". sha256 only set
// when the ref contains @sha256:....
func parseImageRef(ref string) map[string]any {
	out := map[string]any{
		"registry": "docker.io",
		"name":     "",
		"tag":      "latest",
		"sha256":   nil,
	}
	if ref == "" {
		return out
	}
	// Pull out digest first.
	if i := strings.Index(ref, "@sha256:"); i >= 0 {
		out["sha256"] = ref[i+len("@sha256:"):]
		ref = ref[:i]
	}
	// Decide whether the first path component is a registry. The Docker
	// convention is: if it contains '.' or ':' or is "localhost", it's a
	// registry; otherwise the first component is an org under docker.io.
	registry := "docker.io"
	rest := ref
	if i := strings.Index(ref, "/"); i >= 0 {
		head := ref[:i]
		if strings.ContainsAny(head, ".:") || head == "localhost" {
			registry = head
			rest = ref[i+1:]
		}
	}
	out["registry"] = registry

	// Extract tag.
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		out["name"] = rest[:i]
		out["tag"] = rest[i+1:]
	} else {
		out["name"] = rest
	}
	return out
}

// buildContainerSecurityContextEnv normalises a container's securityContext.
func buildContainerSecurityContextEnv(sc map[string]any) map[string]any {
	out := map[string]any{
		"privileged":               nil,
		"allowPrivilegeEscalation": nil,
		"readOnlyRootFilesystem":   nil,
		"runAsUser":                nil,
		"runAsGroup":               nil,
		"runAsNonRoot":             nil,
		"procMount":                nil,
		"seccompProfileType":       nil,
		"capabilities": map[string]any{
			"add":  nil,
			"drop": nil,
		},
	}
	if sc == nil {
		return out
	}
	copyIfPresent(out, sc, "privileged")
	copyIfPresent(out, sc, "allowPrivilegeEscalation")
	copyIfPresent(out, sc, "readOnlyRootFilesystem")
	copyIfPresent(out, sc, "runAsUser")
	copyIfPresent(out, sc, "runAsGroup")
	copyIfPresent(out, sc, "runAsNonRoot")
	copyIfPresent(out, sc, "procMount")
	if sp, ok := sc["seccompProfile"].(map[string]any); ok {
		out["seccompProfileType"] = sp["type"]
	}
	if caps, ok := sc["capabilities"].(map[string]any); ok {
		out["capabilities"] = map[string]any{
			"add":  caps["add"],
			"drop": caps["drop"],
		}
	}
	return out
}

// buildSpecEnv extracts the pod-level spec sugar.
func buildSpecEnv(spec map[string]any) map[string]any {
	out := map[string]any{
		"hostPID":                      nil,
		"hostNetwork":                  nil,
		"hostIPC":                      nil,
		"serviceAccountName":           nil,
		"automountServiceAccountToken": nil,
	}
	if spec == nil {
		return out
	}
	copyIfPresent(out, spec, "hostPID")
	copyIfPresent(out, spec, "hostNetwork")
	copyIfPresent(out, spec, "hostIPC")
	copyIfPresent(out, spec, "serviceAccountName")
	copyIfPresent(out, spec, "automountServiceAccountToken")
	return out
}

// buildPodSecurityContextEnv extracts the pod-level securityContext sugar.
func buildPodSecurityContextEnv(spec map[string]any) map[string]any {
	out := map[string]any{
		"runAsUser":          nil,
		"runAsGroup":         nil,
		"runAsNonRoot":       nil,
		"fsGroup":            nil,
		"supplementalGroups": nil,
		"seccompProfileType": nil,
	}
	if spec == nil {
		return out
	}
	sc, _ := spec["securityContext"].(map[string]any)
	if sc == nil {
		return out
	}
	copyIfPresent(out, sc, "runAsUser")
	copyIfPresent(out, sc, "runAsGroup")
	copyIfPresent(out, sc, "runAsNonRoot")
	copyIfPresent(out, sc, "fsGroup")
	copyIfPresent(out, sc, "supplementalGroups")
	if sp, ok := sc["seccompProfile"].(map[string]any); ok {
		out["seccompProfileType"] = sp["type"]
	}
	return out
}

// buildMetadataEnv extracts the object metadata sugar.
func buildMetadataEnv(obj *unstructured.Unstructured) map[string]any {
	out := map[string]any{
		"name":        nil,
		"namespace":   nil,
		"labels":      nil,
		"annotations": nil,
	}
	if obj == nil {
		return out
	}
	if n := obj.GetName(); n != "" {
		out["name"] = n
	}
	if ns := obj.GetNamespace(); ns != "" {
		out["namespace"] = ns
	}
	if l := obj.GetLabels(); l != nil {
		// convert map[string]string -> map[string]any for expr-lang
		m := make(map[string]any, len(l))
		for k, v := range l {
			m[k] = v
		}
		out["labels"] = m
	}
	if a := obj.GetAnnotations(); a != nil {
		m := make(map[string]any, len(a))
		for k, v := range a {
			m[k] = v
		}
		out["annotations"] = m
	}
	return out
}

// copyIfPresent copies src[key] into dst[key] if src[key] exists.
func copyIfPresent(dst, src map[string]any, key string) {
	if v, ok := src[key]; ok {
		dst[key] = v
	}
}
