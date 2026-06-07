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
<out>/tidert/runtime.go        // package tidert — runtime helpers
                               //  (Option/Result/Map/Set/Stack representations,
                               //  panic helper, channel wrappers, refEq)
<out>/bindings/<pkg>.go        // package bindings — generated stdlib
                               //  wrappers, one file per imported Go package
                               //  (fmt, os, strings, context, etc.)
<out>/go.mod                   // module declaration; toolchain `go 1.22`
```

Both helper directories use **plain Go package names**
(`tidert`, `bindings`) — leading `_` would make them invisible
to `go build` (Go convention). Collision protection is at the
**Tide source level**: user-source identifiers `tidert` and
`bindings` are reserved (see §Identifier encoding, E0107
applies). The full `<out>` location is set by the `tide build`
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

**Reserved user-source prefix.** To guarantee no collision
between user-source identifiers and codegen's `_tide_NN` fresh
names, the lexer rejects any user-source identifier whose
**first six characters** are `_tide_` (case-sensitive). Emits
**E0107 Reserved identifier prefix** — a hard error at lex time.
This is a paired edit with `grammar.ebnf` (lexical
`Ident` production); a user-source identifier starting with
`_tide_` is grammar-illegal.

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

`unit` values (the `()` literal) emit as `tidert.Unit` — a
package-level variable of type `struct{}` — so `unit`-typed
expressions are non-empty Go expressions.

## Container types — runtime representation

```go
// tidert/runtime.go

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

Method bodies for these types live in `tidert/runtime.go`. The
codegen pass calls them by Go-qualified name; e.g.,
`m.set(k, v)` in Tide IR lowers to `m.Set(k, v)` in Go (note
the capital — runtime methods are exported).

Empty-state semantics (per `builtins.md`):

- `Option.None`: `Option[T]{Tag: 0}` (no `V` — left at Go's zero
  value for `T`; codegen **never reads `V` when `Tag == 0`**, so
  the zero value is invisible to user code).
- `Some(x)`: `Option[T]{Tag: 1, V: x}`.
- `Result.Ok(v)`: `Result[T, E]{Tag: 0, V: v}` (`E` zero-valued
  and unread).
- `Result.Err(e)`: `Result[T, E]{Tag: 1, E: e}` (`V` zero-valued
  and unread).
- `Map.new()`: `&Map[K, V]{m: map[K]V{}, order: nil}`.
  Pointer receiver — methods mutate `order`.
- `Set.new()`: `&Set[T]{m: map[T]struct{}{}, order: nil}`.
- `Stack.new()`: `&Stack[T]{xs: nil}`. `Stack.pop()` returns
  `Result[T, error]` with the canonical empty-stack error
  (`tidert.NewError("empty stack")`).

**Tag/field invariants.** Codegen and the runtime helpers
guarantee:
- `Option.V` is read only when `Tag == 1`.
- `Result.V` is read only when `Tag == 0`.
- `Result.E` is read only when `Tag == 1`.

`MatchIR` lowering enforces this — the `case` for `Tag == n`
reads only the field associated with that tag. Pattern
desugaring in `desugaring.md` Stage 4 (`try`) and Stage 5
(`match`) preserves the invariant.

`tidert.NewError(msg string) error` is a thin wrapper around
`errors.New(msg)` from the Go stdlib; signature `func
NewError(msg string) error`. It exists so codegen can emit
short typed errors without import-rewriting the standard
`errors` package for every internal use.

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

## SelectStmt

A `select { … }` lowers to a Go `select` statement, one case per
arm (T-Select : unit):

```
case x = <-ch => B     ⟿   case x := <-ch: <lowering of B>
case <-ch => B         ⟿   case <-ch:      <lowering of B>   // drop
case ch.send(v) => B   ⟿   case ch <- v:   <lowering of B>
default => B           ⟿   default:        <lowering of B>
```

