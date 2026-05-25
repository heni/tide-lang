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

The closed set of primitive type names, exactly the same as
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
Never                                   [bottom; no values]
Any                                     [escape; see type-system.md §Notation]
```

`byte` aliases `uint8` and `rune` aliases `int32`; the alias is
*reflective* — `byte` and `uint8` are interchangeable at type
positions, but tokens are not rewritten and diagnostics quote
the source spelling.

## Special types

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
(K, V)`). Order is **insertion order**; insertion order is the
order in which `m.set(k, ...)` was first called for each `k`.
(This is stronger than Go's randomised iteration order — Tide
preserves order for predictable golden tests; lowering keeps a
parallel `[]K` for ordering.)

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
  pop(): Option<T>                 [total — None on empty]
  peek(): Option<T>                [total — None on empty]
}
```

LIFO. Brace literal `Stack<T>{ e1, e2, ..., en }` (`T-Stack-Lit`)
pushes in left-to-right order, so `e_n` is on top after construction.

Iteration: `for e in s { ... }` consumes the stack **in pop
order** (`IterElem(Stack<T>) = T`). To iterate without
consuming, copy via `s.toSlice()` (forthcoming as soon as the
corpus needs it; not in v1 if unused).

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
close. Closing a `SendChan<T>` is illegal — only the owning
`Channel<T>` may close.

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
                                        error() => msg]
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
semantics). `string(s : []byte)` interprets the bytes as UTF-8
without copying invariants — the result shares the byte
sequence semantically but is immutable.

Out-of-set conversions fire **E0205 Illegal type conversion**.

## Iterable<T>

`Iterable<T>` is the (closed) set of source-types that can be
the right-hand side of `for x in <iter>`. D11 parks the
extensibility — user types cannot opt in in v1.

```
IterElem : Type → Type
  []T            → T                              [or (int, T) if pat is 2-tuple]
  Map<K, V>      → (K, V)
  Set<T>         → T
  Stack<T>       → T                              [pop order; consumes the stack]
  RangeExpr      → int                            [a..b or a..=b]
  RecvChan<T>    → T                              [terminates on close]
```

See `type-system.md` §Control-flow expressions, T-For.

## Comparable

Used by `T-Cmp` / `T-Ord`. The set is closed:

```
Comparable :=
  | numeric primitives (int, int8..int64, uint..uint64, byte, rune,
                        float32, float64)
  | string
  | bool                                          [Cmp only; not Ord per T-Ord]
  | tuple T1 × T2 × ... iff each Ti Comparable
  | record { f1: T1; ... } iff each Ti Comparable
```

Notably excluded: function values, channels, maps, sets, stacks,
slices, class instances (use `refEq` for class identity, manual
field-wise comparison otherwise).

## Lowering pointers

Each built-in maps onto a Go construct at codegen time. The full
table lives in `lowering-go.md` (forthcoming); a sketch:

| Tide built-in | Go lowering |
|---|---|
| `int`, `string`, ... | identical Go primitives |
| `[]T` | `[]T` |
| `Option<T>` | tagged struct `{tag uint8; v T}` (zero-cost for `None`) |
| `Result<T, E>` | tagged struct `{tag uint8; v T; e E}` |
| `Map<K, V>` | wrapper around `map[K]V` plus `[]K` for insertion-order |
| `Set<T>` | wrapper around `map[T]struct{}` plus `[]T` for order |
| `Stack<T>` | `[]T` with `len`-based push/pop |
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
