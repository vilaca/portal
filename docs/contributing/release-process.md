# Release process

Portal v1 is greenfield — there isn't a release yet. The process below is the documented intent for the **inaugural v0.1.0** cut and every release thereafter.

## Versioning

Semantic versioning, with the caveat that `v0.x.y` is pre-1.0 and breaking changes are permitted between minor versions until we cut `v1.0.0`. The CRD `apiVersion: portal.io/v1alpha1` reflects this: the API is not yet stable.

- `v0.1.0` — inaugural release.
- `v0.2.0`, `v0.3.0` — minor releases; may contain breaking CR-shape changes (documented in `CHANGELOG.md`).
- `v0.1.1`, `v0.1.2` — patch releases; bugfix-only against `v0.1.0`'s shape.
- `v1.0.0` — graduation; from then on, breaking CR changes require a `v1beta1` → `v1` transition and a conversion webhook.

## Release artifact set

Each tagged release produces:

1. A **container image** — `ghcr.io/vilaca/portal:vX.Y.Z` (placeholder registry; real publishing is gated on GitHub org setup).
2. A **Helm chart** — packaged tarball uploaded to the GitHub release assets and to a Helm chart repo (TODO: chart repo hosting setup).
3. **CRD YAMLs** as separate release assets — `portal.io_portalclusterrules.yaml`, `portal.io_portalrules.yaml`, vendored `wgpolicyk8s.io_policyreports.yaml`, `wgpolicyk8s.io_clusterpolicyreports.yaml`. These are also bundled inside the chart.
4. The **portal CLI binary** for `linux/amd64` and `linux/arm64`.

These are produced by `.github/workflows/release.yml`, triggered on push of a `v*` tag.

## Cut a release — the steps

```bash
# 1. Make sure main is green.
git checkout main
git pull
gh run list --branch main --limit 1   # confirm CI is green

# 2. Update CHANGELOG.md with the release notes.
$EDITOR CHANGELOG.md

# 3. Commit and push the changelog.
git add CHANGELOG.md
git commit -m "chore: changelog for v0.1.0"
git push

# 4. Tag.
git tag -a v0.1.0 -m "Portal v0.1.0"
git push origin v0.1.0
```

The push of the tag triggers `release.yml`, which:

- Re-runs the test suite.
- Builds and pushes the container image to GHCR (`linux/amd64` + `linux/arm64`).
- Packages the Helm chart (`helm package deploy/helm/portal --version v0.1.0`).
- Creates a GitHub release with the chart tarball and CLI binaries as assets.

## Changelog convention

`CHANGELOG.md` follows [Keep a Changelog](https://keepachangelog.com/) format:

```markdown
## [v0.1.0] — 2026-xx-xx

### Added
- Admission webhook with fail-closed default.
- ...

### Changed

### Deprecated

### Removed

### Fixed

### Security
```

The file does not exist yet — `v0.1.0` is the inaugural release and it will be created as part of that cut.

## Documentation publishing

`.github/workflows/docs.yml` publishes the mkdocs site on every push to `main` that touches `docs/**` or `mkdocs.yml`. Documentation always reflects `main`; older release docs are accessible via git tag if needed (no per-version docs hosting in v1).

## Deprecation policy

**TODO.** v1 is the inaugural shape; deprecation windows will be defined when we move to `v1beta1` or `v1`. The intent: any field removed from a CRD will:

1. First be marked deprecated for at least one minor release.
2. Trigger a `kubectl warning` when used (via `apiserver` deprecation warnings, which CRD `+kubebuilder:deprecatedversion` markers wire up).
3. Be removed only in the subsequent major release.

Until that policy is formalised, treat the rule schema as eventually-stable; the PLAN's "additive only" principle has been followed throughout v1.

## Post-release checklist

After the workflow finishes:

1. Visit the GitHub release page and verify the assets attached cleanly.
2. `helm pull portal/portal --version vX.Y.Z` (or fetch from release assets) and `helm install` into a kind cluster — confirm the chart resolves CRDs correctly.
3. Pull and run the image: `docker run --rm ghcr.io/vilaca/portal:vX.Y.Z version`.
4. Announce: write up the highlights from `CHANGELOG.md` on the project's communication channels.