The `x :=` binding is emitted only for a named receive (dropped for
`<-ch` and for `_`). The recv channel operand reuses the `<-ch`
operator lowering above; the send case reuses `ch <- v`.

## TopContextExpr

```
TopContextExpr                          ⟿   context.Background()
```

`TopContextExpr` is the no-parent placeholder produced by
desugaring at the root `scope` call site when the source has
no explicit parent context. It lowers to a single Go
expression with no side effects.

## ScopeIR / SpawnIR

```
ScopeIR { group_name: g, ctx_name: ctx, parent: P, body: B,
          result_ty: Result<T, E> }
                                       ⟿
  (in Go, as an expression — wrapped in an immediate function:)

  func() tidert.Result[T, E] {
    eg, _ := tideNewGroup(<lowering of P; defaults to
                           context.Background()>)
    <lowering of B>                             // ends with a trailing
                                                //  tidert.Result[T, E]
                                                //  value or unit-Ok
    if err := eg.Wait(); err != nil {
        return tidert.Result[T, E]{Tag: 1, E: err.(E)}
    }
    return <the trailing-expression Ok-wrap>
  }()
```

**Inline group helper (no external dependency).** Generated modules
are stdlib-only — they carry no `errgroup` import — so the group is
the inline `tideGroup` helper, emitted into the prelude (conditional
on a `scope` appearing). It is built from `sync` + `context` and
replicates `errgroup.WithContext` semantics: the first spawned func
to return a non-nil error stores it (once) and cancels the derived
context; `Wait` blocks for every spawn and returns that error.
`tideNewGroup(parent)` returns `(*tideGroup, context.Context)`. Like
`tidert.Result` / the containers, the canonical home is
`tidert/runtime.go`; v1 emits it inline (the transitional state
Block R relocates).

`ScopeRef` (the `scope` identifier — value access to the scope's
context) is a v1 follow-up: the derived context is currently
discarded (`_`) and no `_scope` binding is emitted. When `ScopeRef`
lands, `scope.context` lowers to that bound context.

```
SpawnIR { parent_group: g, parent_ctx: ctx, body: B }
                                       ⟿
  g.Go(func() error {
    <lowering of B, with the body's Result<unit, E> returns
     converted to the func's `error` return:
       return Ok(_)   ⟿  return nil
       return Err(e)  ⟿  return e          // E = error, no assertion
       return <other Result expr r>
                      ⟿  if r.Tag == 1 { return r.E }; return nil>
    return nil                              // fall-through (body has no
  })                                        //  trailing return)
```

A spawn body in the corpus ends in an explicit `return Ok(())` /
`return Err(e)`, so the conversion is applied per-return; the
trailing `return nil` is emitted only when the body falls through
without a return.

The asserted-`E` (`res.E.(error)`) is safe because **v1
restricts `scope<T, E>` to `E = error`**. Any other `E` is
rejected by sema with **E0407 `scope` error parameter must be
`error` in v1** (paired with `type-system.md`; the relaxation
to arbitrary `E` is parked until a typed-error adapter lands
in the runtime). Every example in the corpus uses
`scope<T, error>`, so v1 is unaffected. Codegen relies on this
restriction — it never sees `E != error`.

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

### `match` in value position

A `match` whose result is consumed (LHS of an assignment, RHS of
a `let`/`var`, argument of a call) lowers to a Go IIFE:

```
let r = match subject { p_1 => e_1, ..., p_n => e_n }
                                       ⟿
  r := func() T {
    switch subject(.Tag)? {
      case <head-1>: return e_1
      ...
      case <head-n>: return e_n
    }
    var __zero T; return __zero
  }()
```

`T` is the unified type of the arm bodies per `T-Match`. The
trailing zero-value return is unreachable when the match is
exhaustive but required by Go's reachability checker for any
switch without a `default:`. Payload-binding patterns in
value-position match aren't supported in v1 — a value-position
match with payload bindings must be rewritten to a
statement-position match that assigns to a `var` from each arm.

