# Built-in identifiers — predeclared scope

The contents of the *predeclared* scope (per
`name-resolution.md` §Scopes #5). Everything here is in scope at
the start of every Tide compilation, before any user `import` or
top-level declaration. Names in this scope can be shadowed by
user code but doing so is bad style.

This file is the **single source of truth** for built-in
signatures. Type rules reference these signatures by name — see
`type-system.md` for how each operation is typed.

**Authority.** This file is the contract. Cross-refs to
`keywords.md` (the bare list), `type-system.md` (rules that
consume these signatures), and `ast.md` (node kinds for the
literal forms).

## Notation

```
fn name<TypeParams>(p1: T1, p2: T2, ...): R
```

is the canonical signature shape. `<TypeParams>` is omitted when
the function is not generic. `name(...)` may be written by users
unqualified (it lives in the predeclared scope). Methods on
container types are written as `T.method(...)` here and called as
`receiver.method(...)` at use-sites.

Lower-case `int`, `string`, etc. are primitive types from
`ast.md PrimitiveName`. Upper-case `Option`, `Result`, etc. are
predeclared generic types defined below.

## Primitive types

The closed set of primitive type names — exactly mirrors
`ast.md PrimitiveName`:

```
bool
int  int8  int16  int32  int64
uint uint8 uint16 uint32 uint64
byte                                    [= uint8 alias at the type level —
                                         distinct as a token, identical
                                         as a type]
rune                                    [= int32 codepoint]
float32  float64
string                                  [UTF-8 byte sequence, indexable
                                         by byte but iterable by rune;
                                         see for-loop iter rules]
unit                                    [only inhabitant `()`]
```

`byte` aliases `uint8` and `rune` aliases `int32`; the alias is
*reflective* — `byte` and `uint8` are interchangeable at type
positions, but tokens are not rewritten and diagnostics quote
the source spelling.

## Special types

`Never` and `Any` are predeclared **non-primitive** types: they
are not in `ast.md PrimitiveName` but live in the predeclared
scope as `NamedType` (with no type args).

- **`Never`** — bottom type; no inhabitants. Produced by
  `DivergingExpr` (`return`/`break`/`continue`/`panic`/`os.exit`).
  Subtypes every other type per `type-system.md` §Notation
  (`Never <: T` for all `T`).
- **`Any`** — escape type used at binding-boundary
  `...Any` variadic parameters. Does **not** narrow back to a
  concrete `T`; users may not introduce `Any`-typed parameters
  in their own code (D11 / G23, enforced by the resolver).

### `error`

```
interface error {
  error(): string
}
```

A nominal interface with a single method `error(): string`. Any
class declaring `implements error` and an `error(): string`
method satisfies it. Bound Go-side `error` values land in Tide as
this interface.

### `Scope`

```
class Scope {
  context: context.Context        [read-only; var-style accessor at the lowering level]
}
```

The receiver of a `scope { ... }` block (see `name-resolution.md`
§Special names). Its only public member is `.context`, the
cancellable context propagated to children. `Scope` is **not**
constructible by users — it's produced by `T-ScopeExpr` and
bound to the lexical identifier `scope`.

## Option

```
type Option<T> =
  | None
  | Some(value: T)
```

Sum type with two variants. The exhaustive forms in the corpus
are `match` on `Some(x) | None`. Option has **no methods** in
v1 (no `.unwrap`, `.unwrapOr`, `.map`) — consumption is by
pattern match or `try` (see `T-Try-Option`).

The constructors `Some` and `None` are predeclared variants;
they are usable unqualified.

## Result

```
type Result<T, E> =
  | Ok(value: T)
  | Err(err: E)
```

Sum type. Consumed by pattern match or `try` (see
`T-Try-Result`). E is conventionally bound to `error` but any
type is admissible — `try` requires the inner `E` to equal the
enclosing function's declared error type (G11, no implicit
conversion).

`Ok` / `Err` are predeclared variants, unqualified usable.

## Slice methods (`[]T`)

`[]T` is a built-in type (per `ast.md SliceType`); the
predeclared scope attaches the following total methods to every
slice value:

