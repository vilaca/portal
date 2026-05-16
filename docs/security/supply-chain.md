# Supply chain

Portal v1 ships a single Go binary in a distroless container. This page documents what's in place today and what's deliberately deferred to v2.

## What's in place today (v1)

### Distroless base image

The image uses Google's `gcr.io/distroless/static-debian12:nonroot` (or equivalent). No shell, no package manager, no userspace utilities — just the Portal binary and the trust bundle. The container runs as a non-root user.

Implications:

- Reduced attack surface — no `/bin/sh`, no `kubectl`, no `curl`.
- Smaller image (~20 MB).
- Forensics need a debug sidecar or `kubectl debug --image=...` if you want a shell.

### Reproducible builds

The `go build` invocation pins the Go version (1.22+ per `go.mod`) and uses `-trimpath` so the binary doesn't embed local paths. Combined with `GOFLAGS=-mod=readonly`, the same source tree produces the same binary byte-for-byte across builders.

### `go.sum` integrity

Every dependency is pinned by content hash in `go.sum`. CI rejects PRs that modify `go.sum` without a corresponding `go.mod` change. The Go toolchain enforces the hash on every `go build` / `go test`.

### Vulnerability scanning hooks

The CI workflow (see `.github/workflows/ci.yml`) runs `go vet` and `go test -race`. Adding `govulncheck` is straightforward and is a v1 task once the GH org is ready to take the signal — left as a TODO so this doc doesn't lie about what's there.

## What is **not** in place (v2 candidates)

### SBOM generation — TODO

There is no `make sbom` target today. The plan:

- Adopt `syft` to emit CycloneDX or SPDX from the built image.
- Publish the SBOM alongside each tagged release as a release asset.
- Sign the SBOM with the same key used for image signing.

Tracking issue: `v2-candidates/supply-chain-sbom`.

### Image signing — TODO

There is no cosign signature on Portal images today. The plan:

- Sign every released image with `cosign sign --keyless` using GitHub's OIDC identity.
- Publish the signature alongside the image in the GHCR registry.
- Document a Kyverno or sigstore-policy-controller manifest in `docs/cookbook/` that operators can use to enforce signature presence at admission.

Tracking issue: `v2-candidates/supply-chain-signing`.

### Container image attestation — TODO

`provenance` attestation (SLSA build provenance, also via cosign) is on the same v2 track as signing. See the same tracking issue.

## What you should still do at the consumer end

Until v2 closes the gaps above:

1. **Mirror Portal images to your own registry** rather than pulling directly from upstream. Pin by digest (`@sha256:...`), not by tag.
2. **Run your own scanner** against the mirrored image (Trivy, Grype). Distroless is small, so scans are quick.
3. **Compare `go.mod` against your dependency policy** if you ship internal-software-only. Portal pulls from `k8s.io`, `prometheus`, `expr-lang`, `sigs.k8s.io` — well-known upstream.
4. **Track the v2 supply-chain milestone** — when image signing lands, integrate it into your sigstore/Kyverno policy.

For threat-model context see `threat-model.md`; for responsible disclosure see `responsible-disclosure.md`.
