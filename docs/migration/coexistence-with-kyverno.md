# Coexistence with Kyverno

Portal and Kyverno occupy adjacent corners of the same layer-4 + layer-5 space. They are **complementary** in a defense-in-depth posture; you do not have to pick one. This page explains when each tool is the right answer and how to run both in the same cluster.

## When to reach for Portal

- You want **AlertManager-native admission** — your security team consumes alerts through Prometheus/AlertManager, and your podwatcher-poc setup already feeds them. Portal preserves that JSON shape byte-for-byte.
- You need **built-in static NetworkPolicy analysis** — default-deny gaps, broad CIDRs, unreachable selectors. Other admission tools require you to write these checks yourself; Portal ships them.
- You want a **response-action engine** beyond "deny the request": label-and-alert, evict, patch a NetworkPolicy, revoke a SA token. These all flow off the same rule schema, with idempotency and rate-limiting baked in.
- You have a **podwatcher-poc rule corpus** to migrate — `portal migrate-rules` does 4/6 byte-identical translations and one-line tweaks on the other two.
- You like terse expressions — `x in ['a','b']`, `?.`, `??` — without YAML-pattern boilerplate.

## When to reach for Kyverno

- You need **mutation** — defaulting, injection, label management at admission. Portal v1 is explicitly non-mutating (see `../adr/0004-no-mutation-no-ebpf.md`).
- You need **resource generation or cleanup** — Kyverno's `generate` and `cleanup` rules.
- You want **image signature verification** out of the box (sigstore/cosign integration). Portal v1 ships no signature verification.
- You want a **large public policy library** — Nirmata's Kyverno policies repo is broad and battle-tested.
- You need **richer match/exclude semantics** — label selectors, name globs, etc. as part of the rule schema.

## Running both in the same cluster

Both controllers register their own `ValidatingWebhookConfiguration`. Kubernetes evaluates all webhooks in parallel; any single deny short-circuits the admission decision. Failing-closed in both means you'll need both controllers up — that's the point.

Mutual exclusion is handled at the namespace-selector level:

- **Portal** always excludes `kube-system`, `kube-public`, `kube-node-lease`, and its own install namespace from its webhook (`internal/admission/server.go` — `DefaultExcludedNamespaces`).
- **Kyverno** has its own analogous exclusion list, configured independently in its Helm values.

Neither tool excludes the other's namespace by default; if you have a strict policy that "Portal cannot evaluate Kyverno workloads" (or vice versa), add the namespace explicitly to the relevant exclusion list.

A typical layout:

```
Portal:    enforces security baselines (privileged, hostPath, registries),
           runs NetworkPolicy analysis, fires AlertManager.
Kyverno:   mutates incoming workloads (default labels, sidecar injection),
           generates per-namespace RBAC, verifies image signatures.
```

## Sanity-check after installing both

1. `kubectl get validatingwebhookconfiguration` — confirm both `portal.io` and `kyverno-*` configurations are present.
2. `kubectl apply --dry-run=server -f a-privileged-pod.yaml` — should be denied/warned by Portal and (depending on your Kyverno rules) by Kyverno too.
3. Watch `portal_admission_requests_total{decision="deny"}` and Kyverno's `kyverno_admission_review_duration_seconds` to confirm both are seeing traffic.

For an honest feature-by-feature comparison see `../comparison/feature-matrix.md`.
