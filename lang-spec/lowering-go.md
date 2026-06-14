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

## Record / struct field lowering

A nominal record (`type X = { f: T }`) and a class lower to a named
Go `struct`. Each Tide field `f` lowers to an **exported** Go field
(`exportFieldName`: first letter capitalised; a leading non-letter
gets an `X` prefix) carrying a `` `json:"f"` `` tag that pins the JSON
key to the verbatim Tide name:

```
type Config struct {
    Host string `json:"host"`
    Port int    `json:"port"`
}
```

This is **independent** of the top-level exported-visibility algorithm
above (which governs type/func *decl* names and stays lower-cased in
single-package v1). Struct fields export *unconditionally*: Go's
`encoding/json` reflects from outside package main, so an unexported
field is invisible to marshal/unmarshal — exporting is what makes JSON
round-trip work. The `json` tag keeps field-name == JSON-key
(binding-surface.md §encoding/json) so the capitalised Go spelling is
invisible at the Tide-source and wire levels.

Every field *site* follows the same spelling: the struct decl, the
record/class brace literal (`Config{Host: …}`), value-position field
access (`cfg.Host`), the implicit-receiver bare field (`this.x` →
`t.X`), and the reflection field accessors. **Method** selectors are
*not* exported — they keep their lowercase Go spelling (reachable
within package main), so field-access and method-call lowering use
distinct spelling functions (`goFieldName` vs `goMethodName`). The
package-namespace exemption (`os.args` → `os.Args` keeps its binding
rename, not the export path) is gated on the receiver's sema symbol
being a builtin module, not on its spelling — a local value that
shadows a package name still exports its fields.

A func-typed *data field* may itself be called (`handler.fn(x)`); the
call site spells it with the exported **field** form (`handler.Fn(x)`),
not the method form — the two lowering paths are kept distinct so this
does not collapse to a method-name spelling.

**Known limitation — non-injective export.** `exportFieldName` is not
injective: two field names differing only in first letter case
(`port` / `Port`) both map to `Port`, and `_x` / `X_x` both map to
`X_x`. Such a record produces a duplicate Go struct field — a *loud*
`go build` failure, never a silent miscompile. v1 leaves this
unguarded (no corpus shape hits it); a sema diagnostic on colliding
exported forms is the eventual fix.

Tuple fields keep their positional unexported spelling (`_0`, `_1`):
they are the anonymous-struct tuple representation, not nominal record
fields, and no v1 program serialises a tuple to JSON.

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

## TopLevelLet

A module-level `let Name [: T] = Value` (ast.md §TopLevelLet) lowers
to a Go **package-level `var`**:

```
let version = 5            ⟿  var version = 5
let label: string = "tide" ⟿  var label string = "tide"
```

The annotation, when present, becomes the Go var's declared type and
seeds the initialiser's expected type (so a predeclared
`Result`/`Option` constructor gets its explicit type args — same path
as a body-level `let`). Emission is source-order; Go resolves
package-var initialisation order itself, so a constant may reference a
function or another top-level constant declared later. `var` is not a
legal top-level form (grammar.ebnf §TopLevelLet); module-scope mutable
state is a singleton class instead.

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

**Option ⇄ JSON.** When a program uses both `Option` and an
`encoding/json` binding, the generated `Option[T]` carries
`MarshalJSON`/`UnmarshalJSON`: `None` ⇄ JSON `null`, `Some(v)` ⇄ the
JSON of `v` directly. So an Option-typed record field round-trips with
*bare* JSON (`"file": "app.log"` / `"file": null` ⇄ `Some` / `None`)
rather than exposing the internal `{Tag, V}` struct shape. The methods
are emitted only under that combined condition — a program with Option
but no json needs neither them nor the `encoding/json` import. (`Result`
has no JSON methods yet — no v1 program serialises a `Result` field;
added by analogy when one does.)

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

**Container brace literals** (`Set<int>{1,2}`, `Map<K,V>{}`) lower to
the same constructors, so the brace form and the `.new()` / `.from()`
form share one Go representation:

