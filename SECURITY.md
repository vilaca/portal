# Security policy

## Reporting a vulnerability

Please do **not** open a public GitHub issue for security reports.

Email `security@portal.io` (placeholder until GA — the address will be
published in the v1.0 release notes) with:

- A description of the issue and the version / commit it affects.
- A reproducer or proof-of-concept where possible.
- Whether you intend to disclose publicly, and on what timeline.

You should expect:

- **Acknowledgement within 3 business days.**
- A coordinated disclosure window of up to **90 days** from the
  acknowledgement, extendable by mutual agreement if the fix requires
  upstream coordination (e.g. a client-go change).
- Credit in the release notes when the fix lands, unless you ask for
  anonymity.

## Scope

In scope:

- Privilege escalation through Portal's RBAC, ServiceAccount, or webhook
  configuration.
- Bypass of admission decisions (e.g. crafted AdmissionReview payloads
  that produce wrong `allowed` answers).
- Information disclosure through Portal's sinks, metrics, or logs.
- Vulnerabilities in `portal init-certs`'s CA / leaf material handling.
- Container-image issues (base image CVE rollups, build provenance).

Out of scope (filed as regular issues instead):

- The expression language's ability to express dangerous rules — Portal
  trusts rule authors. RBAC on `PortalClusterRule` is the boundary.
- Issues in third-party tools Portal integrates with (cert-manager,
  Prometheus, AlertManager, controller-runtime, kind) — please report
  those upstream and copy us if Portal is the trigger.

## Supported versions

Until v1.0 is tagged, only the `main` branch is supported. Post-v1.0 the
support matrix will be published here.

## What Portal does and does not protect against

See [`docs/security/threat-model.md`](docs/security/threat-model.md). The
short version: Portal sits at K8s layers 4–6 (admission, audit, declarative
NetworkPolicy). It is not a runtime-kernel-level enforcer — that is
Tetragon / Falco territory.
