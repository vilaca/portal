# Threat model

This page is intentionally honest about Portal's coverage. Defense-in-depth means picking the right tool for each layer; Portal owns some, leaves others to neighbours.

## What Portal protects against

### Admission of policy-violating resources

The admission webhook (`internal/admission/`) evaluates rules in `mode: [admission]` against every inbound CREATE/UPDATE/DELETE. Rules with `enforcementAction: deny` cause `kube-apiserver` to reject the request with a 403 and the rule's message; `warn` accumulates into `AdmissionResponse.warnings[]`; `dryrun` records to PolicyReport and metrics without affecting the response.

Coverage: any `GroupVersionKind` that the webhook is configured to receive (the chart templates this from `watchedGvks` plus the rule corpus). Pod-shaped sugar (per `internal/context/pod/`) makes pod rules terser; everything else reaches through `object.<path>`.

### Existing-resources that violate policy

The audit loop (`internal/audit/`) is informer-driven (no polling). Rules in `mode: [audit]` evaluate on every `OnAdd`/`OnUpdate` informer event, plus once at startup (initial list-and-process) and once per 10-minute resync as a safety net. Violations flow to the same sinks (PolicyReport, AlertManager, Prometheus, stdout) and to the action engine for response.

Coverage: any GVK informer Portal has registered. The dep-index (`internal/lookup/`) extends this to cross-resource policies, e.g. "Deployment violates if no PDB selects it".

### NetworkPolicy misconfigurations

`internal/network/` ships four built-in declarative checks against the static NP graph:

- `np.default-deny-missing` — namespace has pods but no NP selecting them with empty ingress.
- `np.broad-cidr` — egress CIDR is `/0` or `/8` outside RFC1918.
- `np.unreachable-selector` — NP selector matches no pods in the namespace.
- `np.policy-without-targets` — NP has no `podSelector` matches.

Findings fire and clear in real time as Pods, NetworkPolicies, and Namespaces change.

### Misused service accounts (with `revoke-sa-token`)

The `revoke-sa-token` action (`internal/actions/revoketoken/`) deletes legacy SA-token Secrets, forcing token rotation. Combined with an audit rule that detects misuse, this provides a blunt-but-effective response.

## What Portal does **not** protect against

### Kernel-level runtime threats

Process exec into a container, syscalls outside an allowed set, file reads of sensitive paths, network packet observation — none of these are Portal's territory. Use **Tetragon** or **Falco**. Portal v2 will consume the K8s API audit log for a different class of runtime detection (long-lived exec sessions, secret reads via the API), but eBPF-level enforcement is not on the v1 or v2 roadmap. See `../adr/0004-no-mutation-no-ebpf.md`.

### Image content

No built-in container-image signature verification, no SBOM enforcement, no CVE scanning. Use **Kyverno** (sigstore-native), **Notary v2**, or a registry-side scanner (Trivy, Snyk). Portal can deny based on `object.spec.containers[*].image` patterns, but it cannot inspect the image itself.

### Resources that bypass the webhook

The webhook deliberately excludes `kube-system`, `kube-public`, `kube-node-lease`, and Portal's own install namespace (`internal/admission/server.go` — `DefaultExcludedNamespaces`). An attacker with `create` rights in any of those namespaces is not gated by Portal admission.

Equally, an operator with `patch` on a `Namespace` resource can set `portal.io/bypass=true` to bypass admission for that namespace's incoming requests. This requires a privileged RBAC role; bypass usage is audited (`portal_admission_bypass_total{namespace}` + slog warning).

### Pre-existing resources never updated

The audit loop sees what's currently in the cluster, but rules only get evaluated on informer events plus the 10-minute resync. A resource created before Portal was installed will be evaluated within ~10 minutes of Portal coming online, not instantly. If the resource is then never touched and Portal restarts (informer reset), it's re-evaluated again. In practice this is rarely a gap.

### Adversaries with full cluster-admin

An attacker who has `update` on `validatingwebhookconfigurations` can delete Portal's webhook. An attacker who can `delete` PortalClusterRules can disable enforcement. These are by design — Portal does not (and should not) self-protect against the cluster-admin role. Pair Portal with Kubernetes audit logging on those resources.

## Trust boundaries

- **Portal trusts its own kubeconfig / ServiceAccount.** Anything Portal can list/watch, Portal believes; rule authors writing rules against `cluster.<gvk>.*` should remember that the cluster lookups read from Portal's informer cache, not from a fresh API server query (`cluster.consistent.<gvk>.*` is the slower, stronger path).
- **Portal trusts rule authors.** A rule can write any expr-lang expression — including expressions that walk large object trees and consume CPU. The mitigations are operational: the admission timeout (`timeoutSeconds: 5`), the lookup cycle-protection budget (`portal_lookup_cycle_suppressed_total`), and per-rule rate limits on actions. There is no Rego-style data-flow proof; rules are trusted code, not sandboxed data.
- **The admission webhook is exposed only to the API server.** TLS with a CA pinned by `caBundle`; the chart's `NetworkPolicy` (if you ship one) restricts traffic to the apiserver subnet.
- **The metrics endpoint is unauthenticated.** Default port `:9090`; if your `Service` is `ClusterIP` and your network is segmented, you're fine. If you expose it via Ingress, add authentication at the Ingress layer.

For RBAC posture and the responsible-disclosure path, see `rbac-posture.md` and `responsible-disclosure.md`.
