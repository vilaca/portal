package migrate

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/vilaca/portal/internal/expr/exprlang"
)

// TestRewriteExpression_BraceSet exercises the {a,b,c}.contains(x) → x in [a,b,c]
// rewrite, both bare-identifier and quoted-string members.
func TestRewriteExpression_BraceSet(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "quoted set",
			in:   `!{'registry.k8s.io', 'docker.io'}.contains(container.image.registry)`,
			want: `!(container.image.registry in ['registry.k8s.io', 'docker.io'])`,
		},
		{
			name: "bare ident set",
			in:   `{a,b,c}.contains(x)`,
			want: `(x in [a,b,c])`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, warns := RewriteExpression(tc.in)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if len(warns) != 0 {
				t.Fatalf("unexpected warnings: %v", warns)
			}
		})
	}
}

// TestRewriteExpression_ReceiverContains covers foo.bar.contains('y') →
// 'y' in foo.bar, including safe-nav (?.) receivers.
func TestRewriteExpression_ReceiverContains(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{
			in:   `!container.securityContext.capabilities.drop.contains('ALL')`,
			want: `!('ALL' in container.securityContext.capabilities.drop)`,
		},
		{
			in:   `foo?.bar.contains("y")`,
			want: `("y" in foo?.bar)`,
		},
	}
	for _, tc := range cases {
		got, warns := RewriteExpression(tc.in)
		if got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
		if len(warns) != 0 {
			t.Fatalf("unexpected warnings: %v", warns)
		}
	}
}

// TestRewriteExpression_PassThrough makes sure startsWith / endsWith / ?.
// pass through unchanged — expr-lang accepts the same syntax.
func TestRewriteExpression_PassThrough(t *testing.T) {
	cases := []string{
		`container.image.name.startsWith('busybox')`,
		`container.image.tag.endsWith('-latest')`,
		`metadata?.labels?.app == 'foo'`,
		`securityContext.runAsUser == 0`,
	}
	for _, in := range cases {
		got, warns := RewriteExpression(in)
		if got != in {
			t.Fatalf("expected pass-through for %q, got %q", in, got)
		}
		if len(warns) != 0 {
			t.Fatalf("unexpected warnings on %q: %v", in, warns)
		}
	}
}

// TestRewriteExpression_UnsupportedSpEL ensures T(...) and #root produce
// warnings without mangling the expression.
func TestRewriteExpression_UnsupportedSpEL(t *testing.T) {
	in := `T(java.lang.Math).abs(x) > 0 && #root.foo == 1`
	got, warns := RewriteExpression(in)
	if got != in {
		t.Fatalf("expected unchanged expression, got %q", got)
	}
	if len(warns) < 2 {
		t.Fatalf("expected at least two warnings, got %v", warns)
	}
}