- `Set<T>{}` → `setNew[T]()`; `Set<T>{e1,…}` →
  `setFrom([]T{e1,…})` (Go infers `setFrom`'s `T` from the slice
  literal).
- `Map<K,V>{}` → `mapNew[K,V]()`. A non-empty `Map<K,V>{ k: v, … }`
  lowers to an insertion IIFE —
  `func() *Map[K,V] { m := mapNew[K,V](); m.set(k, v); …; return m }()`
  — keeping the literal a single Go expression.
- `Stack<T>{}` → `stackNew[T]()`. A `Stack` literal is always empty
  (`ast.md §BraceLit`); entries are a sema error.

**Constructor type-argument stamping.** A constructor call
(`Ok`/`Err`/`Some`/`None`) constrains only the type parameter its
argument supplies — `Ok(v)` fixes `T` but leaves `E` open, `Err(e)`
the reverse, `None()` leaves `T` wholly open. Go infers a generic
function's type parameters from its *arguments only*, never from the
assignment LHS or the `return` target, so the open parameter would
make `go build` fail (`cannot infer E`). Codegen therefore stamps
**explicit** Go type arguments on the constructor from the *expected
type* in scope — the enclosing function's declared return type at a
`return`, or the annotation at a typed `let`/`var` — e.g. a `return
Ok(v)` in a `Result<int, error>`-returning function lowers to
`ResultOk[int, error](v)`, and `let x: Option<int> = None()` to
`OptionNone[int]()`. When no expected type is in scope (e.g. an
un-annotated `let`, or a constructor nested as a call argument), no
stamp is applied and Go's argument inference stands; this leaves a
nested `Ok(Ok(n))` and `wrap(Ok(x))` unstampable in v1 (the
expected type does not propagate through the inner positions).

**Tag/field invariants.** Codegen and the runtime helpers
guarantee:
- `Option.V` is read only when `Tag == 1`.
- `Result.V` is read only when `Tag == 0`.
- `Result.E` is read only when `Tag == 1`.

`MatchIR` lowering enforces this — the `case` for `Tag == n`
reads only the field associated with that tag. Pattern
desugaring in `desugaring.md` Stage 4 (`try`) and Stage 5
(`match`) preserves the invariant.

**`try` lowering (preamble specialisation).** Although
`desugaring.md` Stage 4 models `try e` as a `match` rewrite,
codegen specialises the two well-known shapes (Result / Option)
to an inline *early-return preamble* rather than a full match —
semantically equivalent, smaller output:

```
__tide_try_N := e
if __tide_try_N.Tag == <bail> {        // 1 = Err (Result), 0 = None (Option)
	return <wrapped bail of the enclosing return type>
}
// the value of `try e` is __tide_try_N.V
```

In **expression position** (`f(try e)`, `a + try e`) the preamble
cannot sit inline, so it is **hoisted** to precede the enclosing
statement; the `try` node itself lowers to `__tide_try_N.V`.

Hoisting is only applied when it preserves observable evaluation
order. Lifting a `try`'s early-return ahead of the surrounding
expression would defer (or, on bail, skip) any *side-effecting
expression evaluated before it* — so a `try` is hoisted only when
every expression preceding it in its frame is pure; otherwise the
`try` is left in place and rejected (lift it to a `let`/`var`/
`return` binding). Two adjacent tries are always safe — both move
out, in order — which is the common shape (`f(try a(), try b())`).
The walk also stops at any construct introducing a new return
frame (closure, value-position `match`/`if`/block, `scope`/`spawn`)
— a `try` there belongs to that frame — and does not descend the
right operand of `&&`/`||` (conditional evaluation; an
unconditional preamble would change short-circuit semantics).

`tidert.NewError(msg string) error` is a thin wrapper around
`errors.New(msg)` from the Go stdlib; signature `func
NewError(msg string) error`. It exists so codegen can emit
short typed errors without import-rewriting the standard
`errors` package for every internal use.

