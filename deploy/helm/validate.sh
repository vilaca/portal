#!/usr/bin/env bash
# validate.sh — best-effort YAML validation for the Portal chart's CRDs and
# templates. Does NOT execute helm (helm isn't always installed locally).
#
# - CRDs under crds/ and ../crds/ are parsed as straight YAML via Go's
#   sigs.k8s.io/yaml (already a Portal dependency).
# - templates/*.yaml are parsed *after* a crude Go-template strip pass so the
#   raw structure is at least YAML-shaped. Real lint requires `helm lint` or
#   `helm template` against a kube-apiserver.
#
# Exit non-zero on the first malformed file.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$here/../.." && pwd)"

# Build a small Go program that reads files from argv and runs them through
# sigs.k8s.io/yaml.Unmarshal as a multi-document stream.
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

cat > "$tmpdir/main.go" <<'GO'
package main

import (
	"bytes"
	"fmt"
	"os"
	"regexp"

	"sigs.k8s.io/yaml"
)

var (
	lineDirective = regexp.MustCompile(`(?m)^\s*\{\{-?\s*[^}]+?\s*-?\}\}\s*$`)
	inlineExpr    = regexp.MustCompile(`\{\{-?\s*[^}]*?\s*-?\}\}`)
)

func stripTemplates(src []byte) []byte {
	out := lineDirective.ReplaceAll(src, []byte(""))
	out = inlineExpr.ReplaceAll(out, []byte("PLACEHOLDER"))
	return out
}

func splitDocs(src []byte) [][]byte {
	docs := bytes.Split(src, []byte("\n---"))
	var out [][]byte
	for _, d := range docs {
		d = bytes.TrimSpace(d)
		if len(d) > 0 {
			out = append(out, d)
		}
	}
	return out
}

func main() {
	fail := 0
	for _, path := range os.Args[1:] {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", path, err)
			fail++
			continue
		}
		if matched := inlineExpr.Find(data); matched != nil {
			data = stripTemplates(data)
		}
		ok := true
		for _, doc := range splitDocs(data) {
			var v any
			if err := yaml.Unmarshal(doc, &v); err != nil {
				fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", path, err)
				ok = false
				fail++
				break
			}
		}
		if ok {
			fmt.Printf("ok   %s\n", path)
		}
	}
	if fail > 0 {
		os.Exit(1)
	}
}
GO

files=()
for f in "$here"/portal/crds/*.yaml "$here"/../crds/*.yaml "$here"/portal/templates/*.yaml; do
  [ -f "$f" ] || continue
  files+=("$f")
done

(cd "$repo_root" && go run "$tmpdir/main.go" "${files[@]}")