```
[]T:
  len(): int
  push(e: T): []T                    [returns a NEW slice; the original is
                                       unchanged at the header level. Idiomatic
                                       use: `xs = xs.push(e)` (G45)]
  copy(): []T                        [shallow header-copy with fresh backing
                                       array; used when callers must isolate
                                       mutations]
```

`push` does not mutate the receiver's header — it produces a new
slice with the element appended. The backing array may be
shared with the original if capacity allowed; callers that need
isolation should `copy()` first. This matches the corpus
convention (`xs = xs.push(v)` everywhere).

Slices also support:
- index read `s[i]: T` (T-Index-Slice, `type-system.md`),
- index write `s[i] = v` (slice index-write mutates the backing
  array, not the header — see `type-system.md` §Bindings and
  assignment),
- slicing `s[low:high]: []T` (T-Slice),
- iteration: `for x in s { ... }` (`T`), or
  `for (i, x) in s { ... }` (`(int, T)`) — see `IterElem` below.

## Map

```
class Map<K, V> {
  static new(): Map<K, V>
  static from(pairs: [](K, V)): Map<K, V>

  len(): int
  has(k: K): bool
  get(k: K): Option<V>             [total — returns None on miss]
  set(k: K, v: V): unit
  delete(k: K): unit
  keys(): []K
  values(): []V
  entries(): [](K, V)
}
```

`Map<K, V>` keys must be comparable. The brace literal form is
`Map<K, V>{ k1: v1, ..., kn: vn }` (`T-Map-Lit`). The
`m[k]` index form (`T-Index-Map`) returns `V` directly and
panics at runtime on miss — the **total**-API path is `m.get(k)
: Option<V>` followed by a `match`.

Iteration: `for (k, v) in m { ... }` (`IterElem(Map<K, V>) =
(K, V)`). Order is **insertion order**: the order in which
`m.set(k, ...)` was first called for each `k`. This is stronger
than Go's randomised iteration order — Tide preserves order for
predictable golden tests (see the Lowering pointers section
below for the implementation strategy).

## Set

```
class Set<T> {
  static new(): Set<T>
  static from(elems: []T): Set<T>

  len(): int
  has(e: T): bool
  add(e: T): unit                  [idempotent — re-adding an existing
                                     element is a no-op]
  delete(e: T): unit
  toSlice(): []T
}
```

`Set<T>` element type must be comparable. The brace literal form
is `Set<T>{ e1, ..., en }` (`T-Set-Lit`).

Iteration: `for e in s { ... }` — insertion order (same
ordering invariant as `Map`).

## Stack

```
class Stack<T> {
  static new(): Stack<T>

  len(): int
  push(e: T): unit
  pop(): Result<T, error>          [total — Err("empty stack") on empty,
                                     so `try stack.pop()` propagates inside
                                     a Result-returning function]
  peek(): Option<T>                [total — None on empty; does not consume]
}
```

LIFO. Brace literal `Stack<T>{ e1, e2, ..., en }` (`T-Stack-Lit`)
pushes in left-to-right order, so `e_n` is on top after construction.

`pop()` returns `Result<T, error>` because corpus usage (e.g.
`examples/interview/rpn_calculator.td`,
`examples/leetcode/valid_parentheses.td`) consumes it with
`try` inside `Result`-returning functions and with `match Ok/Err`
arms. The asymmetric `peek(): Option<T>` choice reflects intent:
`peek` is "look without committing", `pop` is "consume; propagate
emptiness as an error".

Stack values are **not iterable** in v1 — there is no
`for x in stack` form (no corpus site uses it). If a consumer
needs ordered iteration, drain by popping in a loop until
`len() == 0`.

## Channel

```
class Channel<T> {
  send(v: T): unit
  recv(): T
  tryRecv(): Option<T>             [None when buffer empty
                                     (non-blocking, distinct from EOF)]
  close(): unit
}

class SendChan<T> {                [send-only widening of Channel<T>]
  send(v: T): unit
  close(): unit                    [producer closes the channel to signal EOF
                                     to consumers; idiomatic pipeline-stage
                                     pattern across the corpus]
}

class RecvChan<T> {                [recv-only widening of Channel<T>]
  recv(): T
  tryRecv(): Option<T>
}
```

