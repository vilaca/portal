package pod

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vilaca/portal/internal/api"
)

func TestSupports(t *testing.T) {
	b := New()
	if !b.Supports(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"}) {
		t.Errorf("expected Pod to be supported")
	}
	if !b.Supports(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}) {
		t.Errorf("expected Deployment to be supported")
	}
	if b.Supports(schema.GroupVersionKind{Group: "v1", Version: "v1", Kind: "ConfigMap"}) {
		t.Errorf("did not expect ConfigMap to be supported")
	}
}

func TestRegistered(t *testing.T) {
	if _, ok := api.ContextBuilders()["pod"]; !ok {
		t.Errorf("pod builder not registered in api registry")
	}
}

func newPod(name string, std, init, eph []map[string]any) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":      name,
			"namespace": "default",
			"labels":    map[string]any{"app": "x"},
		},
		"spec": map[string]any{
			"hostPID":                      true,
			"hostNetwork":                  false,
			"hostIPC":                      false,
			"serviceAccountName":           "default",
			"automountServiceAccountToken": false,
			"securityContext": map[string]any{
				"runAsUser":          int64(1000),
				"runAsGroup":         int64(3000),
				"runAsNonRoot":       true,
				"fsGroup":            int64(2000),
				"supplementalGroups": []any{int64(4000)},
				"seccompProfile":     map[string]any{"type": "RuntimeDefault"},
			},
		},
	}}
	obj.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"})
	if std != nil {
		spec := obj.Object["spec"].(map[string]any)
		anyCs := make([]any, len(std))
		for i, c := range std {
			anyCs[i] = c
		}
		spec["containers"] = anyCs
	}
	if init != nil {
		spec := obj.Object["spec"].(map[string]any)
		anyCs := make([]any, len(init))
		for i, c := range init {
			anyCs[i] = c
		}
		spec["initContainers"] = anyCs
	}
	if eph != nil {
		spec := obj.Object["spec"].(map[string]any)
		anyCs := make([]any, len(eph))
		for i, c := range eph {
			anyCs[i] = c
		}
		spec["ephemeralContainers"] = anyCs
	}
	return obj
}

func TestBuildAllProducesOneContextPerContainer(t *testing.T) {
	b := New()
	obj := newPod("p1",
		[]map[string]any{
			{"name": "app", "image": "nginx:1.25"},
			{"name": "sidecar", "image": "gcr.io/foo/bar:latest"},
		},
		[]map[string]any{
			{"name": "init1", "image": "busybox"},
		},
		[]map[string]any{
			{"name": "debug", "image": "busybox@sha256:abcdef1234567890"},
		},
	)

	ctxs, err := b.BuildAll(obj)
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	if len(ctxs) != 4 {
		t.Fatalf("expected 4 contexts (2 std + 1 init + 1 eph), got %d", len(ctxs))
	}

	want := []struct {
		name, kind string
	}{
		{"app", "standard"},
		{"sidecar", "standard"},
		{"init1", "init"},
		{"debug", "ephemeral"},
	}
	for i, w := range want {
		c := ctxs[i].Env["container"].(map[string]any)
		if c["name"] != w.name {
			t.Errorf("ctx %d name=%v want %s", i, c["name"], w.name)
		}
		if c["containerType"] != w.kind {
			t.Errorf("ctx %d kind=%v want %s", i, c["containerType"], w.kind)
		}
	}
}

