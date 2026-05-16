# Responsible disclosure

Portal is greenfield software. We treat security reports as the highest-priority signal we receive.

## Reporting a vulnerability

Send the report to **`security@portal.io`** _(placeholder — the real address will be set at GA. Until then, open a GitHub Security Advisory on the Portal repository: `https://github.com/vilaca/portal/security/advisories/new`.)_

Include, where possible:

- The Portal version (`portal version` output, or chart version).
- The Kubernetes version and any other relevant cluster characteristics (CNI, admission tooling, RBAC posture).
- A minimal reproducer — manifests, rule YAML, and the exact command sequence.
- The observed impact and any suspected exploitability bounds.
- Whether you have already shared details with anyone else.

Reports are encrypted at rest and visible only to the security triage group. Please **do not** open a public issue, post to a forum, or share details on Slack/Discord/etc. until we agree the report is ready for disclosure.

## Our commitment

- We acknowledge receipt within **two business days**.
- We share an initial assessment (validity, severity, fix complexity) within **seven business days**.
- We coordinate a fix and disclosure timeline with the reporter.
- We credit the reporter in the published advisory (unless they prefer anonymity).
- We **do not** offer monetary bounties at this stage. (When we move out of greenfield this may change.)

## Disclosure window

We target a **90-day** disclosure window from initial report:

- Days 0–14: triage, reproduce, scope.
- Days 14–60: develop and test the fix.
- Days 60–90: coordinated release. The advisory and the patched chart/binary ship on the same day.

If the report is already public or being actively exploited, the timeline compresses; we will work with the reporter to keep the window as small as is responsibly possible.

## Scope

In scope:

- Portal binary (`cmd/portal`, `internal/`).
- Portal Helm chart (`deploy/helm/portal/`).
- Portal CRDs (`deploy/crds/`, `deploy/helm/portal/crds/`).
- The documentation under `docs/` to the extent that following it leads to an insecure deployment.

Out of scope:

- Vulnerabilities in upstream dependencies (`client-go`, `expr-lang`, `prometheus/client_golang`). Report those upstream; we will track and uptake the fix.
- Vulnerabilities in Kubernetes itself. Report those to `security@kubernetes.io`.
- Misconfigurations on the operator's side (e.g. running with `global.failClosed: false`, granting `cluster-admin` to Portal). We will, however, update the docs to make the misconfiguration harder to fall into.

## Hall of fame

Names of reporters who chose attribution will be listed here once we have any to list. Greenfield project, so the list is short today.