**Variant-tag numbering.** For built-in sum types the tag ints
are fixed by `builtins.md`: `None = 0`, `Some = 1`, `Ok = 0`,
`Err = 1`. For user-defined sum types the tag is the
**declaration order** of the variant in the `TypeDecl` (first
variant = 0, second = 1, …). The runtime never persists
tags across runs, so re-ordering variants is a source-level
change with no runtime stability concerns.

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
func boxNew[T any](v T) *Box[T] { return &Box[T]{v: v} }
```

Static methods lower to package-level functions named
`<class>` + capitalised method name (`boxNew`, `mapFrom`, …),
preserving the lower-case visibility convention for v1.

Type parameters lower with constraint `any` **by default**, with
one v1 exception: **constraint propagation from container key
positions**. If a generic type parameter `K` of a user decl
flows into a `Map<K, _>` key or `Set<K>` element position
(transitively, in any field or signature reachable from the
decl), codegen lowers `K` with constraint `comparable` instead
of `any`. This matches the runtime's `Map[K comparable, V any]`
and `Set[T comparable]` requirement; without it, `class
Indexer<K, V> { var m: Map<K, V> ... }` would fail Go's
type-check.

Algorithm sketch (run before lowering each user decl):

```
collect_kvars(decl):
  let kvars = ∅
  for each type expr T mentioned in decl's fields / sigs:
    if T is `Map<X, _>` or `Set<X>` and X is a type parameter:
      add X to kvars
  return kvars

constraint(α) =
  if α ∈ collect_kvars(decl): "comparable"
  else: "any"
```

For non-container constraints (e.g., `Ord`, `Stringer`), D11
parks the surface; v1 has no other constraints, so `any` /
`comparable` is the complete v1 lowering set.

For function calls that fully specify type arguments
explicitly, codegen emits the explicit form `f[T1, T2](...)`;
when type-arg inference held (per `type-system.md` unify), the
inferred substitution is used and codegen emits the explicit
form anyway (Go infers from arguments separately; the explicit
form is always safe).

## Bindings — Go stdlib

For each imported Go package (`fmt`, `os`, `strings`, ...),
the binding generator emits an `bindings/<pkg>.go` file that
re-exports the package's public API with Tide-shaped
signatures. The transformation rules:

- A Go function `func F(a A, b B) (R, error)` becomes a Tide
  function returning `Result<R, error>` — the runtime helper
  is a one-line adapter that constructs the `Result`.
- A Go function `func F(...) error` (single `error` return,
  no `R`) becomes Tide `Result<unit, error>`.
- A Go function `func F(...) R` (no error) becomes Tide
  `R`-returning.
- A Go function `func F(...) (R, bool)` (comma-ok shape)
  becomes Tide `Option<R>`.
- A Go function `func F(...)` (no return) becomes Tide
  `unit`-returning.
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
sub-expression mapping is **not** v1; the directive form is
canonical `//line file.td:NN:1` (line = source span's start
line, column = 1 unconditionally — Go accepts this form per
[Go spec — Source file organisation](https://go.dev/ref/spec#Source_file_organization)).

## Output formatting

Codegen emits Go that is **gofmt-stable**: piping the output
through `gofmt -s` returns it unchanged. The reason is
`test-contract.md` §`--- GO ---` — fixtures store the
post-`gofmt -s` form, so codegen and fixture must agree
byte-for-byte. The contract is therefore stronger than "go
build accepts it": **the emitted source must round-trip
through `gofmt -s` to itself**.

In practice codegen emits canonical formatting (one statement
per line, tab indent, single trailing newline, alphabetised
import groups standard / third-party / project) and runs the
buffer through `gofmt -s` as the last step before writing the
file. Production builds may skip the explicit `gofmt -s` call
if the canonicaliser already produces gofmt-stable output;
fixture comparison always re-runs `gofmt -s` to guard against
drift.

This is the *only* hand-readability concession in lowering — it
exists to keep fixtures deterministic, not because anyone is
supposed to read generated Go (D1).

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