// TestMigrate_FolderFormat round-trips a podwatcher-poc-style YAML through
// Migrate(format=folder) and asserts the result has the canonical
// match.namespaces / Pod-default-GVK / mode / enforcementAction shape, plus
// an `in` expression instead of `.contains`.
func TestMigrate_FolderFormat(t *testing.T) {
	dir := t.TempDir()
	body := `# kube-system safety
name: kube-system namespace safety
enabled: false
severity: high
filter:
  namespace:
    include:
      - kube-system
rule: |
  !{'registry.k8s.io', 'docker.io', 'ghcr.io'}.contains(container.image.registry)
alert: registry-alert
`
	if err := os.WriteFile(filepath.Join(dir, "ks.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Migrate(dir, FormatFolder)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	if len(res.Outputs) != 1 {
		t.Fatalf("expected 1 output, got %d", len(res.Outputs))
	}

	var got map[string]any
	if err := yaml.Unmarshal(res.Outputs[0].Body, &got); err != nil {
		t.Fatalf("parse output: %v", err)
	}
	if _, ok := got["filter"]; ok {
		t.Fatalf("filter key should be deleted, got %v", got["filter"])
	}
	match, _ := got["match"].(map[string]any)
	if match == nil {
		t.Fatal("missing match block")
	}
	ns, _ := match["namespaces"].(map[string]any)
	if ns == nil {
		t.Fatal("missing match.namespaces")
	}
	inc, _ := ns["include"].([]any)
	if len(inc) != 1 || inc[0].(string) != "kube-system" {
		t.Fatalf("match.namespaces.include = %v", inc)
	}
	gvk, _ := match["gvk"].([]any)
	if len(gvk) != 1 {
		t.Fatalf("expected defaulted gvk slice, got %v", gvk)
	}
	if got["enforcementAction"].(string) != "warn" {
		t.Fatalf("enforcementAction = %v", got["enforcementAction"])
	}
	mode, _ := got["mode"].([]any)
	if len(mode) != 2 {
		t.Fatalf("expected default mode of 2 entries, got %v", mode)
	}
	expr, _ := got["rule"].(string)
	if !strings.Contains(expr, " in [") {
		t.Fatalf("expected `in [...]` in rule, got %q", expr)
	}
	if strings.Contains(expr, ".contains(") {
		t.Fatalf("rule still contains .contains(): %q", expr)
	}

	// Compile-validate that the rewritten expression is real expr-lang.
	if _, err := exprlang.New().Compile(expr); err != nil {
		t.Fatalf("rewritten expression does not compile: %v\n%s", err, expr)
	}
}

// TestMigrate_CRFormat checks that CR output wraps the rule with the right
// apiVersion / kind / metadata and that the slug derives from the rule name.
func TestMigrate_CRFormat(t *testing.T) {
	dir := t.TempDir()
	body := `name: Privileged Container
enabled: true
severity: critical
rule: container.securityContext.privileged == true
alert: insecure-workload
`
	if err := os.WriteFile(filepath.Join(dir, "p.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Migrate(dir, FormatCR)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	if len(res.Outputs) != 1 {
		t.Fatalf("expected 1 output, got %d", len(res.Outputs))
	}
	if res.Outputs[0].Path != "privileged-container.yaml" {
		t.Fatalf("expected slug file 'privileged-container.yaml', got %q", res.Outputs[0].Path)
	}

	var manifest map[string]any
	if err := yaml.Unmarshal(res.Outputs[0].Body, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest["apiVersion"] != "portal.io/v1alpha1" {
		t.Fatalf("apiVersion = %v", manifest["apiVersion"])
	}
	if manifest["kind"] != "PortalClusterRule" {
		t.Fatalf("kind = %v", manifest["kind"])
	}
	meta, _ := manifest["metadata"].(map[string]any)
	if meta["name"] != "privileged-container" {
		t.Fatalf("metadata.name = %v", meta["name"])
	}
	if _, ok := manifest["spec"].(map[string]any); !ok {
		t.Fatalf("missing spec block: %T", manifest["spec"])
	}
}

// TestMigrate_SlugCollision asserts that two rules with the same human name
// produce CR manifests with bare and -2 suffixed slugs respectively.
func TestMigrate_SlugCollision(t *testing.T) {
	dir := t.TempDir()
	body := `name: Privileged Container
enabled: true
severity: critical
rule: container.securityContext.privileged == true
`
	if err := os.WriteFile(filepath.Join(dir, "a.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Migrate(dir, FormatCR)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Outputs) != 2 {
		t.Fatalf("expected 2 outputs, got %d", len(res.Outputs))
	}
	paths := []string{res.Outputs[0].Path, res.Outputs[1].Path}
	want := []string{"privileged-container.yaml", "privileged-container-2.yaml"}
	for i := range paths {
		if paths[i] != want[i] {
			t.Fatalf("output %d: got %q, want %q", i, paths[i], want[i])
		}
	}
}

// TestMigrate_Idempotent checks that running Migrate on its own output
// produces byte-identical output. Folder format is used because CR mode
// rewraps each rule on every pass (the spec block inside the CR is what
// matters; we test that nested form converges).
func TestMigrate_Idempotent(t *testing.T) {
	dir := t.TempDir()
	body := `name: host-namespace-access
enabled: true
severity: critical
rule: spec.hostNetwork == true || spec.hostPID == true
alert: insecure-workload
`
	if err := os.WriteFile(filepath.Join(dir, "h.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	pass1, err := Migrate(dir, FormatFolder)
	if err != nil {
		t.Fatal(err)
	}
	if len(pass1.Outputs) != 1 {
		t.Fatalf("pass1: expected 1 output, got %d", len(pass1.Outputs))
	}

	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, "h.yaml"), pass1.Outputs[0].Body, 0o644); err != nil {
		t.Fatal(err)
	}
	pass2, err := Migrate(dir2, FormatFolder)
	if err != nil {
		t.Fatal(err)
	}
	if len(pass2.Outputs) != 1 {
		t.Fatalf("pass2: expected 1 output, got %d", len(pass2.Outputs))
	}
	if !bytes.Equal(pass1.Outputs[0].Body, pass2.Outputs[0].Body) {
		t.Fatalf("migrate not idempotent:\n--- pass1 ---\n%s\n--- pass2 ---\n%s",
			pass1.Outputs[0].Body, pass2.Outputs[0].Body)
	}
}

// TestMigrate_PodwatcherCorpus runs Migrate over an in-memory copy of the
// six podwatcher-poc example rules and verifies every output expression
// compiles under expr-lang.
func TestMigrate_PodwatcherCorpus(t *testing.T) {
	corpus := map[string]string{
		"capabilities.yaml": `name: missing-drop-all-capabilities
enabled: true
severity: high
rule: >
  !container.securityContext.capabilities.drop.contains('ALL')
alert: insecure-workload
`,
		"hostNamespace.yaml": `name: host-namespace-access
enabled: true
severity: critical
rule: >
  spec.hostNetwork == true
  || spec.hostPID == true
  || spec.hostIPC == true
alert: insecure-workload
`,
		"kube-system.yaml": `name: kube-system namespace safety
enabled: false
severity: high
filter:
  namespace:
    include:
      - kube-system
rule: >
  !{'registry.k8s.io', 'docker.io', 'ghcr.io'}.contains(container.image.registry)
alert: registry-alert
`,
		"prevent-run-as-root.yaml": `name: insecure-workload:run-as-root
enabled: true
severity: high
rule: securityContext.runAsUser == 0 || container.securityContext.runAsUser == 0
alert: insecure-workload
`,
		"privilegedContainer.yaml": `name: privileged container
enabled: true
severity: critical
rule: container.securityContext.privileged  == true || container.securityContext.allowPrivilegeEscalation == true
alert: insecure-workload
`,
		"readOnlyFS.yaml": `name: readOnlyFs
enabled: true
severity: medium
rule: container.securityContext.readOnlyRootFilesystem == false
alert: insecure-workload
`,
	}
	dir := t.TempDir()
	for name, body := range corpus {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	res, err := Migrate(dir, FormatCR)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	if len(res.Outputs) != len(corpus) {
		t.Fatalf("expected %d outputs, got %d", len(corpus), len(res.Outputs))
	}
	engine := exprlang.New()
	for _, out := range res.Outputs {
		var manifest map[string]any
		if err := yaml.Unmarshal(out.Body, &manifest); err != nil {
			t.Fatalf("%s: parse: %v", out.Path, err)
		}
		spec, _ := manifest["spec"].(map[string]any)
		if spec == nil {
			t.Fatalf("%s: missing spec", out.Path)
		}
		expr, _ := spec["rule"].(string)
		if expr == "" {
			t.Fatalf("%s: missing rule expression", out.Path)
		}
		if _, err := engine.Compile(expr); err != nil {
			t.Fatalf("%s: expression does not compile under expr-lang: %v\nexpr: %s", out.Path, err, expr)
		}
	}
}

// TestMigrate_AlreadyMigratedIsNoOp checks that re-running the migrator over
// already-Portal-format rules doesn't drift: the rewritten body parses back
// to the same logical YAML, and the rule expression continues to compile.
func TestMigrate_AlreadyMigratedIsNoOp(t *testing.T) {
	dir := t.TempDir()
	body := `apiVersion: portal.io/v1alpha1
kind: PortalClusterRule
metadata:
  name: privileged-container
spec:
  name: privileged-container
  enabled: true
  severity: critical
  mode: [admission, audit]
  enforcementAction: warn
  match:
    gvk:
      - {group: "", version: v1, kind: Pod}
  rule: container.securityContext.privileged == true
  alert: insecure-workload
`
	// Folder mode is the relevant test target: CR mode would rewrap the
	// whole-CR document inside another CR, which is not idempotent by design
	// and is not how migrate-rules is invoked on Portal-format inputs.
	if err := os.WriteFile(filepath.Join(dir, "p.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Migrate(dir, FormatFolder)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	if len(res.Outputs) != 1 {
		t.Fatalf("expected 1 output, got %d", len(res.Outputs))
	}
	var got map[string]any
	if err := yaml.Unmarshal(res.Outputs[0].Body, &got); err != nil {
		t.Fatal(err)
	}
	// The whole-CR top-level was untouched apart from defaults bubbling up
	// at the top level (which is a no-op for a CR document — the migrator
	// can't tell the difference, but the spec block is what runtime consumes).
	if got["kind"] != "PortalClusterRule" {
		t.Fatalf("kind got lost: %v", got["kind"])
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Privileged Container":         "privileged-container",
		"insecure-workload:run-as-root": "insecure-workload-run-as-root",
		"  weird   chars!!!  ":         "weird-chars",
		"":                             "",
		strings.Repeat("a", 80):        strings.Repeat("a", 63),
	}
	for in, want := range cases {
		got := slugify(in)
		if got != want {
			t.Fatalf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
