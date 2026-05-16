SHELL := bash
GO := go
BIN := bin/portal
PKG := ./...

CONTROLLER_GEN ?= $(shell command -v controller-gen 2>/dev/null || echo $$(go env GOPATH)/bin/controller-gen)
HELM_DOCS      ?= $(shell command -v helm-docs      2>/dev/null || echo $$(go env GOPATH)/bin/helm-docs)
GOMARKDOC      ?= $(shell command -v gomarkdoc     2>/dev/null || echo $$(go env GOPATH)/bin/gomarkdoc)

.PHONY: all build vet test lint tidy generate generate-crds generate-docs clean docs

all: build

build:
	$(GO) build -o $(BIN) ./cmd/portal

vet:
	$(GO) vet $(PKG)

test:
	$(GO) test -race -count=1 $(PKG)

tidy:
	$(GO) mod tidy

lint: vet

tools:
	$(GO) install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.17.3
	$(GO) install github.com/norwoodj/helm-docs/cmd/helm-docs@v1.13.1
	$(GO) install github.com/princjef/gomarkdoc/cmd/gomarkdoc@v1.1.0

generate: generate-crds generate-docs

generate-crds:
	$(CONTROLLER_GEN) crd paths=./internal/rule/v1alpha1/... output:crd:dir=deploy/crds
	$(CONTROLLER_GEN) object paths=./internal/rule/v1alpha1/...
	cp deploy/crds/portal.io_portalclusterrules.yaml deploy/helm/portal/crds/portal.io_portalclusterrules.yaml
	cp deploy/crds/portal.io_portalrules.yaml deploy/helm/portal/crds/portal.io_portalrules.yaml

generate-docs:
	$(GO) run ./internal/sink/prometheus/cmd/metricsdoc > docs/reference/metrics.md
	$(GO) run ./cmd/portal docgen --out docs/reference/cli.md
	$(HELM_DOCS) -c deploy/helm/portal -o ../../../docs/reference/helm-values.md
	$(GOMARKDOC) -o docs/plugin-author/interface-reference.md ./internal/api

# generate-docs-check is the CI drift gate. Runs generate-docs and fails if
# the working tree diverges. Run `make generate-docs` locally + commit if
# this fails.
.PHONY: generate-docs-check
generate-docs-check: generate-docs
	@if [ -n "$$(git status --porcelain docs/)" ]; then \
		echo "Generated docs are out of date. Run 'make generate-docs' and commit:"; \
		git --no-pager diff --stat docs/; \
		exit 1; \
	fi

docs:
	@command -v mkdocs >/dev/null && mkdocs build --strict || echo "mkdocs not installed; skipping"

clean:
	rm -rf bin dist site
