SHELL := bash
GO := go
BIN := bin/portal
PKG := ./...

CONTROLLER_GEN ?= $(shell go env GOPATH)/bin/controller-gen
HELM_DOCS      ?= $(shell go env GOPATH)/bin/helm-docs
GOMARKDOC      ?= $(shell go env GOPATH)/bin/gomarkdoc

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
	$(GO) install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.15.0
	$(GO) install github.com/norwoodj/helm-docs/cmd/helm-docs@v1.13.1
	$(GO) install github.com/princjef/gomarkdoc/cmd/gomarkdoc@v1.1.0

generate: generate-crds generate-docs

generate-crds:
	$(CONTROLLER_GEN) crd paths=./internal/rule/crd/... output:crd:dir=deploy/crds
	$(CONTROLLER_GEN) object paths=./internal/rule/crd/...

generate-docs:
	$(GO) run ./internal/sink/prometheus/cmd/metricsdoc > docs/reference/metrics.md
	$(GO) run ./cmd/portal docgen > docs/reference/cli.md || true
	$(HELM_DOCS) -c deploy/helm/portal -o ../../../docs/reference/helm-values.md || true
	$(GOMARKDOC) -o docs/plugin-author/interface-reference.md ./internal/api || true

docs:
	@command -v mkdocs >/dev/null && mkdocs build --strict || echo "mkdocs not installed; skipping"

clean:
	rm -rf bin dist site