func TestImageParsing(t *testing.T) {
	cases := []struct {
		ref      string
		registry string
		name     string
		tag      string
		hasSha   bool
	}{
		{"nginx", "docker.io", "nginx", "latest", false},
		{"nginx:1.25", "docker.io", "nginx", "1.25", false},
		{"library/nginx:1.25", "docker.io", "library/nginx", "1.25", false},
		{"gcr.io/foo/bar:tag", "gcr.io", "foo/bar", "tag", false},
		{"localhost:5000/app:dev", "localhost:5000", "app", "dev", false},
		{"nginx@sha256:abc123", "docker.io", "nginx", "latest", true},
		{"gcr.io/foo:1.0@sha256:abc", "gcr.io", "foo", "1.0", true},
	}
	for _, c := range cases {
		got := parseImageRef(c.ref)
		if got["registry"] != c.registry {
			t.Errorf("%s: registry=%v want %s", c.ref, got["registry"], c.registry)
		}
		if got["name"] != c.name {
			t.Errorf("%s: name=%v want %s", c.ref, got["name"], c.name)
		}
		if got["tag"] != c.tag {
			t.Errorf("%s: tag=%v want %s", c.ref, got["tag"], c.tag)
		}
		if c.hasSha && got["sha256"] == nil {
			t.Errorf("%s: expected sha256 to be set", c.ref)
		}
		if !c.hasSha && got["sha256"] != nil {
			t.Errorf("%s: expected sha256 nil, got %v", c.ref, got["sha256"])
		}
	}
}

func TestContainerSecurityContextSurfaces(t *testing.T) {
	b := New()
	obj := newPod("p", []map[string]any{
		{
			"name":  "main",
			"image": "nginx",
			"securityContext": map[string]any{
				"privileged":               true,
				"allowPrivilegeEscalation": false,
				"readOnlyRootFilesystem":   true,
				"runAsUser":                int64(101),
				"capabilities": map[string]any{
					"add":  []any{"NET_ADMIN"},
					"drop": []any{"ALL"},
				},
				"seccompProfile": map[string]any{"type": "RuntimeDefault"},
			},
		},
	}, nil, nil)
	ctxs, err := b.BuildAll(obj)
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	c := ctxs[0].Env["container"].(map[string]any)
	sc := c["securityContext"].(map[string]any)
	if sc["privileged"] != true {
		t.Errorf("privileged=%v", sc["privileged"])
	}
	if sc["allowPrivilegeEscalation"] != false {
		t.Errorf("allowPrivilegeEscalation=%v", sc["allowPrivilegeEscalation"])
	}
	if sc["readOnlyRootFilesystem"] != true {
		t.Errorf("readOnlyRootFilesystem=%v", sc["readOnlyRootFilesystem"])
	}
	if sc["seccompProfileType"] != "RuntimeDefault" {
		t.Errorf("seccompProfileType=%v", sc["seccompProfileType"])
	}
	caps := sc["capabilities"].(map[string]any)
	if caps["add"] == nil || caps["drop"] == nil {
		t.Errorf("capabilities not surfaced: %+v", caps)
	}
}

func TestPodLevelSugar(t *testing.T) {
	b := New()
	obj := newPod("p", []map[string]any{{"name": "c", "image": "nginx"}}, nil, nil)
	ctxs, _ := b.BuildAll(obj)
	spec := ctxs[0].Env["spec"].(map[string]any)
	if spec["hostPID"] != true {
		t.Errorf("hostPID=%v", spec["hostPID"])
	}
	if spec["serviceAccountName"] != "default" {
		t.Errorf("serviceAccountName=%v", spec["serviceAccountName"])
	}
	psc := ctxs[0].Env["securityContext"].(map[string]any)
	if psc["runAsNonRoot"] != true {
		t.Errorf("runAsNonRoot=%v", psc["runAsNonRoot"])
	}
	if psc["seccompProfileType"] != "RuntimeDefault" {
		t.Errorf("seccompProfileType=%v", psc["seccompProfileType"])
	}
	md := ctxs[0].Env["metadata"].(map[string]any)
	if md["name"] != "p" {
		t.Errorf("metadata.name=%v", md["name"])
	}
}

