# Concepts

Mental model for how Portal evaluates rules across admission, audit, and network layers.

- [Architecture](architecture.md) — layer model and component diagram.
- [Admission vs audit](admission-vs-audit.md) — synchronous denial vs informer-driven review.
- [Cross-resource lookups](cross-resource.md) — referencing related objects from a rule.
- [Context and pod sugar](context-and-pod-sugar.md) — the evaluation context and pod-specific shortcuts.
- [Actions and rate limiting](actions-and-rate-limiting.md) — what happens after a match, and the rate-limit semantics.
- [Fail-closed](fail-closed.md) — what Portal does when the engine itself is unhealthy.