Created via `makeChannel<T>()` or `makeChannel<T>(cap: int)`
(below). Widening from `Channel<T>` to `SendChan<T>` /
`RecvChan<T>` is implicit at call sites — see `T-Chan-Widen`.
The reverse is not allowed.

`recv()` blocks; `tryRecv()` does not. `recv()` on a closed
channel returns the zero value for `T` (Go semantics) — but
Tide's recommended idiom is `for v in ch { ... }` which exits on
close. `close()` is exposed on `Channel<T>` and `SendChan<T>`
but NOT on `RecvChan<T>` — only the owner / producer side may
close. Closing a closed channel panics at runtime.

Iteration: `for v in ch { ... }` over a `RecvChan<T>` —
terminates cleanly when the channel closes (`IterElem(RecvChan<T>)
= T`).

## Variant constructors (predeclared)

```
None : Option<T>                   [no payload]
Some<T>(value: T) : Option<T>

Ok<T, E>(value: T) : Result<T, E>
Err<T, E>(err: E)  : Result<T, E>
```

The type parameters `T` and `E` are inferred from the call site.
The expressions `Some(3)` and `Err("boom")` carry only the
constructor name in the AST (`VariantExpr`); the resolver maps
them to the sum types `Option<_>` and `Result<_, _>` via
predeclared lookup.

When a same-named variant exists in a user-defined sum type, the
unqualified form is disambiguated by `E0104` per
`name-resolution.md` §Variant constructors.

## Free functions

```
fn panic(msg: string): Never
fn refEq<C>(a: C, b: C): bool          [C must be a class type;
                                         a, b same class — see T-RefEq]
fn makeChannel<T>(): Channel<T>
fn makeChannel<T>(cap: int): Channel<T>
fn makeSlice<T>(n: int): []T           [n >= 0; runtime panic if n < 0]
fn error(msg: string): error          [free constructor for the error
                                        interface, equivalent to a tiny
                                        anonymous-class instance with
                                        error() => msg. NOTE: inside the body
                                        of a method on a `class X implements
                                        error`, bare `error(...)` resolves to
                                        the method `this.error()`, not to this
                                        free constructor — class scope outranks
                                        predeclared per name-resolution.md
                                        §Implicit receiver. Use a top-level
                                        wrapper to disambiguate if needed.]
```

`panic` aborts with `msg` on stderr and exit code 2 (matching Go
runtime panic). `refEq` is the only way to compare class
instances for identity — the `==` operator is illegal on class
values (E0401, comparable rule excludes classes — see
`type-system.md` §Arithmetic and logical operators).

## Conversion functions

Each primitive type name acts as a conversion function. The
legal source/target pairs are exactly `ConvOK` from
`type-system.md` §Conversions; this table mirrors them:

```
fn int(x: T): int                  [T ∈ numeric, byte, rune]
fn int8(x: T): int8                [same]
fn int16(x: T): int16
fn int32(x: T): int32
fn int64(x: T): int64
fn uint(x: T): uint
fn uint8(x: T): uint8
fn uint16(x: T): uint16
fn uint32(x: T): uint32
fn uint64(x: T): uint64
fn byte(x: T): byte                [T ∈ numeric, byte, rune]
fn rune(x: T): rune                [T ∈ integer primitives, byte]
fn float32(x: T): float32          [T ∈ numeric]
fn float64(x: T): float64          [T ∈ numeric]
fn string(x: T): string            [T ∈ []byte, rune (UTF-8 single-codepoint),
                                     integer (codepoint encoding)]
```

`string(x)` with `x : int` encodes the codepoint as UTF-8 (Go
semantics). `string(s : []byte)` matches Go's `string([]byte)`:
the runtime makes a defensive copy; the result is immutable and
independent of subsequent mutations to `s`.

Out-of-set conversions fire **E0205 Illegal type conversion**.

