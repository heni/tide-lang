# Lowering — Tide IR → Go

The contract for codegen: how the post-desugaring Tide IR
(`desugaring.md`) becomes Go source. The output is Go that
`go build` accepts; it is **not** a human-reading goal —
generated Go is an intermediate representation (D1, hard
constraint).

This file is the contract for the lowering pass that runs after
desugaring and produces `.go` files in the output tree.

**Authority.** This file is the contract. Cross-refs to
`desugaring.md` (input IR), `builtins.md` (semantic
signatures of built-in operations), `type-system.md` (typing
guarantees codegen relies on), and `test-contract.md` (the
canonical `GO` / `STDOUT` / `EXIT` sections in fixtures).

## Output tree shape

For an input package consisting of `.td` files in a single
directory, codegen emits a sibling directory with:

```
<out>/main.go                  // generated user package
<out>/_tidert/runtime.go       // runtime helpers (Option/Result/Map/Set/Stack
                               //  representations, panic helper, channel
                               //  wrappers, refEq)
<out>/_bindings/<pkg>.go       // generated stdlib wrappers, one file per
                               //  imported Go package (fmt, os, strings,
                               //  context, etc.)
<out>/go.mod                   // module declaration; pinned Go toolchain
```

The runtime helpers and bindings live under names starting with
`_` to be unambiguous: user code in Tide cannot produce names
starting with `_` followed by lowercase (D17 / lexical rule).
The full mapping `<out>` location is set by the `tide build`
CLI; this file fixes only the relative layout.

## Identifier encoding

```
Tide identifier              Go identifier
─────────────────────────────────────────────────────
foo                          foo                       (no change)
fooBar                       fooBar
$tide_NN                     _tide_NN                  (fresh locals — see desugaring.md)
goReservedWord (e.g. type)   tide_type, tide_func, …   (`tide_` prefix to escape)
camelCase                    camelCase
SnakeCase                    SnakeCase
```

The Go reserved-word list as of Go 1.22 is hard-coded into the
codegen pass: `break case chan const continue default defer
else fallthrough for func go goto if import interface map
package range return select struct switch type var`. Any Tide
identifier matching this list gets a `tide_` prefix at every
use site in generated Go.

Exported visibility — Tide has no `pub` qualifier; every
top-level decl is package-visible. Codegen capitalises the first
letter of top-level declarations when they need to be visible
from a sibling Go package (cross-file imports inside a Tide
project), and lower-cases otherwise. Since v1 has single-package
projects, all decls stay lower-cased; the algorithm is here for
future expansion.

## Primitive type lowering

| Tide | Go |
|---|---|
| `bool` | `bool` |
| `int` | `int` |
| `int8`..`int64` | `int8`..`int64` |
| `uint`..`uint64` | `uint`..`uint64` |
| `byte` | `byte` (= `uint8`) |
| `rune` | `rune` (= `int32`) |
| `float32`, `float64` | `float32`, `float64` |
| `string` | `string` |
| `unit` | `struct{}` (the zero-byte type) |
| `Never` | `struct{}` (no value ever flows; codegen errors if encountered) |
| `Any` | `interface{}` (also written `any` in Go 1.18+) |

`unit` values (the `()` literal) emit as `_tidert.Unit` — a
package-level variable of type `struct{}` — so `unit`-typed
expressions are non-empty Go expressions.

## Container types — runtime representation

```go
// _tidert/runtime.go

type Option[T any] struct {
    Tag uint8                 // 0 = None, 1 = Some
    V   T                     // valid only when Tag == 1
}

type Result[T any, E any] struct {
    Tag uint8                 // 0 = Ok, 1 = Err
    V   T                     // valid only when Tag == 0
    E   E                     // valid only when Tag == 1
}

type Map[K comparable, V any] struct {
    m     map[K]V
    order []K                 // insertion order; appended on first .Set per K
}

type Set[T comparable] struct {
    m     map[T]struct{}
    order []T
}

type Stack[T any] struct {
    xs []T
}
```

Method bodies for these types live in `_tidert/runtime.go`. The
codegen pass calls them by Go-qualified name; e.g.,
`m.set(k, v)` in Tide IR lowers to `m.Set(k, v)` in Go (note
the capital — runtime methods are exported).

Empty-state semantics (per `builtins.md`):

- `Option.None`: `Option[T]{Tag: 0}` (no `V`); `Some(x)`:
  `Option[T]{Tag: 1, V: x}`.
- `Result.Ok(v)`: `Result[T, E]{Tag: 0, V: v}`;
  `Result.Err(e)`: `Result[T, E]{Tag: 1, E: e}`.
- `Map.new()`: `&Map[K, V]{m: map[K]V{}, order: nil}`.
  Pointer receiver — methods mutate `order`.
- `Set.new()`: `&Set[T]{m: map[T]struct{}{}, order: nil}`.
- `Stack.new()`: `&Stack[T]{xs: nil}`. `Stack.pop()` returns
  `Result[T, error]` with the canonical empty-stack error
  (`_tidert.NewError("empty stack")`).