The user-level `error(msg): error` free constructor
(`builtins.md` §error) lowers directly to `errors.New(msg)`,
pulling in Go's `errors` import on demand (the v1 prelude is
emitted inline, so there is no `tidert` package to route
through). It is recognised by the bare-`error` identifier callee
with exactly one argument — the `error(): string` interface
method takes none, and `.error()` calls are receiver-qualified,
so the form is unambiguous.

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
value-position match aren't supported in this IIFE form — but a
match in **tail position** (the trailing expression of a
value-returning body) does support them, lowering as a
statement `switch` whose arms `return` (see §Implicit tail
return below).

**Variant-tag numbering.** For built-in sum types the tag ints
are fixed by `builtins.md`: `None = 0`, `Some = 1`, `Ok = 0`,
`Err = 1`. For user-defined sum types the tag is the
**declaration order** of the variant in the `TypeDecl` (first
variant = 0, second = 1, …). The runtime never persists
tags across runs, so re-ordering variants is a source-level
change with no runtime stability concerns.

**Recursive sum types.** A payload field whose declared type
directly names the enclosing sum (`Node(left: Tree, right: Tree)`,
or `Tree<T>` for the generic form) would make the lowered Go struct
infinitely sized — Go forbids a struct that contains itself by
value. Such a field is **pointer-ized**: the struct field becomes
`*Tree` (resp. `*Tree[T]`), the constructor stores the address of
its by-value parameter (`NodeLeft: &left`), and the match-binding
dereferences (`l := *subject.NodeLeft`) so the bound name keeps the
sum's value type. Tide sum values are immutable, so the introduced
sharing is unobservable. Only the **direct** self-reference is
detected; recursion routed through a slice / map / channel
(`[]Tree`, `Map<K, Tree>`) is already an indirection in Go and is
left as-is, while by-value recursion nested inside another generic
(`Option<Tree>`) is a v1 limitation.

## Implicit tail return

A function / method / closure body is a block, and a block's
value is its trailing expression (the block-as-expression value
rule; `type-system.md` §T-Block). When the body's declared
result is a value (return type ≠ `unit`), the trailing
expression is an **implicit return** — codegen emits it in
*tail position* rather than discarding it:

```
func f(...): R {                func f(...) R {
  <stmts>                          <stmts>
  <trailing-expr>      ⟿           <trailing-expr in tail position>
}                               }
```

Tail position **distributes** the `return` into the leaves of a
trailing `match` / `if` / block rather than wrapping the whole
body in a value-position IIFE (§match in value position):