## Iterable<T>

`Iterable<T>` is the (closed) set of source-types that can be
the right-hand side of `for x in <iter>`. D11 parks the
extensibility — user types cannot opt in in v1.

```
IterElem : Type → Type
  []T              → T                            [or (int, T) if pat is 2-tuple]
  string           → rune                         [UTF-8 codepoint iteration; matches
                                                    Go's `for _, r := range s`]
  Map<K, V>        → (K, V)                       [insertion order]
  Set<T>           → T                            [insertion order]
  Iterable<int>    → int                          [a RangeExpr `a..b` / `a..=b`]
  RecvChan<T>      → T                            [terminates on close]
```

Notable absence: `Stack<T>` is not iterable in v1 (see §Stack).

See `type-system.md` §Control-flow expressions, T-For.

## Comparable / Ord

Two closed sets used by `T-Cmp` (`==` / `!=`) and `T-Ord`
(`<` / `<=` / `>` / `>=`) respectively.

```
Comparable (T-Cmp, for == / !=):
  | numeric primitives (int, int8..int64, uint..uint64, byte, rune,
                        float32, float64)
  | string
  | bool
  | rune                                          [as integer codepoint]
  | tuple T1 × T2 × ... iff each Ti Comparable
  | record { f1: T1; ... } iff each Ti Comparable

Ord (T-Ord, for < / <= / > / >=):
  | numeric primitives
  | string                                        [lexicographic byte-wise]
  | rune                                          [codepoint order]
  | bool                                          [false < true, mirroring Go]
```

Notably excluded from **both**: function values, channels, maps,
sets, stacks, slices, class instances. Use `refEq` for class
identity; manual field-wise comparison otherwise. `Ord` excludes
tuples and records — there is no v1 lexicographic comparison
for composite types (corpus does not need one; D11 parks bounded
generics that would enable it).

## Lowering pointers

Each built-in maps onto a Go construct at codegen time. The full
table is forthcoming in `lowering-go.md` (Formalization-I);
sketch below for reviewers' orientation:

| Tide built-in | Go lowering |
|---|---|
| `int`, `string`, ... | identical Go primitives |
| `[]T` | `[]T`; `xs.push(e)` lowers to `append(xs, e)`; `xs.len()` to `len(xs)`; `xs.copy()` to a fresh `make` + `copy(dst, src)` |
| `Option<T>` | tagged struct `{tag uint8; v T}` (zero-cost for `None`) |
| `Result<T, E>` | tagged struct `{tag uint8; v T; e E}` |
| `Map<K, V>` | wrapper around `map[K]V` plus `[]K` for insertion-order |
| `Set<T>` | wrapper around `map[T]struct{}` plus `[]T` for order |
| `Stack<T>` | wrapper around `[]T` with `len`-based push/pop; `pop()` checks length and returns `Err` on empty |
| `Channel<T>` | `chan T` |
| `SendChan<T>` | `chan<- T` |
| `RecvChan<T>` | `<-chan T` |
| `error` | Go `error` interface |
| `scope { ... }` | `errgroup.Group` + cancellable `context.Context` |
| `spawn` | `g.Go(func() error { ... })` |
| `makeChannel<T>(n)` | `make(chan T, n)` |
| `makeSlice<T>(n)` | `make([]T, n)` |
| `panic(msg)` | `panic(msg)` |
| `refEq(a, b)` | `a == b` (interface / pointer identity) |

## Built-in errors — quick index

The full catalog lives in `diagnostics.md` (forthcoming). Codes
touched by this file:

- **E0205** — Illegal type conversion (source/target pair not in
  the conversion table).
- **E0206** — `refEq` on non-class operands or operands of
  different class types.
- **E0103** — Unknown name (any predeclared identifier that
  isn't in this catalog and isn't user-defined).
- **E0104** — Ambiguous variant name (built-in vs user-defined
  same-name variant — see name-resolution).

The catalog must be exhaustive: any name resolved through the
predeclared scope must appear here, and the v1 corpus must not
reference a built-in name not in this file. The Formal-L corpus
audit gates this invariant.
