# ADR 0004 — No mutation, no eBPF in v1

**Status.** Accepted, in v1.

## Context

Two large families of features are popular in the K8s policy ecosystem:

- **Mutation, generation, cleanup** — Kyverno's bread and butter. Modify incoming admission objects (defaulting, sidecar injection), create dependent resources, remove orphans on a schedule.
- **eBPF / kernel-level enforcement** — Tetragon, Falco. Hook syscalls and network events in-kernel, react in microseconds, block process exec.

Both are legitimate. Both are out of scope for Portal v1.

## Decision

- **v1 is non-mutating.** Admission rules can `deny`, `warn`, or `dryrun`. They cannot modify the object, cannot add a default, cannot inject a sidecar.
- **v1 has no resource generation.** Portal does not create dependent resources (no auto-generated `NetworkPolicy` per namespace, no default `ServiceAccount`, etc.).
- **v1 has no cleanup.** Portal does not delete or garbage-collect resources outside the scope of its own actions (the `evict` action evicts a pod the rule flagged; that's not "cleanup").
- **v1 has no eBPF.** Portal is a Go process talking to `kube-apiserver`. It does not load BPF programs, does not read `/sys/fs/bpf`, does not require kernel features.

## Rationale

### Why not mutation in v1

- **Conceptual integrity.** Validation and mutation are different problems with different correctness properties. A mutation webhook must be idempotent; the rule schema would need new constructs (`patch`, `add`, `remove` operations). Tackling both at once in v1 is over-extension.
- **Kyverno already does it well.** If you need mutation, run Kyverno. Portal coexists with Kyverno (see `../migration/coexistence-with-kyverno.md`) — operators are not forced to pick.
- **The migration path is intact.** podwatcher-poc was non-mutating; Portal v1 preserves that property exactly. Operators upgrading from podwatcher-poc don't encounter behavioural drift.

Mutation is **plausibly v3.** The rule schema would gain a `mutate:` block; the admission webhook would emit a JSON patch in the `AdmissionResponse`. The plumbing is well-understood. We're not doing it in v1 because shipping v1 with a tight scope beats shipping v1.5 with everything.

### Why not eBPF, ever (for Portal)

eBPF is a different layer of the stack. Portal is layer 4 (admission), 5 (audit), 6 (network policy analysis), and — in v2 — layer 7 (K8s API audit log). eBPF lands at layer 7+8 (live process, live network, live syscalls).

- **Operational profile is different.** eBPF tools are DaemonSets, kernel-version-sensitive, often privileged. Portal is a Deployment, kernel-agnostic, non-privileged.
- **The user already has tools.** Tetragon and Falco are CNCF projects with significant adoption. Replicating their kernel-level work in Portal would duplicate effort and produce inferior results.
- **v2's runtime story is API-audit-log-driven**, not kernel-driven. The K8s API audit log gives Portal visibility into exec/attach/port-forward/secret-read at the API layer — broad enough for "did someone exec into a prod container" without descending into syscalls. v2 adds an `EventSource` that consumes the audit log; the same v1 action engine handles the response.

If you need kernel-level runtime detection, run Tetragon or Falco. If you want their findings flowing through the same alerting and response pipeline as Portal's, a v3 candidate is a Tetragon/Falco gRPC consumer that wraps their events in `api.EventMeta` and emits to Portal's dispatcher.

## Consequences

- The rule schema has no mutation verb. Adding one later is additive; no v1 rules will break.
- Documentation (this ADR plus `../security/threat-model.md`) is explicit about what Portal does **not** cover.
- The chart does **not** require any kernel capabilities, BPF helpers, or privileged containers. PSA-`restricted`-clean.
- The v2 plan has a clear seam: K8s audit log as new `EventSource`; everything else reused.
- The v3 plan can layer in mutation (new rule-schema block + new `MutationAdmissionWebhook`) and an external runtime consumer (new `EventSource`). Both fit the existing module boundaries.
