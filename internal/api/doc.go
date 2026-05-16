// Package api defines the load-bearing interfaces and DTOs that Portal modules
// communicate through. It is the only package depended on by every other internal
// package; nothing in api may import from internal/admission, internal/audit,
// internal/network, internal/actions, internal/engine, internal/expr, internal/sink,
// internal/lookup, or internal/rule.
//
// The only external dependencies allowed are k8s.io/apimachinery (for GVK and
// Unstructured, which travel through Context) and Go's standard library.
package api