func TestMissingFieldsArePresentAsNil(t *testing.T) {
	b := New()
	// minimal container, no securityContext, no command, no args, no ports
	obj := newPod("p", []map[string]any{{"name": "c", "image": "nginx"}}, nil, nil)
	// remove pod-level securityContext
	obj.Object["spec"].(map[string]any)["securityContext"] = nil
	ctxs, _ := b.BuildAll(obj)
	c := ctxs[0].Env["container"].(map[string]any)

	for _, key := range []string{"command", "args", "ports"} {
		if _, ok := c[key]; !ok {
			t.Errorf("expected container key %q to be present (even if nil)", key)
		}
	}
	sc, ok := c["securityContext"].(map[string]any)
	if !ok {
		t.Fatalf("container.securityContext missing")
	}
	for _, key := range []string{"privileged", "allowPrivilegeEscalation", "readOnlyRootFilesystem", "runAsUser", "runAsGroup", "runAsNonRoot", "procMount", "seccompProfileType"} {
		if _, ok := sc[key]; !ok {
			t.Errorf("expected sc key %q present", key)
		}
	}
	if _, ok := sc["capabilities"]; !ok {
		t.Errorf("expected sc.capabilities present")
	}
	psc := ctxs[0].Env["securityContext"].(map[string]any)
	for _, key := range []string{"runAsUser", "runAsGroup", "runAsNonRoot", "fsGroup", "supplementalGroups", "seccompProfileType"} {
		if _, ok := psc[key]; !ok {
			t.Errorf("expected pod sc key %q present", key)
		}
	}
}

func TestDeploymentWorkloadKind(t *testing.T) {
	b := New()
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": "d", "namespace": "default"},
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []any{
						map[string]any{"name": "app", "image": "nginx:1"},
					},
				},
			},
		},
	}}
	obj.SetGroupVersionKind(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"})
	ctxs, err := b.BuildAll(obj)
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	if len(ctxs) != 1 {
		t.Fatalf("expected 1 context, got %d", len(ctxs))
	}
	c := ctxs[0].Env["container"].(map[string]any)
	if c["name"] != "app" {
		t.Errorf("name=%v", c["name"])
	}
}

func TestCronJobWorkloadKind(t *testing.T) {
	b := New()
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "CronJob",
		"metadata":   map[string]any{"name": "cj", "namespace": "default"},
		"spec": map[string]any{
			"jobTemplate": map[string]any{
				"spec": map[string]any{
					"template": map[string]any{
						"spec": map[string]any{
							"containers": []any{
								map[string]any{"name": "j", "image": "busybox"},
							},
						},
					},
				},
			},
		},
	}}
	obj.SetGroupVersionKind(schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "CronJob"})
	ctxs, err := b.BuildAll(obj)
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	if len(ctxs) != 1 {
		t.Fatalf("expected 1 context for cronjob, got %d", len(ctxs))
	}
	c := ctxs[0].Env["container"].(map[string]any)
	if c["name"] != "j" {
		t.Errorf("cron container name=%v", c["name"])
	}
}

func TestNoContainersStillReturnsOneContext(t *testing.T) {
	b := New()
	obj := newPod("p", nil, nil, nil)
	ctxs, err := b.BuildAll(obj)
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	if len(ctxs) != 1 {
		t.Fatalf("expected 1 ctx, got %d", len(ctxs))
	}
	if ctxs[0].Env["container"] != nil {
		t.Errorf("expected container=nil, got %v", ctxs[0].Env["container"])
	}
}

func TestBuildReturnsFirstContext(t *testing.T) {
	b := New()
	obj := newPod("p", []map[string]any{
		{"name": "first", "image": "nginx"},
		{"name": "second", "image": "redis"},
	}, nil, nil)
	c, err := b.Build(obj)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := c.Env["container"].(map[string]any)
	if got["name"] != "first" {
		t.Errorf("Build first container name=%v", got["name"])
	}
}

func TestBuildNilObject(t *testing.T) {
	b := New()
	if _, err := b.BuildAll(nil); err == nil {
		t.Errorf("expected error for nil object")
	}
}