## Channel lowering

```
Channel<T>      → chan T              (bidirectional)
SendChan<T>     → chan<- T            (send-only)
RecvChan<T>     → <-chan T            (recv-only)
makeChannel<T>(cap)   → make(chan T, cap)         (cap = 0 if absent)
```

`ch.send(v)` → `ch <- v`. `ch.recv()` → `<-ch`. `ch.tryRecv()`
→ a select with a default case:

```go
func tryRecv[T any](ch <-chan T) tidert.Option[T] {
    select {
    case v := <-ch:
        return tidert.Option[T]{Tag: 1, V: v}
    default:
        return tidert.Option[T]{Tag: 0}
    }
}
```

`ch.close()` → `close(ch)`. The widening of `Channel<T>` to
`SendChan<T>` / `RecvChan<T>` at argument sites is handled by
Go's own conversion rules — `chan T` is assignable to `chan<-
T` and `<-chan T` implicitly. No runtime cost.

## ScopeIR / SpawnIR

```
ScopeIR { group_name: g, ctx_name: ctx, parent: P, body: B,
          result_ty: Result<T, E> }
                                       ⟿
  (in Go, as an expression — wrapped in an immediate function:)

  func() tidert.Result[T, E] {
    parentCtx := <lowering of P; defaults to context.Background()>
    eg, ctx := errgroup.WithContext(parentCtx)
    _ = ctx                                     // bound to scope.context
    var _scope = _scopeBinding{g: eg, ctx: ctx} // for ScopeRef in B
    _ = _scope
    <lowering of B>                             // ends with a trailing
                                                //  tidert.Result[T, E]
                                                //  value or unit-Ok
    if err := eg.Wait(); err != nil {
        return tidert.Result[T, E]{Tag: 1, E: err.(E)}
    }
    return <the trailing-expression Ok-wrap>
  }()
```

`ScopeRef` (the `scope` identifier) lowers to `_scope`. Field
access `scope.context` lowers to `_scope.ctx`.

```
SpawnIR { parent_group: g, parent_ctx: ctx, body: B }
                                       ⟿
  g.Go(func() error {
    <lowering of B>                             // ends with a trailing
                                                //  tidert.Result[unit, E]
    res := <the trailing wrap>
    if res.Tag == 1 {                           // Err
      return res.E.(error)                      // E was constrained to error
                                                //  by T-Spawn / G11
    }
    return nil
  })
```

The asserted-`E` (`res.E.(error)`) is safe because sema's
T-Spawn checks `E_spawn = E_outer`, and the surrounding scope's
`E` parameter must satisfy the errgroup signature (returns
`error`). When `E != error`, codegen will have rejected at the
scope's `Wait()` site — currently every v1 corpus example uses
`E = error`, so this is the only supported shape. The runtime
helpers carry a typed-error adapter if needed in the future.

## MatchIR

A `MatchIR` lowers to a Go `switch` statement; each `BranchIR`
becomes a `case`. The shape varies by `BranchIR.tag`:

```
BranchIR { tag: VariantTag(V), payload_binds: [b_1, ..., b_n], body: E }
                                       ⟿
  case <subject>.Tag == <tag-int-for-V>:
    b_1 := <subject>.<payload-field-for-1>
    ...
    b_n := <subject>.<payload-field-for-n>
    <lowering of E>

BranchIR { tag: LiteralValue(L), payload_binds: [], body: E }
                                       ⟿
  case <subject> == <L's Go literal>:
    <lowering of E>
```

When all branches share the same head (`==` for primitives, or
all `Tag == N` for variants), codegen prefers a `switch` with
multiple `case` arms over a chain of `if`. For an
`UnreachableIR` leaf, codegen emits
`panic("unreachable: non-exhaustive match")`.

## Implicit receiver / Field

`Field { receiver: This{type: C}, name: n }` lowers to the Go
expression `t.n`, where `t` is the receiver name chosen for the
generated Go method (codegen uses `t` consistently for clarity;
not exposed in Tide).

Generic class methods carry their type parameters as Go-side
type params; the receiver is a pointer for any class with
`var`-modified fields (mutation visible across calls); a value
receiver otherwise. For v1 every class uses a pointer receiver
unconditionally — keeps the lowering uniform; the few
pure-value classes pay an unnoticeable indirection cost.

## Slice methods

```
s.len()           → len(s)
s.push(e)         → append(s, e)              (returns the new slice)
s.copy()          → cloned := make([]T, len(s)); copy(cloned, s); cloned
s[i]              → s[i]                       (panics on out-of-bounds,
                                                Go semantics)
s[lo:hi]          → s[lo:hi]
```

Slice index-write `s[i] = v` lowers to `s[i] = v` directly.

## Defer / panic / refEq

```
defer call(args...)   →  defer call(args...)
panic(msg)            →  panic(msg)
refEq(a, b)           →  a == b                (Go interface / pointer
                                                identity; sema has
                                                guaranteed C_a = C_b
                                                via T-RefEq)