- **plain value `e`** ⟿ `return e`;
- **`match`** ⟿ the statement `switch` (so payload-binding arms
  lower cleanly, unlike the IIFE form), each arm body emitted in
  tail position. A trailing `panic("unreachable: non-exhaustive
  match")` is emitted after a `switch` with no `default:`, since
  such a `switch` is not a terminating statement in Go even when
  the match is exhaustive (Go would otherwise report "missing
  return");
- **`if`** ⟿ the statement `if`, each branch's trailing in tail
  position; both branches are required (an else-less value `if`
  has no value on the else path);
- **block** ⟿ its statements, then its trailing in tail position;
- a **diverging** trailing (`return` / `break` / `continue` /
  `os.exit`) already terminates control and is emitted as-is,
  with no `return` wrapper.

The declared return type is in scope at every leaf, so a leaf
`Result` / `Option` constructor gets explicit Go type arguments
stamped (§"Constructor type-argument stamping"). A `unit`-result
body keeps the statement-position discard (the trailing
expression is evaluated for side effects only); a body ending in
explicit `return`s has no trailing and emits nothing extra.

## Implicit receiver / Field

`Field { receiver: This{type: C}, name: n }` lowers to the Go
expression `t.N` — `t` is the receiver name chosen for the
generated Go method (codegen uses `t` consistently for clarity;
not exposed in Tide), and the field is exported per §"Record /
struct field lowering".

Generic class methods carry their type parameters as Go-side
type params; the receiver is a pointer for any class with
`var`-modified fields (mutation visible across calls); a value
receiver otherwise. For v1 every class uses a pointer receiver
unconditionally — keeps the lowering uniform; the few
pure-value classes pay an unnoticeable indirection cost.

**Go-error method rewrite.** A method call `e.error()` on a value
whose sema type is the predeclared `error` builtin lowers to Go's
`e.Error()` — the PascalCase↔lowerCamel binding-name convention at
the Go boundary (a **D6** rule, cf. the D14 footnote). This is
gated on the *receiver's sema type*: a user class that
`implements error` is a nominal `Named` type, not the `error`
builtin, so its own declared `error()` method lowers unchanged to
`t.error()`. (v1 hand-codes this single boundary method; the
general exported-method rewrite arrives with the bindgen pipeline.)

## Slice methods

```
s.len()           → len(s)
s.push(e)         → append(s, e)              (returns the new slice)
s.copy()          → append(s[:0:0], s...)     (fresh-backing clone: the
                                                zero-cap reslice forces
                                                append to allocate, so the
                                                result never aliases s —
                                                expression-form, no element
                                                type named; the receiver is
                                                emitted twice, so v1 expects
                                                a side-effect-free receiver
                                                — an Ident in the corpus)
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

## Tuple destructuring

```
LetIR { pat: (p_1, ..., p_n), value: e }
                                       ⟿
  tmp := <lowering of e>           // value bound once (side-effects)
  p_1 := tmp._0                    // IdentPat component
  p_2 := tmp._1
  ...                             // `_` components bind nothing
```

`let (a, b) = e` evaluates `e` once into a fresh temp, then binds each
component positionally off the anonymous-struct tuple representation
(`tmp._0`, `tmp._1`, …), recursing for a nested tuple component. A
binding whose every component is `_` discards the value (`_ = e`)
rather than leaving an unused temp. Refutable / arity-mismatched
patterns are rejected in sema (T-Let-Destructure), so the lowering
only ever sees irrefutable name/`_`/tuple patterns.

## While-loops

```
WhileIR { cond: C, body: B }   ⟿   for <lowering of C> { <lowering of B> }

WhileIR { cond: true, body: B } ⟿   for { <lowering of B> }
```

`while true` lowers to Go's condition-less `for { … }`, **not**
`for true { … }`. Only the condition-less form is a *terminating
statement* in Go: a `while true` whose sole exits are `return`s in
the body (a common shape for `match`-driven loops) would otherwise
draw a spurious "missing return" after the loop. The literal `true`
is recognised through redundant parentheses.

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

A **generic class brace literal** instantiates the Go type
directly — `Box<int>{ v: 42 }` ⟿ `&Box[int]{v: 42}`. Go cannot
infer struct type parameters from a composite literal, so the
type-args are emitted explicitly; a generic class brace literal
without type-args is a codegen error. **Generic record-type
declarations** lower the same way: `type Pair<A, B> = {…}` ⟿
`type Pair[A any, B any] struct {…}`, and `Pair<int, string>{…}` ⟿
`Pair[int, string]{…}`.

**Generic sum-type declarations** (`type Tree<T> = | Leaf | Node(…)`)
carry the type params onto the tagged struct and every constructor:
`type Tree[T any] struct {…}`, `func TreeNode[T any](…) Tree[T]`. A
**nullary** variant of a generic sum cannot be a package-level `var`
(the value would need a type argument), so it becomes a parameterless
generic constructor — `func TreeLeaf[T any]() Tree[T]` — the same
shape as `OptionNone`. Go infers the type args of a *payload*
constructor call from its value arguments, but a nullary constructor
call has none, so codegen stamps explicit type args at the use site:
from the expected type in a return / typed-binding position
(`return Leaf` ⟿ `TreeLeaf[T]()`), or from the inferred instantiation
of the enclosing payload-constructor call when nested as an argument
(`Node(1, Leaf, Leaf)` ⟿ `TreeNode(1, TreeLeaf[int](), TreeLeaf[int]())`).
The instantiation is read off a value argument whose field type is a
bare type-parameter (`value: T`). A v1 limitation: if no field pins the
parameter directly (`Node(left: Tree<T>, right: Tree<T>)` with a nested
nullary `Node(Leaf, Leaf)`), the type-arg cannot be inferred at the
nullary use site and the construct does not yet lower — proper inference
for that shape is deferred to a sema generic-instantiation pass.

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
