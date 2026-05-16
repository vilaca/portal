# Writing a custom ExpressionEngine

`api.ExpressionEngine` is the seam Portal uses to swap rule languages. `expr-lang` is the v1 default; CEL, Rego, or starlark can be added behind the same rule schema. This page covers adding one.

## The interface

```go
// internal/api/engine.go
type ExpressionEngine interface {
    Name() string
    Compile(expression string) (Program, error)
}

type Program interface {
    Eval(ctx Context) (bool, error)
}
```

- **`Name()`** — `"expr"` for `expr-lang`, `"cel"` for CEL, etc. Used in metrics labels and (in a future v3) in a rule-level engine selector.
- **`Compile()`** — parses one rule expression. Diagnostics in the returned error should include line/column when the underlying engine provides them — they end up in `PortalClusterRule.status.parseError` and are read by humans.
- **`Program.Eval()`** — runs the compiled expression against an evaluation `Context`. Must be safe for concurrent calls (Portal evaluates the same `Program` from many goroutines).

## Registration

Engines self-register at `init()` time:

```go
package myengine

import "github.com/vilaca/portal/internal/api"

func init() {
    api.RegisterEngine("myengine", func() api.ExpressionEngine { return New() })
}
```

The composition root reads `api.Engine("myengine")` when the rule index includes a rule whose engine selector matches (engine selection is per-cluster in v1; per-rule in v3).

## What the engine sees in `Context`

```go
// internal/api/context.go
type Context struct {
    GVK     schema.GroupVersionKind
    Object  *unstructured.Unstructured
    Env     map[string]any
    Request *AdmissionRequest
}
```

The **`Env`** map is the canonical evaluation environment. Top-level keys for pod-shaped contexts:

- `container` — current container (rule is re-evaluated per std / init / ephemeral container).
- `spec` — `object.spec` with the pod-sugar surface.
- `securityContext` — pod-level `securityContext`.
- `metadata` — `name`, `namespace`, `labels`, `annotations`.
- `object` — the full `*unstructured.Unstructured` as nested maps (universal escape hatch).
- `request` — admission only (`operation`, `dryRun`, `userInfo`).
- `cluster` — the lookup helpers (`cluster.<gvk>.byName(ns,name)`, `cluster.<gvk>.list(ns,selector)`).

Non-pod GVKs receive only `object`, `metadata`, `request`, and `cluster`.

**An alternate engine must produce something compatible.** That is: the engine's `Eval()` will be called with the same `Context` shape, and rule expressions written for one engine should consume the same surface from the other. If the alternate engine cannot represent (say) `cluster.<gvk>.list(...)` natively, it must adapt — Portal does not maintain per-engine env conversion.

## Example skeleton — a hypothetical CEL adapter

```go
package cel

import (
    "fmt"
    "github.com/google/cel-go/cel"
    "github.com/google/cel-go/checker/decls"

    "github.com/vilaca/portal/internal/api"
)

func init() {
    api.RegisterEngine("cel", func() api.ExpressionEngine { return New() })
}

type engine struct {
    env *cel.Env
}

func New() api.ExpressionEngine {
    env, _ := cel.NewEnv(
        cel.Declarations(
            decls.NewVar("object", decls.NewMapType(decls.String, decls.Dyn)),
            decls.NewVar("container", decls.NewMapType(decls.String, decls.Dyn)),
            decls.NewVar("spec", decls.NewMapType(decls.String, decls.Dyn)),
            // ...etc...
        ),
    )
    return &engine{env: env}
}

func (e *engine) Name() string { return "cel" }

func (e *engine) Compile(src string) (api.Program, error) {
    ast, iss := e.env.Compile(src)
    if iss != nil && iss.Err() != nil {
        return nil, fmt.Errorf("cel compile: %w", iss.Err())
    }
    prg, err := e.env.Program(ast)
    if err != nil {
        return nil, fmt.Errorf("cel program: %w", err)
    }
    return &program{prg: prg}, nil
}

type program struct{ prg cel.Program }

func (p *program) Eval(ctx api.Context) (bool, error) {
    out, _, err := p.prg.Eval(ctx.Env)
    if err != nil {
        return false, err
    }
    return out.Value().(bool), nil
}
```

## Caveats

- **Rule schema is engine-agnostic.** A `PortalClusterRule.spec.rule` is a string. Switching the engine for a cluster means re-evaluating every rule against the new engine — the rule's `.status.parseError` will populate for incompatible expressions. Plan engine swaps with care.
- **Per-rule engine selector is v3.** In v1 the engine is selected by the composition root (which one is `api.RegisterEngine`'d first), not by the rule. The interface seam is already there; the YAML field to select it is not.
- **The `cluster.<gvk>.*` helpers expect map-of-functions shape.** Alternate engines must adapt — for example, CEL doesn't natively support arbitrary function calls in map navigation; you'll need to register `cluster_byName(gvk, ns, name)` etc. as CEL functions and translate the rule syntax. expr-lang gets these for free.
- **Performance matters.** Portal's admission p99 target is 20 ms. CEL is generally fast; Rego is slower for rule sets that aren't pre-compiled to ASTs.

For the canonical reference of the env shape see `../concepts/context-and-pod-sugar.md` (parallel author) and the implementation in `internal/context/pod/builder.go`.