```

`panic` always reaches Go's runtime panic mechanism — there is
no Tide-level recover (D7 / cut). Bound stdlib calls that may
panic at the Go level propagate naturally.

## For-loops

```
ForRangeIR { iter: s : []T, bind: x, body: B, indexed: false }
                                       ⟿
  for _, x := range s {                       // _ discards the index
    <lowering of B>
  }

ForRangeIR { iter: s : []T, bind: (i, x), body: B, indexed: true }
                                       ⟿
  for i, x := range s {
    <lowering of B>
  }

ForRangeIR { iter: IntRange{lo, hi, inclusive: false} }
                                       ⟿
  for i := <lowering of lo>; i < <lowering of hi>; i++ {
    <lowering of B>
  }

ForRangeIR { ... inclusive: true }
                                       ⟿
  for i := <lowering of lo>; i <= <lowering of hi>; i++ {
    <lowering of B>
  }

ForRangeIR { iter: s : string, str_runes: true }
                                       ⟿
  for _, r := range s {                       // Go's `range string` yields runes
    <lowering of B>
  }
```

```
ForMapIR { iter: m : Map<K,V>, bind: (k, v), body: B }
                                       ⟿
  for _, k := range m.Order() {               // m.Order() returns []K
                                              //  in insertion order
    v := m.Get(k).V                           // Map.Get → Option, .V is the
                                              //  value (always Some here
                                              //  because we just read a
                                              //  known key)
    <lowering of B>
  }

ForSetIR { iter: s : Set<T>, bind: x, body: B }
                                       ⟿
  for _, x := range s.Order() {               // insertion order
    <lowering of B>
  }

ForChanIR { iter: ch : RecvChan<T>, bind: x, body: B }
                                       ⟿
  for x := range ch {                         // exits on close
    <lowering of B>
  }
```

The `Order()` accessor on `Map` and `Set` is in the runtime
package; it exposes the insertion-order slice.

## Generics

Tide generics lower to Go generics one-to-one:

```
class Box<T> { var v: T; static new(v: T): Box<T> { ... } }
                                       ⟿
type Box[T any] struct { v T }
func (b *Box[T]) ... { ... }
func NewBox[T any](v T) *Box[T] { return &Box[T]{v: v} }
```

Type parameters are passed through as Go type parameters with
constraint `any`. Constrained generics (D11, parked for v2) will
need a constraint-set lowering; v1 uses `any` everywhere.

For function calls that fully specify type arguments
explicitly, codegen emits the explicit form `f[T1, T2](...)`;
when type-arg inference held (per `type-system.md` unify), the
inferred substitution is used and codegen emits the explicit
form anyway (Go infers from arguments separately; the explicit
form is always safe).

## Bindings — Go stdlib

For each imported Go package (`fmt`, `os`, `strings`, ...),
the binding generator emits an `_bindings/<pkg>.go` file that
re-exports the package's public API with Tide-shaped
signatures. The transformation rules:

- A Go function `func F(a A, b B) (R, error)` becomes a Tide
  function returning `Result<R, error>` — the runtime helper
  is a one-line adapter that constructs the `Result`.
- A Go function `func F(...) R` (no error) becomes Tide
  `R`-returning.
- A Go function `func F(...) (R, bool)` (comma-ok shape)
  becomes Tide `Option<R>`.
- Go types pass through unchanged where possible. Go-only
  receiver methods are re-exposed as Tide methods on the same
  type.
- Variadic Go parameters (`...T`) become Tide `...T`.

The full binding-surface spec is in
`../docs/binding-surface.md`; this lowering chapter only
concerns the **codegen pass that consumes** those bindings.

## Source maps (`//line` directives)

Every emitted Go statement that originates from a `.td` source
position carries a `//line file.td:NN` directive immediately
above it. The runtime's panic stack traces, `go test -run`
failures, and `go vet` diagnostics will then point at the
original `.td` coordinates — required for D10.

Conservative rule: `//line` is emitted at the *outermost* Go
statement boundary for each source construct. Fine-grained
sub-expression mapping is **not** v1; the column is set to 1
unconditionally; the line is the source span's start line.

## Output formatting

Generated Go is **not** `gofmt`'d. Tide's codegen produces
reasonably-formatted Go (indent by tab, one statement per line,
trailing newline) but the contract is "`go build` accepts it",
not "humans read it". Running `gofmt` on the output is
harmless but not required.

## Errors — quick index

Codegen runs after all sema and desugaring checks. The only
diagnostics it raises are internal-consistency failures:

- **E0801** Internal: encountered `TryExpr` / `MatchExpr` /
  `ShortClosure` / un-typed `VariantExpr` in the IR (one of
  the desugaring stages was skipped).
- **E0802** Internal: encountered `Never`-typed value in a
  position requiring a concrete Go type (sema should have
  caught divergence-flow earlier).
- **E0803** Internal: type-arg substitution failed
  (well-formedness was violated).

Each E08xx is a bug-class — they should never reach the user
under correct sema+desugar; the compiler reports them with the
internal-error formatting.
